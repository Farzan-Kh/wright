package provider

import (
	"fmt"
	"strings"
	"time"
)

// The domain vocabulary in this package is deliberately GitHub-flavored
// ("pull request", "issue number"). The GitLab adapter maps its own concepts
// onto these types — a merge request's IID becomes a PullRequest.Number, a
// note becomes a comment, and so on. Keeping one vocabulary lets the rest of
// Wright stay provider-agnostic.

// Repo identifies a repository or project. FullPath is a single string
// ("owner/name" on GitHub, or a full project path like "group/subgroup/name"
// on GitLab) so that GitLab's arbitrarily nested groups work without a
// separate owner/name split.
type Repo struct {
	FullPath string
}

// Issue is an open issue on the provider. Labels holds the label names only.
// Comments holds the issue's discussion thread (oldest first): a lot of the
// detail an implementer needs — clarifications, decisions, scope changes —
// only ever shows up there, not in the original Body.
type Issue struct {
	Number    int
	Title     string
	Body      string
	Labels    []string
	URL       string
	Author    string
	State     string // "open" or "closed"
	CreatedAt time.Time
	UpdatedAt time.Time
	Comments  []Comment
}

// Comment is a single comment (GitLab: note) on an issue's discussion thread.
type Comment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

// FormatComments renders the issue's discussion thread as plain text suitable
// for inclusion in an LLM prompt, oldest first. It returns "" when there are
// no comments.
func (i Issue) FormatComments() string {
	if len(i.Comments) == 0 {
		return ""
	}
	var b strings.Builder
	for n, c := range i.Comments {
		if n > 0 {
			b.WriteString("\n\n")
		}
		author := c.Author
		if author == "" {
			author = "unknown"
		}
		fmt.Fprintf(&b, "@%s:\n%s", author, c.Body)
	}
	return b.String()
}

// Commit is a single commit to be created through the provider's API. It
// carries the whole set of file changes that make up the commit.
type Commit struct {
	Message string
	Files   []CommitFile
}

// CommitFile is one file change within a Commit. When Delete is true the file
// at Path is removed and Content is ignored; otherwise the file at Path is
// created or overwritten with Content.
type CommitFile struct {
	Path    string
	Content string
	Delete  bool
}

// PullRequestSpec describes a pull request (GitLab: merge request) to open.
type PullRequestSpec struct {
	Title      string
	Body       string
	HeadBranch string
	BaseBranch string
	Draft      bool
}

// PullRequest is a pull request (GitLab: merge request) as returned by the
// provider. For GitLab, Number is the merge request IID.
type PullRequest struct {
	Number     int
	URL        string
	HeadBranch string
	BaseBranch string
}

// MergeMethod selects how a pull request is merged.
type MergeMethod string

const (
	MergeMerge  MergeMethod = "merge"
	MergeSquash MergeMethod = "squash"
	MergeRebase MergeMethod = "rebase"
)

// MergeOptions controls a merge. An empty Method lets the adapter pick the
// provider's default.
type MergeOptions struct {
	Method       MergeMethod
	DeleteBranch bool
}
