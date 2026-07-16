// SPDX-License-Identifier: Apache-2.0

package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestFakeReplaceUniqueMatch(t *testing.T) {
	f := &FakeExec{Files: map[string]string{"a.txt": "alpha beta gamma"}}
	if err := f.ReplaceText(context.Background(), "a.txt", "beta", "BETA"); err != nil {
		t.Fatalf("ReplaceText: %v", err)
	}
	if got := f.Files["a.txt"]; got != "alpha BETA gamma" {
		t.Fatalf("content = %q", got)
	}
}

func TestFakeReplaceRejectsAmbiguousMatch(t *testing.T) {
	f := &FakeExec{Files: map[string]string{"a.txt": "x x x"}}
	err := f.ReplaceText(context.Background(), "a.txt", "x", "y")
	if err == nil || !strings.Contains(err.Error(), "not unique") {
		t.Fatalf("err = %v, want a not-unique error", err)
	}
	if got := f.Files["a.txt"]; got != "x x x" {
		t.Fatalf("file mutated on ambiguous replace: %q", got)
	}
}

func TestFakeReplaceRejectsMissingMatch(t *testing.T) {
	f := &FakeExec{Files: map[string]string{"a.txt": "hello"}}
	err := f.ReplaceText(context.Background(), "a.txt", "world", "there")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want a not-found error", err)
	}
}
