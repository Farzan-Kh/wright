// SPDX-License-Identifier: Apache-2.0

// Package logging decorates a provider.Provider with structured logging of
// every call: the method and its key arguments on entry, and the duration
// plus outcome (a brief result summary, or the full error chain) on exit.
//
// It wraps the innermost (github/gitlab) client rather than the outer
// retrying.Provider, so every retry attempt is logged individually — that
// visibility into what each attempt actually failed with is the point: the
// error a caller ultimately sees is often just the last attempt's, which can
// hide what was going wrong across earlier ones.
package logging

import (
	"context"
	"log/slog"
	"time"

	"github.com/farzan-kh/wright/internal/provider"
)

// Provider wraps an inner provider.Provider, logging every method call to log.
type Provider struct {
	inner provider.Provider
	log   *slog.Logger
}

var _ provider.Provider = (*Provider)(nil)

// New wraps inner with call logging. log must not be nil; pass a discarding
// logger (see internal/logging) to disable output.
func New(inner provider.Provider, log *slog.Logger) *Provider {
	return &Provider{inner: inner, log: log.With("provider", inner.Name())}
}

func (p *Provider) Name() string { return p.inner.Name() }

// start logs the call's entry and returns a logger tagged with method/attrs
// plus the start time, for the matching end call.
func (p *Provider) start(method string, attrs ...any) (*slog.Logger, time.Time) {
	l := p.log.With(append([]any{"method", method}, attrs...)...)
	l.Debug("provider call started")
	return l, time.Now()
}

// end logs the call's outcome: Error with the full error chain on failure,
// Debug with a brief result summary on success.
func end(l *slog.Logger, start time.Time, err error, resultAttrs ...any) {
	dur := time.Since(start)
	if err != nil {
		l.Error("provider call failed", append([]any{"duration_ms", dur.Milliseconds(), "error", err.Error()}, resultAttrs...)...)
		return
	}
	l.Debug("provider call ok", append([]any{"duration_ms", dur.Milliseconds()}, resultAttrs...)...)
}

func (p *Provider) ListLabeledIssues(ctx context.Context, repo provider.Repo, label string) ([]provider.Issue, error) {
	l, start := p.start("ListLabeledIssues", "repo", repo.FullPath, "label", label)
	issues, err := p.inner.ListLabeledIssues(ctx, repo, label)
	end(l, start, err, "count", len(issues))
	return issues, err
}

func (p *Provider) GetIssue(ctx context.Context, repo provider.Repo, number int) (*provider.Issue, error) {
	l, start := p.start("GetIssue", "repo", repo.FullPath, "issue", number)
	iss, err := p.inner.GetIssue(ctx, repo, number)
	end(l, start, err)
	return iss, err
}

func (p *Provider) ReadRepoFile(ctx context.Context, repo provider.Repo, ref, path string) (string, error) {
	l, start := p.start("ReadRepoFile", "repo", repo.FullPath, "ref", ref, "path", path)
	content, err := p.inner.ReadRepoFile(ctx, repo, ref, path)
	end(l, start, err, "bytes", len(content))
	return content, err
}

func (p *Provider) ListRepoDir(ctx context.Context, repo provider.Repo, ref, path string) ([]string, error) {
	l, start := p.start("ListRepoDir", "repo", repo.FullPath, "ref", ref, "path", path)
	entries, err := p.inner.ListRepoDir(ctx, repo, ref, path)
	end(l, start, err, "count", len(entries))
	return entries, err
}

func (p *Provider) CommentOnIssue(ctx context.Context, repo provider.Repo, issueNumber int, body string) error {
	l, start := p.start("CommentOnIssue", "repo", repo.FullPath, "issue", issueNumber, "body_bytes", len(body))
	err := p.inner.CommentOnIssue(ctx, repo, issueNumber, body)
	end(l, start, err)
	return err
}

func (p *Provider) AddIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	l, start := p.start("AddIssueLabel", "repo", repo.FullPath, "issue", issueNumber, "label", label)
	err := p.inner.AddIssueLabel(ctx, repo, issueNumber, label)
	end(l, start, err)
	return err
}

func (p *Provider) RemoveIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	l, start := p.start("RemoveIssueLabel", "repo", repo.FullPath, "issue", issueNumber, "label", label)
	err := p.inner.RemoveIssueLabel(ctx, repo, issueNumber, label)
	end(l, start, err)
	return err
}

func (p *Provider) CommentOnPullRequest(ctx context.Context, repo provider.Repo, number int, body string) error {
	l, start := p.start("CommentOnPullRequest", "repo", repo.FullPath, "pr", number, "body_bytes", len(body))
	err := p.inner.CommentOnPullRequest(ctx, repo, number, body)
	end(l, start, err)
	return err
}

func (p *Provider) DefaultBranch(ctx context.Context, repo provider.Repo) (string, error) {
	l, start := p.start("DefaultBranch", "repo", repo.FullPath)
	branch, err := p.inner.DefaultBranch(ctx, repo)
	end(l, start, err, "branch", branch)
	return branch, err
}

func (p *Provider) CreateBranch(ctx context.Context, repo provider.Repo, branch, fromRef string) error {
	l, start := p.start("CreateBranch", "repo", repo.FullPath, "branch", branch, "from_ref", fromRef)
	err := p.inner.CreateBranch(ctx, repo, branch, fromRef)
	end(l, start, err)
	return err
}

func (p *Provider) DeleteBranch(ctx context.Context, repo provider.Repo, branch string) error {
	l, start := p.start("DeleteBranch", "repo", repo.FullPath, "branch", branch)
	err := p.inner.DeleteBranch(ctx, repo, branch)
	end(l, start, err)
	return err
}

func (p *Provider) PushCommits(ctx context.Context, repo provider.Repo, branch string, commits []provider.Commit) (string, error) {
	l, start := p.start("PushCommits", "repo", repo.FullPath, "branch", branch, "commits", len(commits))
	sha, err := p.inner.PushCommits(ctx, repo, branch, commits)
	end(l, start, err, "sha", sha)
	return sha, err
}

func (p *Provider) FindOpenPullRequestByHead(ctx context.Context, repo provider.Repo, headBranch string) (*provider.PullRequest, error) {
	l, start := p.start("FindOpenPullRequestByHead", "repo", repo.FullPath, "head_branch", headBranch)
	pr, err := p.inner.FindOpenPullRequestByHead(ctx, repo, headBranch)
	found := pr != nil
	end(l, start, err, "found", found)
	return pr, err
}

func (p *Provider) OpenPullRequest(ctx context.Context, repo provider.Repo, spec provider.PullRequestSpec) (*provider.PullRequest, error) {
	l, start := p.start("OpenPullRequest", "repo", repo.FullPath, "head_branch", spec.HeadBranch, "base_branch", spec.BaseBranch, "draft", spec.Draft)
	pr, err := p.inner.OpenPullRequest(ctx, repo, spec)
	if pr != nil {
		end(l, start, err, "number", pr.Number)
	} else {
		end(l, start, err)
	}
	return pr, err
}

func (p *Provider) GetPullRequest(ctx context.Context, repo provider.Repo, number int) (*provider.PullRequest, error) {
	l, start := p.start("GetPullRequest", "repo", repo.FullPath, "pr", number)
	pr, err := p.inner.GetPullRequest(ctx, repo, number)
	if pr != nil {
		end(l, start, err, "state", pr.State)
	} else {
		end(l, start, err)
	}
	return pr, err
}

func (p *Provider) UpdatePullRequestBase(ctx context.Context, repo provider.Repo, number int, baseBranch string) error {
	l, start := p.start("UpdatePullRequestBase", "repo", repo.FullPath, "pr", number, "base_branch", baseBranch)
	err := p.inner.UpdatePullRequestBase(ctx, repo, number, baseBranch)
	end(l, start, err)
	return err
}

func (p *Provider) MergePullRequest(ctx context.Context, repo provider.Repo, number int, opts provider.MergeOptions) error {
	l, start := p.start("MergePullRequest", "repo", repo.FullPath, "pr", number, "method", string(opts.Method), "delete_branch", opts.DeleteBranch)
	err := p.inner.MergePullRequest(ctx, repo, number, opts)
	end(l, start, err)
	return err
}

func (p *Provider) ClosePullRequest(ctx context.Context, repo provider.Repo, number int) error {
	l, start := p.start("ClosePullRequest", "repo", repo.FullPath, "pr", number)
	err := p.inner.ClosePullRequest(ctx, repo, number)
	end(l, start, err)
	return err
}
