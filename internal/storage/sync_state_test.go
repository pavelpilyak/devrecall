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
