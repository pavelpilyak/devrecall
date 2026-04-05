//go:build GO || ALL

package embedding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestONNX_Defaults(t *testing.T) {
	e := NewONNX("/tmp/models")
	if e.Name() != "onnx" {
		t.Errorf("Name() = %q", e.Name())
	}
	if e.Dimensions() != 384 {
		t.Errorf("Dimensions() = %d", e.Dimensions())
	}
}

// TestONNX_EmbedIntegration downloads the real model and runs inference.
// Skipped unless DEVRECALL_INTEGRATION=1 is set (the model is ~90MB).
func TestONNX_EmbedIntegration(t *testing.T) {
	if os.Getenv("DEVRECALL_INTEGRATION") != "1" {
		t.Skip("set DEVRECALL_INTEGRATION=1 to run (downloads ~90MB model)")
	}

	modelDir := filepath.Join(t.TempDir(), "models")
	e := NewONNX(modelDir)
	defer e.Close()

	vec, err := e.Embed(context.Background(), "Fix auth token refresh bug")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 384 {
		t.Errorf("len(vec) = %d, want 384", len(vec))
	}

	// Semantic similarity: similar texts should have higher similarity than unrelated.
	vecs, err := e.EmbedBatch(context.Background(), []string{
		"Fix authentication token refresh",
		"Update README with badges",
	})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len(vecs) = %d", len(vecs))
	}

	// "Fix auth token refresh bug" should be more similar to "Fix authentication token refresh"
	// than to "Update README with badges".
	simSimilar := cosine(vec, vecs[0])
	simUnrelated := cosine(vec, vecs[1])
	if simSimilar <= simUnrelated {
		t.Errorf("expected similar text to score higher: similar=%f, unrelated=%f", simSimilar, simUnrelated)
	}
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt(na) * sqrt(nb))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 100; i++ {
		z = (z + x/z) / 2
	}
	return z
}
