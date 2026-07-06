// Package gitops performs deterministic git operations inside the sandbox clone.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/retry"
	"github.com/farzan-kh/wright/internal/sandbox"
)

// BranchName returns the deterministic per-issue branch.
func BranchName(issueNumber int) string {
	return fmt.Sprintf("wright/issue-%d", issueNumber)
}

// InjectCredentialIntoRemoteURL injects basic-auth credentials into an HTTPS
// remote URL.
func InjectCredentialIntoRemoteURL(remoteURL, username, token string) (string, error) {
	if strings.TrimSpace(remoteURL) == "" {
		return "", errors.New("gitops: remote url is empty")
	}
	if strings.TrimSpace(username) == "" {
		return "", errors.New("gitops: username is empty")
	}
	if strings.TrimSpace(token) == "" {
		return "", errors.New("gitops: token is empty")
	}
	u, err := url.Parse(remoteURL)
	if err != nil {
		return "", fmt.Errorf("gitops: parse remote url: %w", err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("gitops: unsupported remote scheme %q (want https)", u.Scheme)
	}
	u.User = url.UserPassword(username, token)
	return u.String(), nil
}

// InjectTokenIntoRemoteURL injects token into an HTTPS remote URL using
// GitHub's x-access-token username convention.
func InjectTokenIntoRemoteURL(remoteURL, token string) (string, error) {
	return InjectCredentialIntoRemoteURL(remoteURL, "x-access-token", token)
}

// Ops executes git operations and opens a PR through the provider.
type Ops struct {
	Exec       sandbox.ToolExec
	Provider   provider.Provider
	Repo       provider.Repo
	RemoteUser string
	// Retry controls retries around git push, the one network connection
	// attempt CommitAndPush makes directly (as opposed to through Provider).
	Retry retry.Config
}

// CommitAndPush commits all staged changes and pushes to the per-issue branch.
func (o *Ops) CommitAndPush(ctx context.Context, issueNumber int, remoteURL, token, commitMessage string) (branch, diffSummary string, err error) {
	branch = provider.SanitizeRef(BranchName(issueNumber))
	commitMessage = provider.SanitizeText(commitMessage)
	if _, err := o.Exec.Bash(ctx, "git checkout -b "+shellQuote(branch)); err != nil {
		return "", "", fmt.Errorf("gitops: create branch %s: %w", branch, err)
	}
	if _, err := o.Exec.Bash(ctx, "git add -A"); err != nil {
		return "", "", fmt.Errorf("gitops: git add: %w", err)
	}
	diffSummary, err = o.Exec.Bash(ctx, "git diff --cached --shortstat")
	if err != nil {
		return "", "", fmt.Errorf("gitops: diff summary: %w", err)
	}
	if strings.TrimSpace(diffSummary) == "" {
		return "", "", errors.New("gitops: no staged changes to commit")
	}
	if _, err := o.Exec.Bash(ctx, "git commit -m "+shellQuote(commitMessage)); err != nil {
		return "", "", fmt.Errorf("gitops: commit: %w", err)
	}
	remoteUser := strings.TrimSpace(o.RemoteUser)
	if remoteUser == "" {
		remoteUser = "x-access-token"
	}
	pushURL, err := InjectCredentialIntoRemoteURL(remoteURL, remoteUser, token)
	if err != nil {
		return "", "", err
	}
	pushCmd := "git push " + shellQuote(pushURL) + " HEAD:" + shellQuote(branch)
	if err := retry.Do(ctx, o.Retry, nil, func(ctx context.Context) error {
		_, err := o.Exec.Bash(ctx, pushCmd)
		return err
	}); err != nil {
		return "", "", fmt.Errorf("gitops: push: %w", err)
	}
	return branch, strings.TrimSpace(diffSummary), nil
}

// OpenPR opens a provider pull request for branch->baseBranch.
func (o *Ops) OpenPR(ctx context.Context, title, body, branch, baseBranch string, draft bool) (*provider.PullRequest, error) {
	return o.Provider.OpenPullRequest(ctx, o.Repo, provider.PullRequestSpec{
		Title:      title,
		Body:       body,
		HeadBranch: path.Clean(branch),
		BaseBranch: path.Clean(baseBranch),
		Draft:      draft,
	})
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
