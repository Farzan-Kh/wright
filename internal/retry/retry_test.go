// SPDX-License-Identifier: Apache-2.0

package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDelayExponential(t *testing.T) {
	cfg := Config{Strategy: Exponential, BaseDelay: 100 * time.Millisecond, Exponent: 2, MaxDelay: time.Second}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, time.Second}, // capped
	}
	for _, c := range cases {
		if got := cfg.Delay(c.attempt); got != c.want {
			t.Errorf("Delay(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestDelayFixed(t *testing.T) {
	cfg := Config{Strategy: Fixed, BaseDelay: 250 * time.Millisecond}
	for attempt := 1; attempt <= 3; attempt++ {
		if got := cfg.Delay(attempt); got != 250*time.Millisecond {
			t.Errorf("Delay(%d) = %v, want 250ms", attempt, got)
		}
	}
}

func TestDoSucceedsAfterRetries(t *testing.T) {
	cfg := Config{Strategy: Fixed, MaxAttempts: 5, BaseDelay: time.Millisecond}
	calls := 0
	err := Do(context.Background(), cfg, nil, func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDoExhaustsMaxAttempts(t *testing.T) {
	cfg := Config{Strategy: Fixed, MaxAttempts: 3, BaseDelay: time.Millisecond}
	calls := 0
	wantErr := errors.New("permanent")
	err := Do(context.Background(), cfg, nil, func(ctx context.Context) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDoZeroOrOneMaxAttemptsMeansNoRetry(t *testing.T) {
	for _, max := range []int{0, 1} {
		cfg := Config{MaxAttempts: max, BaseDelay: time.Millisecond}
		calls := 0
		wantErr := errors.New("fail")
		err := Do(context.Background(), cfg, nil, func(ctx context.Context) error {
			calls++
			return wantErr
		})
		if !errors.Is(err, wantErr) || calls != 1 {
			t.Errorf("MaxAttempts=%d: calls=%d err=%v", max, calls, err)
		}
	}
}

func TestDoNonRetryableStopsImmediately(t *testing.T) {
	cfg := Config{MaxAttempts: 5, BaseDelay: time.Millisecond}
	calls := 0
	sentinel := errors.New("do not retry")
	err := Do(context.Background(), cfg, func(err error) bool { return !errors.Is(err, sentinel) },
		func(ctx context.Context) error {
			calls++
			return sentinel
		})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-retryable should stop immediately)", calls)
	}
}

func TestDoRespectsContextCancellation(t *testing.T) {
	cfg := Config{MaxAttempts: 10, BaseDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Do(ctx, cfg, nil, func(ctx context.Context) error {
		calls++
		cancel()
		return errors.New("transient")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestValueReturnsResult(t *testing.T) {
	cfg := Config{MaxAttempts: 3, BaseDelay: time.Millisecond}
	calls := 0
	got, err := Value(context.Background(), cfg, nil, func(ctx context.Context) (string, error) {
		calls++
		if calls < 2 {
			return "", errors.New("transient")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want ok", got)
	}
}
