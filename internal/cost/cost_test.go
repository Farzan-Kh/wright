// SPDX-License-Identifier: Apache-2.0

package cost

import "testing"

func TestAccumulator(t *testing.T) {
	a := NewAccumulator()
	a.Add(Usage{InputTokens: 1_000_000})
	a.Add(Usage{OutputTokens: 2_000_000})

	s := a.Summary()
	if s.Turns != 2 {
		t.Fatalf("Turns = %d, want 2", s.Turns)
	}
	if s.Usage.InputTokens != 1_000_000 || s.Usage.OutputTokens != 2_000_000 {
		t.Fatalf("Usage = %+v", s.Usage)
	}
}
