package identity

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

func mustOpen(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSetupSelf(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	self, err := r.SetupSelf("Pavel Piliak", []string{"pavel@company.com", "pavel@gmail.com"})
	if err != nil {
		t.Fatalf("SetupSelf: %v", err)
	}
	if !self.IsSelf {
		t.Error("expected IsSelf=true")
	}
	if self.Name != "Pavel Piliak" {
		t.Errorf("name = %q", self.Name)
	}
	if self.Email != "pavel@company.com" {
		t.Errorf("email = %q (should be first email)", self.Email)
	}

	// Both emails should resolve via identity links.
	for _, email := range []string{"pavel@company.com", "pavel@gmail.com"} {
		identity, err := r.ResolveBySourceUID("git", email)
		if err != nil {
			t.Fatalf("ResolveBySourceUID(%q): %v", email, err)
		}
		if identity == nil {
			t.Errorf("email %q should be linked", email)
		} else if identity.ID != self.ID {
			t.Errorf("email %q linked to %d, want %d", email, identity.ID, self.ID)
		}
	}
}

func TestSetupSelf_NoEmails(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	_, err := r.SetupSelf("Pavel", nil)
	if err == nil {
		t.Fatal("expected error for empty emails")
	}
}

func TestSetupSelf_Idempotent(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	first, _ := r.SetupSelf("Pavel", []string{"p@x.com"})
	second, _ := r.SetupSelf("Pavel", []string{"p@x.com"})

	if first.ID != second.ID {
		t.Errorf("SetupSelf should be idempotent: %d != %d", first.ID, second.ID)
	}
}

func TestIsSelf(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)
	r.SetupSelf("Pavel", []string{"primary@x.com", "secondary@x.com"})

	tests := []struct {
		email string
		want  bool
	}{
		{"primary@x.com", true},
		{"secondary@x.com", true},
		{"stranger@x.com", false},
	}

	for _, tt := range tests {
		got, err := r.IsSelf(tt.email)
		if err != nil {
			t.Fatalf("IsSelf(%q): %v", tt.email, err)
		}
		if got != tt.want {
			t.Errorf("IsSelf(%q) = %v, want %v", tt.email, got, tt.want)
		}
	}
}

func TestResolveByEmail(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	r.SetupSelf("Pavel", []string{"p@x.com"})

	identity, _ := r.ResolveByEmail("p@x.com")
	if identity == nil {
		t.Fatal("expected identity")
	}
	if !identity.IsSelf {
		t.Error("expected self")
	}

	identity, _ = r.ResolveByEmail("unknown@x.com")
	if identity != nil {
		t.Error("expected nil for unknown email")
	}
}

func TestAutoLinkSlack_MatchesExistingGitIdentity(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	// Set up self with a git email.
	self, _ := r.SetupSelf("Pavel", []string{"pavel@company.com"})

	// Auto-link Slack with the same email — should merge into the existing identity.
	linked, err := r.AutoLinkSlack("U04ABC", "pavel@company.com", "Pavel P")
	if err != nil {
		t.Fatalf("AutoLinkSlack: %v", err)
	}
	if linked.ID != self.ID {
		t.Errorf("linked to identity %d, want %d (self)", linked.ID, self.ID)
	}

	// Slack source_uid should now resolve to self.
	found, _ := r.ResolveBySourceUID("slack", "U04ABC")
	if found == nil || found.ID != self.ID {
		t.Error("slack link should resolve to self identity")
	}
}

func TestAutoLinkSlack_MatchesViaGitSourceUID(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	// Set up self with primary email "primary@x.com" but also link a secondary git email.
	self, _ := r.SetupSelf("Pavel", []string{"primary@x.com", "secondary@x.com"})

	// Auto-link Slack with the secondary email (which is a git source_uid, not in identities.email).
	linked, err := r.AutoLinkSlack("U04ABC", "secondary@x.com", "Pavel")
	if err != nil {
		t.Fatalf("AutoLinkSlack: %v", err)
	}
	if linked.ID != self.ID {
		t.Errorf("should link to self via git source_uid, got identity %d", linked.ID)
	}
}

func TestAutoLinkSlack_CreatesNewIdentity(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	r.SetupSelf("Pavel", []string{"pavel@x.com"})

	// Different email — should create a new identity.
	linked, err := r.AutoLinkSlack("U99999", "stranger@other.com", "Stranger")
	if err != nil {
		t.Fatalf("AutoLinkSlack: %v", err)
	}
	if linked.Email != "stranger@other.com" {
		t.Errorf("email = %q", linked.Email)
	}
	if linked.IsSelf {
		t.Error("new identity should not be self")
	}
}

func TestAutoLinkSlack_Idempotent(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	first, _ := r.AutoLinkSlack("U001", "bob@x.com", "Bob")
	second, _ := r.AutoLinkSlack("U001", "bob@x.com", "Bob")

	if first.ID != second.ID {
		t.Errorf("AutoLinkSlack should be idempotent: %d != %d", first.ID, second.ID)
	}
}

func TestAutoLinkSlack_EmptyEmail(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	_, err := r.AutoLinkSlack("U001", "", "Bob")
	if err == nil {
		t.Error("expected error for empty email")
	}
}

func TestListAll(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	r.SetupSelf("Pavel", []string{"pavel@x.com"})
	r.AutoLinkSlack("U001", "bob@x.com", "Bob")

	all, err := r.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d identities, want 2", len(all))
	}
	// Self should come first (storage orders by is_self DESC).
	if !all[0].Identity.IsSelf {
		t.Error("self should be first")
	}
	if len(all[0].Links) == 0 {
		t.Error("self should have links")
	}
}

func TestMergeIdentities(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	self, _ := r.SetupSelf("Pavel", []string{"pavel@x.com"})
	other, _ := r.AutoLinkSlack("U001", "other@x.com", "Other")

	if err := r.MergeIdentities(self.ID, []int64{other.ID}); err != nil {
		t.Fatalf("MergeIdentities: %v", err)
	}

	// Other should be gone.
	found, _ := r.ResolveByEmail("other@x.com")
	if found != nil {
		t.Error("merged identity should be gone from identities table")
	}

	// Slack link should resolve to self now.
	linked, _ := r.ResolveBySourceUID("slack", "U001")
	if linked == nil || linked.ID != self.ID {
		t.Error("slack link should be reassigned to primary")
	}
}

func TestMergeIdentities_NotFound(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	if err := r.MergeIdentities(999, []int64{1}); err == nil {
		t.Error("expected error for non-existent primary")
	}
}

func TestMergeIdentities_SelfMerge(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	self, _ := r.SetupSelf("Pavel", []string{"p@x.com"})
	if err := r.MergeIdentities(self.ID, []int64{self.ID}); err == nil {
		t.Error("expected error when merging identity into itself")
	}
}

func TestDeleteIdentity(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	linked, _ := r.AutoLinkSlack("U001", "bob@x.com", "Bob")

	if err := r.DeleteIdentity(linked.ID); err != nil {
		t.Fatalf("DeleteIdentity: %v", err)
	}

	found, _ := r.ResolveByEmail("bob@x.com")
	if found != nil {
		t.Error("deleted identity should not be found")
	}
}

func TestDeleteIdentity_CannotDeleteSelf(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	self, _ := r.SetupSelf("Pavel", []string{"p@x.com"})

	if err := r.DeleteIdentity(self.ID); err == nil {
		t.Error("expected error when deleting self identity")
	}
}

func TestDeleteIdentity_NotFound(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	if err := r.DeleteIdentity(999); err == nil {
		t.Error("expected error for non-existent identity")
	}
}

func makeCalendarActivity(title string, attendees []calendarAttendee) models.Activity {
	meta, _ := json.Marshal(calendarMeta{Attendees: attendees})
	return models.Activity{
		Source:   models.SourceCalendar,
		SourceID: "calendar:primary:" + title,
		Type:     models.TypeMeeting,
		Title:    title,
		Metadata: string(meta),
		Timestamp: time.Now(),
	}
}

func TestEnrichFromCalendar_CreatesNewIdentities(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)
	r.SetupSelf("Pavel", []string{"pavel@company.com"})

	activities := []models.Activity{
		makeCalendarActivity("1:1 with Sarah", []calendarAttendee{
			{Email: "pavel@company.com", Self: true},
			{Email: "sarah@company.com", DisplayName: "Sarah Chen"},
		}),
	}

	created, err := r.EnrichFromCalendar(activities)
	if err != nil {
		t.Fatalf("EnrichFromCalendar: %v", err)
	}

	if created != 1 {
		t.Errorf("created = %d, want 1", created)
	}

	// Self identity should be set on the activity.
	self, _ := db.GetSelfIdentity()
	if activities[0].IdentityID != self.ID {
		t.Errorf("IdentityID = %d, want %d (self)", activities[0].IdentityID, self.ID)
	}

	// Sarah should exist as a new identity.
	sarah, err := r.ResolveByEmail("sarah@company.com")
	if err != nil {
		t.Fatalf("ResolveByEmail: %v", err)
	}
	if sarah == nil {
		t.Fatal("expected sarah identity to be created")
	}
	if sarah.Name != "Sarah Chen" {
		t.Errorf("sarah.Name = %q", sarah.Name)
	}

	// Calendar link should exist.
	linked, _ := r.ResolveBySourceUID("calendar", "sarah@company.com")
	if linked == nil || linked.ID != sarah.ID {
		t.Error("sarah should have a calendar identity link")
	}
}

func TestEnrichFromCalendar_MatchesExistingIdentity(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)
	r.SetupSelf("Pavel", []string{"pavel@company.com"})

	// Sarah already exists from Slack.
	r.AutoLinkSlack("U001", "sarah@company.com", "Sarah")

	activities := []models.Activity{
		makeCalendarActivity("Design Review", []calendarAttendee{
			{Email: "pavel@company.com", Self: true},
			{Email: "sarah@company.com", DisplayName: "Sarah Chen"},
		}),
	}

	created, err := r.EnrichFromCalendar(activities)
	if err != nil {
		t.Fatalf("EnrichFromCalendar: %v", err)
	}

	// No new identities — Sarah already existed.
	if created != 0 {
		t.Errorf("created = %d, want 0 (Sarah already existed)", created)
	}

	// Sarah should now also have a calendar link.
	linked, _ := r.ResolveBySourceUID("calendar", "sarah@company.com")
	if linked == nil {
		t.Error("sarah should have a calendar identity link")
	}
}

func TestEnrichFromCalendar_MultipleAttendees(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)
	r.SetupSelf("Pavel", []string{"pavel@company.com"})

	activities := []models.Activity{
		makeCalendarActivity("Team Meeting", []calendarAttendee{
			{Email: "pavel@company.com", Self: true},
			{Email: "alice@company.com", DisplayName: "Alice"},
			{Email: "bob@company.com", DisplayName: "Bob"},
			{Email: "carol@company.com", DisplayName: "Carol"},
		}),
	}

	created, err := r.EnrichFromCalendar(activities)
	if err != nil {
		t.Fatalf("EnrichFromCalendar: %v", err)
	}

	if created != 3 {
		t.Errorf("created = %d, want 3", created)
	}

	identities, _ := db.ListIdentities()
	// 1 self + 3 new = 4
	if len(identities) != 4 {
		t.Errorf("total identities = %d, want 4", len(identities))
	}
}

func TestEnrichFromCalendar_SkipsNonCalendarActivities(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	activities := []models.Activity{
		{Source: models.SourceSlack, Metadata: `{"channel_id": "C1"}`},
		{Source: models.SourceGit, Metadata: ""},
	}

	created, err := r.EnrichFromCalendar(activities)
	if err != nil {
		t.Fatalf("EnrichFromCalendar: %v", err)
	}
	if created != 0 {
		t.Errorf("created = %d, want 0", created)
	}
}

func TestEnrichFromCalendar_SkipsEmptyEmails(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	activities := []models.Activity{
		makeCalendarActivity("Test", []calendarAttendee{
			{Email: "", DisplayName: "No Email"},
		}),
	}

	created, err := r.EnrichFromCalendar(activities)
	if err != nil {
		t.Fatalf("EnrichFromCalendar: %v", err)
	}
	if created != 0 {
		t.Errorf("created = %d, want 0", created)
	}
}

func TestEnrichFromCalendar_Idempotent(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)
	r.SetupSelf("Pavel", []string{"pavel@company.com"})

	activities := []models.Activity{
		makeCalendarActivity("Meeting", []calendarAttendee{
			{Email: "pavel@company.com", Self: true},
			{Email: "bob@company.com", DisplayName: "Bob"},
		}),
	}

	created1, _ := r.EnrichFromCalendar(activities)
	created2, _ := r.EnrichFromCalendar(activities)

	if created1 != 1 {
		t.Errorf("first run: created = %d, want 1", created1)
	}
	if created2 != 0 {
		t.Errorf("second run: created = %d, want 0 (idempotent)", created2)
	}
}

func TestEnrichFromCalendar_UsesEmailAsNameFallback(t *testing.T) {
	db := mustOpen(t)
	r := NewResolver(db)

	activities := []models.Activity{
		makeCalendarActivity("Meeting", []calendarAttendee{
			{Email: "noname@company.com"},
		}),
	}

	r.EnrichFromCalendar(activities)

	identity, _ := r.ResolveByEmail("noname@company.com")
	if identity == nil {
		t.Fatal("expected identity")
	}
	// When no display name, email should be used as name.
	if identity.Name != "noname@company.com" {
		t.Errorf("Name = %q, want email as fallback", identity.Name)
	}
}
