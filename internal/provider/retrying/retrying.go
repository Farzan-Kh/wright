// SPDX-License-Identifier: Apache-2.0

// Package retrying decorates a provider.Provider with configurable retries
// around every connection attempt to the hosting API.
package retrying

import (
	"context"
	"errors"

	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/retry"
)

// Provider wraps an inner provider.Provider, retrying each method call per
// Config until it succeeds, exhausts its attempts, or hits a non-retryable
// error.
type Provider struct {
	inner  provider.Provider
	config retry.Config
}

var _ provider.Provider = (*Provider)(nil)

// New wraps inner with retry behavior controlled by cfg.
func New(inner provider.Provider, cfg retry.Config) *Provider {
	return &Provider{inner: inner, config: cfg}
}

// retryable reports whether err is worth retrying: definite, non-transient
// responses (not found, auth failure, already exists) are not, everything
// else (network errors, rate limiting, unclassified 5xx) is.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, provider.ErrNotFound),
		errors.Is(err, provider.ErrAuth),
		errors.Is(err, provider.ErrAlreadyExists),
		errors.Is(err, provider.ErrInvalidRequest):
		return false
	default:
		return true
	}
}

func (p *Provider) Name() string { return p.inner.Name() }

func (p *Provider) ListLabeledIssues(ctx context.Context, repo provider.Repo, label string) ([]provider.Issue, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) ([]provider.Issue, error) {
		return p.inner.ListLabeledIssues(ctx, repo, label)
	})
}

func (p *Provider) GetIssue(ctx context.Context, repo provider.Repo, number int) (*provider.Issue, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) (*provider.Issue, error) {
		return p.inner.GetIssue(ctx, repo, number)
	})
}

func (p *Provider) ReadRepoFile(ctx context.Context, repo provider.Repo, ref, path string) (string, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) (string, error) {
		return p.inner.ReadRepoFile(ctx, repo, ref, path)
	})
}

func (p *Provider) ListRepoDir(ctx context.Context, repo provider.Repo, ref, path string) ([]string, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) ([]string, error) {
		return p.inner.ListRepoDir(ctx, repo, ref, path)
	})
}

func (p *Provider) CommentOnIssue(ctx context.Context, repo provider.Repo, issueNumber int, body string) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.CommentOnIssue(ctx, repo, issueNumber, body)
	})
}

func (p *Provider) AddIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.AddIssueLabel(ctx, repo, issueNumber, label)
	})
}

func (p *Provider) RemoveIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.RemoveIssueLabel(ctx, repo, issueNumber, label)
	})
}

func (p *Provider) CommentOnPullRequest(ctx context.Context, repo provider.Repo, number int, body string) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.CommentOnPullRequest(ctx, repo, number, body)
	})
}

func (p *Provider) DefaultBranch(ctx context.Context, repo provider.Repo) (string, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) (string, error) {
		return p.inner.DefaultBranch(ctx, repo)
	})
}

func (p *Provider) CreateBranch(ctx context.Context, repo provider.Repo, branch, fromRef string) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.CreateBranch(ctx, repo, branch, fromRef)
	})
}

func (p *Provider) DeleteBranch(ctx context.Context, repo provider.Repo, branch string) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.DeleteBranch(ctx, repo, branch)
	})
}

func (p *Provider) PushCommits(ctx context.Context, repo provider.Repo, branch string, commits []provider.Commit) (string, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) (string, error) {
		return p.inner.PushCommits(ctx, repo, branch, commits)
	})
}

func (p *Provider) FindOpenPullRequestByHead(ctx context.Context, repo provider.Repo, headBranch string) (*provider.PullRequest, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) (*provider.PullRequest, error) {
		return p.inner.FindOpenPullRequestByHead(ctx, repo, headBranch)
	})
}

func (p *Provider) OpenPullRequest(ctx context.Context, repo provider.Repo, spec provider.PullRequestSpec) (*provider.PullRequest, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) (*provider.PullRequest, error) {
		return p.inner.OpenPullRequest(ctx, repo, spec)
	})
}

func (p *Provider) GetPullRequest(ctx context.Context, repo provider.Repo, number int) (*provider.PullRequest, error) {
	return retry.Value(ctx, p.config, retryable, func(ctx context.Context) (*provider.PullRequest, error) {
		return p.inner.GetPullRequest(ctx, repo, number)
	})
}

func (p *Provider) UpdatePullRequestBase(ctx context.Context, repo provider.Repo, number int, baseBranch string) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.UpdatePullRequestBase(ctx, repo, number, baseBranch)
	})
}

func (p *Provider) MergePullRequest(ctx context.Context, repo provider.Repo, number int, opts provider.MergeOptions) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.MergePullRequest(ctx, repo, number, opts)
	})
}

func (p *Provider) ClosePullRequest(ctx context.Context, repo provider.Repo, number int) error {
	return retry.Do(ctx, p.config, retryable, func(ctx context.Context) error {
		return p.inner.ClosePullRequest(ctx, repo, number)
	})
}
