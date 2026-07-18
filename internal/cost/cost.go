// SPDX-License-Identifier: Apache-2.0

// Package cost tracks token usage across agent runs.
package cost

// Usage is provider-agnostic token usage for one model response.
type Usage struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
}

// Summary is an immutable snapshot of accumulated usage.
type Summary struct {
	Turns    int
	Usage    Usage
	USD      float64
	USDKnown bool
}

// Rates is $/MTok for one model, supplied by the user in config.
type Rates struct {
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64 // 0 -> 0.10 x InputPerMTok
	CacheWritePerMTok float64 // 0 -> 1.25 x InputPerMTok
}

// RateTable maps model id -> rates.
type RateTable map[string]Rates

// USD prices u under t; ok=false when model has no entry.
func (t RateTable) USD(model string, u Usage) (usd float64, ok bool) {
	rates, ok := t[model]
	if !ok {
		return 0, false
	}
	inputRate := rates.InputPerMTok
	outputRate := rates.OutputPerMTok
	cacheReadRate := rates.CacheReadPerMTok
	if cacheReadRate == 0 {
		cacheReadRate = 0.10 * inputRate
	}
	cacheWriteRate := rates.CacheWritePerMTok
	if cacheWriteRate == 0 {
		cacheWriteRate = 1.25 * inputRate
	}
	usd = (float64(u.InputTokens)/1_000_000)*inputRate +
		(float64(u.OutputTokens)/1_000_000)*outputRate +
		(float64(u.CacheReadInputTokens)/1_000_000)*cacheReadRate +
		(float64(u.CacheCreationInputTokens)/1_000_000)*cacheWriteRate
	return usd, true
}

// Merge sums turns, all four token fields, and USD into s; ANDs USDKnown.
func (s *Summary) Merge(b Summary) {
	s.Turns += b.Turns
	s.Usage.InputTokens += b.Usage.InputTokens
	s.Usage.OutputTokens += b.Usage.OutputTokens
	s.Usage.CacheCreationInputTokens += b.Usage.CacheCreationInputTokens
	s.Usage.CacheReadInputTokens += b.Usage.CacheReadInputTokens
	s.USD += b.USD
	s.USDKnown = s.USDKnown && b.USDKnown
}

// Accumulator aggregates usage across agent turns.
type Accumulator struct {
	s     Summary
	rates RateTable
}

// NewAccumulator builds a running accumulator with the given rate table.
// A nil rate table disables USD tracking (USDKnown will be false).
func NewAccumulator(rates RateTable) *Accumulator {
	return &Accumulator{
		s:     Summary{USDKnown: true},
		rates: rates,
	}
}

// Add records one turn's usage for the given model.
func (a *Accumulator) Add(model string, u Usage) {
	a.s.Turns++
	a.s.Usage.InputTokens += u.InputTokens
	a.s.Usage.OutputTokens += u.OutputTokens
	a.s.Usage.CacheCreationInputTokens += u.CacheCreationInputTokens
	a.s.Usage.CacheReadInputTokens += u.CacheReadInputTokens

	if a.rates != nil {
		usd, ok := a.rates.USD(model, u)
		if ok {
			a.s.USD += usd
		} else {
			a.s.USDKnown = false
		}
	} else {
		a.s.USDKnown = false
	}
}

// Summary returns a copy of the current totals.
func (a *Accumulator) Summary() Summary {
	return a.s
}

// Merge adds the turn, token, and USD values from b into the accumulator.
func (a *Accumulator) Merge(b Summary) {
	a.s.Merge(b)
}
