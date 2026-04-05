package storage

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

// Embedding is a stored vector embedding for an activity.
type Embedding struct {
	ActivityID int64
	Model      string
	Dimensions int
	Vector     []float32
}

// VectorMatch is a search result with cosine similarity score.
type VectorMatch struct {
	Activity models.Activity
	Score    float64 // cosine similarity, 0..1
}

// scored pairs an activity with its similarity score (internal use).
type scored struct {
	activity models.Activity
	score    float64
}

// InsertEmbedding stores a vector embedding for an activity.
// On conflict (same activity_id), the embedding is replaced.
func (db *DB) InsertEmbedding(activityID int64, model string, vec []float32) error {
	blob := float32sToBytes(vec)
	_, err := db.Exec(`
		INSERT INTO embeddings (activity_id, model, dimensions, vector)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(activity_id) DO UPDATE SET
			model      = excluded.model,
			dimensions = excluded.dimensions,
			vector     = excluded.vector,
			created_at = datetime('now')`,
		activityID, model, len(vec), blob,
	)
	if err != nil {
		return fmt.Errorf("insert embedding: %w", err)
	}
	return nil
}

// InsertEmbeddings stores a batch of embeddings in a single transaction.
func (db *DB) InsertEmbeddings(items []Embedding) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO embeddings (activity_id, model, dimensions, vector)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(activity_id) DO UPDATE SET
			model      = excluded.model,
			dimensions = excluded.dimensions,
			vector     = excluded.vector,
			created_at = datetime('now')`)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	inserted := 0
	for _, e := range items {
		blob := float32sToBytes(e.Vector)
		if _, err := stmt.Exec(e.ActivityID, e.Model, len(e.Vector), blob); err != nil {
			return inserted, fmt.Errorf("insert embedding for activity %d: %w", e.ActivityID, err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return inserted, nil
}

// ListUnembeddedActivityIDs returns IDs of activities that don't have embeddings yet.
func (db *DB) ListUnembeddedActivityIDs(limit int) ([]int64, error) {
	query := `SELECT a.id FROM activities a
		LEFT JOIN embeddings e ON a.id = e.activity_id
		WHERE e.activity_id IS NULL
		ORDER BY a.timestamp DESC`
	var args []any
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query unembedded: %w", err)
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

// SearchSimilar performs brute-force cosine similarity search against all stored embeddings.
// It returns the top-K activities with highest similarity, optionally filtered by time range.
// This will be replaced by sqlite-vss indexing in a future version.
func (db *DB) SearchSimilar(queryVec []float32, limit int, after, before time.Time) ([]VectorMatch, error) {
	query := `SELECT e.vector, a.id, a.source, a.source_id, COALESCE(a.identity_id, 0),
		a.type, a.title, COALESCE(a.content, ''), COALESCE(a.metadata, ''), a.timestamp
		FROM embeddings e
		JOIN activities a ON a.id = e.activity_id
		WHERE 1=1`
	var args []any

	if !after.IsZero() {
		query += " AND a.timestamp >= ?"
		args = append(args, after.UTC().Format(time.RFC3339))
	}
	if !before.IsZero() {
		query += " AND a.timestamp < ?"
		args = append(args, before.UTC().Format(time.RFC3339))
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query embeddings: %w", err)
	}
	defer rows.Close()

	// Scan all rows and compute similarity.
	var results []scored
	for rows.Next() {
		var blob []byte
		var a models.Activity
		var ts string
		err := rows.Scan(&blob, &a.ID, &a.Source, &a.SourceID, &a.IdentityID,
			&a.Type, &a.Title, &a.Content, &a.Metadata, &ts)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		a.Timestamp, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return nil, fmt.Errorf("parse timestamp: %w", err)
		}

		vec := bytesToFloat32s(blob)
		sim := CosineSimilarity(queryVec, vec)
		results = append(results, scored{activity: a, score: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by score descending (simple insertion sort for top-K).
	topK := topKScored(results, limit)

	matches := make([]VectorMatch, len(topK))
	for i, s := range topK {
		matches[i] = VectorMatch{Activity: s.activity, Score: s.score}
	}
	return matches, nil
}

// EmbeddingCount returns the total number of stored embeddings.
func (db *DB) EmbeddingCount() (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM embeddings").Scan(&count)
	return count, err
}

// topKScored returns the top-K items by score descending.
func topKScored(items []scored, k int) []scored {
	if k <= 0 || len(items) == 0 {
		return nil
	}
	if k > len(items) {
		k = len(items)
	}

	// Partial sort: find top-K using a simple selection approach.
	// Fine for thousands of embeddings; sqlite-vss will handle larger scales.
	for i := 0; i < k; i++ {
		maxIdx := i
		for j := i + 1; j < len(items); j++ {
			if items[j].score > items[maxIdx].score {
				maxIdx = j
			}
		}
		items[i], items[maxIdx] = items[maxIdx], items[i]
	}
	return items[:k]
}

// CosineSimilarity computes cosine similarity between two vectors.
// Returns 0 if either vector is zero-length or they differ in length.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// float32sToBytes converts a float32 slice to a little-endian byte slice.
func float32sToBytes(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// bytesToFloat32s converts a little-endian byte slice to a float32 slice.
func bytesToFloat32s(b []byte) []float32 {
	n := len(b) / 4
	v := make([]float32, n)
	for i := 0; i < n; i++ {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
