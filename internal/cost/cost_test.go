// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"testing"
)

func TestAccumulator(t *testing.T) {
	a := NewAccumulator(nil)
	a.Add("", Usage{InputTokens: 1_000_000})
	a.Add("", Usage{OutputTokens: 2_000_000})

	s := a.Summary()
	if s.Turns != 2 {
		t.Fatalf("Turns = %d, want 2", s.Turns)
	}
	if s.Usage.InputTokens != 1_000_000 || s.Usage.OutputTokens != 2_000_000 {
		t.Fatalf("Usage = %+v", s.Usage)
	}
	// nil rate table => USDKnown false
	if s.USDKnown {
		t.Fatal("USDKnown should be false with nil rates")
	}
}

func TestRateTable_USD(t *testing.T) {
	tbl := RateTable{
		"claude-3-opus": {
			InputPerMTok:  15.0,
			OutputPerMTok: 75.0,
		},
		"claude-3-sonnet": {
			InputPerMTok:      3.0,
			OutputPerMTok:     15.0,
			CacheReadPerMTok:  0.30,
			CacheWritePerMTok: 3.75,
		},
	}

	t.Run("known model", func(t *testing.T) {
		u := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
		usd, ok := tbl.USD("claude-3-opus", u)
		if !ok {
			t.Fatal("expected ok=true")
		}
		// 1M input * 15 + 1M output * 75 = 90
		if usd != 90.0 {
			t.Fatalf("USD = %f, want 90.0", usd)
		}
	})

	t.Run("unknown model", func(t *testing.T) {
		u := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
		_, ok := tbl.USD("unknown-model", u)
		if ok {
			t.Fatal("expected ok=false for unknown model")
		}
	})

	t.Run("cache rate defaults", func(t *testing.T) {
		u := Usage{
			InputTokens:              1_000_000,
			OutputTokens:             1_000_000,
			CacheReadInputTokens:     1_000_000,
			CacheCreationInputTokens: 1_000_000,
		}
		usd, ok := tbl.USD("claude-3-opus", u)
		if !ok {
			t.Fatal("expected ok=true")
		}
		// input: 1M * 15 = 15
		// output: 1M * 75 = 75
		// cache read: 1M * 0.10 * 15 / 1M = 1.5
		// cache write: 1M * 1.25 * 15 / 1M = 18.75
		// total: 15 + 75 + 1.5 + 18.75 = 110.25
		want := 15.0 + 75.0 + 1.5 + 18.75
		if usd != want {
			t.Fatalf("USD = %f, want %f", usd, want)
		}
	})

	t.Run("explicit cache rates used", func(t *testing.T) {
		u := Usage{
			InputTokens:              1_000_000,
			OutputTokens:             1_000_000,
			CacheReadInputTokens:     1_000_000,
			CacheCreationInputTokens: 1_000_000,
		}
		usd, ok := tbl.USD("claude-3-sonnet", u)
		if !ok {
			t.Fatal("expected ok=true")
		}
		// input: 1M * 3 = 3
		// output: 1M * 15 = 15
		// cache read: 1M * 0.30 = 0.30
		// cache write: 1M * 3.75 = 3.75
		// total: 3 + 15 + 0.30 + 3.75 = 22.05
		want := 3.0 + 15.0 + 0.30 + 3.75
		if usd != want {
			t.Fatalf("USD = %f, want %f", usd, want)
		}
	})
}

func TestAccumulator_WithRates(t *testing.T) {
	tbl := RateTable{
		"claude-3-opus": {
			InputPerMTok:  15.0,
			OutputPerMTok: 75.0,
		},
	}

	t.Run("known model accumulates USD", func(t *testing.T) {
		a := NewAccumulator(tbl)
		a.Add("claude-3-opus", Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
		s := a.Summary()
		if !s.USDKnown {
			t.Fatal("expected USDKnown=true")
		}
		if s.USD != 90.0 {
			t.Fatalf("USD = %f, want 90.0", s.USD)
		}
	})

	t.Run("unknown model sets USDKnown=false sticky", func(t *testing.T) {
		a := NewAccumulator(tbl)
		// first add a known model
		a.Add("claude-3-opus", Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
		// then add an unknown model
		a.Add("unknown", Usage{InputTokens: 500_000, OutputTokens: 500_000})
		s := a.Summary()
		if s.USDKnown {
			t.Fatal("expected USDKnown=false after unknown model")
		}
		// USD should still accumulate the known part
		if s.USD != 90.0 {
			t.Fatalf("USD = %f, want 90.0", s.USD)
		}
		// tokens from unknown model still counted
		if s.Usage.InputTokens != 1_500_000 {
			t.Fatalf("InputTokens = %d, want 1500000", s.Usage.InputTokens)
		}
	})

	t.Run("unknown model first sets USDKnown=false", func(t *testing.T) {
		a := NewAccumulator(tbl)
		a.Add("unknown", Usage{InputTokens: 500_000})
		s := a.Summary()
		if s.USDKnown {
			t.Fatal("expected USDKnown=false")
		}
	})
}

func TestMerge(t *testing.T) {
	t.Run("merge two summaries", func(t *testing.T) {
		a := Summary{
			Turns: 2,
			Usage: Usage{
				InputTokens:              1_000_000,
				OutputTokens:             2_000_000,
				CacheReadInputTokens:     100_000,
				CacheCreationInputTokens: 50_000,
			},
			USD:      90.0,
			USDKnown: true,
		}
		b := Summary{
			Turns: 3,
			Usage: Usage{
				InputTokens:              2_000_000,
				OutputTokens:             1_000_000,
				CacheReadInputTokens:     200_000,
				CacheCreationInputTokens: 100_000,
			},
			USD:      30.0,
			USDKnown: true,
		}
		a.Merge(b)
		if a.Turns != 5 {
			t.Fatalf("Turns = %d, want 5", a.Turns)
		}
		if a.Usage.InputTokens != 3_000_000 {
			t.Fatalf("InputTokens = %d, want 3000000", a.Usage.InputTokens)
		}
		if a.Usage.OutputTokens != 3_000_000 {
			t.Fatalf("OutputTokens = %d, want 3000000", a.Usage.OutputTokens)
		}
		if a.Usage.CacheReadInputTokens != 300_000 {
			t.Fatalf("CacheReadInputTokens = %d, want 300000", a.Usage.CacheReadInputTokens)
		}
		if a.Usage.CacheCreationInputTokens != 150_000 {
			t.Fatalf("CacheCreationInputTokens = %d, want 150000", a.Usage.CacheCreationInputTokens)
		}
		if a.USD != 120.0 {
			t.Fatalf("USD = %f, want 120.0", a.USD)
		}
		if !a.USDKnown {
			t.Fatal("expected USDKnown=true")
		}
	})

	t.Run("merge ANDs USDKnown", func(t *testing.T) {
		a := Summary{USDKnown: true}
		b := Summary{USDKnown: false}
		a.Merge(b)
		if a.USDKnown {
			t.Fatal("expected USDKnown=false after merge with false")
		}
	})

	t.Run("merge with zero summary", func(t *testing.T) {
		a := Summary{Turns: 1, USDKnown: true}
		b := Summary{USDKnown: true}
		a.Merge(b)
		if a.Turns != 1 {
			t.Fatalf("Turns = %d, want 1", a.Turns)
		}
		if !a.USDKnown {
			t.Fatal("expected USDKnown=true")
		}
	})
}
