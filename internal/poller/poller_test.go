// SPDX-License-Identifier: Apache-2.0

package poller

import (
	"context"
	"testing"

	"github.com/farzan-kh/wright/internal/provider"
)

// fakeProvider is a minimal provider.Provider for Once's ordering test: only
// ListLabeledIssues carries real behavior, everything else is an unused no-op.
type fakeProvider struct {
	issues []provider.Issue
}

var _ provider.Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) ListLabeledIssues(context.Context, provider.Repo, string) ([]provider.Issue, error) {
	return f.issues, nil
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
func (f *fakeProvider) CommentOnPullRequest(context.Context, provider.Repo, int, string) error {
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
func (f *fakeProvider) GetPullRequest(context.Context, provider.Repo, int) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeProvider) UpdatePullRequestBase(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeProvider) MergePullRequest(context.Context, provider.Repo, int, provider.MergeOptions) error {
	return nil
}
func (f *fakeProvider) ClosePullRequest(context.Context, provider.Repo, int) error { return nil }

// TestOnceReturnsIssuesAscendingByNumber is the regression test for
// oldest-first ordering: providers return newest-first by default, but
// resolving oldest issues first gives an issue's dependencies (almost always
// filed, and so numbered, earlier) a chance to be resolved before the issue
// that depends on them is attempted.
func TestOnceReturnsIssuesAscendingByNumber(t *testing.T) {
	p := &Poller{
		Provider: &fakeProvider{issues: []provider.Issue{
			{Number: 17}, {Number: 14}, {Number: 16}, {Number: 15},
		}},
		Repo:  provider.Repo{FullPath: "acme/widgets"},
		Label: "wright",
	}

	issues, err := p.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}

	var got []int
	for _, iss := range issues {
		got = append(got, iss.Number)
	}
	want := []int{14, 15, 16, 17}
	if len(got) != len(want) {
		t.Fatalf("issue numbers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("issue numbers = %v, want %v", got, want)
		}
	}
}
