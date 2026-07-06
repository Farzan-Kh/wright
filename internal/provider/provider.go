// Package provider defines Wright's provider-agnostic interface for hosting
// services (GitHub, GitLab) along with the domain types and sentinel errors
// that adapters map onto. It is a leaf package: everything else in Wright
// depends on it, and it depends on nothing internal. Adapters live in
// subpackages (github, gitlab); the factory in factory.go selects one from
// config.
package provider

import "context"

// Provider is the write-path abstraction over a hosting service. All methods
// take a Repo so a single Provider value can operate across repos on the same
// host. Methods return the sentinel errors in errors.go (wrapped with context)
// so callers can branch with errors.Is.
type Provider interface {
	// Name reports the provider identifier: "github" or "gitlab".
	Name() string

	// ListLabeledIssues returns open issues in repo that carry label. It
	// returns only genuine issues (pull/merge requests are excluded).
	ListLabeledIssues(ctx context.Context, repo Repo, label string) ([]Issue, error)

	// GetIssue fetches a single issue by number, without its comment thread.
	// Used to resolve cross-issue references (e.g. "blocked by #12") against
	// live state rather than trusting stale issue text.
	GetIssue(ctx context.Context, repo Repo, number int) (*Issue, error)

	// ReadRepoFile returns the content of the file at path, at ref (a branch
	// name or SHA; the empty string means the repo's default branch). Returns
	// ErrNotFound when path does not exist or is a directory.
	ReadRepoFile(ctx context.Context, repo Repo, ref, path string) (string, error)

	// ListRepoDir returns the entry names at path, at ref (a branch name or
	// SHA; the empty string means the repo's default branch). Directory
	// entries are suffixed with "/". The empty path lists the repo root.
	ListRepoDir(ctx context.Context, repo Repo, ref, path string) ([]string, error)

	// CommentOnIssue posts body as a comment on the given issue.
	CommentOnIssue(ctx context.Context, repo Repo, issueNumber int, body string) error

	// AddIssueLabel adds label to the given issue.
	AddIssueLabel(ctx context.Context, repo Repo, issueNumber int, label string) error

	// RemoveIssueLabel removes label from the given issue. Removing a label that is
	// already absent succeeds.
	RemoveIssueLabel(ctx context.Context, repo Repo, issueNumber int, label string) error

	// CommentOnPullRequest posts body as a comment on the given pull request
	// (GitLab: merge request). This is distinct from CommentOnIssue because
	// GitLab keeps issue notes and merge-request notes in separate namespaces.
	CommentOnPullRequest(ctx context.Context, repo Repo, number int, body string) error

	// DefaultBranch returns the repo's default branch name.
	DefaultBranch(ctx context.Context, repo Repo) (string, error)

	// CreateBranch creates branch pointing at fromRef (a branch name or SHA).
	CreateBranch(ctx context.Context, repo Repo, branch, fromRef string) error

	// DeleteBranch deletes branch.
	DeleteBranch(ctx context.Context, repo Repo, branch string) error

	// PushCommits creates commits on branch through the provider's commit API
	// (no local clone in Phase 0) and returns the resulting head SHA.
	PushCommits(ctx context.Context, repo Repo, branch string, commits []Commit) (string, error)

	// FindOpenPullRequestByHead returns an open PR whose head/source branch matches
	// headBranch, or nil when no such PR exists.
	FindOpenPullRequestByHead(ctx context.Context, repo Repo, headBranch string) (*PullRequest, error)

	// OpenPullRequest opens a pull request (GitLab: merge request) per spec.
	OpenPullRequest(ctx context.Context, repo Repo, spec PullRequestSpec) (*PullRequest, error)

	// MergePullRequest merges the pull request identified by number.
	MergePullRequest(ctx context.Context, repo Repo, number int, opts MergeOptions) error

	// ClosePullRequest closes the pull request identified by number without
	// merging.
	ClosePullRequest(ctx context.Context, repo Repo, number int) error
}
