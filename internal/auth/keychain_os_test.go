package auth

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeychainTokenStore_SaveLoadDelete(t *testing.T) {
	keyring.MockInit()

	store := NewKeychainTokenStore()

	token := SlackToken{
		AccessToken: "xoxp-test-token",
		UserID:      "U123",
		TeamID:      "T456",
		TeamName:    "Test Workspace",
		Scope:       "channels:history",
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

func TestKeychainTokenStore_LoadMissing(t *testing.T) {
	keyring.MockInit()

	store := NewKeychainTokenStore()

	var token SlackToken
	err := store.Load("slack", "nonexistent", &token)
	if err == nil {
		t.Error("expected error loading nonexistent token")
	}
}

func TestKeychainTokenStore_DeleteNonexistent(t *testing.T) {
	keyring.MockInit()

	store := NewKeychainTokenStore()

	if err := store.Delete("slack", "nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestKeychainTokenStore_OverwriteExisting(t *testing.T) {
	keyring.MockInit()

	store := NewKeychainTokenStore()

	token1 := SlackToken{AccessToken: "old-token"}
	token2 := SlackToken{AccessToken: "new-token"}

	store.Save("slack", "T1", token1)
	store.Save("slack", "T1", token2)

	var loaded SlackToken
	if err := store.Load("slack", "T1", &loaded); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != "new-token" {
		t.Errorf("expected overwritten token, got %q", loaded.AccessToken)
	}
}

func TestNewTokenStore_File(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTokenStore("file", dir)
	if err != nil {
		t.Fatalf("NewTokenStore file: %v", err)
	}
	if _, ok := store.(*FileTokenStore); !ok {
		t.Errorf("expected *FileTokenStore, got %T", store)
	}
}

func TestNewTokenStore_DefaultIsFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTokenStore("", dir)
	if err != nil {
		t.Fatalf("NewTokenStore empty: %v", err)
	}
	if _, ok := store.(*FileTokenStore); !ok {
		t.Errorf("expected *FileTokenStore for empty backend, got %T", store)
	}
}

func TestNewTokenStore_Keychain(t *testing.T) {
	store, err := NewTokenStore("keychain", "")
	if err != nil {
		t.Fatalf("NewTokenStore keychain: %v", err)
	}
	if _, ok := store.(*KeychainTokenStore); !ok {
		t.Errorf("expected *KeychainTokenStore, got %T", store)
	}
}
