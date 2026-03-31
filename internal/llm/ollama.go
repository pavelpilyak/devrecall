package llm

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

// Ollama talks to a local Ollama instance via its HTTP API.
type Ollama struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllama creates a provider for a local Ollama instance.
// If baseURL is empty, defaults to http://localhost:11434.
// If model is empty, defaults to "llama3.2".
func NewOllama(baseURL, model string) *Ollama {
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}
	if model == "" {
		model = "llama3.2"
	}
	return &Ollama{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *Ollama) Name() string { return "ollama" }

func (o *Ollama) Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error) {
	model := opts.Model
	if model == "" {
		model = o.model
	}

	type ollamaMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []ollamaMsg
	for _, m := range messages {
		msgs = append(msgs, ollamaMsg{Role: m.Role, Content: m.Content})
	}

	body := struct {
		Model    string      `json:"model"`
		Messages []ollamaMsg `json:"messages"`
		Stream   bool        `json:"stream"`
		Options  *struct {
			Temperature float64 `json:"temperature,omitempty"`
			NumPredict  int     `json:"num_predict,omitempty"`
		} `json:"options,omitempty"`
	}{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	if opts.Temperature > 0 || opts.MaxTokens > 0 {
		body.Options = &struct {
			Temperature float64 `json:"temperature,omitempty"`
			NumPredict  int     `json:"num_predict,omitempty"`
		}{
			Temperature: opts.Temperature,
			NumPredict:  opts.MaxTokens,
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding ollama response: %w", err)
	}

	return result.Message.Content, nil
}
