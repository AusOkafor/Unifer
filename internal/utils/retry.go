package utils

import (
	"context"
	"math/rand"
	"time"
)

// RetryWithBackoff retries fn up to attempts times using exponential backoff with jitter.
// It stops early if ctx is cancelled.
func RetryWithBackoff(ctx context.Context, attempts int, base time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}

		if i == attempts-1 {
			break
		}

		// Exponential backoff: base * 2^i + jitter(0..base)
		backoff := base*(1<<uint(i)) + time.Duration(rand.Int63n(int64(base)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return err
}

// Retry is a simple retry without context or backoff — for non-critical paths.
func Retry(attempts int, fn func() error) error {
	return RetryWithBackoff(context.Background(), attempts, 500*time.Millisecond, fn)
}
