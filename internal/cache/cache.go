// SPDX-License-Identifier: Apache-2.0

// Package cache persists partial progress from an interrupted issue-resolution
// attempt (turn limit, verify exhaustion, a sandbox fault, or a failed
// commit/push/PR step) so the next attempt at the same issue can resume from
// where the money was already spent instead of re-running the agent from
// scratch. It depends only on internal/agent/llm (to store a resumable
// conversation) and internal/cost (to carry the turn budget already spent
// across resumes); internal/cli wires it into the executor.
package cache

import (
	"time"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
)

// Stage records how far a cached attempt got before it was interrupted. It
// determines how a resume picks the attempt back up: whether the agent needs
// to run again at all, and whether a sandbox is even needed.
type Stage string

const (
	// StageAgentIncomplete means the agent didn't produce a verified change:
	// it hit the turn limit, errored mid-run, or exhausted verify retries.
	// Resuming reapplies Diff into a fresh sandbox and continues the cached
	// agent conversation (System + History).
	StageAgentIncomplete Stage = "agent_incomplete"
	// StageVerifiedUnpushed means the change passed verify but committing or
	// pushing it failed. Resuming reapplies Diff into a fresh sandbox and
	// redoes verify once (no LLM call) followed by commit+push+PR, without
	// re-invoking the agent.
	StageVerifiedUnpushed Stage = "verified_unpushed"
	// StagePRPending means the change was committed and pushed but opening
	// the PR failed. Resuming needs no sandbox or agent at all: it just
	// retries the PR-open call against the already-pushed branch.
	StagePRPending Stage = "pr_pending"
)

// Entry is one cached attempt, keyed by (Repo, IssueNumber).
type Entry struct {
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issue_number"`
	Stage       Stage  `json:"stage"`
	// Reason is the error that caused the attempt to be cached. Diagnostic
	// only; never parsed back.
	Reason string `json:"reason"`

	Branch     string `json:"branch"`
	BaseBranch string `json:"base_branch"`

	// Diff is a unified diff (working tree vs BaseBranch) captured from the
	// sandbox before it was torn down. Empty when Stage is StagePRPending,
	// since the change is already committed and pushed.
	Diff string `json:"diff,omitempty"`

	// System and History are the exact agent conversation state to resume
	// from. Only populated when Stage is StageAgentIncomplete.
	System  []llm.SystemBlock `json:"system,omitempty"`
	History []llm.Message     `json:"history,omitempty"`

	// Cost is cumulative usage across every attempt cached so far for this
	// issue, so a resumed run's turn budget accounts for turns already
	// spent rather than resetting it.
	Cost cost.Summary `json:"cost"`

	VerifyCmd    string `json:"verify_cmd,omitempty"`
	VerifyOutput string `json:"verify_output,omitempty"`
	// DiffSummary is the `git diff --shortstat` line used in PR bodies,
	// carried forward so a StagePRPending resume can rebuild the same PR
	// body without a sandbox.
	DiffSummary string `json:"diff_summary,omitempty"`

	CachedAt time.Time `json:"cached_at"`
}

// Store persists and retrieves cached attempts, one per (repo, issue).
// Implementations must be safe for concurrent use.
type Store interface {
	// Load returns the cached entry for issueNumber in repo, or nil if none
	// exists. A missing cache is not an error.
	Load(repo string, issueNumber int) (*Entry, error)
	// Save writes (overwriting any existing) the cached entry, keyed by its
	// own Repo and IssueNumber fields.
	Save(e Entry) error
	// Clear removes any cached entry for issueNumber in repo. Clearing a
	// nonexistent entry is not an error.
	Clear(repo string, issueNumber int) error
}
