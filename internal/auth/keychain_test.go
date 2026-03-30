package auth

import (
	"os"
	"testing"
)

func TestFileTokenStore_SaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileTokenStore(dir)
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}

	token := SlackToken{
		AccessToken: "xoxp-test-token",
		UserID:      "U123",
		TeamID:      "T456",
		TeamName:    "Test Workspace",
		Scope:       "channels:history,channels:read",
	}

	// Save.
	if err := store.Save("slack", "T456", token); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load.
	var loaded SlackToken
	if err := store.Load("slack", "T456", &loaded); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, token.AccessToken)
	}
	if loaded.TeamName != token.TeamName {
		t.Errorf("TeamName = %q, want %q", loaded.TeamName, token.TeamName)
	}

	// Delete.
	if err := store.Delete("slack", "T456"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Load after delete should fail.
	if err := store.Load("slack", "T456", &loaded); err == nil {
		t.Error("Load after Delete should return error")
	}
}

func TestFileTokenStore_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileTokenStore(dir)
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}

	var token SlackToken
	err = store.Load("slack", "nonexistent", &token)
	if err == nil {
		t.Error("expected error loading nonexistent token")
	}
}

func TestFileTokenStore_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileTokenStore(dir)
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}

	token := SlackToken{AccessToken: "secret"}
	if err := store.Save("slack", "T1", token); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := store.tokenPath("slack", "T1")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestFileTokenStore_DeleteNonexistent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileTokenStore(dir)
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}

	// Should not error on deleting nonexistent token.
	if err := store.Delete("slack", "nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}
