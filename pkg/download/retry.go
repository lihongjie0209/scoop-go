package download

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RetryPolicy defines the retry behavior for failed downloads.
type RetryPolicy struct {
	MaxRetries       int           // maximum number of retries (default 3)
	InitialWait      time.Duration // initial backoff duration (default 2s)
	MaxWait          time.Duration // maximum backoff duration (default 60s)
	RetryableCodes   []int         // HTTP status codes that are retryable
}

// DefaultRetryPolicy returns a sensible default retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:     3,
		InitialWait:    2 * time.Second,
		MaxWait:        60 * time.Second,
		RetryableCodes: []int{429, 500, 502, 503, 504},
	}
}

// IsRetryable checks if an error should trigger a retry.
// Returns:
//   - true if the error is transient (network timeout, server error, rate limit)
//   - false for permanent errors (404, 403, invalid URL)
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Network-level errors (retryable)
	transient := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"connection closed",
		"no such host",
		"tls handshake",
		"i/o timeout",
		"EOF",
		"unexpected end",
		"server closed",
		"reset by peer",
		"broken pipe",
	}
	for _, s := range transient {
		if strings.Contains(strings.ToLower(errStr), s) {
			return true
		}
	}

	// DNS / URL errors (not retryable)
	if _, ok := err.(*url.Error); ok {
		// url.Error wraps underlying errors — check the underlying cause separately
		return false
	}

	return false
}

// IsRetryableStatusCode checks if an HTTP status code is retryable.
func (p *RetryPolicy) IsRetryableStatusCode(code int) bool {
	if code >= 500 && code <= 599 {
		return true // All 5xx are server errors
	}
	for _, c := range p.RetryableCodes {
		if c == code {
			return true
		}
	}
	return false
}

// DoWithRetries executes a function with exponential backoff retry.
// The function should return (shouldRetry bool, error).
func DoWithRetries(ctx context.Context, policy RetryPolicy, operation string,
	fn func() error) error {

	var lastErr error
	wait := policy.InitialWait

	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		if attempt > 0 {
			// Wait with backoff
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s cancelled after %d retries: %w",
					operation, attempt, ctx.Err())
			case <-time.After(wait):
			}

			// Exponential backoff with jitter
			wait *= 2
			if wait > policy.MaxWait {
				wait = policy.MaxWait
			}
		}

		err := fn()
		if err == nil {
			return nil // success
		}

		lastErr = err

		// Check if retryable
		if !IsRetryable(err) {
			if attempt < policy.MaxRetries {
				// Non-retryable error — fail immediately
				return fmt.Errorf("%s failed (non-retryable): %w", operation, err)
			}
		}

		if attempt < policy.MaxRetries {
			continue
		}
	}

	return fmt.Errorf("%s failed after %d retries: %w",
		operation, policy.MaxRetries, lastErr)
}
