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
	Turns int
	Usage Usage
}

// Accumulator aggregates usage across agent turns.
type Accumulator struct {
	s Summary
}

// NewAccumulator builds a running accumulator.
func NewAccumulator() *Accumulator {
	return &Accumulator{}
}

// Add records one turn's usage.
func (a *Accumulator) Add(u Usage) {
	a.s.Turns++
	a.s.Usage.InputTokens += u.InputTokens
	a.s.Usage.OutputTokens += u.OutputTokens
	a.s.Usage.CacheCreationInputTokens += u.CacheCreationInputTokens
	a.s.Usage.CacheReadInputTokens += u.CacheReadInputTokens
}

// Summary returns a copy of the current totals.
func (a *Accumulator) Summary() Summary {
	return a.s
}
