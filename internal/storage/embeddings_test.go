package storage

import (
	"math"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// pad384 pads a short vector to 384 dimensions (vec0 table requirement).
func pad384(v []float32) []float32 {
	out := make([]float32, 384)
	copy(out, v)
	return out
}

func insertTestActivity(t *testing.T, db *DB, sourceID, title string, ts time.Time) int64 {
	t.Helper()
	id, err := db.InsertActivity(models.Activity{
		Source:    models.SourceGit,
		SourceID:  sourceID,
		Type:      models.TypeCommit,
		Title:     title,
		Timestamp: ts,
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	return id
}

func TestInsertEmbedding(t *testing.T) {
	db := mustOpen(t)
	id := insertTestActivity(t, db, "repo:aaa", "Fix auth", time.Now().UTC())

	vec := pad384([]float32{0.1, 0.2, 0.3})
	if err := db.InsertEmbedding(id, "all-minilm", vec); err != nil {
		t.Fatalf("InsertEmbedding: %v", err)
	}

	count, err := db.EmbeddingCount()
	if err != nil {
		t.Fatalf("EmbeddingCount: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestInsertEmbedding_Upsert(t *testing.T) {
	db := mustOpen(t)
	id := insertTestActivity(t, db, "repo:aaa", "Fix auth", time.Now().UTC())

	if err := db.InsertEmbedding(id, "all-minilm", pad384([]float32{0.1, 0.2})); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Overwrite with different vector.
	if err := db.InsertEmbedding(id, "all-minilm", pad384([]float32{0.3, 0.4, 0.5})); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	count, _ := db.EmbeddingCount()
	if count != 1 {
		t.Errorf("count = %d after upsert, want 1", count)
	}
}

func TestInsertEmbeddings_Batch(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()
	id1 := insertTestActivity(t, db, "repo:a1", "Commit 1", now)
	id2 := insertTestActivity(t, db, "repo:a2", "Commit 2", now)

	items := []Embedding{
		{ActivityID: id1, Model: "all-minilm", Vector: pad384([]float32{0.1, 0.2, 0.3})},
		{ActivityID: id2, Model: "all-minilm", Vector: pad384([]float32{0.4, 0.5, 0.6})},
	}

	n, err := db.InsertEmbeddings(items)
	if err != nil {
		t.Fatalf("InsertEmbeddings: %v", err)
	}
	if n != 2 {
		t.Errorf("inserted = %d, want 2", n)
	}

	count, _ := db.EmbeddingCount()
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestListUnembeddedActivityIDs(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()
	id1 := insertTestActivity(t, db, "repo:a1", "Commit 1", now)
	id2 := insertTestActivity(t, db, "repo:a2", "Commit 2", now.Add(-time.Hour))

	// Embed only id1.
	db.InsertEmbedding(id1, "all-minilm", pad384([]float32{0.1}))

	ids, err := db.ListUnembeddedActivityIDs(0)
	if err != nil {
		t.Fatalf("ListUnembedded: %v", err)
	}
	if len(ids) != 1 || ids[0] != id2 {
		t.Errorf("got ids %v, want [%d]", ids, id2)
	}
}

func TestListUnembeddedActivityIDs_Limit(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()
	insertTestActivity(t, db, "repo:a1", "C1", now)
	insertTestActivity(t, db, "repo:a2", "C2", now.Add(-time.Hour))
	insertTestActivity(t, db, "repo:a3", "C3", now.Add(-2*time.Hour))

	ids, err := db.ListUnembeddedActivityIDs(2)
	if err != nil {
		t.Fatalf("ListUnembedded: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("got %d ids, want 2", len(ids))
	}
}

func TestSearchSimilar(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()
	id1 := insertTestActivity(t, db, "repo:a1", "Fix auth token refresh", now)
	id2 := insertTestActivity(t, db, "repo:a2", "Update README", now.Add(-time.Hour))
	id3 := insertTestActivity(t, db, "repo:a3", "Add login validation", now.Add(-2*time.Hour))

	// Embed with vectors where id1 is most similar to query, id3 somewhat, id2 least.
	db.InsertEmbedding(id1, "all-minilm", pad384([]float32{0.9, 0.1, 0.0}))
	db.InsertEmbedding(id2, "all-minilm", pad384([]float32{0.0, 0.1, 0.9}))
	db.InsertEmbedding(id3, "all-minilm", pad384([]float32{0.7, 0.3, 0.1}))

	queryVec := pad384([]float32{1.0, 0.0, 0.0})

	matches, err := db.SearchSimilar(queryVec, 2, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}

	// First match should be id1 (closest to [1,0,0,...]).
	if matches[0].Activity.ID != id1 {
		t.Errorf("top match = %d, want %d", matches[0].Activity.ID, id1)
	}
	if matches[1].Activity.ID != id3 {
		t.Errorf("second match = %d, want %d", matches[1].Activity.ID, id3)
	}

	// Scores should be in descending order.
	if matches[0].Score < matches[1].Score {
		t.Errorf("scores not descending: %f < %f", matches[0].Score, matches[1].Score)
	}
}

func TestSearchSimilar_DateFilter(t *testing.T) {
	db := mustOpen(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	id1 := insertTestActivity(t, db, "repo:a1", "Old commit", old)
	id2 := insertTestActivity(t, db, "repo:a2", "Recent commit", recent)

	db.InsertEmbedding(id1, "all-minilm", pad384([]float32{1.0, 0.0}))
	db.InsertEmbedding(id2, "all-minilm", pad384([]float32{0.9, 0.1}))

	// Search only for activities after Feb 2026.
	after := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	matches, err := db.SearchSimilar(pad384([]float32{1.0, 0.0}), 10, after, time.Time{})
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1 (only recent)", len(matches))
	}
	if matches[0].Activity.ID != id2 {
		t.Errorf("match = %d, want %d", matches[0].Activity.ID, id2)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1.0},
		{"similar", []float32{0.9, 0.1}, []float32{0.8, 0.2}, 0.9910},
		{"empty", []float32{}, []float32{}, 0.0},
		{"length mismatch", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
		{"zero vector", []float32{0, 0, 0}, []float32{1, 0, 0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("CosineSimilarity = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestFloat32Roundtrip(t *testing.T) {
	original := []float32{0.1, -0.5, 3.14, 0, 1e-6}
	blob := float32sToBytes(original)
	restored := bytesToFloat32s(blob)

	if len(restored) != len(original) {
		t.Fatalf("length = %d, want %d", len(restored), len(original))
	}
	for i := range original {
		if restored[i] != original[i] {
			t.Errorf("[%d] = %f, want %f", i, restored[i], original[i])
		}
	}
}
