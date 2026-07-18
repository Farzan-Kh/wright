// SPDX-License-Identifier: Apache-2.0

package stack

import (
	"context"
	"testing"

	"github.com/farzan-kh/wright/internal/provider"
)

// fakeProvider is a minimal provider.Provider for Reconcile's tests: only
// GetPullRequest, UpdatePullRequestBase, and CommentOnPullRequest carry real
// behavior, everything else is an unused no-op.
type fakeProvider struct {
	prs map[int]*provider.PullRequest // PR number -> current state

	retargeted    map[int]string // stacked PR number -> new base it was set to
	comments      map[int]string // stacked PR number -> last comment body
	updateBaseErr error
}

var _ provider.Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) ListLabeledIssues(context.Context, provider.Repo, string) ([]provider.Issue, error) {
	return nil, nil
}
func (f *fakeProvider) GetIssue(context.Context, provider.Repo, int) (*provider.Issue, error) {
	return nil, nil
}
func (f *fakeProvider) ReadRepoFile(context.Context, provider.Repo, string, string) (string, error) {
	return "", nil
}
func (f *fakeProvider) ListRepoDir(context.Context, provider.Repo, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeProvider) CommentOnIssue(context.Context, provider.Repo, int, string) error { return nil }
func (f *fakeProvider) AddIssueLabel(context.Context, provider.Repo, int, string) error  { return nil }
func (f *fakeProvider) RemoveIssueLabel(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeProvider) CommentOnPullRequest(_ context.Context, _ provider.Repo, number int, body string) error {
	if f.comments == nil {
		f.comments = map[int]string{}
	}
	f.comments[number] = body
	return nil
}
func (f *fakeProvider) DefaultBranch(context.Context, provider.Repo) (string, error) {
	return "main", nil
}
func (f *fakeProvider) CreateBranch(context.Context, provider.Repo, string, string) error { return nil }
func (f *fakeProvider) DeleteBranch(context.Context, provider.Repo, string) error         { return nil }
func (f *fakeProvider) PushCommits(context.Context, provider.Repo, string, []provider.Commit) (string, error) {
	return "", nil
}
func (f *fakeProvider) FindOpenPullRequestByHead(context.Context, provider.Repo, string) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeProvider) OpenPullRequest(context.Context, provider.Repo, provider.PullRequestSpec) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeProvider) GetPullRequest(_ context.Context, _ provider.Repo, number int) (*provider.PullRequest, error) {
	pr, ok := f.prs[number]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return pr, nil
}
func (f *fakeProvider) UpdatePullRequestBase(_ context.Context, _ provider.Repo, number int, baseBranch string) error {
	if f.updateBaseErr != nil {
		return f.updateBaseErr
	}
	if f.retargeted == nil {
		f.retargeted = map[int]string{}
	}
	f.retargeted[number] = baseBranch
	return nil
}
func (f *fakeProvider) MergePullRequest(context.Context, provider.Repo, int, provider.MergeOptions) error {
	return nil
}
func (f *fakeProvider) ClosePullRequest(context.Context, provider.Repo, int) error { return nil }

var testRepo = provider.Repo{FullPath: "acme/widgets"}

func TestReconcileRetargetsOnMergedDependency(t *testing.T) {
	p := &fakeProvider{prs: map[int]*provider.PullRequest{
		40: {Number: 40, State: "merged"},
	}}
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Add(Entry{
		Repo:              testRepo.FullPath,
		StackedPRNumber:   45,
		StackedHeadBranch: "wright/issue-13",
		DependsOnIssue:    13,
		DependsOnPRNumber: 40,
		RealBaseBranch:    "main",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := Reconcile(t.Context(), p, testRepo, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := p.retargeted[45]; got != "main" {
		t.Fatalf("retargeted base = %q, want main", got)
	}
	if _, ok := p.comments[45]; !ok {
		t.Fatal("expected a comment on the stacked PR")
	}
	entries, err := s.ListPending(testRepo.FullPath)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after reconcile = %+v, want empty (resolved entry removed)", entries)
	}
}

func TestReconcileFlagsClosedUnmergedDependencyWithoutRetargeting(t *testing.T) {
	p := &fakeProvider{prs: map[int]*provider.PullRequest{
		40: {Number: 40, State: "closed"},
	}}
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Add(Entry{
		Repo:              testRepo.FullPath,
		StackedPRNumber:   45,
		DependsOnIssue:    13,
		DependsOnPRNumber: 40,
		RealBaseBranch:    "main",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := Reconcile(t.Context(), p, testRepo, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if _, retargeted := p.retargeted[45]; retargeted {
		t.Fatal("should not retarget when dependency was closed unmerged")
	}
	if _, ok := p.comments[45]; !ok {
		t.Fatal("expected a comment flagging the abandoned dependency")
	}
	entries, err := s.ListPending(testRepo.FullPath)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after reconcile = %+v, want empty (dropped from tracking)", entries)
	}
}

func TestReconcileLeavesStillOpenDependencyPending(t *testing.T) {
	p := &fakeProvider{prs: map[int]*provider.PullRequest{
		40: {Number: 40, State: "open"},
	}}
	s := &FileStore{Dir: t.TempDir()}
	if err := s.Add(Entry{
		Repo:              testRepo.FullPath,
		StackedPRNumber:   45,
		DependsOnIssue:    13,
		DependsOnPRNumber: 40,
		RealBaseBranch:    "main",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := Reconcile(t.Context(), p, testRepo, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(p.retargeted) != 0 || len(p.comments) != 0 {
		t.Fatalf("no action expected while dependency is still open: retargeted=%v comments=%v", p.retargeted, p.comments)
	}
	entries, err := s.ListPending(testRepo.FullPath)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries after reconcile = %+v, want 1 (still pending)", entries)
	}
}
