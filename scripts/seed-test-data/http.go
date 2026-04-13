package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// apiClient wraps HTTP calls with auth and JSON handling.
type apiClient struct {
	base    string
	headers map[string]string
	client  *http.Client
}

func newAPI(base string, headers map[string]string) *apiClient {
	return &apiClient{
		base:    base,
		headers: headers,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *apiClient) do(method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, a.base+path, bodyReader)
	if err != nil {
		return err
	}
	for k, v := range a.headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d %s %s: %s", resp.StatusCode, method, path, string(respBody))
	}

	if result != nil && len(respBody) > 0 {
		return json.Unmarshal(respBody, result)
	}
	return nil
}

func (a *apiClient) get(path string, result any) error {
	return a.do("GET", path, nil, result)
}

func (a *apiClient) post(path string, body, result any) error {
	return a.do("POST", path, body, result)
}

func (a *apiClient) put(path string, body, result any) error {
	return a.do("PUT", path, body, result)
}

func (a *apiClient) delete(path string) error {
	return a.do("DELETE", path, nil, nil)
}
