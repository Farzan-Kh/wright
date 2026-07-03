package github

import (
	"context"
	"fmt"
	"net/http"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/patchr/internal/provider"
)

// blobMode is the git file mode for a normal (non-executable) file.
const blobMode = "100644"

// DefaultBranch returns the repo's default branch name.
func (c *Client) DefaultBranch(ctx context.Context, repo provider.Repo) (string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return "", err
	}
	r, _, err := c.gh.Repositories.Get(ctx, owner, name)
	if err != nil {
		return "", fmt.Errorf("github: default branch of %s: %w", repo.FullPath, classify(err))
	}
	return r.GetDefaultBranch(), nil
}

// CreateBranch creates branch pointing at fromRef (a branch name or SHA).
func (c *Client) CreateBranch(ctx context.Context, repo provider.Repo, branch, fromRef string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	sha, err := c.resolveSHA(ctx, owner, name, fromRef)
	if err != nil {
		return fmt.Errorf("github: resolve %q in %s: %w", fromRef, repo.FullPath, classify(err))
	}
	_, _, err = c.gh.Git.CreateRef(ctx, owner, name, gh.CreateRef{
		Ref: "refs/heads/" + branch,
		SHA: sha,
	})
	if err != nil {
		// GitHub answers 422 when the ref already exists.
		if code, ok := statusCode(err); ok && code == http.StatusUnprocessableEntity {
			return fmt.Errorf("github: create branch %q in %s: %w", branch, repo.FullPath, provider.ErrAlreadyExists)
		}
		return fmt.Errorf("github: create branch %q in %s: %w", branch, repo.FullPath, classify(err))
	}
	return nil
}

// DeleteBranch deletes branch.
func (c *Client) DeleteBranch(ctx context.Context, repo provider.Repo, branch string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	_, err = c.gh.Git.DeleteRef(ctx, owner, name, "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("github: delete branch %q in %s: %w", branch, repo.FullPath, classify(err))
	}
	return nil
}

// PushCommits creates each commit in order on branch through the Git Data API
// and returns the resulting head SHA. No local clone is involved (Phase 0).
func (c *Client) PushCommits(ctx context.Context, repo provider.Repo, branch string, commits []provider.Commit) (string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return "", err
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("github: push commits to %q in %s: no commits", branch, repo.FullPath)
	}

	parentSHA, err := c.resolveSHA(ctx, owner, name, branch)
	if err != nil {
		return "", fmt.Errorf("github: head of %q in %s: %w", branch, repo.FullPath, classify(err))
	}

	for _, commit := range commits {
		parentCommit, _, err := c.gh.Git.GetCommit(ctx, owner, name, parentSHA)
		if err != nil {
			return "", fmt.Errorf("github: get commit %s in %s: %w", parentSHA, repo.FullPath, classify(err))
		}

		entries, err := c.buildTree(ctx, owner, name, commit.Files)
		if err != nil {
			return "", fmt.Errorf("github: build tree for %s: %w", repo.FullPath, err)
		}

		tree, _, err := c.gh.Git.CreateTree(ctx, owner, name, parentCommit.GetTree().GetSHA(), entries)
		if err != nil {
			return "", fmt.Errorf("github: create tree in %s: %w", repo.FullPath, classify(err))
		}

		newCommit, _, err := c.gh.Git.CreateCommit(ctx, owner, name, gh.Commit{
			Message: gh.Ptr(commit.Message),
			Tree:    &gh.Tree{SHA: tree.SHA},
			Parents: []*gh.Commit{{SHA: gh.Ptr(parentSHA)}},
		}, nil)
		if err != nil {
			return "", fmt.Errorf("github: create commit in %s: %w", repo.FullPath, classify(err))
		}
		parentSHA = newCommit.GetSHA()
	}

	_, _, err = c.gh.Git.UpdateRef(ctx, owner, name, "refs/heads/"+branch, gh.UpdateRef{SHA: parentSHA})
	if err != nil {
		return "", fmt.Errorf("github: update branch %q in %s: %w", branch, repo.FullPath, classify(err))
	}
	return parentSHA, nil
}

// buildTree turns domain file changes into tree entries: a blob per created or
// updated file, and a nil-SHA/nil-Content entry per deletion (which go-github
// marshals as {"sha":null} to remove the path).
func (c *Client) buildTree(ctx context.Context, owner, name string, files []provider.CommitFile) ([]*gh.TreeEntry, error) {
	entries := make([]*gh.TreeEntry, 0, len(files))
	for _, f := range files {
		if f.Delete {
			entries = append(entries, &gh.TreeEntry{
				Path: gh.Ptr(f.Path),
				Mode: gh.Ptr(blobMode),
				Type: gh.Ptr("blob"),
			})
			continue
		}
		blob, _, err := c.gh.Git.CreateBlob(ctx, owner, name, gh.Blob{
			Content:  gh.Ptr(f.Content),
			Encoding: gh.Ptr("utf-8"),
		})
		if err != nil {
			return nil, fmt.Errorf("create blob for %q: %w", f.Path, classify(err))
		}
		entries = append(entries, &gh.TreeEntry{
			Path: gh.Ptr(f.Path),
			Mode: gh.Ptr(blobMode),
			Type: gh.Ptr("blob"),
			SHA:  blob.SHA,
		})
	}
	return entries, nil
}

// resolveSHA turns a branch name or SHA into a commit SHA. A 40-character hex
// string is treated as a SHA directly; anything else is looked up as a branch.
func (c *Client) resolveSHA(ctx context.Context, owner, name, ref string) (string, error) {
	if looksLikeSHA(ref) {
		return ref, nil
	}
	r, _, err := c.gh.Git.GetRef(ctx, owner, name, "refs/heads/"+ref)
	if err != nil {
		return "", err
	}
	return r.GetObject().GetSHA(), nil
}

// looksLikeSHA reports whether s is a 40-character lowercase hex string.
func looksLikeSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
