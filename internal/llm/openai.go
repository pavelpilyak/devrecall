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

const defaultOpenAIURL = "https://api.openai.com/v1"

// OpenAI talks to the OpenAI Chat Completions API (or any compatible endpoint).
type OpenAI struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAI creates an OpenAI provider.
// baseURL can be overridden for compatible providers (Groq, Together, local vLLM).
func NewOpenAI(apiKey, model, baseURL string) *OpenAI {
	if baseURL == "" {
		baseURL = defaultOpenAIURL
	}
	if model == "" {
		model = "gpt-5.4-mini"
	}
	return &OpenAI{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 2 * time.Minute},
	}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error) {
	model := opts.Model
	if model == "" {
		model = o.model
	}

	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []chatMsg
	for _, m := range messages {
		msgs = append(msgs, chatMsg{Role: m.Role, Content: m.Content})
	}

	reqBody := map[string]any{
		"model":    model,
		"messages": msgs,
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}
	if opts.MaxTokens > 0 {
		reqBody["max_completion_tokens"] = opts.MaxTokens
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("openai: invalid API key — check your key at platform.openai.com")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("openai: rate limited — try again shortly")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding openai response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}

	return result.Choices[0].Message.Content, nil
}
