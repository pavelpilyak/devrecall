package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// WorkItemLink is a desired activity→work-item association, addressed by
// work-item key so callers don't need to know row IDs.
type WorkItemLink struct {
	ActivityID int64
	Key        string
	LinkKind   string // 'issue_key' | 'pr_sha' | 'self'
}

// ReplaceWorkItems reconciles the work_items and activity_work_items tables
// to exactly the given desired state, in one transaction. Existing work
// items are matched by key (their IDs are preserved); items whose key is
// absent from the desired set are deleted, along with their links.
func (db *DB) ReplaceWorkItems(items []models.WorkItem, links []WorkItemLink) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	upsert, err := tx.Prepare(`
		INSERT INTO work_items (key, kind, title, status, status_changed_at, url, first_seen, last_seen, updated_at)
		VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET
			kind              = excluded.kind,
			title             = excluded.title,
			status            = excluded.status,
			status_changed_at = excluded.status_changed_at,
			url               = excluded.url,
			first_seen        = excluded.first_seen,
			last_seen         = excluded.last_seen,
			updated_at        = datetime('now')`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer upsert.Close()

	keys := make([]string, 0, len(items))
	for _, w := range items {
		statusChanged := ""
		if !w.StatusChangedAt.IsZero() {
			statusChanged = w.StatusChangedAt.UTC().Format(time.RFC3339)
		}
		_, err := upsert.Exec(w.Key, w.Kind, w.Title, w.Status, statusChanged, w.URL,
			w.FirstSeen.UTC().Format(time.RFC3339), w.LastSeen.UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("upsert work item %s: %w", w.Key, err)
		}
		keys = append(keys, w.Key)
	}

	// Delete work items (and their links, via cascade) no longer desired.
	if len(keys) == 0 {
		if _, err := tx.Exec("DELETE FROM work_items"); err != nil {
			return fmt.Errorf("delete stale work items: %w", err)
		}
	} else {
		placeholders := strings.Repeat("?,", len(keys)-1) + "?"
		args := make([]any, len(keys))
		for i, k := range keys {
			args[i] = k
		}
		if _, err := tx.Exec("DELETE FROM work_items WHERE key NOT IN ("+placeholders+")", args...); err != nil {
			return fmt.Errorf("delete stale work items: %w", err)
		}
	}

	// Links are fully recomputed: wipe and re-insert, resolving keys to IDs.
	if _, err := tx.Exec("DELETE FROM activity_work_items"); err != nil {
		return fmt.Errorf("clear links: %w", err)
	}
	linkStmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO activity_work_items (activity_id, work_item_id, link_kind)
		SELECT ?, id, ? FROM work_items WHERE key = ?`)
	if err != nil {
		return fmt.Errorf("prepare link insert: %w", err)
	}
	defer linkStmt.Close()

	for _, l := range links {
		if _, err := linkStmt.Exec(l.ActivityID, l.LinkKind, l.Key); err != nil {
			return fmt.Errorf("link activity %d to %s: %w", l.ActivityID, l.Key, err)
		}
	}

	return tx.Commit()
}

// GetWorkItemByKey returns the work item with the given key, or nil if none exists.
func (db *DB) GetWorkItemByKey(key string) (*models.WorkItem, error) {
	return db.getWorkItem("key = ?", key)
}

// GetWorkItemByID returns the work item with the given ID, or nil if none exists.
func (db *DB) GetWorkItemByID(id int64) (*models.WorkItem, error) {
	return db.getWorkItem("id = ?", id)
}

func (db *DB) getWorkItem(where string, arg any) (*models.WorkItem, error) {
	row := db.QueryRow(`
		SELECT id, key, kind, title, COALESCE(status, ''), COALESCE(status_changed_at, ''),
			COALESCE(url, ''), first_seen, last_seen
		FROM work_items WHERE `+where, arg)
	w, err := scanWorkItem(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get work item: %w", err)
	}
	return w, nil
}

// WorkItemFilter controls which work items ListWorkItems returns.
type WorkItemFilter struct {
	Status string    // exact match, case-insensitive
	Query  string    // substring match on key or title, case-insensitive
	After  time.Time // work item active on or after (last_seen >= After)
	Before time.Time // work item active before (first_seen < Before)
	Limit  int
}

// ListWorkItems returns work items matching the filter, most recently
// active first. Date filters match items whose [first_seen, last_seen]
// range overlaps the given window.
func (db *DB) ListWorkItems(f WorkItemFilter) ([]models.WorkItem, error) {
	query := `
		SELECT id, key, kind, title, COALESCE(status, ''), COALESCE(status_changed_at, ''),
			COALESCE(url, ''), first_seen, last_seen
		FROM work_items WHERE 1=1`
	var args []any

	if f.Status != "" {
		query += " AND LOWER(status) = LOWER(?)"
		args = append(args, f.Status)
	}
	if f.Query != "" {
		query += " AND (key LIKE ? OR title LIKE ?)"
		pattern := "%" + f.Query + "%"
		args = append(args, pattern, pattern)
	}
	if !f.After.IsZero() {
		query += " AND last_seen >= ?"
		args = append(args, f.After.UTC().Format(time.RFC3339))
	}
	if !f.Before.IsZero() {
		query += " AND first_seen < ?"
		args = append(args, f.Before.UTC().Format(time.RFC3339))
	}

	query += " ORDER BY last_seen DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list work items: %w", err)
	}
	defer rows.Close()

	var result []models.WorkItem
	for rows.Next() {
		w, err := scanWorkItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan work item: %w", err)
		}
		result = append(result, *w)
	}
	return result, rows.Err()
}

// ListActivityWorkItems returns, for each given activity ID, the work items
// it is linked to. Activities with no links are absent from the map.
func (db *DB) ListActivityWorkItems(ids []int64) (map[int64][]models.WorkItemRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT awi.activity_id, w.id, w.key, w.kind, w.title, COALESCE(w.status, ''),
			COALESCE(w.status_changed_at, '')
		FROM activity_work_items awi
		JOIN work_items w ON w.id = awi.work_item_id
		WHERE awi.activity_id IN (%s)
		ORDER BY awi.activity_id, w.key`, strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list activity work items: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]models.WorkItemRef)
	for rows.Next() {
		var activityID int64
		var ref models.WorkItemRef
		var statusChanged string
		if err := rows.Scan(&activityID, &ref.ID, &ref.Key, &ref.Kind, &ref.Title, &ref.Status, &statusChanged); err != nil {
			return nil, fmt.Errorf("scan work item ref: %w", err)
		}
		if statusChanged != "" {
			ref.StatusChangedAt, _ = time.Parse(time.RFC3339, statusChanged)
		}
		result[activityID] = append(result[activityID], ref)
	}
	return result, rows.Err()
}

// ListActivitiesByWorkItem returns the activities linked to a work item,
// in chronological order (oldest first — a timeline).
func (db *DB) ListActivitiesByWorkItem(workItemID int64, limit int) ([]models.Activity, error) {
	query := `
		SELECT a.id, a.source, a.source_id, COALESCE(a.identity_id, 0), a.type, a.title,
			COALESCE(a.content, ''), COALESCE(a.metadata, ''), a.timestamp
		FROM activity_work_items awi
		JOIN activities a ON a.id = awi.activity_id
		WHERE awi.work_item_id = ?
		ORDER BY a.timestamp ASC`
	args := []any{workItemID}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list activities by work item: %w", err)
	}
	defer rows.Close()

	return scanActivities(rows)
}

// ListActivityHeaders returns every activity without its content body —
// enough for bulk passes (like work-item linking) that read only metadata.
func (db *DB) ListActivityHeaders() ([]models.Activity, error) {
	rows, err := db.Query(`
		SELECT id, source, source_id, COALESCE(identity_id, 0), type, title, '',
			COALESCE(metadata, ''), timestamp
		FROM activities`)
	if err != nil {
		return nil, fmt.Errorf("list activity headers: %w", err)
	}
	defer rows.Close()

	return scanActivities(rows)
}

func scanWorkItem(scan func(...any) error) (*models.WorkItem, error) {
	var w models.WorkItem
	var statusChanged, firstSeen, lastSeen string
	err := scan(&w.ID, &w.Key, &w.Kind, &w.Title, &w.Status, &statusChanged, &w.URL, &firstSeen, &lastSeen)
	if err != nil {
		return nil, err
	}
	if statusChanged != "" {
		w.StatusChangedAt, _ = time.Parse(time.RFC3339, statusChanged)
	}
	if w.FirstSeen, err = time.Parse(time.RFC3339, firstSeen); err != nil {
		return nil, fmt.Errorf("parse first_seen %q: %w", firstSeen, err)
	}
	if w.LastSeen, err = time.Parse(time.RFC3339, lastSeen); err != nil {
		return nil, fmt.Errorf("parse last_seen %q: %w", lastSeen, err)
	}
	return &w, nil
}
