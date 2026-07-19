// SPDX-License-Identifier: Apache-2.0

// Package text provides shared text-manipulation utilities (truncation, etc.)
// used by multiple internal packages. It lives in its own leaf package to keep
// the dependency graph acyclic: executor and cli both need truncate, and
// neither should depend on the other.
package text

import "unicode/utf8"

// Truncate returns s truncated to at most n bytes. If s is longer than n
// bytes, the result is shortened to n-1 bytes (pulled back to the nearest
// valid rune boundary) with a trailing ellipsis character appended. An
// ellipsis consumes one byte in the limit so the result never exceeds n
// bytes.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return safeCut(s, n)
	}
	return safeCut(s, n-1) + "…"
}

// safeCut returns the first n bytes of s, pulled back to the nearest earlier
// rune boundary so a multi-byte UTF-8 character is never split in half.
func safeCut(s string, n int) string {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
