package gitlab

import (
	"context"
	"errors"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/farzan-kh/patchr/internal/provider"
)

// DefaultBranch returns the project's default branch name.
func (c *Client) DefaultBranch(ctx context.Context, repo provider.Repo) (string, error) {
	p, _, err := c.gl.Projects.GetProject(pid(repo), nil, ctxOpt(ctx))
	if err != nil {
		return "", fmt.Errorf("gitlab: default branch of %s: %w", repo.FullPath, classify(err))
	}
	return p.DefaultBranch, nil
}

// CreateBranch creates branch pointing at fromRef (a branch name or SHA).
func (c *Client) CreateBranch(ctx context.Context, repo provider.Repo, branch, fromRef string) error {
	_, _, err := c.gl.Branches.CreateBranch(pid(repo), &gl.CreateBranchOptions{
		Branch: gl.Ptr(branch),
		Ref:    gl.Ptr(fromRef),
	}, ctxOpt(ctx))
	if err != nil {
		if isAlreadyExists(err) {
			return fmt.Errorf("gitlab: create branch %q in %s: %w", branch, repo.FullPath, provider.ErrAlreadyExists)
		}
		return fmt.Errorf("gitlab: create branch %q in %s: %w", branch, repo.FullPath, classify(err))
	}
	return nil
}

// DeleteBranch deletes branch.
func (c *Client) DeleteBranch(ctx context.Context, repo provider.Repo, branch string) error {
	_, err := c.gl.Branches.DeleteBranch(pid(repo), branch, ctxOpt(ctx))
	if err != nil {
		return fmt.Errorf("gitlab: delete branch %q in %s: %w", branch, repo.FullPath, classify(err))
	}
	return nil
}

// PushCommits creates each commit in order on branch through the Commits API
// and returns the resulting head SHA. No local clone is involved (Phase 0).
func (c *Client) PushCommits(ctx context.Context, repo provider.Repo, branch string, commits []provider.Commit) (string, error) {
	if len(commits) == 0 {
		return "", fmt.Errorf("gitlab: push commits to %q in %s: no commits", branch, repo.FullPath)
	}

	var head string
	for _, commit := range commits {
		actions := make([]*gl.CommitActionOptions, 0, len(commit.Files))
		for _, f := range commit.Files {
			action, err := c.fileAction(ctx, repo, branch, f)
			if err != nil {
				return "", err
			}
			actions = append(actions, action)
		}
		created, _, err := c.gl.Commits.CreateCommit(pid(repo), &gl.CreateCommitOptions{
			Branch:        gl.Ptr(branch),
			CommitMessage: gl.Ptr(commit.Message),
			Actions:       actions,
		}, ctxOpt(ctx))
		if err != nil {
			return "", fmt.Errorf("gitlab: create commit on %q in %s: %w", branch, repo.FullPath, classify(err))
		}
		head = created.ID
	}
	return head, nil
}

// fileAction turns a domain file change into a GitLab commit action. For a
// create/update it probes whether the file already exists on the branch to pick
// the right action; deletions map straight to the delete action.
func (c *Client) fileAction(ctx context.Context, repo provider.Repo, branch string, f provider.CommitFile) (*gl.CommitActionOptions, error) {
	if f.Delete {
		return &gl.CommitActionOptions{
			Action:   gl.Ptr(gl.FileDelete),
			FilePath: gl.Ptr(f.Path),
		}, nil
	}

	var action gl.FileActionValue
	_, _, err := c.gl.RepositoryFiles.GetFileMetaData(pid(repo), f.Path, &gl.GetFileMetaDataOptions{
		Ref: gl.Ptr(branch),
	}, ctxOpt(ctx))
	switch {
	case err == nil:
		action = gl.FileUpdate
	case classifyIsNotFound(err):
		action = gl.FileCreate
	default:
		return nil, fmt.Errorf("gitlab: probe file %q in %s: %w", f.Path, repo.FullPath, classify(err))
	}

	return &gl.CommitActionOptions{
		Action:   gl.Ptr(action),
		FilePath: gl.Ptr(f.Path),
		Content:  gl.Ptr(f.Content),
	}, nil
}

// classifyIsNotFound reports whether err maps to provider.ErrNotFound.
func classifyIsNotFound(err error) bool {
	return errors.Is(classify(err), provider.ErrNotFound)
}
