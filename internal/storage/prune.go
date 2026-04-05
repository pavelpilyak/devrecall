package storage

import (
	"fmt"
	"time"
)

// PruneResult holds the counts of rows deleted by a prune operation.
type PruneResult struct {
	Activities int
	Summaries  int
}

// PruneActivities deletes activities with a timestamp before the cutoff.
// Embeddings are cleaned up automatically via ON DELETE CASCADE.
// FTS entries are cleaned up via the AFTER DELETE trigger.
func (db *DB) PruneActivities(before time.Time) (int, error) {
	res, err := db.Exec(
		"DELETE FROM activities WHERE timestamp < ?",
		before.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("prune activities: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PruneSummaries deletes summaries with period_start before the cutoff.
// If keepTypes is non-empty, summaries of those types are preserved.
func (db *DB) PruneSummaries(before time.Time, keepTypes []string) (int, error) {
	query := "DELETE FROM summaries WHERE period_start < ?"
	args := []any{before.Format("2006-01-02")}

	for _, kt := range keepTypes {
		query += " AND period_type != ?"
		args = append(args, kt)
	}

	res, err := db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("prune summaries: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CountActivitiesBefore returns the number of activities older than the given time.
func (db *DB) CountActivitiesBefore(before time.Time) (int, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM activities WHERE timestamp < ?",
		before.UTC().Format(time.RFC3339),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count old activities: %w", err)
	}
	return count, nil
}

// CountSummariesBefore returns the number of summaries with period_start before
// the given time, excluding the specified types.
func (db *DB) CountSummariesBefore(before time.Time, keepTypes []string) (int, error) {
	query := "SELECT COUNT(*) FROM summaries WHERE period_start < ?"
	args := []any{before.Format("2006-01-02")}

	for _, kt := range keepTypes {
		query += " AND period_type != ?"
		args = append(args, kt)
	}

	var count int
	err := db.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count old summaries: %w", err)
	}
	return count, nil
}
