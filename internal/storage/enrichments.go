package storage

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Enrichment provenance markers stored in the model column.
const (
	EnrichmentModelDeterministic = "deterministic" // rule-based, no LLM call
	EnrichmentModelFallback      = "fallback"      // LLM output unusable; placeholder written
)

// Enrichment is the LLM-digested view of an activity: a one-line factual
// digest plus classification tags.
type Enrichment struct {
	ActivityID int64
	Digest     string
	Tags       []string
	Entities   string // raw JSON, e.g. {"people":[],"systems":[]}; empty if none
	Model      string // provider name, or 'deterministic' / 'fallback'
}

// InsertEnrichments upserts a batch of enrichments in a single transaction.
func (db *DB) InsertEnrichments(items []Enrichment) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO enrichments (activity_id, digest, tags, entities, model)
		VALUES (?, ?, ?, NULLIF(?, ''), ?)
		ON CONFLICT(activity_id) DO UPDATE SET
			digest     = excluded.digest,
			tags       = excluded.tags,
			entities   = excluded.entities,
			model      = excluded.model,
			created_at = datetime('now')`)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	inserted := 0
	for _, e := range items {
		tags := e.Tags
		if tags == nil {
			tags = []string{}
		}
		tagsJSON, err := json.Marshal(tags)
		if err != nil {
			return inserted, fmt.Errorf("marshal tags for activity %d: %w", e.ActivityID, err)
		}
		if _, err := stmt.Exec(e.ActivityID, e.Digest, string(tagsJSON), e.Entities, e.Model); err != nil {
			return inserted, fmt.Errorf("insert enrichment for activity %d: %w", e.ActivityID, err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return inserted, nil
}

// ListUnenrichedActivityIDs returns IDs of activities without an enrichment
// row yet, newest first, so recent activity is digested before backlog.
func (db *DB) ListUnenrichedActivityIDs(limit int) ([]int64, error) {
	query := `SELECT a.id FROM activities a
		LEFT JOIN enrichments e ON a.id = e.activity_id
		WHERE e.activity_id IS NULL
		ORDER BY a.timestamp DESC`
	var args []any
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query unenriched: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetEnrichmentsByActivityIDs returns enrichments keyed by activity ID.
// Activities without enrichment are absent from the map.
func (db *DB) GetEnrichmentsByActivityIDs(ids []int64) (map[int64]Enrichment, error) {
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
		SELECT activity_id, digest, tags, COALESCE(entities, ''), model
		FROM enrichments WHERE activity_id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("get enrichments: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]Enrichment)
	for rows.Next() {
		var e Enrichment
		var tagsJSON string
		if err := rows.Scan(&e.ActivityID, &e.Digest, &tagsJSON, &e.Entities, &e.Model); err != nil {
			return nil, fmt.Errorf("scan enrichment: %w", err)
		}
		json.Unmarshal([]byte(tagsJSON), &e.Tags)
		result[e.ActivityID] = e
	}
	return result, rows.Err()
}
