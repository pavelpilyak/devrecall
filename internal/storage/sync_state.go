package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// SyncState represents the last sync cursor for a source.
type SyncState struct {
	Source   string
	Cursor   string
	SyncedAt time.Time
}

// GetSyncState returns the sync state for the given source.
// Returns nil if the source has never been synced.
func (db *DB) GetSyncState(source string) (*SyncState, error) {
	var s SyncState
	var syncedAt string
	err := db.QueryRow(
		"SELECT source, COALESCE(cursor, ''), synced_at FROM sync_state WHERE source = ?",
		source,
	).Scan(&s.Source, &s.Cursor, &syncedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sync state: %w", err)
	}
	s.SyncedAt, _ = time.Parse("2006-01-02 15:04:05", syncedAt)
	return &s, nil
}

// SetSyncState upserts the sync cursor for the given source.
func (db *DB) SetSyncState(source, cursor string) error {
	_, err := db.Exec(`
		INSERT INTO sync_state (source, cursor, synced_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(source) DO UPDATE SET
			cursor    = excluded.cursor,
			synced_at = excluded.synced_at`,
		source, cursor,
	)
	if err != nil {
		return fmt.Errorf("set sync state: %w", err)
	}
	return nil
}
