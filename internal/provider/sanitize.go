// SPDX-License-Identifier: Apache-2.0

package provider

import "strings"

// SanitizeText makes s safe to send as an API payload (a PR/MR title or body, or
// an issue/PR comment). Text assembled from raw sandbox command output can carry
// null bytes, other control characters, and invalid UTF-8. GitLab's null-byte
// middleware in particular rejects any such request with a plain-text
// "400 Bad Request" before it reaches the API. This coerces s to valid UTF-8 and
// drops disallowed control runes, keeping the whitespace (tab, newline, carriage
// return) that Markdown needs.
//
// Adapters apply it at the provider boundary so every outbound text field is
// covered regardless of caller.
func SanitizeText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r':
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// SanitizeRef makes s safe to use as a git ref (a branch name or a fromRef that
// is a branch/SHA). A valid ref contains no control characters and no
// whitespace, so unlike SanitizeText this also drops tab, newline, carriage
// return, and space rather than preserving them — both because they make a ref
// invalid and because the ref may reach an unquoted shell position in the
// sandbox git path. It never rewrites otherwise-valid names (e.g. wright/issue-7).
func SanitizeRef(s string) string {
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		if r <= 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
