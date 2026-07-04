// Package cost tracks token usage and dollar spend for agent runs.
package cost

import "time"

// Usage is provider-agnostic token usage for one model response.
type Usage struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
}

// Rate holds per-million-token prices in USD.
type Rate struct {
	InputUSDPerMTok      float64
	OutputUSDPerMTok     float64
	CacheReadUSDPerMTok  float64
	CacheWriteUSDPerMTok float64
}

// sonnetIntroPricingCutoff is when claude-sonnet-5's introductory pricing
// reverts to standard pricing. Computed from the wall clock (via now) rather
// than a value someone has to remember to edit, so cost reports don't
// silently under-report once the promo ends.
var sonnetIntroPricingCutoff = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

var (
	// Cache read is 0.1x and 5-minute cache write is 1.25x the input rate, for
	// both the introductory and standard tiers.
	sonnetIntroRate = Rate{
		InputUSDPerMTok:      2.00,
		OutputUSDPerMTok:     10.00,
		CacheReadUSDPerMTok:  0.20,
		CacheWriteUSDPerMTok: 2.50,
	}
	sonnetStandardRate = Rate{
		InputUSDPerMTok:      3.00,
		OutputUSDPerMTok:     15.00,
		CacheReadUSDPerMTok:  0.30,
		CacheWriteUSDPerMTok: 3.75,
	}
)

// Rates is the model pricing table keyed by model ID, for models with a
// single fixed rate. claude-sonnet-5 isn't listed here because its rate
// changes over time; see rateFor and sonnetIntroPricingCutoff.
var Rates = map[string]Rate{
	"claude-haiku-4-5": {
		InputUSDPerMTok:      1.00,
		OutputUSDPerMTok:     5.00,
		CacheReadUSDPerMTok:  0.10,
		CacheWriteUSDPerMTok: 1.25,
	},
	"claude-opus-4-8": {
		InputUSDPerMTok:      5.00,
		OutputUSDPerMTok:     25.00,
		CacheReadUSDPerMTok:  0.50,
		CacheWriteUSDPerMTok: 6.25,
	},
}

// now is a test seam for sonnetIntroPricingCutoff comparisons.
var now = time.Now

// rateFor resolves model's current rate.
func rateFor(model string) (Rate, bool) {
	if model == "claude-sonnet-5" {
		if now().Before(sonnetIntroPricingCutoff) {
			return sonnetIntroRate, true
		}
		return sonnetStandardRate, true
	}
	r, ok := Rates[model]
	return r, ok
}

const million = 1_000_000.0

// USD converts one usage sample to dollars for model.
func USD(model string, u Usage) float64 {
	r, ok := rateFor(model)
	if !ok {
		return 0
	}
	return (float64(u.InputTokens)/million)*r.InputUSDPerMTok +
		(float64(u.OutputTokens)/million)*r.OutputUSDPerMTok +
		(float64(u.CacheReadInputTokens)/million)*r.CacheReadUSDPerMTok +
		(float64(u.CacheCreationInputTokens)/million)*r.CacheWriteUSDPerMTok
}

// Summary is an immutable snapshot of accumulated usage.
type Summary struct {
	Turns int
	Usage Usage
	USD   float64
	// USDApplicable is false when running under subscription/OAuth auth where
	// there is no per-token dollar billing.
	USDApplicable bool
}

// Accumulator aggregates usage across agent turns.
type Accumulator struct {
	s Summary
}

// NewAccumulator builds a running accumulator.
func NewAccumulator(usdApplicable bool) *Accumulator {
	return &Accumulator{s: Summary{USDApplicable: usdApplicable}}
}

// Add records one turn's usage against model pricing.
func (a *Accumulator) Add(model string, u Usage) {
	a.s.Turns++
	a.s.Usage.InputTokens += u.InputTokens
	a.s.Usage.OutputTokens += u.OutputTokens
	a.s.Usage.CacheCreationInputTokens += u.CacheCreationInputTokens
	a.s.Usage.CacheReadInputTokens += u.CacheReadInputTokens
	if a.s.USDApplicable {
		a.s.USD += USD(model, u)
	}
}

// Summary returns a copy of the current totals.
func (a *Accumulator) Summary() Summary {
	return a.s
}
