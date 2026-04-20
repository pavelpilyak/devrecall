package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pavelpilyak/devrecall/internal/collector/ratelimit"
)

const defaultOpenAIURL = "https://api.openai.com/v1"

// OpenAI generates embeddings using the OpenAI Embeddings API.
type OpenAI struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	client     *http.Client
}

// NewOpenAI creates an OpenAI embedder.
// Default model is text-embedding-3-small (1536 dimensions).
func NewOpenAI(apiKey, model, baseURL string, dimensions int) *OpenAI {
	if baseURL == "" {
		baseURL = defaultOpenAIURL
	}
	if model == "" {
		model = "text-embedding-3-small"
	}
	if dimensions <= 0 {
		dimensions = 1536
	}
	return &OpenAI{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		client:     &http.Client{Timeout: 2 * time.Minute},
	}
}

func (o *OpenAI) Name() string       { return "openai" }
func (o *OpenAI) Dimensions() int    { return o.dimensions }

func (o *OpenAI) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (o *OpenAI) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := map[string]any{
		"model":      o.model,
		"input":      texts,
		"dimensions": o.dimensions,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := ratelimit.Do(ctx, o.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/embeddings", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("openai embeddings request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("openai: invalid API key")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embeddings returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding openai embeddings response: %w", err)
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("openai returned %d embeddings for %d inputs", len(result.Data), len(texts))
	}

	// OpenAI returns data sorted by index, but let's be safe.
	vecs := make([][]float32, len(texts))
	for _, d := range result.Data {
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}
