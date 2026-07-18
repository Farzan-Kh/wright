// SPDX-License-Identifier: Apache-2.0

package stack

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreListPendingEmptyReturnsNil(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	entries, err := s.ListPending("acme/widgets")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if entries != nil {
		t.Fatalf("ListPending = %+v, want nil", entries)
	}
}

func TestFileStoreAddListRoundTrip(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	want := Entry{
		Repo:              "acme/widgets",
		StackedPRNumber:   45,
		StackedHeadBranch: "wright/issue-14",
		DependsOnIssue:    13,
		DependsOnPRNumber: 40,
		RealBaseBranch:    "main",
		CreatedAt:         time.Now().Truncate(time.Second),
	}
	if err := s.Add(want); err != nil {
		t.Fatalf("Add: %v", err)
	}

	entries, err := s.ListPending("acme/widgets")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListPending = %+v, want 1 entry", entries)
	}
	got := entries[0]
	if got.StackedPRNumber != want.StackedPRNumber || got.DependsOnPRNumber != want.DependsOnPRNumber ||
		got.RealBaseBranch != want.RealBaseBranch || got.StackedHeadBranch != want.StackedHeadBranch {
		t.Fatalf("got = %+v, want %+v", got, want)
	}
}

func TestFileStoreListPendingReturnsMultiple(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Add(Entry{Repo: "acme/widgets", StackedPRNumber: 45, DependsOnPRNumber: 40}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(Entry{Repo: "acme/widgets", StackedPRNumber: 46, DependsOnPRNumber: 41}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	entries, err := s.ListPending("acme/widgets")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListPending = %+v, want 2 entries", entries)
	}
}

func TestFileStoreRemove(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Add(Entry{Repo: "acme/widgets", StackedPRNumber: 45}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Remove("acme/widgets", 45); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	entries, err := s.ListPending("acme/widgets")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ListPending after Remove = %+v, want empty", entries)
	}
	// Removing a nonexistent entry is not an error.
	if err := s.Remove("acme/widgets", 999); err != nil {
		t.Fatalf("Remove nonexistent: %v", err)
	}
}

func TestFileStoreSanitizesRepoPathForDirName(t *testing.T) {
	dir := t.TempDir()
	s := &FileStore{Dir: dir}
	if err := s.Add(Entry{Repo: "group/subgroup/name", StackedPRNumber: 7}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	want := filepath.Join(dir, "group_subgroup_name", "7.json")
	if s.path("group/subgroup/name", 7) != want {
		t.Fatalf("path = %q, want %q", s.path("group/subgroup/name", 7), want)
	}
}

func TestFileStoreAddRequiresRepoAndPRNumber(t *testing.T) {
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Add(Entry{StackedPRNumber: 1}); err == nil {
		t.Fatal("Add with empty repo: want error")
	}
	if err := s.Add(Entry{Repo: "acme/widgets"}); err == nil {
		t.Fatal("Add with zero PR number: want error")
	}
}
