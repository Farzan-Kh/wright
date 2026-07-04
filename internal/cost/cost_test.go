package cost

import (
	"math"
	"testing"
	"time"
)

func TestUSD(t *testing.T) {
	u := Usage{
		InputTokens:              1_500_000,
		OutputTokens:             500_000,
		CacheCreationInputTokens: 200_000,
		CacheReadInputTokens:     300_000,
	}
	got := USD("claude-sonnet-5", u)
	// 1.5*2 + 0.5*10 + 0.2*2.50 + 0.3*0.20 = 8.56 (intro pricing)
	if math.Abs(got-8.56) > 1e-9 {
		t.Fatalf("USD = %v, want 8.56", got)
	}
}

func TestUSDUnknownModel(t *testing.T) {
	if got := USD("unknown-model", Usage{InputTokens: 1_000_000}); got != 0 {
		t.Fatalf("USD(unknown) = %v, want 0", got)
	}
}

func TestAccumulator(t *testing.T) {
	a := NewAccumulator(true)
	a.Add("claude-haiku-4-5", Usage{InputTokens: 1_000_000})
	a.Add("claude-haiku-4-5", Usage{OutputTokens: 2_000_000})

	s := a.Summary()
	if s.Turns != 2 {
		t.Fatalf("Turns = %d, want 2", s.Turns)
	}
	if s.Usage.InputTokens != 1_000_000 || s.Usage.OutputTokens != 2_000_000 {
		t.Fatalf("Usage = %+v", s.Usage)
	}
	if math.Abs(s.USD-11) > 1e-9 { // 1*1 + 2*5
		t.Fatalf("USD = %v, want 11", s.USD)
	}
	if !s.USDApplicable {
		t.Fatalf("USDApplicable = false, want true")
	}
}

func TestAccumulatorUSDNotApplicable(t *testing.T) {
	a := NewAccumulator(false)
	a.Add("claude-sonnet-5", Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	s := a.Summary()
	if s.USD != 0 {
		t.Fatalf("USD = %v, want 0 in non-applicable mode", s.USD)
	}
	if s.Usage.InputTokens != 1_000_000 || s.Usage.OutputTokens != 1_000_000 {
		t.Fatalf("Usage should still accumulate, got %+v", s.Usage)
	}
	if s.USDApplicable {
		t.Fatalf("USDApplicable = true, want false")
	}
}

func TestRatesTablePinned(t *testing.T) {
	cases := map[string]Rate{
		"claude-haiku-4-5": {InputUSDPerMTok: 1.00, OutputUSDPerMTok: 5.00, CacheReadUSDPerMTok: 0.10, CacheWriteUSDPerMTok: 1.25},
		"claude-opus-4-8":  {InputUSDPerMTok: 5.00, OutputUSDPerMTok: 25.00, CacheReadUSDPerMTok: 0.50, CacheWriteUSDPerMTok: 6.25},
	}
	for model, want := range cases {
		got, ok := Rates[model]
		if !ok {
			t.Fatalf("Rates missing model %q", model)
		}
		if got != want {
			t.Fatalf("Rates[%q] = %+v, want %+v", model, got, want)
		}
	}
}

func TestSonnetRateRevertsAfterIntroPricingCutoff(t *testing.T) {
	origNow := now
	defer func() { now = origNow }()

	now = func() time.Time { return sonnetIntroPricingCutoff.Add(-time.Hour) }
	if got, want := USD("claude-sonnet-5", Usage{InputTokens: 1_000_000}), 2.00; math.Abs(got-want) > 1e-9 {
		t.Fatalf("USD before cutoff = %v, want %v (intro pricing)", got, want)
	}

	now = func() time.Time { return sonnetIntroPricingCutoff.Add(time.Hour) }
	if got, want := USD("claude-sonnet-5", Usage{InputTokens: 1_000_000}), 3.00; math.Abs(got-want) > 1e-9 {
		t.Fatalf("USD after cutoff = %v, want %v (standard pricing)", got, want)
	}
}
