// SPDX-License-Identifier: Apache-2.0

// Package retry implements configurable retry-with-backoff for connection
// attempts to external services: provider APIs (GitHub/GitLab), the LLM API,
// the Docker daemon, and git network operations.
package retry

import (
	"context"
	"errors"
	"math"
	"time"
)

// Strategy selects how the delay between attempts grows.
type Strategy string

const (
	// Exponential multiplies BaseDelay by Exponent^attempt, capped at MaxDelay.
	Exponential Strategy = "exponential"
	// Fixed waits BaseDelay between every attempt (simple time-based retry).
	Fixed Strategy = "fixed"
)

// Config controls retry behavior for one connection attempt.
type Config struct {
	// Strategy is Exponential or Fixed. The zero value behaves as Exponential.
	Strategy Strategy
	// MaxAttempts is the total number of tries, including the first. Values
	// <= 1 disable retrying.
	MaxAttempts int
	// BaseDelay is the wait before the first retry (and every retry under the
	// Fixed strategy).
	BaseDelay time.Duration
	// MaxDelay caps any single retry delay. Zero means uncapped.
	MaxDelay time.Duration
	// Exponent is the per-attempt delay multiplier under the Exponential
	// strategy. Values <= 0 behave as 2.
	Exponent float64
}

// Delay returns the wait before the retry following the attempt-th failed
// try (attempt starts at 1 for the first try).
func (c Config) Delay(attempt int) time.Duration {
	var d time.Duration
	switch c.Strategy {
	case Fixed:
		d = c.BaseDelay
	default:
		exp := c.Exponent
		if exp <= 0 {
			exp = 2
		}
		d = time.Duration(float64(c.BaseDelay) * math.Pow(exp, float64(attempt-1)))
	}
	if d < 0 || (c.MaxDelay > 0 && d > c.MaxDelay) {
		d = c.MaxDelay
	}
	return d
}

// Do calls fn, retrying per cfg while retryable(err) reports true, until
// MaxAttempts is exhausted or ctx is done. retryable == nil treats every
// error as retryable. It returns the last error fn produced.
func Do(ctx context.Context, cfg Config, retryable func(error) bool, fn func(ctx context.Context) error) error {
	attempts := max(cfg.MaxAttempts, 1)

	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = fn(ctx)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if retryable != nil && !retryable(err) {
			return err
		}
		if attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cfg.Delay(attempt)):
		}
	}
	return err
}

// Value calls fn like Do, returning its successful result. On exhaustion or a
// non-retryable error, it returns the last error alongside T's zero value.
func Value[T any](ctx context.Context, cfg Config, retryable func(error) bool, fn func(ctx context.Context) (T, error)) (T, error) {
	var result T
	err := Do(ctx, cfg, retryable, func(ctx context.Context) error {
		v, err := fn(ctx)
		if err != nil {
			return err
		}
		result = v
		return nil
	})
	return result, err
}
