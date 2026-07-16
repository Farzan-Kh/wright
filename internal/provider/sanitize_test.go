// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeTextStripsNullAndControlBytes(t *testing.T) {
	// Raw verify output can carry a null byte (rejected by GitLab's null-byte
	// middleware with a plain "400 Bad Request") plus other control chars.
	in := "ok\x00 done\x1b[31mred\x1b[0m\tkeep\nkeep\rkeep\x07"
	got := SanitizeText(in)
	if strings.ContainsRune(got, 0x00) {
		t.Errorf("null byte survived: %q", got)
	}
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("control bytes survived: %q", got)
	}
	for _, keep := range []string{"\t", "\n", "\r", "ok", "done", "red", "keep"} {
		if !strings.Contains(got, keep) {
			t.Errorf("sanitize dropped %q: %q", keep, got)
		}
	}
}

func TestSanitizeTextCoercesInvalidUTF8(t *testing.T) {
	got := SanitizeText("bad\xffbytes")
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
	if !strings.Contains(got, "bad") || !strings.Contains(got, "bytes") {
		t.Errorf("sanitize corrupted surrounding text: %q", got)
	}
}

func TestSanitizeTextLeavesCleanTextUnchanged(t *testing.T) {
	in := "## Title\n\n- item one\n- Persian: پ\n\n```\nok\tmodule\t0.1s\n```\n"
	if got := SanitizeText(in); got != in {
		t.Errorf("clean text changed:\n in: %q\nout: %q", in, got)
	}
}

func TestSanitizeRefLeavesValidRefUnchanged(t *testing.T) {
	for _, ref := range []string{"wright/issue-7", "main", "feature/87-mock-admin-api", "4912f7bdaa60be7ef2dd3f85bc5a5863be11e524"} {
		if got := SanitizeRef(ref); got != ref {
			t.Errorf("valid ref changed: %q -> %q", ref, got)
		}
	}
}

func TestSanitizeRefStripsWhitespaceAndControlBytes(t *testing.T) {
	// Unlike SanitizeText, a ref must not keep whitespace: newline/tab/space
	// make a ref invalid and, on the sandbox git path, could reach an unquoted
	// shell position.
	got := SanitizeRef("wright/issue-7\n\t rm -rf\x00")
	for _, bad := range []string{"\n", "\t", " ", "\x00"} {
		if strings.Contains(got, bad) {
			t.Errorf("ref kept disallowed byte %q: %q", bad, got)
		}
	}
	if !strings.HasPrefix(got, "wright/issue-7") {
		t.Errorf("ref lost its valid prefix: %q", got)
	}
}
