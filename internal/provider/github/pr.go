// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/wright/internal/provider"
)

// FindOpenPullRequestByHead returns an open pull request for headBranch, or nil.
func (c *Client) FindOpenPullRequestByHead(ctx context.Context, repo provider.Repo, headBranch string) (*provider.PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	prs, _, err := c.gh.PullRequests.List(ctx, owner, name, &gh.PullRequestListOptions{
		State:       "open",
		Head:        owner + ":" + headBranch,
		ListOptions: gh.ListOptions{PerPage: 1},
	})
	if err != nil {
		return nil, fmt.Errorf("github: list open pull requests for head %q in %s: %w", headBranch, repo.FullPath, classify(err))
	}
	if len(prs) == 0 {
		return nil, nil
	}
	pr := prs[0]
	return &provider.PullRequest{
		Number:     pr.GetNumber(),
		URL:        pr.GetHTMLURL(),
		HeadBranch: pr.GetHead().GetRef(),
		BaseBranch: pr.GetBase().GetRef(),
	}, nil
}

// OpenPullRequest opens a pull request per spec.
func (c *Client) OpenPullRequest(ctx context.Context, repo provider.Repo, spec provider.PullRequestSpec) (*provider.PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	pr, _, err := c.gh.PullRequests.Create(ctx, owner, name, &gh.NewPullRequest{
		Title: gh.Ptr(provider.SanitizeText(spec.Title)),
		Body:  gh.Ptr(provider.SanitizeText(spec.Body)),
		Head:  gh.Ptr(spec.HeadBranch),
		Base:  gh.Ptr(spec.BaseBranch),
		Draft: gh.Ptr(spec.Draft),
	})
	if err != nil {
		// The caller retries this non-idempotent create on transient errors
		// (see provider/retrying). If an earlier attempt actually reached
		// GitHub but the response was lost (timeout, connection reset), the
		// retry lands here with a duplicate-branch conflict even though the PR
		// already exists. Recover by returning that PR instead of failing.
		if isAlreadyExists(err) {
			if existing, findErr := c.FindOpenPullRequestByHead(ctx, repo, spec.HeadBranch); findErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("github: open pull request %s->%s in %s: %w", spec.HeadBranch, spec.BaseBranch, repo.FullPath, classify(err))
	}
	return &provider.PullRequest{
		Number:     pr.GetNumber(),
		URL:        pr.GetHTMLURL(),
		HeadBranch: pr.GetHead().GetRef(),
		BaseBranch: pr.GetBase().GetRef(),
	}, nil
}

// CommentOnPullRequest posts body as a comment on the pull request. On GitHub a
// PR is also an issue (shared numbering), so this uses the same issue-comment
// endpoint as CommentOnIssue.
func (c *Client) CommentOnPullRequest(ctx context.Context, repo provider.Repo, number int, body string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	if _, _, err := c.gh.Issues.CreateComment(ctx, owner, name, number, &gh.IssueComment{Body: gh.Ptr(provider.SanitizeText(body))}); err != nil {
		return fmt.Errorf("github: comment on pull request #%d in %s: %w", number, repo.FullPath, classify(err))
	}
	return nil
}

// MergePullRequest merges the pull request identified by number, optionally
// deleting its head branch afterwards.
func (c *Client) MergePullRequest(ctx context.Context, repo provider.Repo, number int, opts provider.MergeOptions) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}

	var mergeOpts *gh.PullRequestOptions
	if opts.Method != "" {
		mergeOpts = &gh.PullRequestOptions{MergeMethod: string(opts.Method)}
	}
	if _, _, err := c.gh.PullRequests.Merge(ctx, owner, name, number, "", mergeOpts); err != nil {
		return fmt.Errorf("github: merge pull request #%d in %s: %w", number, repo.FullPath, classify(err))
	}

	if opts.DeleteBranch {
		pr, _, err := c.gh.PullRequests.Get(ctx, owner, name, number)
		if err != nil {
			return fmt.Errorf("github: look up merged pull request #%d in %s: %w", number, repo.FullPath, classify(err))
		}
		if err := c.DeleteBranch(ctx, repo, pr.GetHead().GetRef()); err != nil {
			return err
		}
	}
	return nil
}

// ClosePullRequest closes the pull request without merging.
func (c *Client) ClosePullRequest(ctx context.Context, repo provider.Repo, number int) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	if _, _, err := c.gh.PullRequests.Edit(ctx, owner, name, number, &gh.PullRequest{State: gh.Ptr("closed")}); err != nil {
		return fmt.Errorf("github: close pull request #%d in %s: %w", number, repo.FullPath, classify(err))
	}
	return nil
}
