// SPDX-License-Identifier: Apache-2.0

// Package stack tracks pull requests that Wright stacked on top of an
// in-flight dependency PR (see internal/gate and internal/executor's
// stacking base-branch selection), and reconciles them once their dependency
// merges: retargeting the stacked PR's base back onto the repo's real base
// branch. It depends only on internal/provider; internal/cli wires Reconcile
// into the run loop alongside the main pipeline.
package stack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/provider"
)

// Entry tracks one stacked PR waiting for its dependency to merge.
type Entry struct {
	Repo string `json:"repo"`

	StackedPRNumber   int    `json:"stacked_pr_number"`
	StackedHeadBranch string `json:"stacked_head_branch"`

	DependsOnIssue    int `json:"depends_on_issue"`
	DependsOnPRNumber int `json:"depends_on_pr_number"`

	// RealBaseBranch is the base branch this PR would have targeted had it
	// not been stacked - resolved once, at stacking time, and reused as-is at
	// reconcile time rather than re-resolved (RepoConfig.BaseBranch could
	// change in between).
	RealBaseBranch string `json:"real_base_branch"`

	CreatedAt time.Time `json:"created_at"`
}

// Store persists and retrieves pending stacked-PR entries, one per stacked
// PR. Implementations must be safe for concurrent use.
type Store interface {
	// Add records a newly stacked PR.
	Add(e Entry) error
	// ListPending returns every entry still awaiting reconciliation for repo.
	ListPending(repo string) ([]Entry, error)
	// Remove deletes the entry for stackedPRNumber in repo. Removing a
	// nonexistent entry is not an error.
	Remove(repo string, stackedPRNumber int) error
}

// Reconcile checks every pending stacked PR in repo against its dependency's
// current state and, once resolved, updates the stacked PR accordingly:
//
//   - dependency PR merged: retarget the stacked PR onto RealBaseBranch and
//     comment that the base changed and tests/CI should be rechecked (Wright
//     does not re-verify automatically - see docs/CONFIGURATION.md).
//   - dependency PR closed without merging: comment flagging that the
//     stacked PR's dependency was abandoned and it needs human attention.
//   - dependency PR still open: leave the entry for the next reconcile pass.
//
// It is deterministic and makes no LLM calls. One entry failing does not
// stop the rest from being processed; all failures are joined and returned.
func Reconcile(ctx context.Context, p provider.Provider, repo provider.Repo, store Store) error {
	l := logging.FromContext(ctx)

	entries, err := store.ListPending(repo.FullPath)
	if err != nil {
		return fmt.Errorf("stack: list pending for %s: %w", repo.FullPath, err)
	}

	var errs []error
	for _, e := range entries {
		if err := reconcileOne(ctx, l, p, repo, store, e); err != nil {
			errs = append(errs, fmt.Errorf("stack: reconcile PR #%d: %w", e.StackedPRNumber, err))
		}
	}
	return errors.Join(errs...)
}

func reconcileOne(ctx context.Context, l *slog.Logger, p provider.Provider, repo provider.Repo, store Store, e Entry) error {
	dep, err := p.GetPullRequest(ctx, repo, e.DependsOnPRNumber)
	if err != nil {
		return fmt.Errorf("get dependency PR #%d: %w", e.DependsOnPRNumber, err)
	}

	switch dep.State {
	case "merged":
		if err := p.UpdatePullRequestBase(ctx, repo, e.StackedPRNumber, e.RealBaseBranch); err != nil {
			return fmt.Errorf("retarget to %q: %w", e.RealBaseBranch, err)
		}
		comment := fmt.Sprintf(
			"Wright: dependency #%d (PR #%d) merged, so this PR's base has been retargeted from %q to %q. "+
				"The diff and CI/tests have not been re-verified against the new base - please recheck before merging.",
			e.DependsOnIssue, e.DependsOnPRNumber, e.StackedHeadBranch, e.RealBaseBranch)
		if err := p.CommentOnPullRequest(ctx, repo, e.StackedPRNumber, comment); err != nil {
			l.Warn("stack: retargeted PR but comment failed", "pr", e.StackedPRNumber, "error", err.Error())
		}
		l.Info("stack: retargeted stacked PR", "pr", e.StackedPRNumber, "base", e.RealBaseBranch, "depends_on_pr", e.DependsOnPRNumber)
		return store.Remove(repo.FullPath, e.StackedPRNumber)

	case "closed":
		comment := fmt.Sprintf(
			"Wright: this PR was stacked on dependency #%d (PR #%d), which was closed without merging. "+
				"It needs human review before it can proceed.",
			e.DependsOnIssue, e.DependsOnPRNumber)
		if err := p.CommentOnPullRequest(ctx, repo, e.StackedPRNumber, comment); err != nil {
			l.Warn("stack: dependency closed unmerged but comment failed", "pr", e.StackedPRNumber, "error", err.Error())
		}
		l.Info("stack: dependency closed unmerged, dropped from tracking", "pr", e.StackedPRNumber, "depends_on_pr", e.DependsOnPRNumber)
		return store.Remove(repo.FullPath, e.StackedPRNumber)

	default: // still open
		return nil
	}
}
