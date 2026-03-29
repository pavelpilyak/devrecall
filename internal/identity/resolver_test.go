package identity

import (
	"testing"

	"github.com/pavelpiliak/devrecall/internal/storage"
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
