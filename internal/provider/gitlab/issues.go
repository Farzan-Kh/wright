package gitlab

import (
	"context"
	"errors"
	"fmt"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/farzan-kh/wright/internal/provider"
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

	for i := range issues {
		notes, err := c.listIssueNotes(ctx, repo, issues[i].Number)
		if err != nil {
			return nil, fmt.Errorf("gitlab: list notes on issue #%d in %s: %w", issues[i].Number, repo.FullPath, classify(err))
		}
		issues[i].Comments = notes
	}
	return issues, nil
}

// listIssueNotes fetches every user-authored note (comment) on the given
// issue, oldest first. System notes (label changes, status transitions, etc.)
// are excluded — they aren't discussion content.
func (c *Client) listIssueNotes(ctx context.Context, repo provider.Repo, issueNumber int) ([]provider.Comment, error) {
	opts := &gl.ListIssueNotesOptions{ListOptions: gl.ListOptions{PerPage: 100}}
	var comments []provider.Comment
	for {
		page, resp, err := c.gl.Notes.ListIssueNotes(pid(repo), int64(issueNumber), opts, ctxOpt(ctx))
		if err != nil {
			return nil, err
		}
		for _, note := range page {
			if note.System {
				continue
			}
			var created time.Time
			if note.CreatedAt != nil {
				created = *note.CreatedAt
			}
			comments = append(comments, provider.Comment{
				Author:    note.Author.Username,
				Body:      note.Body,
				CreatedAt: created,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return comments, nil
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

// AddIssueLabel adds label to the issue.
func (c *Client) AddIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	labels := gl.LabelOptions{label}
	_, _, err := c.gl.Issues.UpdateIssue(pid(repo), int64(issueNumber), &gl.UpdateIssueOptions{
		AddLabels: &labels,
	}, ctxOpt(ctx))
	if err != nil {
		return fmt.Errorf("gitlab: add label %q on issue #%d in %s: %w", label, issueNumber, repo.FullPath, classify(err))
	}
	return nil
}

// RemoveIssueLabel removes label from the issue. GitLab's UpdateIssue is
// idempotent for a label that's already absent from the issue, but treat a
// 404 (e.g. issue or label deleted concurrently) as success too, matching the
// GitHub adapter's already-absent handling.
func (c *Client) RemoveIssueLabel(ctx context.Context, repo provider.Repo, issueNumber int, label string) error {
	labels := gl.LabelOptions{label}
	_, _, err := c.gl.Issues.UpdateIssue(pid(repo), int64(issueNumber), &gl.UpdateIssueOptions{
		RemoveLabels: &labels,
	}, ctxOpt(ctx))
	if err != nil {
		if errors.Is(classify(err), provider.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("gitlab: remove label %q on issue #%d in %s: %w", label, issueNumber, repo.FullPath, classify(err))
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
