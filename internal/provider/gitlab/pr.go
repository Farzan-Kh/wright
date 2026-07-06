package gitlab

import (
	"context"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/farzan-kh/wright/internal/provider"
)

// FindOpenPullRequestByHead returns an open merge request for headBranch, or nil.
func (c *Client) FindOpenPullRequestByHead(ctx context.Context, repo provider.Repo, headBranch string) (*provider.PullRequest, error) {
	mrs, _, err := c.gl.MergeRequests.ListProjectMergeRequests(pid(repo), &gl.ListProjectMergeRequestsOptions{
		State:        gl.Ptr("opened"),
		SourceBranch: gl.Ptr(headBranch),
		ListOptions:  gl.ListOptions{PerPage: 1},
	}, ctxOpt(ctx))
	if err != nil {
		return nil, fmt.Errorf("gitlab: list open merge requests for head %q in %s: %w", headBranch, repo.FullPath, classify(err))
	}
	if len(mrs) == 0 {
		return nil, nil
	}
	mr := mrs[0]
	return &provider.PullRequest{
		Number:     int(mr.IID),
		URL:        mr.WebURL,
		HeadBranch: mr.SourceBranch,
		BaseBranch: mr.TargetBranch,
	}, nil
}

// OpenPullRequest opens a merge request per spec. A draft is expressed as a
// "Draft:" title prefix, GitLab's convention.
func (c *Client) OpenPullRequest(ctx context.Context, repo provider.Repo, spec provider.PullRequestSpec) (*provider.PullRequest, error) {
	title := spec.Title
	if spec.Draft {
		title = "Draft: " + title
	}
	mr, _, err := c.gl.MergeRequests.CreateMergeRequest(pid(repo), &gl.CreateMergeRequestOptions{
		Title:        gl.Ptr(title),
		Description:  gl.Ptr(spec.Body),
		SourceBranch: gl.Ptr(spec.HeadBranch),
		TargetBranch: gl.Ptr(spec.BaseBranch),
	}, ctxOpt(ctx))
	if err != nil {
		// The caller retries this non-idempotent create on transient errors
		// (see provider/retrying). If an earlier attempt actually reached
		// GitLab but the response was lost (timeout, connection reset), the
		// retry lands here with a duplicate-branch conflict even though the MR
		// already exists. Recover by returning that MR instead of failing.
		if isAlreadyExists(err) {
			if existing, findErr := c.FindOpenPullRequestByHead(ctx, repo, spec.HeadBranch); findErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("gitlab: open merge request %s->%s in %s: %w", spec.HeadBranch, spec.BaseBranch, repo.FullPath, classify(err))
	}
	return &provider.PullRequest{
		Number:     int(mr.IID),
		URL:        mr.WebURL,
		HeadBranch: mr.SourceBranch,
		BaseBranch: mr.TargetBranch,
	}, nil
}

// CommentOnPullRequest posts body as a note on the merge request. GitLab keeps
// merge-request notes separate from issue notes, so this uses the MR-note
// endpoint rather than CommentOnIssue.
func (c *Client) CommentOnPullRequest(ctx context.Context, repo provider.Repo, number int, body string) error {
	_, _, err := c.gl.Notes.CreateMergeRequestNote(pid(repo), int64(number), &gl.CreateMergeRequestNoteOptions{
		Body: gl.Ptr(body),
	}, ctxOpt(ctx))
	if err != nil {
		return fmt.Errorf("gitlab: comment on merge request !%d in %s: %w", number, repo.FullPath, classify(err))
	}
	return nil
}

// MergePullRequest accepts the merge request identified by number.
func (c *Client) MergePullRequest(ctx context.Context, repo provider.Repo, number int, opts provider.MergeOptions) error {
	acceptOpts := &gl.AcceptMergeRequestOptions{}
	if opts.Method == provider.MergeSquash {
		acceptOpts.Squash = gl.Ptr(true)
	}
	if opts.DeleteBranch {
		acceptOpts.ShouldRemoveSourceBranch = gl.Ptr(true)
	}
	if _, _, err := c.gl.MergeRequests.AcceptMergeRequest(pid(repo), int64(number), acceptOpts, ctxOpt(ctx)); err != nil {
		return fmt.Errorf("gitlab: merge merge request !%d in %s: %w", number, repo.FullPath, classify(err))
	}
	return nil
}

// ClosePullRequest closes the merge request without merging.
func (c *Client) ClosePullRequest(ctx context.Context, repo provider.Repo, number int) error {
	if _, _, err := c.gl.MergeRequests.UpdateMergeRequest(pid(repo), int64(number), &gl.UpdateMergeRequestOptions{
		StateEvent: gl.Ptr("close"),
	}, ctxOpt(ctx)); err != nil {
		return fmt.Errorf("gitlab: close merge request !%d in %s: %w", number, repo.FullPath, classify(err))
	}
	return nil
}
