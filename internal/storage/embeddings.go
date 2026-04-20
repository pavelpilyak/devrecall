package storage

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/pavelpilyak/devrecall/pkg/models"
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

// InsertEmbedding stores a vector embedding for an activity.
// On conflict (same activity_id), the existing row is updated.
// Writes to both the embeddings metadata table and the vec0 index.
func (db *DB) InsertEmbedding(activityID int64, model string, vec []float32) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	blob := float32sToBytes(vec)
	_, err = tx.Exec(`
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

	// Upsert into vec0 index: delete then insert (vec0 doesn't support ON CONFLICT).
	tx.Exec("DELETE FROM vec_activities WHERE activity_id = ?", activityID)
	serialized, err := sqlite_vec.SerializeFloat32(vec)
	if err != nil {
		return fmt.Errorf("serialize vec: %w", err)
	}
	_, err = tx.Exec("INSERT INTO vec_activities(activity_id, embedding) VALUES (?, ?)", activityID, serialized)
	if err != nil {
		return fmt.Errorf("insert vec: %w", err)
	}

	return tx.Commit()
}

// InsertEmbeddings stores a batch of embeddings in a single transaction.
func (db *DB) InsertEmbeddings(items []Embedding) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	embStmt, err := tx.Prepare(`
		INSERT INTO embeddings (activity_id, model, dimensions, vector)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(activity_id) DO UPDATE SET
			model      = excluded.model,
			dimensions = excluded.dimensions,
			vector     = excluded.vector,
			created_at = datetime('now')`)
	if err != nil {
		return 0, fmt.Errorf("prepare embeddings: %w", err)
	}
	defer embStmt.Close()

	delStmt, err := tx.Prepare("DELETE FROM vec_activities WHERE activity_id = ?")
	if err != nil {
		return 0, fmt.Errorf("prepare vec delete: %w", err)
	}
	defer delStmt.Close()

	vecStmt, err := tx.Prepare("INSERT INTO vec_activities(activity_id, embedding) VALUES (?, ?)")
	if err != nil {
		return 0, fmt.Errorf("prepare vec insert: %w", err)
	}
	defer vecStmt.Close()

	inserted := 0
	for _, e := range items {
		blob := float32sToBytes(e.Vector)
		if _, err := embStmt.Exec(e.ActivityID, e.Model, len(e.Vector), blob); err != nil {
			return inserted, fmt.Errorf("insert embedding for activity %d: %w", e.ActivityID, err)
		}
		delStmt.Exec(e.ActivityID)
		serialized, err := sqlite_vec.SerializeFloat32(e.Vector)
		if err != nil {
			return inserted, fmt.Errorf("serialize vec for activity %d: %w", e.ActivityID, err)
		}
		if _, err := vecStmt.Exec(e.ActivityID, serialized); err != nil {
			return inserted, fmt.Errorf("insert vec for activity %d: %w", e.ActivityID, err)
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

// SearchSimilar performs KNN vector search using sqlite-vec's vec0 index.
// Returns the top-K activities with highest cosine similarity, optionally filtered by time range.
func (db *DB) SearchSimilar(queryVec []float32, limit int, after, before time.Time) ([]VectorMatch, error) {
	if limit <= 0 {
		limit = 10
	}

	serialized, err := sqlite_vec.SerializeFloat32(queryVec)
	if err != nil {
		return nil, fmt.Errorf("serialize query vec: %w", err)
	}

	// If we have date filters, we need to post-filter after KNN.
	// Fetch more candidates than needed to account for filtering.
	fetchLimit := limit
	hasDateFilter := !after.IsZero() || !before.IsZero()
	if hasDateFilter {
		fetchLimit = limit * 5
		if fetchLimit < 50 {
			fetchLimit = 50
		}
	}

	// KNN query via vec0 virtual table. distance is L2 by default.
	// We convert to cosine similarity score afterward.
	query := `
		SELECT v.activity_id, v.distance,
			a.id, a.source, a.source_id, COALESCE(a.identity_id, 0),
			a.type, a.title, COALESCE(a.content, ''), COALESCE(a.metadata, ''), a.timestamp
		FROM vec_activities v
		JOIN activities a ON a.id = v.activity_id
		WHERE v.embedding MATCH ? AND k = ?`
	args := []any{serialized, fetchLimit}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("vec search: %w", err)
	}
	defer rows.Close()

	var matches []VectorMatch
	for rows.Next() {
		var distance float64
		var a models.Activity
		var ts string
		var vecActivityID int64
		err := rows.Scan(&vecActivityID, &distance,
			&a.ID, &a.Source, &a.SourceID, &a.IdentityID,
			&a.Type, &a.Title, &a.Content, &a.Metadata, &ts)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		a.Timestamp, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return nil, fmt.Errorf("parse timestamp: %w", err)
		}

		// Apply date filters.
		if !after.IsZero() && a.Timestamp.Before(after) {
			continue
		}
		if !before.IsZero() && !a.Timestamp.Before(before) {
			continue
		}

		// Convert L2 distance to a similarity score (smaller distance = higher score).
		// Score = 1 / (1 + distance) gives a 0..1 range.
		score := 1.0 / (1.0 + distance)
		matches = append(matches, VectorMatch{Activity: a, Score: score})

		if len(matches) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return matches, nil
}

// EmbeddingCount returns the total number of stored embeddings.
func (db *DB) EmbeddingCount() (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM embeddings").Scan(&count)
	return count, err
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
