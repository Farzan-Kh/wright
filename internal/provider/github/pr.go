package github

import (
	"context"
	"fmt"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/patchr/internal/provider"
)

// OpenPullRequest opens a pull request per spec.
func (c *Client) OpenPullRequest(ctx context.Context, repo provider.Repo, spec provider.PullRequestSpec) (*provider.PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	pr, _, err := c.gh.PullRequests.Create(ctx, owner, name, &gh.NewPullRequest{
		Title: gh.Ptr(spec.Title),
		Body:  gh.Ptr(spec.Body),
		Head:  gh.Ptr(spec.HeadBranch),
		Base:  gh.Ptr(spec.BaseBranch),
		Draft: gh.Ptr(spec.Draft),
	})
	if err != nil {
		return nil, fmt.Errorf("github: open pull request %s->%s in %s: %w", spec.HeadBranch, spec.BaseBranch, repo.FullPath, classify(err))
	}
	return &provider.PullRequest{
		Number:     pr.GetNumber(),
		URL:        pr.GetHTMLURL(),
		HeadBranch: pr.GetHead().GetRef(),
		BaseBranch: pr.GetBase().GetRef(),
	}, nil
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
