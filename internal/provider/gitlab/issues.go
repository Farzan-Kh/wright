package gitlab

import (
	"context"
	"fmt"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/farzan-kh/patchr/internal/provider"
)

// ListLabeledIssues returns open issues carrying label. GitLab's issues API
// returns only issues (never merge requests), so no filtering is needed.
func (c *Client) ListLabeledIssues(ctx context.Context, repo provider.Repo, label string) ([]provider.Issue, error) {
	labels := gl.LabelOptions{label}
	opts := &gl.ListProjectIssuesOptions{
		State:       gl.Ptr("opened"),
		Labels:      &labels,
		ListOptions: gl.ListOptions{PerPage: 100},
	}

	var issues []provider.Issue
	for {
		page, resp, err := c.gl.Issues.ListProjectIssues(pid(repo), opts, ctxOpt(ctx))
		if err != nil {
			return nil, fmt.Errorf("gitlab: list issues labeled %q in %s: %w", label, repo.FullPath, classify(err))
		}
		for _, iss := range page {
			issues = append(issues, toIssue(iss))
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return issues, nil
}

// CommentOnIssue posts body as a note on the issue.
func (c *Client) CommentOnIssue(ctx context.Context, repo provider.Repo, issueNumber int, body string) error {
	_, _, err := c.gl.Notes.CreateIssueNote(pid(repo), int64(issueNumber), &gl.CreateIssueNoteOptions{
		Body: gl.Ptr(body),
	}, ctxOpt(ctx))
	if err != nil {
		return fmt.Errorf("gitlab: comment on issue #%d in %s: %w", issueNumber, repo.FullPath, classify(err))
	}
	return nil
}

// toIssue maps a client-go Issue onto the domain type.
func toIssue(iss *gl.Issue) provider.Issue {
	var author string
	if iss.Author != nil {
		author = iss.Author.Username
	}
	var created, updated time.Time
	if iss.CreatedAt != nil {
		created = *iss.CreatedAt
	}
	if iss.UpdatedAt != nil {
		updated = *iss.UpdatedAt
	}
	return provider.Issue{
		Number:    int(iss.IID),
		Title:     iss.Title,
		Body:      iss.Description,
		Labels:    []string(iss.Labels),
		URL:       iss.WebURL,
		Author:    author,
		CreatedAt: created,
		UpdatedAt: updated,
	}
}
