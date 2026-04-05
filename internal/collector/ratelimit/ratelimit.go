package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ErrRateLimited is returned when all retries are exhausted.
type ErrRateLimited struct {
	RetryAfter time.Duration
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("rate limited (retry after %s)", e.RetryAfter)
}

// MaxRetries is the default number of retry attempts for rate-limited requests.
const MaxRetries = 3

// Do executes an HTTP request via the given client, automatically retrying on
// 429 responses with exponential backoff. It respects the Retry-After header
// when present. Returns the response on success (caller must close body).
//
// The doReq function should build and return a fresh *http.Request each time
// it is called, since a request body can only be read once.
func Do(ctx context.Context, client *http.Client, doReq func() (*http.Request, error)) (*http.Response, error) {
	return DoWithRetries(ctx, client, doReq, MaxRetries)
}

// DoWithRetries is like Do but allows overriding the retry count.
func DoWithRetries(ctx context.Context, client *http.Client, doReq func() (*http.Request, error), maxRetries int) (*http.Response, error) {
	backoff := 1 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := doReq()
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		// We got rate limited — close this response and prepare to retry.
		resp.Body.Close()

		if attempt == maxRetries {
			wait := parseRetryAfter(resp, backoff)
			return nil, &ErrRateLimited{RetryAfter: wait}
		}

		wait := parseRetryAfter(resp, backoff)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}

		// Exponential backoff for next attempt (if Retry-After is not set).
		backoff *= 2
	}

	// Unreachable, but satisfy compiler.
	return nil, fmt.Errorf("rate limit retries exhausted")
}

// parseRetryAfter extracts the wait duration from a 429 response.
// Checks Retry-After header first (seconds), falls back to the provided default.
func parseRetryAfter(resp *http.Response, fallback time.Duration) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return fallback
}
