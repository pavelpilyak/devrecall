package pipeline

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// stubEmbedder records the texts it was asked to embed.
type stubEmbedder struct {
	texts []string
}

func (s *stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := s.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (s *stubEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	s.texts = append(s.texts, texts...)
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, 384)
	}
	return vecs, nil
}

func (s *stubEmbedder) Dimensions() int { return 384 }
func (s *stubEmbedder) Name() string    { return "stub" }

func TestEmbedMissing_IncludesDigestAndTags(t *testing.T) {
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	id, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit,
		Title: "fix login", Content: "handle nil session",
		Timestamp: time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	if _, err := db.InsertEnrichments([]storage.Enrichment{
		{ActivityID: id, Digest: "Fixed a login crash on 401.", Tags: []string{"bugfix", "auth"}, Model: "m"},
	}); err != nil {
		t.Fatalf("InsertEnrichments: %v", err)
	}

	stub := &stubEmbedder{}
	if err := embedMissing(context.Background(), db, stub, io.Discard); err != nil {
		t.Fatalf("embedMissing: %v", err)
	}

	if len(stub.texts) != 1 {
		t.Fatalf("embedded %d texts, want 1", len(stub.texts))
	}
	want := "fix login handle nil session Fixed a login crash on 401. bugfix auth"
	if stub.texts[0] != want {
		t.Errorf("embed text = %q\nwant %q", stub.texts[0], want)
	}

	// Idempotent: second run embeds nothing.
	stub.texts = nil
	if err := embedMissing(context.Background(), db, stub, io.Discard); err != nil {
		t.Fatalf("embedMissing (2nd): %v", err)
	}
	if len(stub.texts) != 0 {
		t.Errorf("re-embedded %d texts, want 0", len(stub.texts))
	}
}

func TestEmbedMissing_NoEnrichmentFallsBackToRawText(t *testing.T) {
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	if _, err := db.InsertActivity(models.Activity{
		Source: models.SourceGit, SourceID: "r:1", Type: models.TypeCommit,
		Title: "fix login", Content: "handle nil session",
		Timestamp: time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}

	stub := &stubEmbedder{}
	if err := embedMissing(context.Background(), db, stub, io.Discard); err != nil {
		t.Fatalf("embedMissing: %v", err)
	}
	if len(stub.texts) != 1 || stub.texts[0] != "fix login handle nil session" {
		t.Errorf("embed texts = %v", stub.texts)
	}
}
