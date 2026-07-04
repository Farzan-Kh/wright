package github

import (
	"context"
	"errors"
	"fmt"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/patchr/internal/provider"
)

// ListLabeledIssues returns open issues carrying label. The GitHub issues API
// also returns pull requests, so those are skipped via IsPullRequest.
func (c *Client) ListLabeledIssues(ctx context.Context, repo provider.Repo, label string) ([]provider.Issue, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}

	opts := &gh.IssueListByRepoOptions{
		State:       "open",
		Labels:      []string{label},
		ListOptions: gh.ListOptions{PerPage: 100},
	}

	var issues []provider.Issue
	for {
		page, resp, err := c.gh.Issues.ListByRepo(ctx, owner, name, opts)
		if err != nil {
			return nil, fmt.Errorf("github: list issues labeled %q in %s: %w", label, repo.FullPath, classify(err))
		}
		for _, iss := range page {
			if iss.IsPullRequest() {
				continue
			}
			issues = append(issues, toIssue(iss))
		}
		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}
	return issues, nil
}

// CommentOnIssue posts body as a comment on the issue.
func (c *Client) CommentOnIssue(ctx context.Context, repo provider.Repo, issueNumber int, body string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	_, _, err = c.gh.Issues.CreateComment(ctx, owner, name, issueNumber, &gh.IssueComment{Body: gh.Ptr(body)})
	if err != nil {
		return fmt.Errorf("github: comment on issue #%d in %s: %w", issueNumber, repo.FullPath, classify(err))
	}
	return nil
}

// AddIssueLabel adds label to the issue.
func (c *Client) AddIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	_, _, err = c.gh.Issues.AddLabelsToIssue(ctx, owner, name, issueNumber, []string{label})
	if err != nil {
		return fmt.Errorf("github: add label %q on issue #%d in %s: %w", label, issueNumber, repo.FullPath, classify(err))
	}
	return nil
}

// RemoveIssueLabel removes label from the issue. GitHub returns 404 when the
// label is already absent; treat that as success.
func (c *Client) RemoveIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	_, err = c.gh.Issues.RemoveLabelForIssue(ctx, owner, name, issueNumber, label)
	if err != nil {
		if errors.Is(classify(err), provider.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("github: remove label %q on issue #%d in %s: %w", label, issueNumber, repo.FullPath, classify(err))
	}
	return nil
}

// toIssue maps a go-github Issue onto the domain type.
func toIssue(iss *gh.Issue) provider.Issue {
	labels := make([]string, 0, len(iss.Labels))
	for _, l := range iss.Labels {
		labels = append(labels, l.GetName())
	}
	return provider.Issue{
		Number:    iss.GetNumber(),
		Title:     iss.GetTitle(),
		Body:      iss.GetBody(),
		Labels:    labels,
		URL:       iss.GetHTMLURL(),
		Author:    iss.GetUser().GetLogin(),
		CreatedAt: iss.GetCreatedAt().Time,
		UpdatedAt: iss.GetUpdatedAt().Time,
	}
}
