package storage

import (
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

func TestInsertIdentity(t *testing.T) {
	db := mustOpen(t)

	id, err := db.InsertIdentity("Pavel", "pavel@example.com", true)
	if err != nil {
		t.Fatalf("InsertIdentity: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestInsertIdentity_DuplicateEmail(t *testing.T) {
	db := mustOpen(t)

	id1, _ := db.InsertIdentity("Pavel", "pavel@example.com", false)
	id2, _ := db.InsertIdentity("Pavel P.", "pavel@example.com", true)

	if id1 != id2 {
		t.Errorf("duplicate email should return same ID: %d != %d", id1, id2)
	}

	// Should upgrade is_self to true.
	identity, _ := db.GetIdentityByEmail("pavel@example.com")
	if !identity.IsSelf {
		t.Error("is_self should be upgraded to true on re-insert")
	}
	if identity.Name != "Pavel P." {
		t.Errorf("name should be updated: got %q", identity.Name)
	}
}

func TestInsertIdentity_NormalizesEmail(t *testing.T) {
	db := mustOpen(t)

	db.InsertIdentity("Pavel", "  Pavel@Example.COM  ", false)

	identity, err := db.GetIdentityByEmail("pavel@example.com")
	if err != nil {
		t.Fatalf("GetIdentityByEmail: %v", err)
	}
	if identity == nil {
		t.Fatal("expected identity, got nil")
	}
}

func TestGetIdentityByEmail_NotFound(t *testing.T) {
	db := mustOpen(t)

	identity, err := db.GetIdentityByEmail("nobody@example.com")
	if err != nil {
		t.Fatalf("GetIdentityByEmail: %v", err)
	}
	if identity != nil {
		t.Errorf("expected nil, got %+v", identity)
	}
}

func TestGetSelfIdentity(t *testing.T) {
	db := mustOpen(t)

	db.InsertIdentity("Other", "other@example.com", false)
	db.InsertIdentity("Pavel", "pavel@example.com", true)

	self, err := db.GetSelfIdentity()
	if err != nil {
		t.Fatalf("GetSelfIdentity: %v", err)
	}
	if self == nil {
		t.Fatal("expected self identity")
	}
	if !self.IsSelf {
		t.Error("expected IsSelf=true")
	}
	if self.Email != "pavel@example.com" {
		t.Errorf("email = %q", self.Email)
	}
}

func TestGetSelfIdentity_NoneSetUp(t *testing.T) {
	db := mustOpen(t)

	self, err := db.GetSelfIdentity()
	if err != nil {
		t.Fatalf("GetSelfIdentity: %v", err)
	}
	if self != nil {
		t.Errorf("expected nil when no self identity, got %+v", self)
	}
}

func TestListIdentities(t *testing.T) {
	db := mustOpen(t)

	db.InsertIdentity("Bob", "bob@example.com", false)
	db.InsertIdentity("Alice", "alice@example.com", false)
	db.InsertIdentity("Self", "self@example.com", true)

	identities, err := db.ListIdentities()
	if err != nil {
		t.Fatalf("ListIdentities: %v", err)
	}
	if len(identities) != 3 {
		t.Fatalf("got %d, want 3", len(identities))
	}
	// Self should come first.
	if !identities[0].IsSelf {
		t.Error("self identity should be listed first")
	}
}

func TestInsertAndGetIdentityLink(t *testing.T) {
	db := mustOpen(t)

	id, _ := db.InsertIdentity("Pavel", "pavel@example.com", true)
	if err := db.InsertIdentityLink(id, "git", "pavel@example.com"); err != nil {
		t.Fatalf("InsertIdentityLink: %v", err)
	}
	if err := db.InsertIdentityLink(id, "slack", "U04ABC"); err != nil {
		t.Fatalf("InsertIdentityLink: %v", err)
	}

	// Look up by git email.
	identity, err := db.GetIdentityBySourceUID("git", "pavel@example.com")
	if err != nil {
		t.Fatalf("GetIdentityBySourceUID: %v", err)
	}
	if identity == nil {
		t.Fatal("expected identity")
	}
	if identity.ID != id {
		t.Errorf("id = %d, want %d", identity.ID, id)
	}

	// Look up by slack ID.
	identity, err = db.GetIdentityBySourceUID("slack", "U04ABC")
	if err != nil {
		t.Fatalf("GetIdentityBySourceUID: %v", err)
	}
	if identity == nil || identity.ID != id {
		t.Error("slack link should resolve to same identity")
	}
}

func TestGetIdentityBySourceUID_NotFound(t *testing.T) {
	db := mustOpen(t)

	identity, err := db.GetIdentityBySourceUID("git", "unknown@example.com")
	if err != nil {
		t.Fatalf("GetIdentityBySourceUID: %v", err)
	}
	if identity != nil {
		t.Errorf("expected nil, got %+v", identity)
	}
}

func TestGetIdentityByID(t *testing.T) {
	db := mustOpen(t)

	id, _ := db.InsertIdentity("Pavel", "pavel@example.com", true)
	identity, err := db.GetIdentityByID(id)
	if err != nil {
		t.Fatalf("GetIdentityByID: %v", err)
	}
	if identity == nil {
		t.Fatal("expected identity")
	}
	if identity.Email != "pavel@example.com" {
		t.Errorf("email = %q", identity.Email)
	}

	identity, err = db.GetIdentityByID(9999)
	if err != nil {
		t.Fatalf("GetIdentityByID: %v", err)
	}
	if identity != nil {
		t.Errorf("expected nil for non-existent ID, got %+v", identity)
	}
}

func TestListIdentityLinks(t *testing.T) {
	db := mustOpen(t)

	id, _ := db.InsertIdentity("Pavel", "pavel@example.com", true)
	db.InsertIdentityLink(id, "git", "pavel@example.com")
	db.InsertIdentityLink(id, "slack", "U04ABC")

	links, err := db.ListIdentityLinks(id)
	if err != nil {
		t.Fatalf("ListIdentityLinks: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
	// Ordered by source, source_uid.
	if links[0].Source != "git" {
		t.Errorf("first link source = %q, want git", links[0].Source)
	}
	if links[1].Source != "slack" {
		t.Errorf("second link source = %q, want slack", links[1].Source)
	}
}

func TestMergeIdentities(t *testing.T) {
	db := mustOpen(t)

	primaryID, _ := db.InsertIdentity("Pavel", "pavel@example.com", true)
	db.InsertIdentityLink(primaryID, "git", "pavel@example.com")

	secID, _ := db.InsertIdentity("Pavel Work", "pavel@work.com", false)
	db.InsertIdentityLink(secID, "slack", "U04ABC")

	// Insert an activity linked to the secondary identity.
	db.InsertActivity(models.Activity{
		Source: models.SourceSlack, SourceID: "msg-1", IdentityID: secID,
		Type: models.TypeMessage, Title: "test msg", Timestamp: time.Now(),
	})

	if err := db.MergeIdentities(primaryID, []int64{secID}); err != nil {
		t.Fatalf("MergeIdentities: %v", err)
	}

	// Secondary identity should be gone.
	sec, _ := db.GetIdentityByID(secID)
	if sec != nil {
		t.Error("secondary identity should be deleted")
	}

	// Slack link should now point to primary.
	linked, _ := db.GetIdentityBySourceUID("slack", "U04ABC")
	if linked == nil || linked.ID != primaryID {
		t.Error("slack link should be reassigned to primary")
	}

	// Activity should be reassigned.
	activities, _ := db.ListActivities(ActivityFilter{Source: models.SourceSlack})
	if len(activities) != 1 {
		t.Fatalf("got %d activities", len(activities))
	}
	if activities[0].IdentityID != primaryID {
		t.Errorf("activity identity_id = %d, want %d", activities[0].IdentityID, primaryID)
	}
}

func TestDeleteIdentity(t *testing.T) {
	db := mustOpen(t)

	id, _ := db.InsertIdentity("Bob", "bob@example.com", false)
	db.InsertIdentityLink(id, "slack", "U001")
	db.InsertActivity(models.Activity{
		Source: models.SourceSlack, SourceID: "msg-1", IdentityID: id,
		Type: models.TypeMessage, Title: "test", Timestamp: time.Now(),
	})

	if err := db.DeleteIdentity(id); err != nil {
		t.Fatalf("DeleteIdentity: %v", err)
	}

	// Identity should be gone.
	identity, _ := db.GetIdentityByID(id)
	if identity != nil {
		t.Error("identity should be deleted")
	}

	// Links should be gone.
	links, _ := db.ListIdentityLinks(id)
	if len(links) != 0 {
		t.Errorf("got %d links, want 0", len(links))
	}

	// Activity should be unlinked (identity_id = 0).
	activities, _ := db.ListActivities(ActivityFilter{Source: models.SourceSlack})
	if len(activities) != 1 {
		t.Fatalf("got %d activities", len(activities))
	}
	if activities[0].IdentityID != 0 {
		t.Errorf("activity identity_id = %d, want 0 (unlinked)", activities[0].IdentityID)
	}
}
