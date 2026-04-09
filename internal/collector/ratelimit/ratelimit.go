package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrRateLimited is returned when all retries are exhausted on 429 responses.
type ErrRateLimited struct {
	RetryAfter time.Duration
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("rate limited (retry after %s)", e.RetryAfter)
}

// ErrRetriesExhausted is returned when all retries are exhausted on transient errors.
type ErrRetriesExhausted struct {
	LastErr    error
	StatusCode int // 0 if the last failure was a network error
	Attempts   int
}

func (e *ErrRetriesExhausted) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("server returned %d after %d attempts", e.StatusCode, e.Attempts)
	}
	return fmt.Sprintf("request failed after %d attempts: %v", e.Attempts, e.LastErr)
}

func (e *ErrRetriesExhausted) Unwrap() error { return e.LastErr }

// MaxRetries is the default number of retry attempts.
const MaxRetries = 3

// Do executes an HTTP request via the given client, automatically retrying on
// rate-limited (429), server error (5xx), and transient network errors with
// exponential backoff. It respects the Retry-After header when present.
// Returns the response on success (caller must close body).
//
// The doReq function should build and return a fresh *http.Request each time
// it is called, since a request body can only be read once.
func Do(ctx context.Context, client *http.Client, doReq func() (*http.Request, error)) (*http.Response, error) {
	return DoWithRetries(ctx, client, doReq, MaxRetries)
}

// DoWithRetries is like Do but allows overriding the retry count.
func DoWithRetries(ctx context.Context, client *http.Client, doReq func() (*http.Request, error), maxRetries int) (*http.Response, error) {
	backoff := 1 * time.Second
	var lastErr error
	var lastStatus int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := doReq()
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)

		// Transient network error — retry.
		if err != nil {
			if !isTransient(err) || attempt == maxRetries {
				if attempt > 0 {
					return nil, &ErrRetriesExhausted{LastErr: err, Attempts: attempt + 1}
				}
				return nil, err
			}
			lastErr = err
			lastStatus = 0

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}

		// 429 — rate limited.
		if resp.StatusCode == http.StatusTooManyRequests {
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
			backoff *= 2
			continue
		}

		// 5xx — server error, retry.
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("server returned %d", resp.StatusCode)
			lastStatus = resp.StatusCode
			if attempt == maxRetries {
				return nil, &ErrRetriesExhausted{LastErr: lastErr, StatusCode: lastStatus, Attempts: attempt + 1}
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}

		// Success or client error (4xx except 429) — return as-is.
		return resp, nil
	}

	// Unreachable, but satisfy compiler.
	return nil, &ErrRetriesExhausted{LastErr: lastErr, StatusCode: lastStatus, Attempts: maxRetries + 1}
}

// isTransient returns true for network errors that are likely to resolve on retry.
func isTransient(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation is not transient — the caller chose to stop.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Connection refused, reset, broken pipe.
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	// Net package errors: DNS, timeout, etc.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Temporary()
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Catch common transient error strings as a fallback (e.g. wrapped errors).
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "EOF")
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
