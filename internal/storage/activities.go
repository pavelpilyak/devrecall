package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

// InsertActivity upserts a single activity. On conflict (same source+source_id),
// the existing row is updated.
func (db *DB) InsertActivity(a models.Activity) (int64, error) {
	res, err := db.Exec(`
		INSERT INTO activities (source, source_id, identity_id, type, title, content, metadata, timestamp)
		VALUES (?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?)
		ON CONFLICT(source, source_id) DO UPDATE SET
			identity_id = COALESCE(excluded.identity_id, identity_id),
			type        = excluded.type,
			title       = excluded.title,
			content     = excluded.content,
			metadata    = excluded.metadata,
			timestamp   = excluded.timestamp`,
		a.Source, a.SourceID, a.IdentityID, a.Type, a.Title, a.Content, a.Metadata,
		a.Timestamp.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("insert activity: %w", err)
	}
	return res.LastInsertId()
}

// InsertActivities upserts a batch of activities in a single transaction.
func (db *DB) InsertActivities(activities []models.Activity) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO activities (source, source_id, identity_id, type, title, content, metadata, timestamp)
		VALUES (?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?)
		ON CONFLICT(source, source_id) DO UPDATE SET
			identity_id = COALESCE(excluded.identity_id, identity_id),
			type        = excluded.type,
			title       = excluded.title,
			content     = excluded.content,
			metadata    = excluded.metadata,
			timestamp   = excluded.timestamp`)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	inserted := 0
	for _, a := range activities {
		_, err := stmt.Exec(
			a.Source, a.SourceID, a.IdentityID, a.Type, a.Title, a.Content, a.Metadata,
			a.Timestamp.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return inserted, fmt.Errorf("insert %s: %w", a.SourceID, err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return inserted, nil
}

// ActivityFilter controls which activities are returned by ListActivities.
type ActivityFilter struct {
	Source models.Source
	Type   models.ActivityType
	After  time.Time
	Before time.Time
	Limit  int
}

// ListActivities returns activities matching the filter, ordered by timestamp descending.
func (db *DB) ListActivities(f ActivityFilter) ([]models.Activity, error) {
	query := "SELECT id, source, source_id, COALESCE(identity_id, 0), type, title, COALESCE(content, ''), COALESCE(metadata, ''), timestamp FROM activities WHERE 1=1"
	var args []any

	if f.Source != "" {
		query += " AND source = ?"
		args = append(args, f.Source)
	}
	if f.Type != "" {
		query += " AND type = ?"
		args = append(args, f.Type)
	}
	if !f.After.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, f.After.UTC().Format(time.RFC3339))
	}
	if !f.Before.IsZero() {
		query += " AND timestamp < ?"
		args = append(args, f.Before.UTC().Format(time.RFC3339))
	}

	query += " ORDER BY timestamp DESC"

	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query activities: %w", err)
	}
	defer rows.Close()

	return scanActivities(rows)
}

func scanActivities(rows *sql.Rows) ([]models.Activity, error) {
	var result []models.Activity
	for rows.Next() {
		var a models.Activity
		var ts string
		err := rows.Scan(&a.ID, &a.Source, &a.SourceID, &a.IdentityID, &a.Type, &a.Title, &a.Content, &a.Metadata, &ts)
		if err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		a.Timestamp, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return nil, fmt.Errorf("parse timestamp %q: %w", ts, err)
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
