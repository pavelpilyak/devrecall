package embedding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

const (
	defaultONNXModel = "sentence-transformers/all-MiniLM-L6-v2"
	onnxDimensions   = 384
	pipelineName     = "devrecall-embeddings"
)

// ONNX generates embeddings using a bundled ONNX model via hugot's pure Go backend.
// No external dependencies required — works offline, no Ollama, no API keys.
type ONNX struct {
	modelPath  string
	session    *hugot.Session
	pipeline   *pipelines.FeatureExtractionPipeline
	dimensions int
	mu         sync.Mutex
}

// NewONNX creates an ONNX embedder. modelDir is where models are stored
// (typically ~/.devrecall/models/). The model is downloaded on first use.
func NewONNX(modelDir string) *ONNX {
	return &ONNX{
		modelPath:  modelDir,
		dimensions: onnxDimensions,
	}
}

func (o *ONNX) Name() string    { return "onnx" }
func (o *ONNX) Dimensions() int { return o.dimensions }

// init lazily initializes the session and pipeline on first use.
func (o *ONNX) init() error {
	if o.pipeline != nil {
		return nil
	}

	// Ensure model is downloaded.
	modelPath, err := o.ensureModel()
	if err != nil {
		return fmt.Errorf("model setup: %w", err)
	}

	// Create pure Go session (no CGO, no onnxruntime shared lib).
	session, err := hugot.NewGoSession()
	if err != nil {
		return fmt.Errorf("hugot session: %w", err)
	}

	config := hugot.FeatureExtractionConfig{
		ModelPath: modelPath,
		Name:      pipelineName,
		Options:   []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
	}

	pipeline, err := hugot.NewPipeline[*pipelines.FeatureExtractionPipeline](session, config)
	if err != nil {
		session.Destroy()
		return fmt.Errorf("pipeline init: %w", err)
	}

	o.session = session
	o.pipeline = pipeline
	return nil
}

func (o *ONNX) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (o *ONNX) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if err := o.init(); err != nil {
		return nil, err
	}

	output, err := o.pipeline.RunPipeline(texts)
	if err != nil {
		return nil, fmt.Errorf("onnx inference: %w", err)
	}

	return output.Embeddings, nil
}

// Close releases the ONNX session resources.
func (o *ONNX) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.session != nil {
		err := o.session.Destroy()
		o.session = nil
		o.pipeline = nil
		return err
	}
	return nil
}

// ensureModel downloads the model if not already present.
func (o *ONNX) ensureModel() (string, error) {
	if err := os.MkdirAll(o.modelPath, 0o755); err != nil {
		return "", fmt.Errorf("create model dir: %w", err)
	}

	// Check if model already exists.
	modelDir := filepath.Join(o.modelPath, "sentence-transformers_all-MiniLM-L6-v2")
	if _, err := os.Stat(filepath.Join(modelDir, "tokenizer.json")); err == nil {
		return modelDir, nil
	}

	// Download from Hugging Face.
	downloadedPath, err := hugot.DownloadModel(defaultONNXModel, o.modelPath, hugot.NewDownloadOptions())
	if err != nil {
		return "", fmt.Errorf("download model %s: %w", defaultONNXModel, err)
	}
	return downloadedPath, nil
}
