package storage

import (
	"fmt"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

// InsertSummary stores a new periodic summary. If a summary for the same
// period_type and period_start already exists, it is replaced.
func (db *DB) InsertSummary(s models.Summary) (int64, error) {
	res, err := db.Exec(`
		INSERT OR REPLACE INTO summaries (period_type, period_start, period_end, summary_text, highlights, activity_count)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.PeriodType, s.PeriodStart, s.PeriodEnd, s.SummaryText, s.Highlights, s.ActivityCount,
	)
	if err != nil {
		return 0, fmt.Errorf("insert summary: %w", err)
	}
	return res.LastInsertId()
}

// GetSummary returns a summary by period type and start date.
func (db *DB) GetSummary(periodType, periodStart string) (*models.Summary, error) {
	row := db.QueryRow(`
		SELECT id, period_type, period_start, period_end, summary_text, COALESCE(highlights, ''), activity_count
		FROM summaries WHERE period_type = ? AND period_start = ?`,
		periodType, periodStart,
	)

	var s models.Summary
	err := row.Scan(&s.ID, &s.PeriodType, &s.PeriodStart, &s.PeriodEnd,
		&s.SummaryText, &s.Highlights, &s.ActivityCount)
	if err != nil {
		return nil, fmt.Errorf("get summary: %w", err)
	}
	return &s, nil
}

// ListSummaries returns summaries matching the given period type, ordered by period_start descending.
// If periodType is empty, all summaries are returned.
func (db *DB) ListSummaries(periodType string, limit int) ([]models.Summary, error) {
	if limit <= 0 {
		limit = 50
	}

	query := "SELECT id, period_type, period_start, period_end, summary_text, COALESCE(highlights, ''), activity_count FROM summaries"
	var args []any

	if periodType != "" {
		query += " WHERE period_type = ?"
		args = append(args, periodType)
	}
	query += " ORDER BY period_start DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list summaries: %w", err)
	}
	defer rows.Close()

	var result []models.Summary
	for rows.Next() {
		var s models.Summary
		if err := rows.Scan(&s.ID, &s.PeriodType, &s.PeriodStart, &s.PeriodEnd,
			&s.SummaryText, &s.Highlights, &s.ActivityCount); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// MissingSummaryPeriods returns period_start dates that don't have a summary yet,
// for a given period type. It checks from the oldest activity date up to now.
func (db *DB) MissingSummaryPeriods(periodType string, since, until time.Time) ([]time.Time, error) {
	// Get existing summary starts for this period type.
	rows, err := db.Query(
		"SELECT period_start FROM summaries WHERE period_type = ? AND period_start >= ? AND period_start < ?",
		periodType, since.Format("2006-01-02"), until.Format("2006-01-02"),
	)
	if err != nil {
		return nil, fmt.Errorf("query existing summaries: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var ps string
		if err := rows.Scan(&ps); err != nil {
			return nil, err
		}
		existing[ps] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Generate all expected period starts between since and until.
	var missing []time.Time
	for cursor := since; cursor.Before(until); {
		key := cursor.Format("2006-01-02")
		if !existing[key] {
			missing = append(missing, cursor)
		}
		cursor = advancePeriod(cursor, periodType)
	}

	return missing, nil
}

// ListSummariesInRange returns summaries of a given type within a date range,
// ordered by period_start ascending.
func (db *DB) ListSummariesInRange(periodType string, after, before time.Time) ([]models.Summary, error) {
	rows, err := db.Query(`
		SELECT id, period_type, period_start, period_end, summary_text, COALESCE(highlights, ''), activity_count
		FROM summaries
		WHERE period_type = ? AND period_start >= ? AND period_start < ?
		ORDER BY period_start ASC`,
		periodType, after.Format("2006-01-02"), before.Format("2006-01-02"),
	)
	if err != nil {
		return nil, fmt.Errorf("list summaries in range: %w", err)
	}
	defer rows.Close()

	var result []models.Summary
	for rows.Next() {
		var s models.Summary
		if err := rows.Scan(&s.ID, &s.PeriodType, &s.PeriodStart, &s.PeriodEnd,
			&s.SummaryText, &s.Highlights, &s.ActivityCount); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// advancePeriod moves a date forward by one period.
func advancePeriod(t time.Time, periodType string) time.Time {
	switch periodType {
	case "daily":
		return t.AddDate(0, 0, 1)
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "monthly":
		return t.AddDate(0, 1, 0)
	case "quarterly":
		return t.AddDate(0, 3, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}
