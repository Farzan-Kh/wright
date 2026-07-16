// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateIsUTF8Safe(t *testing.T) {
	// "پ" (U+067E) is 2 bytes in UTF-8. Repeated 40 times that's an 80-byte
	// string; cutting at byte 72 (as buildPRBody's title truncation does)
	// used to land mid-character and corrupt the last rune.
	s := strings.Repeat("پ", 40)
	got := truncate(s, 72)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
}

func TestTruncateShortStringUnchanged(t *testing.T) {
	if got := truncate("short", 72); got != "short" {
		t.Errorf("truncate = %q, want unchanged", got)
	}
}

func TestTruncateAddsEllipsis(t *testing.T) {
	got := truncate(strings.Repeat("a", 10), 5)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate = %q, want ellipsis suffix", got)
	}
}
