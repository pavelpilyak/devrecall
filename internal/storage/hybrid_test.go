package storage

import (
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// The vector arm is exercised with hand-placed vectors: the query vector points
// along dimension 0, so a candidate's similarity is controlled by how much of
// its own weight sits on that axis.
func queryVec() []float32 { return pad384([]float32{1, 0}) }

func TestHybridSearch_FusesBothArms(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()

	// Matches the keyword query only.
	kwOnly, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:kw", Type: models.TypeCommit,
		Title: "Fix auth token refresh", Timestamp: now,
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	// Matches the vector query only (no shared keywords).
	vecOnly, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:vec", Type: models.TypeCommit,
		Title: "Rotate expired credentials", Timestamp: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	if err := db.InsertEmbedding(vecOnly, "test", pad384([]float32{1, 0})); err != nil {
		t.Fatalf("InsertEmbedding: %v", err)
	}

	got, err := db.HybridSearch("auth token", queryVec(), ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}

	seen := map[int64]HybridMatch{}
	for _, m := range got {
		seen[m.Activity.ID] = m
	}
	// The whole point of fusion: an item found by only one arm still surfaces.
	if _, ok := seen[kwOnly]; !ok {
		t.Error("keyword-only match missing from fused results")
	}
	if _, ok := seen[vecOnly]; !ok {
		t.Error("vector-only match missing from fused results")
	}
	if m := seen[kwOnly]; m.FTSRank == 0 {
		t.Error("keyword-only match should carry an FTS rank")
	}
	if m := seen[vecOnly]; m.VecRank == 0 {
		t.Error("vector-only match should carry a vector rank")
	}
}

func TestHybridSearch_BothArmsOutranksSingleArm(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()

	// Found by keyword *and* vector — should win on the summed RRF score.
	both, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:both", Type: models.TypeCommit,
		Title: "Fix auth token refresh", Timestamp: now,
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	if err := db.InsertEmbedding(both, "test", pad384([]float32{1, 0})); err != nil {
		t.Fatalf("InsertEmbedding: %v", err)
	}
	// Found by keyword only.
	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:kw", Type: models.TypeCommit,
		Title: "Document auth token rotation", Timestamp: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}

	got, err := db.HybridSearch("auth token", queryVec(), ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("got %d results, want at least 2", len(got))
	}
	if got[0].Activity.ID != both {
		t.Errorf("top result = %d, want %d (matched by both arms)", got[0].Activity.ID, both)
	}
	if got[0].FTSRank == 0 || got[0].VecRank == 0 {
		t.Errorf("top result should be ranked by both arms, got fts=%d vec=%d",
			got[0].FTSRank, got[0].VecRank)
	}
}

// The vector arm only understands the date range, so source/type/identity/tag
// have to be enforced by the fusion layer. Without that, a vector hit could slip
// past a filter the keyword arm honours.
func TestHybridSearch_FilterAppliesToVectorArm(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()

	slackHit, err := db.InsertActivity(models.Activity{
		Source: models.SourceSlack, SourceID: "s:1", Type: models.TypeMessage,
		Title: "Rotate expired credentials", Timestamp: now,
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	if err := db.InsertEmbedding(slackHit, "test", pad384([]float32{1, 0})); err != nil {
		t.Fatalf("InsertEmbedding: %v", err)
	}

	got, err := db.HybridSearch("", queryVec(), ActivityFilter{Source: models.SourceGit}, 10)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	for _, m := range got {
		if m.Activity.ID == slackHit {
			t.Error("slack activity leaked through a git-only filter on the vector arm")
		}
	}
}

func TestHybridSearch_DegradesToSingleArm(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()

	id, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit,
		Title: "Fix auth token refresh", Timestamp: now,
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}

	// No embeddings stored anywhere: keyword-only must still work, which is the
	// common case on a fresh install before the embedding pass has run.
	got, err := db.HybridSearch("auth token", nil, ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("HybridSearch (keyword only): %v", err)
	}
	if len(got) == 0 || got[0].Activity.ID != id {
		t.Fatalf("keyword-only search did not return the match")
	}

	// Empty query with a vector is the mirror case.
	if err := db.InsertEmbedding(id, "test", pad384([]float32{1, 0})); err != nil {
		t.Fatalf("InsertEmbedding: %v", err)
	}
	got, err = db.HybridSearch("", queryVec(), ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("HybridSearch (vector only): %v", err)
	}
	if len(got) == 0 || got[0].Activity.ID != id {
		t.Fatalf("vector-only search did not return the match")
	}
}

func TestHasEmbeddings(t *testing.T) {
	db := mustOpen(t)
	if db.HasEmbeddings() {
		t.Error("HasEmbeddings() = true on an empty database, want false")
	}

	id := insertTestActivity(t, db, "repo:a", "Fix auth", time.Now().UTC())
	if err := db.InsertEmbedding(id, "test", pad384([]float32{1, 0})); err != nil {
		t.Fatalf("InsertEmbedding: %v", err)
	}
	if !db.HasEmbeddings() {
		t.Error("HasEmbeddings() = false after storing one, want true")
	}
}

func TestHybridSearch_EmptyInputs(t *testing.T) {
	db := mustOpen(t)
	got, err := db.HybridSearch("", nil, ActivityFilter{}, 10)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results for an empty query, want 0", len(got))
	}
}

func TestHybridSearch_RespectsLimit(t *testing.T) {
	db := mustOpen(t)
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if _, err := db.InsertActivity(models.Activity{
			Source: models.SourceGit, SourceID: string(rune('a'+i)) + ":auth",
			Type: models.TypeCommit, Title: "Fix auth token refresh",
			Timestamp: now.Add(-time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("InsertActivity: %v", err)
		}
	}

	got, err := db.HybridSearch("auth token", nil, ActivityFilter{}, 3)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d results, want 3", len(got))
	}
}
