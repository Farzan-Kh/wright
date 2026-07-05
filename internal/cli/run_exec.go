package cli

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/farzan-kh/patchr/internal/agent"
	"github.com/farzan-kh/patchr/internal/agent/llm"
	"github.com/farzan-kh/patchr/internal/config"
	"github.com/farzan-kh/patchr/internal/cost"
	"github.com/farzan-kh/patchr/internal/gitops"
	"github.com/farzan-kh/patchr/internal/pipeline"
	"github.com/farzan-kh/patchr/internal/provider"
	"github.com/farzan-kh/patchr/internal/sandbox"
	"github.com/farzan-kh/patchr/internal/verifier"
)

type issueExecutor struct {
	Provider      provider.Provider
	Repo          provider.Repo
	RepoConfig    *config.RepoConfig
	ProviderToken string
	LLM           llm.LLMProvider
	Sandbox       sandbox.Orchestrator
}

func (e *issueExecutor) Handle(ctx context.Context, issue provider.Issue) (cost.Summary, error) {
	zeroCost := cost.Summary{}
	branchName := gitops.BranchName(issue.Number)

	existingPR, err := e.Provider.FindOpenPullRequestByHead(ctx, e.Repo, branchName)
	if err != nil {
		return zeroCost, err
	}
	if existingPR != nil {
		reason := fmt.Sprintf("idempotency: open PR already exists for %s (PR #%d %s)", branchName, existingPR.Number, existingPR.URL)
		_ = e.Provider.CommentOnIssue(ctx, e.Repo, issue.Number, "Patchr skipped this run because an open PR already exists for this issue branch:\n\n"+existingPR.URL)
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

	remoteURL, err := repoRemoteURL(e.RepoConfig)
	if err != nil {
		return cost.Summary{}, err
	}
	authRemoteURL, err := gitops.InjectCredentialIntoRemoteURL(remoteURL, gitRemoteUsername(e.RepoConfig.Provider), e.ProviderToken)
	if err != nil {
		return cost.Summary{}, fmt.Errorf("build authenticated remote url: %w", err)
	}

	task, err := e.Sandbox.Start(ctx, sandbox.TaskSpec{
		Image:      e.RepoConfig.Sandbox.Image,
		Workdir:    e.RepoConfig.Sandbox.Workdir,
		RepoDir:    sandbox.DefaultRepoDir,
		CloneURL:   authRemoteURL,
		BaseBranch: baseBranch,
	})
	if err != nil {
		return cost.Summary{}, err
	}
	defer func() {
		_ = task.Close(context.Background())
	}()

	branchExists, err := remoteBranchExists(ctx, task, branchName)
	if err != nil {
		return zeroCost, err
	}
	if branchExists {
		reason := fmt.Sprintf("idempotency: branch %s already exists", branchName)
		_ = e.Provider.CommentOnIssue(ctx, e.Repo, issue.Number, "Patchr skipped this run because branch `"+branchName+"` already exists. If you want a retry, close/delete existing artifacts and re-apply the label.")
		return zeroCost, pipeline.NewSkipError(reason)
	}

	runner := &agent.Runner{
		LLM:  e.LLM,
		Exec: task,
		Cfg: agent.Config{
			Model:       e.RepoConfig.LLM.AgentModel,
			MaxTokens:   8192,
			MaxTurns:    e.RepoConfig.Budget.MaxTurns,
			ThinkEffort: e.RepoConfig.LLM.Effort,
		},
	}

	v := &verifier.Verifier{OverrideCommand: e.RepoConfig.Verify.Command}
	verifyCmd, err := v.DetectCommand(ctx, task)
	if err != nil {
		return cost.Summary{}, err
	}

	systemPrompt := buildAgentSystemPrompt(issue, e.RepoConfig, baseBranch, verifyCmd)
	history := buildAgentHistory(issue)
	tools := []llm.ToolSpec{
		{Type: "bash_20250124"},
		{Type: "text_editor_20250728"},
	}
	totalCost := zeroCost
	var verifyOut string
	const maxVerifyAttempts = 3
	for attempt := 1; attempt <= maxVerifyAttempts; attempt++ {
		cfg := runner.Cfg
		if cfg.MaxTurns > 0 {
			remainingTurns := cfg.MaxTurns - totalCost.Turns
			if remainingTurns <= 0 {
				return totalCost, agent.ErrTurnLimit
			}
			cfg.MaxTurns = remainingTurns
		}
		runner.Cfg = cfg

		runResult, runErr := runner.Run(ctx, agent.RunRequest{System: systemPrompt, History: history, Tools: tools})
		totalCost = mergeCostSummary(totalCost, runResult.UsageAndCost)
		if runErr != nil {
			return totalCost, runErr
		}
		history = runResult.History

		verifyOut, err = task.Bash(ctx, verifyCmd)
		if err == nil {
			break
		}
		if attempt == maxVerifyAttempts {
			return totalCost, fmt.Errorf("verify failed after %d attempt(s) (%s): %w\n\n%s", attempt, verifyCmd, err, truncate(verifyOut, 8_000))
		}
		feedback := "Verification failed. Fix the implementation and run the tests again.\n\n" +
			"Command: " + verifyCmd + "\n\nOutput:\n" + truncate(verifyOut, 8_000)
		history = append(history, llm.Message{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: feedback}},
		})
	}

	ops := &gitops.Ops{Exec: task, Provider: e.Provider, Repo: e.Repo, RemoteUser: gitRemoteUsername(e.RepoConfig.Provider)}
	commitTitle := fmt.Sprintf("patchr: resolve issue #%d", issue.Number)
	branch, diffSummary, err := ops.CommitAndPush(ctx, issue.Number, remoteURL, e.ProviderToken, commitTitle)
	if err != nil {
		return totalCost, err
	}

	prTitle := fmt.Sprintf("Issue #%d: %s", issue.Number, truncate(strings.TrimSpace(issue.Title), 72))
	prBody := buildPRBody(issue, diffSummary, verifyCmd, verifyOut)
	pr, err := ops.OpenPR(ctx, prTitle, prBody, branch, baseBranch, false)
	if err != nil {
		return totalCost, err
	}

	_ = e.Provider.CommentOnIssue(ctx, e.Repo, issue.Number,
		fmt.Sprintf("Patchr opened PR #%d: %s", pr.Number, pr.URL))

	return totalCost, nil
}

// defaultAgentBehaviorPrompt is the default identity/behavior guidance for the
// coding agent. It is replaced wholesale by RepoConfig.Prompt.SystemOverride,
// or extended by RepoConfig.Prompt.SystemAppend, when configured.
const defaultAgentBehaviorPrompt = "You are Patchr, an autonomous software-maintenance agent. " +
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

func buildAgentSystemPrompt(issue provider.Issue, rc *config.RepoConfig, baseBranch, verifyCmd string) []llm.SystemBlock {
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

	return []llm.SystemBlock{
		{Text: behavior},
		{Text: agentOperationalContract},
		{Text: environment},
		{Text: "Target issue:\n\n#" + fmt.Sprintf("%d", issue.Number) + " " + issue.Title + "\n\n" + issue.Body, CachePrompt: true},
	}
}

func buildAgentHistory(issue provider.Issue) []llm.Message {
	msg := "Implement this issue in the checked-out repository.\n\n" +
		"Issue title: " + issue.Title + "\n\n" +
		"Issue body:\n" + issue.Body + "\n\n" +
		"When done, stop and summarize what changed."
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

func mergeCostSummary(a, b cost.Summary) cost.Summary {
	a.Turns += b.Turns
	a.Usage.InputTokens += b.Usage.InputTokens
	a.Usage.OutputTokens += b.Usage.OutputTokens
	a.Usage.CacheCreationInputTokens += b.Usage.CacheCreationInputTokens
	a.Usage.CacheReadInputTokens += b.Usage.CacheReadInputTokens
	return a
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
