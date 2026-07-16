// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"
	"time"

	"github.com/farzan-kh/wright/internal/retry"
)

func TestRetryConfigToRetryConfig(t *testing.T) {
	rc := RetryConfig{Strategy: RetryStrategyFixed, MaxAttempts: 5, BaseDelayMS: 250, MaxDelayMS: 5000, Exponent: 1.5}
	got := rc.ToRetryConfig()
	want := retry.Config{Strategy: retry.Fixed, MaxAttempts: 5, BaseDelay: 250 * time.Millisecond, MaxDelay: 5 * time.Second, Exponent: 1.5}
	if got != want {
		t.Errorf("ToRetryConfig() = %+v, want %+v", got, want)
	}
}

func TestRetryConfigToRetryConfigExponentialDefault(t *testing.T) {
	rc := RetryConfig{Strategy: RetryStrategyExponential, MaxAttempts: 4, BaseDelayMS: 500, MaxDelayMS: 30_000, Exponent: 2}
	got := rc.ToRetryConfig()
	if got.Strategy != retry.Exponential {
		t.Errorf("Strategy = %v, want Exponential", got.Strategy)
	}
}
