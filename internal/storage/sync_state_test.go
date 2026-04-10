package storage

import (
	"testing"
)

func TestGetSyncState_NotFound(t *testing.T) {
	db := mustOpen(t)

	state, err := db.GetSyncState("git")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil for unseen source, got %+v", state)
	}
}

func TestSetAndGetSyncState(t *testing.T) {
	db := mustOpen(t)

	if err := db.SetSyncState("git", "2026-03-27T10:00:00Z"); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}

	state, err := db.GetSyncState("git")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.Source != "git" {
		t.Errorf("source = %q, want %q", state.Source, "git")
	}
	if state.Cursor != "2026-03-27T10:00:00Z" {
		t.Errorf("cursor = %q", state.Cursor)
	}
	if state.SyncedAt.IsZero() {
		t.Error("synced_at should not be zero")
	}
}

func TestSetSyncState_Upsert(t *testing.T) {
	db := mustOpen(t)

	db.SetSyncState("git", "cursor-1")
	db.SetSyncState("git", "cursor-2")

	state, _ := db.GetSyncState("git")
	if state.Cursor != "cursor-2" {
		t.Errorf("cursor = %q, want %q (should be updated)", state.Cursor, "cursor-2")
	}
}

func TestSyncState_MultipleSources(t *testing.T) {
	db := mustOpen(t)

	db.SetSyncState("git", "git-cursor")
	db.SetSyncState("slack", "slack-cursor")

	gitState, _ := db.GetSyncState("git")
	slackState, _ := db.GetSyncState("slack")

	if gitState.Cursor != "git-cursor" {
		t.Errorf("git cursor = %q", gitState.Cursor)
	}
	if slackState.Cursor != "slack-cursor" {
		t.Errorf("slack cursor = %q", slackState.Cursor)
	}
}

func TestSetSyncState_ClearsError(t *testing.T) {
	db := mustOpen(t)

	// Record an error, then a successful sync — error should be cleared.
	db.SetSyncError("slack", "connection refused")
	db.SetSyncState("slack", "new-cursor")

	state, err := db.GetSyncState("slack")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state.LastError != "" {
		t.Errorf("last_error = %q, want empty (should be cleared after success)", state.LastError)
	}
	if state.Cursor != "new-cursor" {
		t.Errorf("cursor = %q, want %q", state.Cursor, "new-cursor")
	}
}

func TestSetSyncError_RecordsError(t *testing.T) {
	db := mustOpen(t)

	if err := db.SetSyncError("github", "API rate limit exceeded"); err != nil {
		t.Fatalf("SetSyncError: %v", err)
	}

	state, err := db.GetSyncState("github")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.LastError != "API rate limit exceeded" {
		t.Errorf("last_error = %q, want %q", state.LastError, "API rate limit exceeded")
	}
	if state.SyncedAt.IsZero() {
		t.Error("synced_at should not be zero")
	}
}

func TestSetSyncError_PreservesCursor(t *testing.T) {
	db := mustOpen(t)

	// Set a successful sync with cursor, then record an error.
	// The cursor from the last success should be preserved.
	db.SetSyncState("calendar", "sync-token-abc")

	db.SetSyncError("calendar", "token expired")

	state, _ := db.GetSyncState("calendar")
	if state.LastError != "token expired" {
		t.Errorf("last_error = %q, want %q", state.LastError, "token expired")
	}
	if state.Cursor != "sync-token-abc" {
		t.Errorf("cursor = %q, want %q (should be preserved from last success)", state.Cursor, "sync-token-abc")
	}
}

func TestGetSyncState_NoErrorByDefault(t *testing.T) {
	db := mustOpen(t)

	db.SetSyncState("git", "cursor-1")

	state, _ := db.GetSyncState("git")
	if state.LastError != "" {
		t.Errorf("last_error = %q, want empty for successful sync", state.LastError)
	}
}
