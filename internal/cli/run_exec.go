// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/farzan-kh/wright/internal/agent"
	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cache"
	"github.com/farzan-kh/wright/internal/config"
	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/gate"
	"github.com/farzan-kh/wright/internal/gitops"
	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/pipeline"
	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/sandbox"
	"github.com/farzan-kh/wright/internal/stack"
	"github.com/farzan-kh/wright/internal/verifier"
)

type issueExecutor struct {
	Provider      provider.Provider
	Repo          provider.Repo
	RepoConfig    *config.RepoConfig
	ProviderToken string
	LLM           llm.LLMProvider
	Sandbox       sandbox.Orchestrator
	// Cache persists partial progress from an interrupted attempt so the
	// next attempt at the same issue can resume rather than re-spending LLM
	// turns from scratch. Nil disables caching entirely.
	Cache cache.Store
	// Stack tracks PRs stacked on an in-flight dependency PR (see
	// RepoConfig.Stacking) so they can be retargeted once that dependency
	// merges. Nil when stacking is disabled.
	Stack stack.Store
	// RateTable maps model id -> per-model pricing for USD tracking. Built
	// once per repo from rc.LLM.ToRateTable() and passed to both the Gate
	// and agent runner.
	RateTable cost.RateTable
}

// stackedDependency is a referenced issue that already has an open Wright PR
// Handle can stack this issue's work on top of, instead of blocking until a
// human merges it.
type stackedDependency struct {
	issueNumber int
	pr          *provider.PullRequest
	// others holds additional referenced issues that also have an open
	// Wright PR but weren't chosen to stack on - a branch can only have one
	// parent, so at most one dependency can be stacked on at a time.
	others []int
}

func (e *issueExecutor) Handle(ctx context.Context, issue provider.Issue) (cost.Summary, error) {
	l := logging.FromContext(ctx).With("issue", issue.Number)
	zeroCost := cost.Summary{}
	branchName := gitops.BranchName(issue.Number)
	repoKey := e.Repo.FullPath

	existingPR, err := e.Provider.FindOpenPullRequestByHead(ctx, e.Repo, branchName)
	if err != nil {
		return zeroCost, err
	}
	if existingPR != nil {
		reason := fmt.Sprintf("idempotency: open PR already exists for %s (PR #%d %s)", branchName, existingPR.Number, existingPR.URL)
		l.Info("executor: skipping issue", "reason", reason)
		_ = e.Provider.CommentOnIssue(ctx, e.Repo, issue.Number, "Wright skipped this run because an open PR already exists for this issue branch:\n\n"+existingPR.URL)
		e.clearCache(repoKey, issue.Number)
		return zeroCost, pipeline.NewSkipError(reason)
	}

	baseBranch := strings.TrimSpace(e.RepoConfig.BaseBranch)
	if baseBranch == "" {
		b, err := e.Provider.DefaultBranch(ctx, e.Repo)
		if err != nil {
			return cost.Summary{}, fmt.Errorf("resolve default branch: %w", err)
		}
		baseBranch = b
	}
	l.Debug("executor: base branch resolved", "base_branch", baseBranch)

	realBaseBranch := baseBranch
	var stackedOn *stackedDependency
	if e.RepoConfig.Stacking.Enabled {
		stackedOn = e.findStackDependency(ctx, l, issue)
		if stackedOn != nil {
			baseBranch = stackedOn.pr.HeadBranch
			l.Info("executor: stacking on in-flight dependency PR",
				"depends_on_issue", stackedOn.issueNumber, "depends_on_pr", stackedOn.pr.Number, "stacked_base_branch", baseBranch)
		}
	}

	remoteURL, err := repoRemoteURL(e.RepoConfig)
	if err != nil {
		return cost.Summary{}, err
	}
	authRemoteURL, err := gitops.InjectCredentialIntoRemoteURL(remoteURL, gitRemoteUsername(e.RepoConfig.Provider), e.ProviderToken)
	if err != nil {
		return cost.Summary{}, fmt.Errorf("build authenticated remote url: %w", err)
	}

	cached := e.loadCache(l, repoKey, issue.Number)

	ops := &gitops.Ops{
		Provider:   e.Provider,
		Repo:       e.Repo,
		RemoteUser: gitRemoteUsername(e.RepoConfig.Provider),
		Retry:      e.RepoConfig.Retry.ToRetryConfig(),
	}

	// A cached pr_pending attempt already has verified, committed, pushed
	// code sitting on its branch: the only thing that failed was opening
	// the PR. Resuming needs no sandbox or agent call at all.
	if cached != nil && cached.Stage == cache.StagePRPending {
		return e.resumePRPending(ctx, l, ops, repoKey, issue, *cached)
	}

	l.Debug("executor: starting sandbox", "image", e.RepoConfig.Sandbox.Image)
	task, err := e.Sandbox.Start(ctx, sandbox.TaskSpec{
		Image:      e.RepoConfig.Sandbox.Image,
		Workdir:    e.RepoConfig.Sandbox.Workdir,
		RepoDir:    sandbox.DefaultRepoDir,
		CloneURL:   authRemoteURL,
		BaseBranch: baseBranch,
	})
	if err != nil {
		l.Error("executor: sandbox start failed", "error", err.Error())
		return cost.Summary{}, err
	}
	l.Debug("executor: sandbox ready")
	defer func() {
		_ = task.Close(context.Background())
	}()
	ops.Exec = task

	branchExists, err := remoteBranchExists(ctx, task, branchName)
	if err != nil {
		return zeroCost, err
	}
	if branchExists {
		reason := fmt.Sprintf("idempotency: branch %s already exists", branchName)
		l.Info("executor: skipping issue", "reason", reason)
		_ = e.Provider.CommentOnIssue(ctx, e.Repo, issue.Number, "Wright skipped this run because branch `"+branchName+"` already exists. If you want a retry, close/delete existing artifacts and re-apply the label.")
		e.clearCache(repoKey, issue.Number)
		return zeroCost, pipeline.NewSkipError(reason)
	}

	runner := &agent.Runner{
		LLM:  e.LLM,
		Exec: task,
		Cfg: agent.Config{
			Model:          e.RepoConfig.LLM.AgentModel,
			MaxTokens:      8192,
			MaxTurns:       e.RepoConfig.Budget.MaxTurns,
			MaxTotalTokens: e.RepoConfig.Budget.MaxTotalTokens,
			MaxUSD:         e.RepoConfig.Budget.MaxUSD,
			RateTable:      e.RateTable,
			ThinkEffort:    e.RepoConfig.LLM.Effort,
		},
	}

	v := &verifier.Verifier{OverrideCommand: e.RepoConfig.Verify.Command}
	verifyCmd, err := v.DetectCommand(ctx, task)
	if err != nil {
		return cost.Summary{}, err
	}

	totalCost := zeroCost
	skipToCommit := false
	var verifyOut string

	if cached != nil && strings.TrimSpace(cached.Diff) != "" {
		if applyErr := applyCachedDiff(ctx, task, cached.Diff); applyErr != nil {
			l.Info("executor: cached diff failed to apply, discarding cache and starting fresh", "error", applyErr.Error())
			cached = nil
		} else {
			totalCost = cached.Cost
			l.Info("executor: reapplied cached diff", "stage", cached.Stage)
		}
	}

	if cached != nil && cached.Stage == cache.StageVerifiedUnpushed {
		// The diff already passed verify last time; confirm it still does
		// in this fresh sandbox (no LLM cost) before skipping straight to
		// commit+push+PR.
		out, verr := task.Bash(ctx, verifyCmd)
		if verr == nil {
			verifyOut = out
			skipToCommit = true
			l.Info("executor: cached diff still verifies clean, skipping straight to commit+push+PR")
		} else {
			l.Info("executor: cached verified diff no longer passes verify, resuming agent instead", "error", verr.Error())
		}
	}

	var history []llm.Message
	var systemPrompt []llm.SystemBlock
	if !skipToCommit {
		if cached != nil && cached.Stage == cache.StageAgentIncomplete && len(cached.History) > 0 {
			history = cached.History
			systemPrompt = cached.System
		} else {
			instrFile, instrContent, err := readRepoInstructions(ctx, task)
			if err != nil {
				return cost.Summary{}, err
			}
			if instrFile != "" {
				l.Debug("executor: repo instructions found", "file", instrFile)
			}
			systemPrompt = buildAgentSystemPrompt(issue, e.RepoConfig, baseBranch, verifyCmd, instrFile, instrContent)
			history = buildAgentHistory(issue)
		}

		tools := []llm.ToolSpec{
			{Type: "bash_20250124"},
			{Type: "text_editor_20250728"},
		}
		const maxVerifyAttempts = 3
		for attempt := 1; attempt <= maxVerifyAttempts; attempt++ {
			cfg := runner.Cfg
			if cfg.MaxTurns > 0 {
				remainingTurns := cfg.MaxTurns - totalCost.Turns
				if remainingTurns <= 0 {
					l.Error("executor: turn budget exhausted", "max_turns", runner.Cfg.MaxTurns)
					e.cacheIncomplete(ctx, l, task, repoKey, issue, branchName, baseBranch, systemPrompt, history, totalCost, verifyCmd, verifyOut, agent.ErrTurnLimit.Error())
					return totalCost, agent.ErrTurnLimit
				}
				cfg.MaxTurns = remainingTurns
			}
			if cfg.MaxTotalTokens > 0 {
				totalTokens := totalCost.Usage.InputTokens + totalCost.Usage.OutputTokens +
					totalCost.Usage.CacheCreationInputTokens + totalCost.Usage.CacheReadInputTokens
				remainingTokens := cfg.MaxTotalTokens - totalTokens
				if remainingTokens <= 0 {
					l.Error("executor: token budget exhausted", "max_total_tokens", runner.Cfg.MaxTotalTokens)
					e.cacheIncomplete(ctx, l, task, repoKey, issue, branchName, baseBranch, systemPrompt, history, totalCost, verifyCmd, verifyOut, "max total tokens reached")
					return totalCost, fmt.Errorf("max total tokens exhausted (%d >= %d)", totalTokens, runner.Cfg.MaxTotalTokens)
				}
				cfg.MaxTotalTokens = remainingTokens
			}
			if cfg.MaxUSD > 0 && totalCost.USDKnown {
				remainingUSD := cfg.MaxUSD - totalCost.USD
				if remainingUSD <= 0 {
					l.Error("executor: USD budget exhausted", "max_usd", runner.Cfg.MaxUSD, "usd", totalCost.USD)
					e.cacheIncomplete(ctx, l, task, repoKey, issue, branchName, baseBranch, systemPrompt, history, totalCost, verifyCmd, verifyOut, "max USD reached")
					return totalCost, fmt.Errorf("max USD exhausted ($%.4f >= $%.4f)", totalCost.USD, runner.Cfg.MaxUSD)
				}
				cfg.MaxUSD = remainingUSD
			}
			runner.Cfg = cfg

			l.Debug("executor: agent run started", "attempt", attempt, "remaining_turns", cfg.MaxTurns)
			runResult, runErr := runner.Run(ctx, agent.RunRequest{System: systemPrompt, History: history, Tools: tools})
			totalCost.Merge(runResult.UsageAndCost)
			if runErr != nil {
				l.Error("executor: agent run failed", "attempt", attempt, "error", runErr.Error())
				e.cacheIncomplete(ctx, l, task, repoKey, issue, branchName, baseBranch, systemPrompt, runResult.History, totalCost, verifyCmd, verifyOut, runErr.Error())
				return totalCost, runErr
			}
			history = runResult.History
			l.Debug("executor: agent run ok", "attempt", attempt, "stop_reason", runResult.StopReason, "turns", totalCost.Turns)

			verifyOut, err = task.Bash(ctx, verifyCmd)
			if err == nil {
				l.Debug("executor: verify ok", "attempt", attempt, "command", verifyCmd)
				break
			}
			l.Info("executor: verify failed", "attempt", attempt, "command", verifyCmd, "error", err.Error())
			if attempt == maxVerifyAttempts {
				e.cacheIncomplete(ctx, l, task, repoKey, issue, branchName, baseBranch, systemPrompt, history, totalCost, verifyCmd, verifyOut, fmt.Sprintf("verify failed after %d attempts: %s", attempt, err.Error()))
				return totalCost, fmt.Errorf("verify failed after %d attempt(s) (%s): %w\n\n%s", attempt, verifyCmd, err, truncate(verifyOut, 8_000))
			}
			feedback := "Verification failed. Fix the implementation and run the tests again.\n\n" +
				"Command: " + verifyCmd + "\n\nOutput:\n" + truncate(verifyOut, 8_000)
			history = append(history, llm.Message{
				Role:    "user",
				Content: []llm.ContentBlock{{Type: "text", Text: feedback}},
			})
		}
	}

	commitTitle := fmt.Sprintf("wright: resolve issue #%d", issue.Number)
	branch, diffSummary, err := ops.CommitAndPush(ctx, issue.Number, remoteURL, e.ProviderToken, commitTitle)
	if err != nil {
		e.cacheVerifiedUnpushed(ctx, l, task, repoKey, issue, branchName, baseBranch, verifyCmd, verifyOut, totalCost, err.Error())
		return totalCost, err
	}

	prTitle := fmt.Sprintf("Issue #%d: %s", issue.Number, truncate(strings.TrimSpace(issue.Title), 72))
	prBody := buildPRBody(issue, diffSummary, verifyCmd, verifyOut)
	if stackedOn != nil {
		prBody += "\n\n---\n" + stackingNote(stackedOn)
	}
	pr, err := ops.OpenPR(ctx, prTitle, prBody, branch, baseBranch, false)
	if err != nil {
		e.cachePRPending(l, repoKey, issue, branch, baseBranch, diffSummary, verifyCmd, verifyOut, totalCost, err.Error())
		return totalCost, err
	}
	l.Info("executor: PR opened", "pr", pr.Number, "url", pr.URL)
	e.clearCache(repoKey, issue.Number)

	// A cached (interrupted) PR-open attempt resumes through resumePRPending,
	// which doesn't carry stackedOn/realBaseBranch - cache.Entry doesn't
	// record them - so a stacked PR whose OpenPR call failed and later
	// succeeds on resume won't be tracked for auto-retarget. Rare (PR-open is
	// already the least likely step to fail) and left as a known v1 gap.
	if stackedOn != nil && e.Stack != nil {
		if err := e.Stack.Add(stack.Entry{
			Repo:              repoKey,
			StackedPRNumber:   pr.Number,
			StackedHeadBranch: branch,
			DependsOnIssue:    stackedOn.issueNumber,
			DependsOnPRNumber: stackedOn.pr.Number,
			RealBaseBranch:    realBaseBranch,
			CreatedAt:         time.Now(),
		}); err != nil {
			l.Info("executor: stack: failed to register stacked PR for retarget tracking", "error", err.Error())
		}
	}

	_ = e.Provider.CommentOnIssue(ctx, e.Repo, issue.Number,
		fmt.Sprintf("Wright opened PR #%d: %s", pr.Number, pr.URL))

	return totalCost, nil
}

// findStackDependency looks for a referenced issue that already has an open
// Wright PR and returns the lowest-numbered such dependency to stack this
// issue's work on top of, or nil if none do. Multiple simultaneous open
// dependencies can't all be stacked on at once - a branch has one parent -
// so any others found are carried along on the result for a PR-body note.
func (e *issueExecutor) findStackDependency(ctx context.Context, l *slog.Logger, issue provider.Issue) *stackedDependency {
	refs := gate.ExtractIssueReferences(issue)
	if len(refs) == 0 {
		return nil
	}
	sorted := append([]int(nil), refs...)
	sort.Ints(sorted)

	var found []stackedDependency
	for _, n := range sorted {
		pr, err := e.Provider.FindOpenPullRequestByHead(ctx, e.Repo, gitops.BranchName(n))
		if err != nil {
			l.Info("executor: stack: dependency PR lookup failed", "issue", n, "error", err.Error())
			continue
		}
		if pr != nil {
			found = append(found, stackedDependency{issueNumber: n, pr: pr})
		}
	}
	if len(found) == 0 {
		return nil
	}
	primary := found[0]
	for _, other := range found[1:] {
		primary.others = append(primary.others, other.issueNumber)
	}
	return &primary
}

// stackingNote documents, directly on the PR, why its base isn't the repo's
// real base branch: a human reviewer needs this even though it's also
// tracked internally, and it's the only place a "some other dependency was
// also referenced but not stacked" limitation is surfaced at all.
func stackingNote(s *stackedDependency) string {
	note := fmt.Sprintf(
		"**Stacked PR**: this branches off `%s` (dependency #%d, PR #%d), not the repo's base branch, "+
			"because that dependency hasn't merged yet. Wright will retarget this PR onto the real base "+
			"branch automatically once #%d merges.",
		s.pr.HeadBranch, s.issueNumber, s.pr.Number, s.issueNumber)
	if len(s.others) > 0 {
		note += fmt.Sprintf(" Also references %s, each with its own open Wright PR - a PR can only have "+
			"one base, so those were not stacked on; verify them manually.", formatIssueRefs(s.others))
	}
	return note
}

func formatIssueRefs(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// resumePRPending retries opening the PR for an attempt that already got as
// far as a verified, committed, pushed branch last time - no sandbox or
// agent call needed, just another shot at the provider API call that failed.
func (e *issueExecutor) resumePRPending(ctx context.Context, l *slog.Logger, ops *gitops.Ops, repoKey string, issue provider.Issue, cached cache.Entry) (cost.Summary, error) {
	prTitle := fmt.Sprintf("Issue #%d: %s", issue.Number, truncate(strings.TrimSpace(issue.Title), 72))
	prBody := buildPRBody(issue, cached.DiffSummary, cached.VerifyCmd, cached.VerifyOutput)
	pr, err := ops.OpenPR(ctx, prTitle, prBody, cached.Branch, cached.BaseBranch, false)
	if err != nil {
		cached.Reason = err.Error()
		cached.CachedAt = time.Now()
		if e.Cache != nil {
			_ = e.Cache.Save(cached)
		}
		return cached.Cost, err
	}
	l.Info("executor: PR opened from cached attempt", "pr", pr.Number, "url", pr.URL)
	e.clearCache(repoKey, issue.Number)
	_ = e.Provider.CommentOnIssue(ctx, e.Repo, issue.Number,
		fmt.Sprintf("Wright opened PR #%d: %s", pr.Number, pr.URL))
	return cached.Cost, nil
}

// loadCache returns the cached attempt for issue, or nil if none exists or
// caching is disabled. A load error is logged and treated as no cache, so a
// corrupt entry degrades to "start fresh" instead of blocking the run.
func (e *issueExecutor) loadCache(l *slog.Logger, repoKey string, issueNumber int) *cache.Entry {
	if e.Cache == nil {
		return nil
	}
	entry, err := e.Cache.Load(repoKey, issueNumber)
	if err != nil {
		l.Info("executor: cache load failed, starting fresh", "error", err.Error())
		return nil
	}
	if entry != nil {
		l.Info("executor: resuming cached attempt", "stage", entry.Stage, "cached_at", entry.CachedAt)
	}
	return entry
}

func (e *issueExecutor) clearCache(repoKey string, issueNumber int) {
	if e.Cache == nil {
		return
	}
	_ = e.Cache.Clear(repoKey, issueNumber)
}

// cacheIncomplete persists a diff+conversation snapshot for an attempt that
// didn't reach a verified state (turn limit, agent error, or verify
// exhausted), so the next run on this issue picks the conversation back up
// instead of re-spending turns from scratch. Best-effort throughout: any
// failure to capture or persist just means the next run starts fresh, which
// is no worse than today's behavior.
func (e *issueExecutor) cacheIncomplete(ctx context.Context, l *slog.Logger, task sandbox.ToolExec, repoKey string, issue provider.Issue, branch, baseBranch string, system []llm.SystemBlock, history []llm.Message, totalCost cost.Summary, verifyCmd, verifyOut, reason string) {
	if e.Cache == nil {
		return
	}
	diff, err := captureDiff(ctx, task, baseBranch)
	if err != nil {
		l.Info("executor: cache: capture diff failed", "error", err.Error())
		return
	}
	if strings.TrimSpace(diff) == "" {
		return
	}
	if err := e.Cache.Save(cache.Entry{
		Repo: repoKey, IssueNumber: issue.Number, Stage: cache.StageAgentIncomplete,
		Reason: reason, Branch: branch, BaseBranch: baseBranch, Diff: diff,
		System: system, History: history, Cost: totalCost,
		VerifyCmd: verifyCmd, VerifyOutput: verifyOut, CachedAt: time.Now(),
	}); err != nil {
		l.Info("executor: cache: save failed", "error", err.Error())
		return
	}
	l.Info("executor: cached incomplete attempt for resume", "reason", reason)
}

// cacheVerifiedUnpushed persists a diff that already passed verify but
// couldn't be committed or pushed, so a resume can skip the agent entirely
// and go straight back to the git steps.
func (e *issueExecutor) cacheVerifiedUnpushed(ctx context.Context, l *slog.Logger, task sandbox.ToolExec, repoKey string, issue provider.Issue, branch, baseBranch, verifyCmd, verifyOut string, totalCost cost.Summary, reason string) {
	if e.Cache == nil {
		return
	}
	diff, err := captureDiff(ctx, task, baseBranch)
	if err != nil {
		l.Info("executor: cache: capture diff failed", "error", err.Error())
		return
	}
	if strings.TrimSpace(diff) == "" {
		return
	}
	if err := e.Cache.Save(cache.Entry{
		Repo: repoKey, IssueNumber: issue.Number, Stage: cache.StageVerifiedUnpushed,
		Reason: reason, Branch: branch, BaseBranch: baseBranch, Diff: diff,
		Cost: totalCost, VerifyCmd: verifyCmd, VerifyOutput: verifyOut, CachedAt: time.Now(),
	}); err != nil {
		l.Info("executor: cache: save failed", "error", err.Error())
		return
	}
	l.Info("executor: cached verified-but-unpushed attempt for resume", "reason", reason)
}

// cachePRPending persists a pushed-branch attempt whose PR creation failed.
// No diff is needed: the branch already has the commit, so a resume only
// needs to retry the provider PR-open call.
func (e *issueExecutor) cachePRPending(l *slog.Logger, repoKey string, issue provider.Issue, branch, baseBranch, diffSummary, verifyCmd, verifyOut string, totalCost cost.Summary, reason string) {
	if e.Cache == nil {
		return
	}
	if err := e.Cache.Save(cache.Entry{
		Repo: repoKey, IssueNumber: issue.Number, Stage: cache.StagePRPending,
		Reason: reason, Branch: branch, BaseBranch: baseBranch, DiffSummary: diffSummary,
		Cost: totalCost, VerifyCmd: verifyCmd, VerifyOutput: verifyOut, CachedAt: time.Now(),
	}); err != nil {
		l.Info("executor: cache: save failed", "error", err.Error())
		return
	}
	l.Info("executor: cached pushed-but-no-PR attempt for resume", "reason", reason)
}

// captureDiff returns a unified diff of the sandbox's working tree (staged,
// unstaged, and any locally committed-but-unpushed changes) against
// baseBranch, so it can be replayed into a fresh sandbox later. Untracked
// files only show up in `git diff` once staged, hence the `git add -A`
// first; that's harmless here since the sandbox is about to be torn down
// regardless of outcome.
func captureDiff(ctx context.Context, exec sandbox.ToolExec, baseBranch string) (string, error) {
	out, err := exec.Bash(ctx, "git add -A && git diff --cached "+shellQuote(baseBranch))
	if err != nil {
		return "", fmt.Errorf("capture diff against %s: %w", baseBranch, err)
	}
	return out, nil
}

// applyCachedDiff replays a diff captured by captureDiff into a freshly
// cloned sandbox at the same base branch.
func applyCachedDiff(ctx context.Context, exec sandbox.ToolExec, diff string) error {
	const patchPath = ".wright-resume.patch"
	if err := exec.WriteFile(ctx, patchPath, diff); err != nil {
		return fmt.Errorf("write resume patch: %w", err)
	}
	if _, err := exec.Bash(ctx, "git apply --whitespace=nowarn "+shellQuote(patchPath)+" && rm -f "+shellQuote(patchPath)); err != nil {
		return fmt.Errorf("apply resume patch: %w", err)
	}
	return nil
}

// defaultAgentBehaviorPrompt is the default identity/behavior guidance for the
// coding agent. It is replaced wholesale by RepoConfig.Prompt.SystemOverride,
// or extended by RepoConfig.Prompt.SystemAppend, when configured.
const defaultAgentBehaviorPrompt = "You are Wright, an autonomous software-maintenance agent. " +
	"Resolve the target issue with the smallest correct code change; do not expand scope beyond what " +
	"the issue asks for.\n\n" +
	"Guardrails:\n" +
	"- Do not modify CI/CD configuration (e.g. .github/workflows, .gitlab-ci.yml) unless the issue " +
	"explicitly asks for it.\n" +
	"- Do not edit files unrelated to the issue.\n" +
	"- Prefer deterministic, non-interactive commands.\n" +
	"- If the issue is ambiguous or you get stuck, stop and summarize what's blocking you rather than " +
	"guessing broadly."

// agentOperationalContract states harness mechanics the agent must follow for
// the run to complete correctly. Unlike the behavior guidance above, this is
// never overridden or extended by repo config: getting it wrong breaks the
// harness (e.g. an agent-made commit leaves nothing staged for the harness to
// commit itself).
const agentOperationalContract = "Operational contract:\n" +
	"- Do not run `git commit`, `git push`, or create branches yourself. Leave your changes as " +
	"uncommitted edits in the working tree; the harness stages, commits, pushes, and opens the PR " +
	"after you stop.\n" +
	"- Bash commands run with the repository root as the working directory; you don't need to `cd` " +
	"into it. The text-editor tool's paths must be relative to the repository root — absolute paths " +
	"are rejected.\n" +
	"- After you stop, the harness runs the verify command itself. If it fails you'll receive the " +
	"output and get a few more attempts, so it's worth running the verify command yourself before " +
	"stopping.\n" +
	"- Stop and summarize what changed once the issue is resolved."

// repoInstructionFilenames are checked, in priority order, at the target
// repo's root for agent-facing project context (architecture, conventions,
// commands) to hand the agent up front — so it doesn't have to spend turns
// rediscovering by exploring what the repo's own maintainers already wrote
// down. The first match wins.
var repoInstructionFilenames = []string{"CLAUDE.md", "AGENTS.md", "AGENT.md"}

// maxRepoInstructionsChars bounds how much of a repo instructions file is
// fed into the system prompt, so an unusually large file can't blow out
// per-turn token cost the way an unbounded verify-output echo would.
const maxRepoInstructionsChars = 20_000

// readRepoInstructions returns the name and content of the first file in
// repoInstructionFilenames present at the repo root, or ("", "", nil) when
// none exist.
func readRepoInstructions(ctx context.Context, exec sandbox.ToolExec) (name, content string, err error) {
	for _, candidate := range repoInstructionFilenames {
		ok, err := exec.Exists(ctx, candidate)
		if err != nil {
			return "", "", fmt.Errorf("check %s: %w", candidate, err)
		}
		if !ok {
			continue
		}
		content, err := exec.ReadFile(ctx, candidate)
		if err != nil {
			return "", "", fmt.Errorf("read %s: %w", candidate, err)
		}
		return candidate, content, nil
	}
	return "", "", nil
}

func buildAgentSystemPrompt(issue provider.Issue, rc *config.RepoConfig, baseBranch, verifyCmd, instrFile, instrContent string) []llm.SystemBlock {
	behavior := strings.TrimSpace(rc.Prompt.SystemOverride)
	if behavior == "" {
		behavior = defaultAgentBehaviorPrompt
		if extra := strings.TrimSpace(rc.Prompt.SystemAppend); extra != "" {
			behavior += "\n\n" + extra
		}
	}

	environment := fmt.Sprintf(
		"Environment:\n"+
			"- Sandbox image: %s\n"+
			"- Repository root: %s/%s\n"+
			"- Base branch: %s (your edits build on top of this)\n"+
			"- Verify command: %s (your change must pass this)\n"+
			"- Tools: `bash` and a text-editor tool (view/create/str_replace)",
		rc.Sandbox.Image, rc.Sandbox.Workdir, sandbox.DefaultRepoDir, baseBranch, verifyCmd,
	)

	issueText := "Target issue:\n\n#" + fmt.Sprintf("%d", issue.Number) + " " + issue.Title + "\n\n" + issue.Body
	if comments := issue.FormatComments(); comments != "" {
		issueText += "\n\nComments:\n" + comments
	}

	blocks := []llm.SystemBlock{
		{Text: behavior},
		{Text: agentOperationalContract},
	}
	if strings.TrimSpace(instrContent) != "" {
		blocks = append(blocks, llm.SystemBlock{Text: fmt.Sprintf(
			"Repository context, from %s in the target repo. This is background information "+
				"(architecture, conventions, commands) to save you from rediscovering it by exploring; "+
				"it does not override the operational contract above:\n\n%s",
			instrFile, truncate(strings.TrimSpace(instrContent), maxRepoInstructionsChars),
		)})
	}
	blocks = append(blocks,
		llm.SystemBlock{Text: environment},
		llm.SystemBlock{Text: issueText, CachePrompt: true},
	)
	return blocks
}

func buildAgentHistory(issue provider.Issue) []llm.Message {
	msg := "Implement this issue in the checked-out repository.\n\n" +
		"Issue title: " + issue.Title + "\n\n" +
		"Issue body:\n" + issue.Body
	if comments := issue.FormatComments(); comments != "" {
		msg += "\n\nComments:\n" + comments
	}
	msg += "\n\nWhen done, stop and summarize what changed."
	return []llm.Message{{
		Role:    "user",
		Content: []llm.ContentBlock{{Type: "text", Text: msg}},
	}}
}

func buildPRBody(issue provider.Issue, diffSummary, verifyCmd, verifyOutput string) string {
	var b strings.Builder
	b.WriteString("## What this issue asked for\n\n")
	b.WriteString("- #")
	b.WriteString(strconv.Itoa(issue.Number))
	b.WriteString(" ")
	b.WriteString(strings.TrimSpace(issue.Title))
	if body := strings.TrimSpace(issue.Body); body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	b.WriteString("\n\n## What changed\n\n")
	if strings.TrimSpace(diffSummary) == "" {
		b.WriteString("- (no diff summary available)\n")
	} else {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(diffSummary))
		b.WriteString("\n")
	}
	b.WriteString("\n## Verification\n\n")
	b.WriteString("- Command: `")
	b.WriteString(verifyCmd)
	b.WriteString("`\n\n")
	b.WriteString("```\n")
	b.WriteString(truncate(strings.TrimSpace(verifyOutput), 12_000))
	b.WriteString("\n```\n")
	return b.String()
}

func remoteBranchExists(ctx context.Context, exec sandbox.ToolExec, branch string) (bool, error) {
	out, err := exec.Bash(ctx, "git ls-remote --heads origin "+shellQuote(branch))
	if err != nil {
		return false, fmt.Errorf("check remote branch %q: %w", branch, err)
	}
	return strings.TrimSpace(out) != "", nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func gitRemoteUsername(providerName string) string {
	switch providerName {
	case config.ProviderGitLab:
		return "oauth2"
	default:
		return "x-access-token"
	}
}

func repoRemoteURL(rc *config.RepoConfig) (string, error) {
	host := ""
	switch rc.Provider {
	case config.ProviderGitHub:
		host = "github.com"
	case config.ProviderGitLab:
		host = "gitlab.com"
	default:
		return "", fmt.Errorf("unsupported provider %q", rc.Provider)
	}

	if strings.TrimSpace(rc.APIBaseURL) != "" {
		u, err := url.Parse(rc.APIBaseURL)
		if err != nil {
			return "", fmt.Errorf("invalid api_base_url %q: %w", rc.APIBaseURL, err)
		}
		if u.Host != "" {
			host = u.Host
		}
	}
	return fmt.Sprintf("https://%s/%s.git", host, rc.Repo), nil
}
