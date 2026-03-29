package storage

import (
	"testing"
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
