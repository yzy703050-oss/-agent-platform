package toolquery

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"agent-platform/backend/internal/model"
)

type Options struct {
	CleanupGrace time.Duration
	Jitter       func(time.Duration) time.Duration
}

func waitUntil(ctx context.Context, at time.Time) error {
	delay := time.Until(at)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func defaultJitter(window time.Duration) time.Duration {
	return time.Duration(rand.Int64N(int64(window) + 1))
}
func retryable(err error) bool { return errors.Is(err, model.ErrProviderTransient) }

type retryAfter interface{ RetryDelay() time.Duration }
