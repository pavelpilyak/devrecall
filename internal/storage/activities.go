package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
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
// Returns the number of genuinely new rows inserted (not counting updates to existing rows).
func (db *DB) InsertActivities(activities []models.Activity) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Count rows before to compute truly new inserts.
	var countBefore int
	if err := tx.QueryRow("SELECT COUNT(*) FROM activities").Scan(&countBefore); err != nil {
		return 0, fmt.Errorf("count before: %w", err)
	}

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

	for _, a := range activities {
		_, err := stmt.Exec(
			a.Source, a.SourceID, a.IdentityID, a.Type, a.Title, a.Content, a.Metadata,
			a.Timestamp.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return 0, fmt.Errorf("insert %s: %w", a.SourceID, err)
		}
	}

	var countAfter int
	if err := tx.QueryRow("SELECT COUNT(*) FROM activities").Scan(&countAfter); err != nil {
		return 0, fmt.Errorf("count after: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return countAfter - countBefore, nil
}

// ActivityFilter controls which activities are returned by ListActivities.
type ActivityFilter struct {
	Source     models.Source
	Type       models.ActivityType
	IdentityID int64
	After      time.Time
	Before     time.Time
	Limit      int
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

// FindCommitsBySHAs returns git commit activities whose source_id ends with one of the given SHAs.
// This enables PR↔commit linking: PRs store commit SHAs in metadata, and this function
// resolves them to actual stored commit activities.
func (db *DB) FindCommitsBySHAs(shas []string) (map[string]models.Activity, error) {
	if len(shas) == 0 {
		return nil, nil
	}

	// Build query with OR conditions for each SHA suffix.
	query := `SELECT id, source, source_id, COALESCE(identity_id, 0), type, title, COALESCE(content, ''), COALESCE(metadata, ''), timestamp
		FROM activities WHERE type = 'commit' AND (`
	var args []any
	for i, sha := range shas {
		if i > 0 {
			query += " OR "
		}
		query += "source_id LIKE ?"
		args = append(args, "%:"+sha)
	}
	query += ")"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query commits by SHA: %w", err)
	}
	defer rows.Close()

	activities, err := scanActivities(rows)
	if err != nil {
		return nil, err
	}

	// Index by SHA (last part of source_id after the final colon).
	result := make(map[string]models.Activity, len(activities))
	for _, a := range activities {
		for i := len(a.SourceID) - 1; i >= 0; i-- {
			if a.SourceID[i] == ':' {
				sha := a.SourceID[i+1:]
				result[sha] = a
				break
			}
		}
	}
	return result, nil
}

// CountActivitiesBySource returns the total number of activities for each source.
func (db *DB) CountActivitiesBySource() (map[string]int, error) {
	rows, err := db.Query("SELECT source, COUNT(*) FROM activities GROUP BY source")
	if err != nil {
		return nil, fmt.Errorf("count activities: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var source string
		var count int
		if err := rows.Scan(&source, &count); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}
		counts[source] = count
	}
	return counts, rows.Err()
}

// FTSMatch is an FTS5 search result with BM25 relevance score.
type FTSMatch struct {
	Activity models.Activity
	Rank     float64 // BM25 rank (lower = more relevant; negated to higher = better)
}

// sanitizeFTS wraps each token in double quotes so that special FTS5
// characters (-, *, etc.) are treated as literals.
func sanitizeFTS(query string) string {
	tokens := strings.Fields(query)
	for i, t := range tokens {
		// Strip existing quotes, then re-quote.
		t = strings.ReplaceAll(t, `"`, ``)
		if t != "" {
			tokens[i] = `"` + t + `"`
		}
	}
	return strings.Join(tokens, " ")
}

// SearchFTS performs full-text keyword search using the FTS5 index.
// Returns activities matching the query, scored by BM25 relevance.
func (db *DB) SearchFTS(query string, filter ActivityFilter, limit int) ([]FTSMatch, error) {
	if query == "" {
		return nil, nil
	}
	query = sanitizeFTS(query)
	if limit <= 0 {
		limit = 20
	}

	sqlQuery := `
		SELECT a.id, a.source, a.source_id, COALESCE(a.identity_id, 0),
			a.type, a.title, COALESCE(a.content, ''), COALESCE(a.metadata, ''), a.timestamp,
			rank
		FROM activities_fts
		JOIN activities a ON a.id = activities_fts.rowid
		WHERE activities_fts MATCH ?`
	args := []any{query}

	if filter.Source != "" {
		sqlQuery += " AND a.source = ?"
		args = append(args, filter.Source)
	}
	if filter.Type != "" {
		sqlQuery += " AND a.type = ?"
		args = append(args, filter.Type)
	}
	if !filter.After.IsZero() {
		sqlQuery += " AND a.timestamp >= ?"
		args = append(args, filter.After.UTC().Format(time.RFC3339))
	}
	if !filter.Before.IsZero() {
		sqlQuery += " AND a.timestamp < ?"
		args = append(args, filter.Before.UTC().Format(time.RFC3339))
	}
	if filter.IdentityID > 0 {
		sqlQuery += " AND a.identity_id = ?"
		args = append(args, filter.IdentityID)
	}

	sqlQuery += " ORDER BY rank LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	var results []FTSMatch
	for rows.Next() {
		var a models.Activity
		var ts string
		var rank float64
		err := rows.Scan(&a.ID, &a.Source, &a.SourceID, &a.IdentityID,
			&a.Type, &a.Title, &a.Content, &a.Metadata, &ts, &rank)
		if err != nil {
			return nil, fmt.Errorf("scan fts: %w", err)
		}
		a.Timestamp, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return nil, fmt.Errorf("parse timestamp: %w", err)
		}
		results = append(results, FTSMatch{Activity: a, Rank: rank})
	}
	return results, rows.Err()
}

// ActivityStats holds aggregate information about stored activities.
type ActivityStats struct {
	TotalCount   int
	BySource     map[string]int
	EmbeddedCount int
	OldestTime   time.Time
	NewestTime   time.Time
}

// Stats returns aggregate statistics about all stored activities.
func (db *DB) Stats() (*ActivityStats, error) {
	s := &ActivityStats{BySource: make(map[string]int)}

	// Count by source.
	counts, err := db.CountActivitiesBySource()
	if err != nil {
		return nil, err
	}
	s.BySource = counts
	for _, c := range counts {
		s.TotalCount += c
	}

	// Date range.
	row := db.QueryRow("SELECT MIN(timestamp), MAX(timestamp) FROM activities")
	var minTS, maxTS sql.NullString
	if err := row.Scan(&minTS, &maxTS); err != nil {
		return nil, fmt.Errorf("date range: %w", err)
	}
	if minTS.Valid {
		s.OldestTime, _ = time.Parse(time.RFC3339, minTS.String)
	}
	if maxTS.Valid {
		s.NewestTime, _ = time.Parse(time.RFC3339, maxTS.String)
	}

	// Embedding count.
	embCount, err := db.EmbeddingCount()
	if err != nil {
		return nil, err
	}
	s.EmbeddedCount = embCount

	return s, nil
}

// GetActivitiesByIDs returns activities matching the given IDs, preserving order.
func (db *DB) GetActivitiesByIDs(ids []int64) ([]models.Activity, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT id, source, source_id, COALESCE(identity_id, 0), type, title, COALESCE(content, ''), COALESCE(metadata, ''), timestamp
		FROM activities WHERE id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("get activities by ids: %w", err)
	}
	defer rows.Close()

	return scanActivities(rows)
}

// FindByIssueKeys returns activities whose metadata references any of the
// given ticket keys, either via the singular `issue_key` field (Jira) or as
// an element of the `issue_keys` array (git, GitHub, GitLab, Bitbucket,
// Linear, Confluence). Excludes excludeID so callers can ask for "related to
// this row, but not this row itself." Sorted by timestamp DESC.
func (db *DB) FindByIssueKeys(keys []string, excludeID int64, limit int) ([]models.Activity, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	keysJSON, err := json.Marshal(keys)
	if err != nil {
		return nil, fmt.Errorf("marshal keys: %w", err)
	}

	// Some rows have metadata="" (older inserts) or NULL — json_extract on
	// either errors with "malformed JSON". Use json_valid as a guard and fall
	// back to an empty object before any json_extract calls.
	const query = `
		WITH safe AS (
		  SELECT a.id, a.source, a.source_id, COALESCE(a.identity_id, 0) AS identity_id,
		         a.type, a.title, COALESCE(a.content, '') AS content,
		         COALESCE(a.metadata, '') AS metadata, a.timestamp,
		         CASE WHEN json_valid(a.metadata) THEN a.metadata ELSE '{}' END AS m
		  FROM activities a
		  WHERE a.id != ?
		)
		SELECT id, source, source_id, identity_id, type, title, content, metadata, timestamp
		FROM safe
		WHERE
		  json_extract(m, '$.issue_key') IN (SELECT value FROM json_each(?))
		  OR EXISTS (
		    SELECT 1
		    FROM json_each(COALESCE(json_extract(m, '$.issue_keys'), '[]')) j
		    WHERE j.value IN (SELECT value FROM json_each(?))
		  )
		ORDER BY timestamp DESC
		LIMIT ?`

	rows, err := db.Query(query, excludeID, string(keysJSON), string(keysJSON), limit)
	if err != nil {
		return nil, fmt.Errorf("find by issue keys: %w", err)
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
