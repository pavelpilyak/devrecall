package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultOllamaURL = "http://localhost:11434"

// Ollama generates embeddings using a local Ollama instance.
// Supports all-MiniLM-L6-v2 (384 dimensions) and other embedding models.
type Ollama struct {
	baseURL    string
	model      string
	dimensions int
	client     *http.Client
}

// NewOllama creates an Ollama embedder.
// Default model is all-minilm (384 dimensions).
func NewOllama(baseURL, model string, dimensions int) *Ollama {
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}
	if model == "" {
		model = "all-minilm"
	}
	if dimensions <= 0 {
		dimensions = 384
	}
	return &Ollama{
		baseURL:    baseURL,
		model:      model,
		dimensions: dimensions,
		client:     &http.Client{Timeout: 2 * time.Minute},
	}
}

func (o *Ollama) Name() string       { return "ollama" }
func (o *Ollama) Dimensions() int    { return o.dimensions }

func (o *Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	body := struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}{
		Model: o.model,
		Input: text,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding ollama embed response: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama returned no embeddings")
	}

	return result.Embeddings[0], nil
}

func (o *Ollama) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body := struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{
		Model: o.model,
		Input: texts,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed batch request failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed batch returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding ollama embed batch response: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(result.Embeddings), len(texts))
	}

	return result.Embeddings, nil
}
