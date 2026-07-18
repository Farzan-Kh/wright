// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"path/filepath"
	"testing"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
)

func TestFileStoreLoadMissingReturnsNil(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	e, err := s.Load("acme/widgets", 42)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e != nil {
		t.Fatalf("Load = %+v, want nil", e)
	}
}

func TestFileStoreSaveLoadRoundTrip(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	want := Entry{
		Repo:        "acme/widgets",
		IssueNumber: 42,
		Stage:       StageAgentIncomplete,
		Reason:      "agent: max turns reached",
		Branch:      "wright/issue-42",
		BaseBranch:  "main",
		Diff:        "diff --git a/x b/x\n",
		System:      []llm.SystemBlock{{Text: "system"}},
		History:     []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Cost:        cost.Summary{Turns: 3, Usage: cost.Usage{InputTokens: 10}},
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load("acme/widgets", 42)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("Load = nil, want entry")
	}
	if got.Reason != want.Reason || got.Stage != want.Stage || got.Diff != want.Diff {
		t.Fatalf("got = %+v, want %+v", got, want)
	}
	if len(got.History) != 1 || got.History[0].Content[0].Text != "hi" {
		t.Fatalf("history not round-tripped: %+v", got.History)
	}
	if got.Cost.Turns != 3 {
		t.Fatalf("cost not round-tripped: %+v", got.Cost)
	}
}

func TestFileStoreSaveOverwrites(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Save(Entry{Repo: "acme/widgets", IssueNumber: 1, Reason: "first"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(Entry{Repo: "acme/widgets", IssueNumber: 1, Reason: "second"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load("acme/widgets", 1)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Reason != "second" {
		t.Fatalf("Reason = %q, want %q", got.Reason, "second")
	}
}

func TestFileStoreClear(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Save(Entry{Repo: "acme/widgets", IssueNumber: 1}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Clear("acme/widgets", 1); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err := s.Load("acme/widgets", 1)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != nil {
		t.Fatalf("Load after Clear = %+v, want nil", got)
	}
	// Clearing a nonexistent entry is not an error.
	if err := s.Clear("acme/widgets", 999); err != nil {
		t.Fatalf("Clear nonexistent: %v", err)
	}
}

func TestFileStoreSanitizesRepoPathForDirName(t *testing.T) {
	dir := t.TempDir()
	s := &FileStore{Dir: dir}
	if err := s.Save(Entry{Repo: "group/subgroup/name", IssueNumber: 7}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := filepath.Join(dir, "group_subgroup_name", "7.json")
	if _, err := s.Load("group/subgroup/name", 7); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.path("group/subgroup/name", 7) != want {
		t.Fatalf("path = %q, want %q", s.path("group/subgroup/name", 7), want)
	}
}

func TestFileStoreSaveRequiresRepoAndIssueNumber(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Save(Entry{IssueNumber: 1}); err == nil {
		t.Fatal("Save with empty repo: want error")
	}
	if err := s.Save(Entry{Repo: "acme/widgets"}); err == nil {
		t.Fatal("Save with zero issue number: want error")
	}
}
