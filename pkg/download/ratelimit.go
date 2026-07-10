package download

import (
	"context"
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter for download speed control.
// It limits the number of bytes per second across all connections.
type RateLimiter struct {
	rate  int64   // bytes per second
	tokens float64
	mu    sync.Mutex
	cond  *sync.Cond
	done  chan struct{}
}

// NewRateLimiter creates a new rate limiter.
// rateBytes is the maximum bytes per second (0 = unlimited).
func NewRateLimiter(rateBytes int64) *RateLimiter {
	rl := &RateLimiter{
		rate:  rateBytes,
		tokens: float64(rateBytes),
		done:  make(chan struct{}),
	}
	rl.cond = sync.NewCond(&rl.mu)

	if rateBytes > 0 {
		go rl.refill()
	}

	return rl
}

// Wait blocks until n bytes can be consumed, respecting the rate limit.
func (rl *RateLimiter) Wait(ctx context.Context, n int64) error {
	if rl.rate <= 0 {
		return nil // unlimited
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	for rl.tokens < float64(n) {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Wait for more tokens
		rl.cond.Wait()

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	rl.tokens -= float64(n)
	return nil
}

// Stop stops the rate limiter's background refill goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.done)
}

func (rl *RateLimiter) refill() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.mu.Lock()
			// Add tokens (100ms worth)
			rl.tokens += float64(rl.rate) * 0.1
			if rl.tokens > float64(rl.rate) {
				rl.tokens = float64(rl.rate)
			}
			rl.cond.Broadcast()
			rl.mu.Unlock()
		}
	}
}
