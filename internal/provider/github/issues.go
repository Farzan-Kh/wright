package github

import (
	"context"
	"errors"
	"fmt"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/wright/internal/provider"
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

	for i := range issues {
		comments, err := c.listIssueComments(ctx, owner, name, issues[i].Number)
		if err != nil {
			return nil, fmt.Errorf("github: list comments on issue #%d in %s: %w", issues[i].Number, repo.FullPath, classify(err))
		}
		issues[i].Comments = comments
	}
	return issues, nil
}

// GetIssue fetches a single issue by number, without its comment thread.
func (c *Client) GetIssue(ctx context.Context, repo provider.Repo, number int) (*provider.Issue, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	iss, _, err := c.gh.Issues.Get(ctx, owner, name, number)
	if err != nil {
		return nil, fmt.Errorf("github: get issue #%d in %s: %w", number, repo.FullPath, classify(err))
	}
	got := toIssue(iss)
	return &got, nil
}

// listIssueComments fetches every comment on the given issue, oldest first.
func (c *Client) listIssueComments(ctx context.Context, owner, name string, issueNumber int) ([]provider.Comment, error) {
	opts := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	var comments []provider.Comment
	for {
		page, resp, err := c.gh.Issues.ListComments(ctx, owner, name, issueNumber, opts)
		if err != nil {
			return nil, err
		}
		for _, com := range page {
			comments = append(comments, provider.Comment{
				Author:    com.GetUser().GetLogin(),
				Body:      com.GetBody(),
				CreatedAt: com.GetCreatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}
	return comments, nil
}

// CommentOnIssue posts body as a comment on the issue.
func (c *Client) CommentOnIssue(ctx context.Context, repo provider.Repo, issueNumber int, body string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	_, _, err = c.gh.Issues.CreateComment(ctx, owner, name, issueNumber, &gh.IssueComment{Body: gh.Ptr(provider.SanitizeText(body))})
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
		State:     iss.GetState(),
		CreatedAt: iss.GetCreatedAt().Time,
		UpdatedAt: iss.GetUpdatedAt().Time,
	}
}
