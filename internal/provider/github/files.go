package github

import (
	"context"
	"fmt"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/wright/internal/provider"
)

// ReadRepoFile returns the content of the file at path, at ref (the repo's
// default branch when ref is empty). Returns ErrNotFound when path does not
// exist or is a directory.
func (c *Client) ReadRepoFile(ctx context.Context, repo provider.Repo, ref, path string) (string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return "", err
	}
	file, _, _, err := c.gh.Repositories.GetContents(ctx, owner, name, path, refOpts(ref))
	if err != nil {
		return "", fmt.Errorf("github: read file %q in %s: %w", path, repo.FullPath, classify(err))
	}
	if file == nil {
		return "", fmt.Errorf("github: read file %q in %s: %w", path, repo.FullPath, provider.ErrNotFound)
	}
	content, err := file.GetContent()
	if err != nil {
		return "", fmt.Errorf("github: decode file %q in %s: %w", path, repo.FullPath, err)
	}
	return content, nil
}

// ListRepoDir returns the entry names at path, at ref (the repo's default
// branch when ref is empty). Directory entries are suffixed with "/". The
// empty path lists the repo root.
func (c *Client) ListRepoDir(ctx context.Context, repo provider.Repo, ref, path string) ([]string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	file, dir, _, err := c.gh.Repositories.GetContents(ctx, owner, name, path, refOpts(ref))
	if err != nil {
		return nil, fmt.Errorf("github: list dir %q in %s: %w", path, repo.FullPath, classify(err))
	}
	if file != nil {
		return nil, fmt.Errorf("github: list dir %q in %s: not a directory", path, repo.FullPath)
	}
	entries := make([]string, 0, len(dir))
	for _, e := range dir {
		name := e.GetName()
		if e.GetType() == "dir" {
			name += "/"
		}
		entries = append(entries, name)
	}
	return entries, nil
}

func refOpts(ref string) *gh.RepositoryContentGetOptions {
	if ref == "" {
		return nil
	}
	return &gh.RepositoryContentGetOptions{Ref: ref}
}
