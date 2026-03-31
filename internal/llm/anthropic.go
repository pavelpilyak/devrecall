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

const (
	defaultAnthropicURL = "https://api.anthropic.com"
	anthropicAPIVersion = "2023-06-01"
)

// Anthropic talks to the Anthropic Messages API.
type Anthropic struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewAnthropic creates an Anthropic provider.
func NewAnthropic(apiKey, model, baseURL string) *Anthropic {
	if baseURL == "" {
		baseURL = defaultAnthropicURL
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &Anthropic{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 2 * time.Minute},
	}
}

func (a *Anthropic) Name() string { return "anthropic" }

func (a *Anthropic) Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error) {
	model := opts.Model
	if model == "" {
		model = a.model
	}

	// Anthropic separates system from messages.
	var system string
	type apiMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var apiMsgs []apiMsg
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		apiMsgs = append(apiMsgs, apiMsg{Role: m.Role, Content: m.Content})
	}

	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	reqBody := map[string]any{
		"model":      model,
		"messages":   apiMsgs,
		"max_tokens": maxTokens,
	}
	if system != "" {
		reqBody["system"] = system
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("anthropic: invalid API key — check your key at console.anthropic.com")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("anthropic: rate limited — try again shortly")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding anthropic response: %w", err)
	}

	for _, block := range result.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("anthropic returned no text content")
}
