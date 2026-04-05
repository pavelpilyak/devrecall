package rag

import (
	"context"
	"testing"
	"time"

	"github.com/pavelpiliak/devrecall/internal/storage"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// mockEmbedder returns a fixed vector for any input.
type mockEmbedder struct {
	vec []float32
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, nil
}
func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range texts {
		vecs[i] = m.vec
	}
	return vecs, nil
}
func (m *mockEmbedder) Dimensions() int { return len(m.vec) }
func (m *mockEmbedder) Name() string    { return "mock" }

// pad384 pads a short vector to 384 dimensions.
func pad384(v []float32) []float32 {
	out := make([]float32, 384)
	copy(out, v)
	return out
}

func mustOpenDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertActivity(t *testing.T, db *storage.DB, source models.Source, sourceID, title, content string, ts time.Time) int64 {
	t.Helper()
	id, err := db.InsertActivity(models.Activity{
		Source:    source,
		SourceID:  sourceID,
		Type:      models.TypeCommit,
		Title:     title,
		Content:   content,
		Timestamp: ts,
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	return id
}

func TestHybridRetriever_MergesVecAndFTS(t *testing.T) {
	db := mustOpenDB(t)
	now := time.Now().UTC()

	// Activity 1: matches both vector AND keyword "auth".
	id1 := insertActivity(t, db, models.SourceGit, "r:a1",
		"Fix auth token refresh", "Handle expired tokens", now)

	// Activity 2: matches keyword "auth" but not vector (different embedding).
	insertActivity(t, db, models.SourceSlack, "s:m1",
		"Discussion about auth flow", "Token expiry issue", now.Add(-time.Hour))

	// Activity 3: matches vector but not keyword "auth".
	id3 := insertActivity(t, db, models.SourceGit, "r:a3",
		"Update login session handling", "Refresh logic", now.Add(-2*time.Hour))

	// Embed activities with known vectors.
	// Query vector will be [1,0,0,...], so id1 and id3 are closest.
	db.InsertEmbedding(id1, "mock", pad384([]float32{0.9, 0.1, 0.0}))
	// id2 has no embedding (not all activities need one).
	db.InsertEmbedding(id3, "mock", pad384([]float32{0.8, 0.2, 0.0}))

	embedder := &mockEmbedder{vec: pad384([]float32{1.0, 0.0, 0.0})}
	retriever := NewHybridRetriever(db, embedder)

	results, err := retriever.Retrieve(context.Background(), "auth", 10)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("got %d results, want at least 2", len(results))
	}

	// Activity 1 should rank highest: it appears in BOTH vector and FTS results.
	if results[0].Activity.ID != id1 {
		t.Errorf("top result = activity %d (%q), want %d (auth token refresh)",
			results[0].Activity.ID, results[0].Activity.Title, id1)
	}
}

func TestHybridRetriever_SourceFilter(t *testing.T) {
	db := mustOpenDB(t)
	now := time.Now().UTC()

	id1 := insertActivity(t, db, models.SourceGit, "r:a1",
		"Fix auth bug in git", "", now)
	insertActivity(t, db, models.SourceSlack, "s:m1",
		"Fix auth bug in slack", "", now)

	db.InsertEmbedding(id1, "mock", pad384([]float32{0.9, 0.1}))

	embedder := &mockEmbedder{vec: pad384([]float32{1.0, 0.0})}
	retriever := NewHybridRetriever(db, embedder)

	results, err := retriever.RetrieveWithFilters(context.Background(), "auth", 10,
		QueryFilters{Source: models.SourceGit})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	for _, r := range results {
		if r.Activity.Source != models.SourceGit {
			t.Errorf("got source %q, want git", r.Activity.Source)
		}
	}
}

func TestHybridRetriever_DateFilter(t *testing.T) {
	db := mustOpenDB(t)
	old := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	insertActivity(t, db, models.SourceGit, "r:a1",
		"Fix auth old", "", old)
	id2 := insertActivity(t, db, models.SourceGit, "r:a2",
		"Fix auth recent", "", recent)

	db.InsertEmbedding(id2, "mock", pad384([]float32{0.9, 0.1}))

	embedder := &mockEmbedder{vec: pad384([]float32{1.0, 0.0})}
	retriever := NewHybridRetriever(db, embedder)

	after := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	results, err := retriever.RetrieveWithFilters(context.Background(), "auth", 10,
		QueryFilters{After: after})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	for _, r := range results {
		if r.Activity.Timestamp.Before(after) {
			t.Errorf("activity %d has timestamp %v, want >= %v",
				r.Activity.ID, r.Activity.Timestamp, after)
		}
	}
}

func TestHybridRetriever_FTSOnlyWhenNoEmbeddings(t *testing.T) {
	db := mustOpenDB(t)
	now := time.Now().UTC()

	insertActivity(t, db, models.SourceGit, "r:a1",
		"Fix auth token refresh", "", now)

	// No embeddings stored — should still work via FTS only.
	embedder := &mockEmbedder{vec: pad384([]float32{1.0})}
	retriever := NewHybridRetriever(db, embedder)

	results, err := retriever.Retrieve(context.Background(), "auth", 10)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 from FTS", len(results))
	}
	if results[0].Activity.Title != "Fix auth token refresh" {
		t.Errorf("title = %q", results[0].Activity.Title)
	}
}

func TestReciprocalRankFusion_RanksDoubleAppearanceHigher(t *testing.T) {
	// Activity A appears at rank 0 in both lists.
	// Activity B appears at rank 0 in vec only.
	// Activity C appears at rank 0 in FTS only.
	// A should score highest.
	actA := models.Activity{ID: 1, Title: "A"}
	actB := models.Activity{ID: 2, Title: "B"}
	actC := models.Activity{ID: 3, Title: "C"}

	vecResults := []storage.VectorMatch{
		{Activity: actA, Score: 0.9},
		{Activity: actB, Score: 0.8},
	}
	ftsResults := []storage.FTSMatch{
		{Activity: actA, Rank: -10},
		{Activity: actC, Rank: -5},
	}

	results := reciprocalRankFusion(vecResults, ftsResults, "", 0)

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Activity.ID != 1 {
		t.Errorf("top = activity %d, want 1 (appears in both)", results[0].Activity.ID)
	}
	// A's score should be higher than B's and C's.
	if results[0].Score <= results[1].Score {
		t.Errorf("A score (%f) should be > B/C score (%f)", results[0].Score, results[1].Score)
	}
}
