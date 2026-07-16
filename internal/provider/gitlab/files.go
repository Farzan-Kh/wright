// SPDX-License-Identifier: Apache-2.0

package gitlab

import (
	"context"
	"encoding/base64"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/farzan-kh/wright/internal/provider"
)

// ReadRepoFile returns the content of the file at path, at ref (the repo's
// default branch when ref is empty). Returns ErrNotFound when path does not
// exist or is a directory.
func (c *Client) ReadRepoFile(ctx context.Context, repo provider.Repo, ref, path string) (string, error) {
	ref, err := c.resolveRef(ctx, repo, ref)
	if err != nil {
		return "", err
	}
	f, _, err := c.gl.RepositoryFiles.GetFile(pid(repo), path, &gl.GetFileOptions{Ref: gl.Ptr(ref)}, ctxOpt(ctx))
	if err != nil {
		return "", fmt.Errorf("gitlab: read file %q in %s: %w", path, repo.FullPath, classify(err))
	}
	content, err := base64.StdEncoding.DecodeString(f.Content)
	if err != nil {
		return "", fmt.Errorf("gitlab: decode file %q in %s: %w", path, repo.FullPath, err)
	}
	return string(content), nil
}

// ListRepoDir returns the entry names at path, at ref (the repo's default
// branch when ref is empty). Directory entries are suffixed with "/". The
// empty path lists the repo root.
func (c *Client) ListRepoDir(ctx context.Context, repo provider.Repo, ref, path string) ([]string, error) {
	ref, err := c.resolveRef(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	nodes, _, err := c.gl.Repositories.ListTree(pid(repo), &gl.ListTreeOptions{
		Path:        gl.Ptr(path),
		Ref:         gl.Ptr(ref),
		ListOptions: gl.ListOptions{PerPage: 100},
	}, ctxOpt(ctx))
	if err != nil {
		return nil, fmt.Errorf("gitlab: list dir %q in %s: %w", path, repo.FullPath, classify(err))
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("gitlab: list dir %q in %s: %w", path, repo.FullPath, provider.ErrNotFound)
	}
	entries := make([]string, 0, len(nodes))
	for _, n := range nodes {
		name := n.Name
		if n.Type == "tree" {
			name += "/"
		}
		entries = append(entries, name)
	}
	return entries, nil
}

// resolveRef returns ref unchanged when non-empty, otherwise looks up the
// repo's default branch: GitLab's file/tree endpoints require an explicit ref.
func (c *Client) resolveRef(ctx context.Context, repo provider.Repo, ref string) (string, error) {
	if ref != "" {
		return ref, nil
	}
	return c.DefaultBranch(ctx, repo)
}
