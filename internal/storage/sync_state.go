package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// SyncState represents the last sync cursor for a source.
type SyncState struct {
	Source    string
	Cursor    string
	SyncedAt  time.Time
	LastError string // empty string means last sync succeeded
}

// GetSyncState returns the sync state for the given source.
// Returns nil if the source has never been synced.
func (db *DB) GetSyncState(source string) (*SyncState, error) {
	var s SyncState
	var syncedAt string
	var lastError sql.NullString
	err := db.QueryRow(
		"SELECT source, COALESCE(cursor, ''), synced_at, last_error FROM sync_state WHERE source = ?",
		source,
	).Scan(&s.Source, &s.Cursor, &syncedAt, &lastError)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sync state: %w", err)
	}
	s.SyncedAt, _ = time.Parse("2006-01-02 15:04:05", syncedAt)
	if lastError.Valid {
		s.LastError = lastError.String
	}
	return &s, nil
}

// SetSyncState upserts the sync cursor for the given source and clears any previous error.
func (db *DB) SetSyncState(source, cursor string) error {
	_, err := db.Exec(`
		INSERT INTO sync_state (source, cursor, synced_at, last_error)
		VALUES (?, ?, datetime('now'), NULL)
		ON CONFLICT(source) DO UPDATE SET
			cursor     = excluded.cursor,
			synced_at  = excluded.synced_at,
			last_error = NULL`,
		source, cursor,
	)
	if err != nil {
		return fmt.Errorf("set sync state: %w", err)
	}
	return nil
}

// SetSyncError records a sync failure for the given source without updating the cursor.
func (db *DB) SetSyncError(source, syncErr string) error {
	_, err := db.Exec(`
		INSERT INTO sync_state (source, synced_at, last_error)
		VALUES (?, datetime('now'), ?)
		ON CONFLICT(source) DO UPDATE SET
			synced_at  = excluded.synced_at,
			last_error = excluded.last_error`,
		source, syncErr,
	)
	if err != nil {
		return fmt.Errorf("set sync error: %w", err)
	}
	return nil
}
