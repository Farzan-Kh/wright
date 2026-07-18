// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farzan-kh/wright/internal/agent"
	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cache"
	"github.com/farzan-kh/wright/internal/config"
	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/pipeline"
	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/sandbox"
	"github.com/farzan-kh/wright/internal/stack"
)

func TestBuildLLMRejectsOAuthInPhase1(t *testing.T) {
	rc := &config.RepoConfig{
		LLM: config.LLMConfig{Provider: config.LLMProviderClaude, Auth: "oauth"},
	}
	_, err := buildLLM(rc, logging.FromContext(context.Background()))
	if err == nil {
		t.Fatal("buildLLM(oauth) = nil error, want a Phase 1 not-supported error")
	}
	if !strings.Contains(err.Error(), "not supported in Phase 1") || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error = %q, want it to mention Phase 1 and api_key", err)
	}
}

func TestRepoRemoteURL(t *testing.T) {
	tests := []struct {
		name string
		rc   config.RepoConfig
		want string
	}{
		{
			name: "github_saas",
			rc:   config.RepoConfig{Provider: config.ProviderGitHub, Repo: "acme/widgets"},
			want: "https://github.com/acme/widgets.git",
		},
		{
			name: "gitlab_saas",
			rc:   config.RepoConfig{Provider: config.ProviderGitLab, Repo: "group/sub/app"},
			want: "https://gitlab.com/group/sub/app.git",
		},
		{
			name: "github_enterprise",
			rc: config.RepoConfig{
				Provider:   config.ProviderGitHub,
				Repo:       "acme/widgets",
				APIBaseURL: "https://ghe.example.com/api/v3",
			},
			want: "https://ghe.example.com/acme/widgets.git",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repoRemoteURL(&tc.rc)
			if err != nil {
				t.Fatalf("repoRemoteURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("repoRemoteURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGitRemoteUsername(t *testing.T) {
	if got := gitRemoteUsername(config.ProviderGitHub); got != "x-access-token" {
		t.Fatalf("github username = %q, want x-access-token", got)
	}
	if got := gitRemoteUsername(config.ProviderGitLab); got != "oauth2" {
		t.Fatalf("gitlab username = %q, want oauth2", got)
	}
}

func TestBuildPRBody(t *testing.T) {
	body := buildPRBody(
		provider.Issue{Number: 7, Title: "Fix crash", Body: "When clicking save"},
		"1 file changed, 10 insertions(+), 2 deletions(-)",
		"go test ./...",
		"ok\tmodule\t0.123s",
	)

	for _, frag := range []string{
		"## What this issue asked for",
		"#7 Fix crash",
		"## What changed",
		"1 file changed",
		"## Verification",
		"`go test ./...`",
		"ok\tmodule",
	} {
		if !strings.Contains(body, frag) {
			t.Fatalf("PR body missing %q\n\n%s", frag, body)
		}
	}
}

func TestBuildAgentSystemPromptDefaultIncludesDynamicFacts(t *testing.T) {
	rc := &config.RepoConfig{
		Sandbox: config.SandboxConfig{Image: "alpine/git:2.47.2", Workdir: "/workspace"},
	}
	blocks := buildAgentSystemPrompt(provider.Issue{Number: 5, Title: "Fix bug", Body: "Steps"}, rc, "main", "go test ./...", "", "")

	var all strings.Builder
	for _, b := range blocks {
		all.WriteString(b.Text)
		all.WriteString("\n")
	}
	text := all.String()

	for _, frag := range []string{
		defaultAgentBehaviorPrompt,
		"the harness stages, commits, pushes",
		"alpine/git:2.47.2",
		"/workspace/repo",
		"Base branch: main",
		"Verify command: go test ./...",
		"#5 Fix bug",
	} {
		if !strings.Contains(text, frag) {
			t.Fatalf("system prompt missing %q\n\n%s", frag, text)
		}
	}

	last := blocks[len(blocks)-1]
	if !last.CachePrompt {
		t.Fatal("last block (issue text) should have CachePrompt=true")
	}
}

func TestBuildAgentSystemPromptSystemAppend(t *testing.T) {
	rc := &config.RepoConfig{Prompt: config.PromptConfig{SystemAppend: "Always update CHANGELOG.md."}}
	blocks := buildAgentSystemPrompt(provider.Issue{Number: 1, Title: "T"}, rc, "main", "make test", "", "")

	if !strings.Contains(blocks[0].Text, defaultAgentBehaviorPrompt) {
		t.Fatalf("behavior block should still contain the default text, got %q", blocks[0].Text)
	}
	if !strings.Contains(blocks[0].Text, "Always update CHANGELOG.md.") {
		t.Fatalf("behavior block should contain the appended text, got %q", blocks[0].Text)
	}
}

func TestBuildAgentSystemPromptSystemOverride(t *testing.T) {
	rc := &config.RepoConfig{Prompt: config.PromptConfig{SystemOverride: "You are a specialized widget-repo agent."}}
	blocks := buildAgentSystemPrompt(provider.Issue{Number: 1, Title: "T"}, rc, "main", "make test", "", "")

	if blocks[0].Text != "You are a specialized widget-repo agent." {
		t.Fatalf("behavior block = %q, want the override verbatim", blocks[0].Text)
	}
	if strings.Contains(blocks[0].Text, defaultAgentBehaviorPrompt) {
		t.Fatal("override should fully replace the default behavior text")
	}

	var all strings.Builder
	for _, b := range blocks {
		all.WriteString(b.Text)
	}
	if !strings.Contains(all.String(), "the harness stages, commits, pushes") {
		t.Fatal("operational contract must still be present even when behavior text is overridden")
	}
}

func TestReadRepoInstructionsPrefersCLAUDEMD(t *testing.T) {
	exec := &sandbox.FakeExec{Files: map[string]string{
		"CLAUDE.md": "claude instructions",
		"AGENTS.md": "agents instructions",
	}}
	name, content, err := readRepoInstructions(context.Background(), exec)
	if err != nil {
		t.Fatalf("readRepoInstructions: %v", err)
	}
	if name != "CLAUDE.md" || content != "claude instructions" {
		t.Fatalf("got (%q, %q), want (CLAUDE.md, claude instructions)", name, content)
	}
}

func TestReadRepoInstructionsFallsBackToAgentsMD(t *testing.T) {
	exec := &sandbox.FakeExec{Files: map[string]string{
		"AGENTS.md": "agents instructions",
	}}
	name, content, err := readRepoInstructions(context.Background(), exec)
	if err != nil {
		t.Fatalf("readRepoInstructions: %v", err)
	}
	if name != "AGENTS.md" || content != "agents instructions" {
		t.Fatalf("got (%q, %q), want (AGENTS.md, agents instructions)", name, content)
	}
}

func TestReadRepoInstructionsNoneFound(t *testing.T) {
	exec := &sandbox.FakeExec{Files: map[string]string{"README.md": "hi"}}
	name, content, err := readRepoInstructions(context.Background(), exec)
	if err != nil {
		t.Fatalf("readRepoInstructions: %v", err)
	}
	if name != "" || content != "" {
		t.Fatalf("got (%q, %q), want (\"\", \"\")", name, content)
	}
}

func TestBuildAgentSystemPromptRepoInstructions(t *testing.T) {
	rc := &config.RepoConfig{Sandbox: config.SandboxConfig{Image: "alpine/git:2.47.2", Workdir: "/workspace"}}
	blocks := buildAgentSystemPrompt(provider.Issue{Number: 1, Title: "T"}, rc, "main", "make test", "CLAUDE.md", "This repo uses X.")

	if len(blocks) != 5 {
		t.Fatalf("got %d blocks, want 5 (behavior, contract, repo instructions, environment, issue text)", len(blocks))
	}
	instr := blocks[2].Text
	if !strings.Contains(instr, "CLAUDE.md") || !strings.Contains(instr, "This repo uses X.") {
		t.Fatalf("repo instructions block missing content: %q", instr)
	}
	if !strings.Contains(instr, "does not override the operational contract") {
		t.Fatalf("repo instructions block should be framed as non-authoritative: %q", instr)
	}
	if !strings.HasPrefix(blocks[3].Text, "Environment:") {
		t.Fatalf("environment block should follow repo instructions, got %q", blocks[3].Text)
	}

	last := blocks[len(blocks)-1]
	if !last.CachePrompt {
		t.Fatal("last block (issue text) should have CachePrompt=true")
	}
}

func TestBuildAgentSystemPromptNoRepoInstructionsOmitsBlock(t *testing.T) {
	rc := &config.RepoConfig{Sandbox: config.SandboxConfig{Image: "alpine/git:2.47.2", Workdir: "/workspace"}}
	blocks := buildAgentSystemPrompt(provider.Issue{Number: 1, Title: "T"}, rc, "main", "make test", "", "")
	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4 (behavior, contract, environment, issue text) when no repo instructions exist", len(blocks))
	}
}

type fakeExecProvider struct {
	findPR         *provider.PullRequest
	defaultBranch  string
	openPRCount    int
	openPRLastSpec provider.PullRequestSpec
	openPRErr      error
	// findPRByBranch overrides findPR for specific head branches, so a test
	// can distinguish the current issue's own branch (idempotency check)
	// from a dependency's branch (stacking lookup). A branch absent here
	// falls back to findPR.
	findPRByBranch    map[string]*provider.PullRequest
	commentIssueCalls []string
}

func (f *fakeExecProvider) Name() string { return "fake" }
func (f *fakeExecProvider) ListLabeledIssues(context.Context, provider.Repo, string) ([]provider.Issue, error) {
	return nil, nil
}
func (f *fakeExecProvider) GetIssue(context.Context, provider.Repo, int) (*provider.Issue, error) {
	return nil, provider.ErrNotFound
}
func (f *fakeExecProvider) ReadRepoFile(context.Context, provider.Repo, string, string) (string, error) {
	return "", provider.ErrNotFound
}
func (f *fakeExecProvider) ListRepoDir(context.Context, provider.Repo, string, string) ([]string, error) {
	return nil, provider.ErrNotFound
}
func (f *fakeExecProvider) CommentOnIssue(_ context.Context, _ provider.Repo, issueNumber int, body string) error {
	f.commentIssueCalls = append(f.commentIssueCalls, strings.TrimSpace(body))
	_ = issueNumber
	return nil
}
func (f *fakeExecProvider) AddIssueLabel(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeExecProvider) RemoveIssueLabel(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeExecProvider) CommentOnPullRequest(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeExecProvider) DefaultBranch(context.Context, provider.Repo) (string, error) {
	if strings.TrimSpace(f.defaultBranch) == "" {
		return "main", nil
	}
	return f.defaultBranch, nil
}
func (f *fakeExecProvider) CreateBranch(context.Context, provider.Repo, string, string) error {
	return nil
}
func (f *fakeExecProvider) DeleteBranch(context.Context, provider.Repo, string) error { return nil }
func (f *fakeExecProvider) PushCommits(context.Context, provider.Repo, string, []provider.Commit) (string, error) {
	return "", nil
}
func (f *fakeExecProvider) FindOpenPullRequestByHead(_ context.Context, _ provider.Repo, headBranch string) (*provider.PullRequest, error) {
	if pr, ok := f.findPRByBranch[headBranch]; ok {
		return pr, nil
	}
	return f.findPR, nil
}
func (f *fakeExecProvider) OpenPullRequest(_ context.Context, _ provider.Repo, spec provider.PullRequestSpec) (*provider.PullRequest, error) {
	f.openPRCount++
	f.openPRLastSpec = spec
	if f.openPRErr != nil {
		return nil, f.openPRErr
	}
	return &provider.PullRequest{Number: 77, URL: "https://example.com/pr/77", HeadBranch: spec.HeadBranch, BaseBranch: spec.BaseBranch}, nil
}
func (f *fakeExecProvider) GetPullRequest(context.Context, provider.Repo, int) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeExecProvider) UpdatePullRequestBase(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeExecProvider) MergePullRequest(context.Context, provider.Repo, int, provider.MergeOptions) error {
	return nil
}
func (f *fakeExecProvider) ClosePullRequest(context.Context, provider.Repo, int) error { return nil }

type fakeTask struct {
	sandbox.FakeExec
	closed bool
}

func (t *fakeTask) RepoDir() string { return "/workspace/repo" }
func (t *fakeTask) Close(context.Context) error {
	t.closed = true
	return nil
}

type fakeOrchestrator struct {
	task     sandbox.Task
	lastSpec sandbox.TaskSpec
	starts   int
}

func (o *fakeOrchestrator) Start(_ context.Context, spec sandbox.TaskSpec) (sandbox.Task, error) {
	o.starts++
	o.lastSpec = spec
	return o.task, nil
}

func TestIssueExecutorHandleSkipsWhenOpenPRExists(t *testing.T) {
	fp := &fakeExecProvider{findPR: &provider.PullRequest{Number: 12, URL: "https://example.com/pr/12"}}
	exec := &issueExecutor{
		Provider: fp,
		Repo:     provider.Repo{FullPath: "acme/widgets"},
		RepoConfig: &config.RepoConfig{
			Provider: config.ProviderGitHub,
			Repo:     "acme/widgets",
			LLM:      config.LLMConfig{Auth: "api_key"},
		},
	}

	s, err := exec.Handle(context.Background(), provider.Issue{Number: 9, Title: "Fix"})
	var skip *pipeline.SkipError
	if !errors.As(err, &skip) {
		t.Fatalf("err = %v, want SkipError", err)
	}
	if s.Turns != 0 {
		t.Fatalf("summary = %+v", s)
	}
	if len(fp.commentIssueCalls) != 1 {
		t.Fatalf("commentIssueCalls = %d, want 1", len(fp.commentIssueCalls))
	}
}

func TestIssueExecutorHandleRetriesAfterVerifyFailure(t *testing.T) {
	fp := &fakeExecProvider{defaultBranch: "main"}
	llmFake := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "first pass"}}},
			StopReason: "end_turn",
			Usage:      cost.Usage{InputTokens: 100, OutputTokens: 10},
		},
		{
			Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "retry pass"}}},
			StopReason: "end_turn",
			Usage:      cost.Usage{InputTokens: 80, OutputTokens: 12},
		},
	}}

	verifyCalls := 0
	task := &fakeTask{}
	task.Files = map[string]string{
		"go.mod":    "module acme/widgets",
		"CLAUDE.md": "This repo is a Go module; run go test ./... to verify.",
	}
	task.BashFn = func(command string) (string, error) {
		switch {
		case command == "git ls-remote --heads origin 'wright/issue-11'":
			return "", nil // branch does not exist remotely
		case command == "go test ./...":
			verifyCalls++
			if verifyCalls == 1 {
				return "FAIL\tacme/widgets\t0.01s", errors.New("tests failed")
			}
			return "ok\tacme/widgets\t0.02s", nil
		case command == "git checkout -b wright/issue-11":
			return "", nil
		case command == "git add -A":
			return "", nil
		case command == "git diff --cached --shortstat":
			return "1 file changed, 1 insertion(+)\n", nil
		case strings.HasPrefix(command, "git commit -m "):
			return "", nil
		case strings.HasPrefix(command, "git push "):
			return "", nil
		default:
			return "", nil
		}
	}

	orch := &fakeOrchestrator{task: task}
	exec := &issueExecutor{
		Provider:      fp,
		Repo:          provider.Repo{FullPath: "acme/widgets"},
		ProviderToken: "provider-token",
		LLM:           llmFake,
		Sandbox:       orch,
		RepoConfig: &config.RepoConfig{
			Provider: config.ProviderGitHub,
			Repo:     "acme/widgets",
			LLM: config.LLMConfig{
				Auth:       "api_key",
				AgentModel: "claude-sonnet-5",
				Effort:     "high",
			},
			Budget: config.BudgetConfig{MaxTurns: 10},
		},
	}

	s, err := exec.Handle(context.Background(), provider.Issue{Number: 11, Title: "Fix flaky test", Body: "details"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if s.Turns != 2 {
		t.Fatalf("turns = %d, want 2", s.Turns)
	}
	if verifyCalls != 2 {
		t.Fatalf("verifyCalls = %d, want 2", verifyCalls)
	}
	if fp.openPRCount != 1 {
		t.Fatalf("openPRCount = %d, want 1", fp.openPRCount)
	}
	if fp.openPRLastSpec.HeadBranch != "wright/issue-11" || fp.openPRLastSpec.BaseBranch != "main" {
		t.Fatalf("unexpected PR spec: %+v", fp.openPRLastSpec)
	}
	if orch.starts != 1 {
		t.Fatalf("orchestrator starts = %d, want 1", orch.starts)
	}
	if !task.closed {
		t.Fatal("sandbox task should be closed")
	}

	if len(llmFake.Requests) != 2 {
		t.Fatalf("llm requests = %d, want 2", len(llmFake.Requests))
	}
	foundFeedback := false
	for _, m := range llmFake.Requests[1].Messages {
		for _, c := range m.Content {
			if c.Type == "text" && strings.Contains(c.Text, "Verification failed") {
				foundFeedback = true
			}
		}
	}
	if !foundFeedback {
		t.Fatalf("second LLM request should include verification feedback, got %+v", llmFake.Requests[1].Messages)
	}

	foundRepoInstructions := false
	for _, sb := range llmFake.Requests[0].System {
		if strings.Contains(sb.Text, "CLAUDE.md") && strings.Contains(sb.Text, "run go test ./... to verify") {
			foundRepoInstructions = true
		}
	}
	if !foundRepoInstructions {
		t.Fatalf("first LLM request should include CLAUDE.md repo instructions, got %+v", llmFake.Requests[0].System)
	}
}

// TestIssueExecutorHandleStacksOnDependencyPR is the regression test for the
// stacked-PR feature: issue #14 references #13, which already has an open
// Wright PR (#40, branch wright/issue-13). With stacking enabled, Wright must
// branch #14's work off wright/issue-13 instead of the repo's real base
// branch, note the stacking relationship in the PR body, and register the
// new PR with the stack store so it can be retargeted once #13 merges.
func TestIssueExecutorHandleStacksOnDependencyPR(t *testing.T) {
	fp := &fakeExecProvider{
		defaultBranch: "main",
		findPRByBranch: map[string]*provider.PullRequest{
			"wright/issue-13": {Number: 40, URL: "https://example.com/pr/40", HeadBranch: "wright/issue-13", State: "open"},
		},
	}
	llmFake := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage:      cost.Usage{InputTokens: 50, OutputTokens: 5},
	}}}

	task := &fakeTask{}
	task.Files = map[string]string{"go.mod": "module acme/widgets"}
	task.BashFn = func(command string) (string, error) {
		switch {
		case command == "git ls-remote --heads origin 'wright/issue-14'":
			return "", nil
		case command == "go test ./...":
			return "ok\tacme/widgets\t0.02s", nil
		case strings.HasPrefix(command, "git checkout -b "),
			command == "git add -A",
			strings.HasPrefix(command, "git commit -m "),
			strings.HasPrefix(command, "git push "):
			return "", nil
		case command == "git diff --cached --shortstat":
			return "1 file changed, 1 insertion(+)\n", nil
		default:
			return "", nil
		}
	}
	orch := &fakeOrchestrator{task: task}

	stackStore := &stack.FileStore{Dir: t.TempDir()}
	exec := &issueExecutor{
		Provider:      fp,
		Repo:          provider.Repo{FullPath: "acme/widgets"},
		ProviderToken: "provider-token",
		LLM:           llmFake,
		Sandbox:       orch,
		Stack:         stackStore,
		RepoConfig: &config.RepoConfig{
			Provider: config.ProviderGitHub,
			Repo:     "acme/widgets",
			LLM: config.LLMConfig{
				Auth:       "api_key",
				AgentModel: "claude-sonnet-5",
				Effort:     "high",
			},
			Budget:   config.BudgetConfig{MaxTurns: 10},
			Stacking: config.StackingConfig{Enabled: true},
		},
	}

	_, err := exec.Handle(context.Background(), provider.Issue{Number: 14, Title: "Use config", Body: "Requires #13."})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if orch.lastSpec.BaseBranch != "wright/issue-13" {
		t.Fatalf("sandbox base branch = %q, want wright/issue-13 (stacked)", orch.lastSpec.BaseBranch)
	}
	if fp.openPRLastSpec.BaseBranch != "wright/issue-13" {
		t.Fatalf("PR base = %q, want wright/issue-13 (stacked)", fp.openPRLastSpec.BaseBranch)
	}
	if !strings.Contains(fp.openPRLastSpec.Body, "Stacked PR") || !strings.Contains(fp.openPRLastSpec.Body, "#13") {
		t.Fatalf("PR body missing stacking note:\n%s", fp.openPRLastSpec.Body)
	}

	entries, err := stackStore.ListPending("acme/widgets")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("stack entries = %+v, want 1", entries)
	}
	e := entries[0]
	if e.DependsOnIssue != 13 || e.DependsOnPRNumber != 40 || e.RealBaseBranch != "main" {
		t.Fatalf("stack entry = %+v, want depends on #13/PR40 with real base main", e)
	}
}

// TestIssueExecutorHandleCachesOnTurnLimitAndResumes exercises the main
// resume path end to end: a first attempt burns its whole turn budget
// mid-edit, gets cached, and a second attempt against the same cache picks
// the conversation back up (with the prior diff already reapplied to the
// fresh sandbox) instead of starting the agent over from scratch.
func TestIssueExecutorHandleCachesOnTurnLimitAndResumes(t *testing.T) {
	store := &cache.FileStore{Dir: t.TempDir()}
	repo := provider.Repo{FullPath: "acme/widgets"}
	rc := &config.RepoConfig{
		Provider: config.ProviderGitHub,
		Repo:     "acme/widgets",
		LLM:      config.LLMConfig{Auth: "api_key", AgentModel: "claude-sonnet-5", Effort: "high"},
		Budget:   config.BudgetConfig{MaxTurns: 1},
		Verify:   config.VerifyConfig{Command: "go test ./..."},
	}
	issue := provider.Issue{Number: 21, Title: "Add feature", Body: "details"}
	fp := &fakeExecProvider{defaultBranch: "main"}

	// Phase A: the agent uses a tool and immediately runs out of turn budget.
	llmFakeA := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{
				{Type: "tool_use", Name: "bash", ToolUseID: "t1", Input: map[string]any{"command": "echo hi"}},
			}},
			StopReason: "tool_use",
			Usage:      cost.Usage{InputTokens: 50, OutputTokens: 5},
		},
	}}
	taskA := &fakeTask{}
	taskA.BashFn = func(command string) (string, error) {
		switch command {
		case "git ls-remote --heads origin 'wright/issue-21'":
			return "", nil
		case "git add -A && git diff --cached 'main'":
			return "diff --git a/foo.go b/foo.go\n+package foo\n", nil
		default:
			return "", nil
		}
	}
	orchA := &fakeOrchestrator{task: taskA}
	execA := &issueExecutor{
		Provider: fp, Repo: repo, RepoConfig: rc, ProviderToken: "tok",
		LLM: llmFakeA, Sandbox: orchA, Cache: store,
	}

	_, err := execA.Handle(context.Background(), issue)
	if !errors.Is(err, agent.ErrTurnLimit) {
		t.Fatalf("Handle phase A err = %v, want agent.ErrTurnLimit", err)
	}

	entry, loadErr := store.Load(repo.FullPath, issue.Number)
	if loadErr != nil {
		t.Fatalf("cache Load: %v", loadErr)
	}
	if entry == nil {
		t.Fatal("expected a cached entry after turn-limit failure")
	}
	if entry.Stage != cache.StageAgentIncomplete {
		t.Fatalf("Stage = %q, want %q", entry.Stage, cache.StageAgentIncomplete)
	}
	if strings.TrimSpace(entry.Diff) == "" {
		t.Fatal("expected a non-empty cached diff")
	}
	if entry.Cost.Turns != 1 {
		t.Fatalf("cached Cost.Turns = %d, want 1", entry.Cost.Turns)
	}
	cachedHistoryLen := len(entry.History)
	if cachedHistoryLen == 0 {
		t.Fatal("expected cached conversation history")
	}

	// Phase B: a fresh sandbox, resumed with a bigger turn budget, finishes.
	rcResume := *rc
	rcResume.Budget.MaxTurns = 10
	llmFakeB := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
			Usage:      cost.Usage{InputTokens: 30, OutputTokens: 5},
		},
	}}
	taskB := &fakeTask{}
	verifyCalls := 0
	taskB.BashFn = func(command string) (string, error) {
		switch {
		case command == "git ls-remote --heads origin 'wright/issue-21'":
			return "", nil
		case command == "git apply --whitespace=nowarn '.wright-resume.patch' && rm -f '.wright-resume.patch'":
			return "", nil
		case command == "go test ./...":
			verifyCalls++
			return "ok", nil
		case command == "git checkout -b wright/issue-21":
			return "", nil
		case command == "git add -A":
			return "", nil
		case command == "git diff --cached --shortstat":
			return "1 file changed, 1 insertion(+)\n", nil
		case strings.HasPrefix(command, "git commit -m "):
			return "", nil
		case strings.HasPrefix(command, "git push "):
			return "", nil
		default:
			return "", nil
		}
	}
	orchB := &fakeOrchestrator{task: taskB}
	execB := &issueExecutor{
		Provider: fp, Repo: repo, RepoConfig: &rcResume, ProviderToken: "tok",
		LLM: llmFakeB, Sandbox: orchB, Cache: store,
	}

	s, err := execB.Handle(context.Background(), issue)
	if err != nil {
		t.Fatalf("Handle phase B: %v", err)
	}
	if verifyCalls != 1 {
		t.Fatalf("verifyCalls = %d, want 1", verifyCalls)
	}
	if s.Turns != 2 { // 1 turn already cached + 1 turn to finish
		t.Fatalf("total turns = %d, want 2", s.Turns)
	}
	if len(llmFakeB.Requests) != 1 {
		t.Fatalf("llm requests in phase B = %d, want 1", len(llmFakeB.Requests))
	}
	if len(llmFakeB.Requests[0].Messages) != cachedHistoryLen {
		t.Fatalf("resumed history length = %d, want cached %d", len(llmFakeB.Requests[0].Messages), cachedHistoryLen)
	}
	if fp.openPRCount != 1 {
		t.Fatalf("openPRCount = %d, want 1", fp.openPRCount)
	}

	final, loadErr := store.Load(repo.FullPath, issue.Number)
	if loadErr != nil {
		t.Fatalf("cache Load after success: %v", loadErr)
	}
	if final != nil {
		t.Fatal("cache should be cleared after a successful resume")
	}
}

// TestIssueExecutorHandleCachesOnCommitPushFailureAndResumes covers the
// verified_unpushed stage: verify passes but the commit/push step fails, and
// a resume reapplies the diff, re-verifies (no LLM call), and finishes the
// git + PR steps without invoking the agent again.
func TestIssueExecutorHandleCachesOnCommitPushFailureAndResumes(t *testing.T) {
	store := &cache.FileStore{Dir: t.TempDir()}
	repo := provider.Repo{FullPath: "acme/widgets"}
	rc := &config.RepoConfig{
		Provider: config.ProviderGitHub,
		Repo:     "acme/widgets",
		LLM:      config.LLMConfig{Auth: "api_key", AgentModel: "claude-sonnet-5", Effort: "high"},
		Budget:   config.BudgetConfig{MaxTurns: 10},
		Verify:   config.VerifyConfig{Command: "go test ./..."},
	}
	issue := provider.Issue{Number: 30, Title: "Fix bug", Body: "details"}
	fp := &fakeExecProvider{defaultBranch: "main"}

	// Phase A: agent finishes and verify passes, but the push fails.
	llmFakeA := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
			Usage:      cost.Usage{InputTokens: 40, OutputTokens: 5},
		},
	}}
	taskA := &fakeTask{}
	taskA.BashFn = func(command string) (string, error) {
		switch {
		case command == "git ls-remote --heads origin 'wright/issue-30'":
			return "", nil
		case command == "go test ./...":
			return "ok", nil
		case command == "git add -A && git diff --cached 'main'":
			return "diff --git a/bar.go b/bar.go\n+package bar\n", nil
		case command == "git checkout -b wright/issue-30":
			return "", nil
		case command == "git add -A":
			return "", nil
		case command == "git diff --cached --shortstat":
			return "1 file changed, 1 insertion(+)\n", nil
		case strings.HasPrefix(command, "git commit -m "):
			return "", nil
		case strings.HasPrefix(command, "git push "):
			return "", errors.New("connection reset")
		default:
			return "", nil
		}
	}
	orchA := &fakeOrchestrator{task: taskA}
	execA := &issueExecutor{
		Provider: fp, Repo: repo, RepoConfig: rc, ProviderToken: "tok",
		LLM: llmFakeA, Sandbox: orchA, Cache: store,
	}

	_, err := execA.Handle(context.Background(), issue)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("Handle phase A err = %v, want a push error", err)
	}

	entry, loadErr := store.Load(repo.FullPath, issue.Number)
	if loadErr != nil {
		t.Fatalf("cache Load: %v", loadErr)
	}
	if entry == nil || entry.Stage != cache.StageVerifiedUnpushed {
		t.Fatalf("entry = %+v, want a StageVerifiedUnpushed entry", entry)
	}
	if strings.TrimSpace(entry.Diff) == "" {
		t.Fatal("expected a non-empty cached diff")
	}

	// Phase B: fresh sandbox, no LLM responses queued at all - if the agent
	// were invoked here it would fail with ErrFakeExhausted.
	llmFakeB := &llm.FakeProvider{}
	taskB := &fakeTask{}
	verifyCalls, pushCalls := 0, 0
	taskB.BashFn = func(command string) (string, error) {
		switch {
		case command == "git ls-remote --heads origin 'wright/issue-30'":
			return "", nil
		case command == "git apply --whitespace=nowarn '.wright-resume.patch' && rm -f '.wright-resume.patch'":
			return "", nil
		case command == "go test ./...":
			verifyCalls++
			return "ok", nil
		case command == "git checkout -b wright/issue-30":
			return "", nil
		case command == "git add -A":
			return "", nil
		case command == "git diff --cached --shortstat":
			return "1 file changed, 1 insertion(+)\n", nil
		case strings.HasPrefix(command, "git commit -m "):
			return "", nil
		case strings.HasPrefix(command, "git push "):
			pushCalls++
			return "", nil
		default:
			return "", nil
		}
	}
	orchB := &fakeOrchestrator{task: taskB}
	execB := &issueExecutor{
		Provider: fp, Repo: repo, RepoConfig: rc, ProviderToken: "tok",
		LLM: llmFakeB, Sandbox: orchB, Cache: store,
	}

	if _, err := execB.Handle(context.Background(), issue); err != nil {
		t.Fatalf("Handle phase B: %v", err)
	}
	if verifyCalls != 1 {
		t.Fatalf("verifyCalls = %d, want 1 (re-verify, no agent call)", verifyCalls)
	}
	if pushCalls != 1 {
		t.Fatalf("pushCalls = %d, want 1", pushCalls)
	}
	if len(llmFakeB.Requests) != 0 {
		t.Fatalf("llm requests in phase B = %d, want 0 (agent should not run)", len(llmFakeB.Requests))
	}
	if fp.openPRCount != 1 {
		t.Fatalf("openPRCount = %d, want 1", fp.openPRCount)
	}

	final, loadErr := store.Load(repo.FullPath, issue.Number)
	if loadErr != nil {
		t.Fatalf("cache Load after success: %v", loadErr)
	}
	if final != nil {
		t.Fatal("cache should be cleared after a successful resume")
	}
}

// TestIssueExecutorHandleCachesOnPRFailureAndResumesWithoutSandbox covers the
// pr_pending stage: commit+push succeed but opening the PR fails. This used
// to be a permanent dead end (the branch-exists idempotency check would skip
// the issue forever on the next attempt); a resume should retry only the
// PR-open call, touching neither the sandbox nor the agent.
func TestIssueExecutorHandleCachesOnPRFailureAndResumesWithoutSandbox(t *testing.T) {
	store := &cache.FileStore{Dir: t.TempDir()}
	repo := provider.Repo{FullPath: "acme/widgets"}
	rc := &config.RepoConfig{
		Provider: config.ProviderGitHub,
		Repo:     "acme/widgets",
		LLM:      config.LLMConfig{Auth: "api_key", AgentModel: "claude-sonnet-5", Effort: "high"},
		Budget:   config.BudgetConfig{MaxTurns: 10},
		Verify:   config.VerifyConfig{Command: "go test ./..."},
	}
	issue := provider.Issue{Number: 44, Title: "Add widget", Body: "details"}
	fp := &fakeExecProvider{defaultBranch: "main", openPRErr: errors.New("provider unavailable")}

	llmFake := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
			Usage:      cost.Usage{InputTokens: 40, OutputTokens: 5},
		},
	}}
	task := &fakeTask{}
	task.BashFn = func(command string) (string, error) {
		switch {
		case command == "git ls-remote --heads origin 'wright/issue-44'":
			return "", nil
		case command == "go test ./...":
			return "ok", nil
		case command == "git checkout -b wright/issue-44":
			return "", nil
		case command == "git add -A":
			return "", nil
		case command == "git diff --cached --shortstat":
			return "1 file changed, 1 insertion(+)\n", nil
		case strings.HasPrefix(command, "git commit -m "):
			return "", nil
		case strings.HasPrefix(command, "git push "):
			return "", nil
		default:
			return "", nil
		}
	}
	orch := &fakeOrchestrator{task: task}
	exec := &issueExecutor{
		Provider: fp, Repo: repo, RepoConfig: rc, ProviderToken: "tok",
		LLM: llmFake, Sandbox: orch, Cache: store,
	}

	if _, err := exec.Handle(context.Background(), issue); err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("Handle phase A err = %v, want the PR-open error", err)
	}
	if fp.openPRCount != 1 {
		t.Fatalf("openPRCount = %d, want 1", fp.openPRCount)
	}

	entry, loadErr := store.Load(repo.FullPath, issue.Number)
	if loadErr != nil {
		t.Fatalf("cache Load: %v", loadErr)
	}
	if entry == nil || entry.Stage != cache.StagePRPending {
		t.Fatalf("entry = %+v, want a StagePRPending entry", entry)
	}
	if entry.Branch != "wright/issue-44" {
		t.Fatalf("entry.Branch = %q, want wright/issue-44", entry.Branch)
	}

	// Resume: the provider recovers. The orchestrator must not be started
	// again and the (empty) LLM fake must not be called.
	fp.openPRErr = nil
	if _, err := exec.Handle(context.Background(), issue); err != nil {
		t.Fatalf("Handle resume: %v", err)
	}
	if orch.starts != 1 {
		t.Fatalf("orchestrator starts = %d, want 1 (resume must not touch the sandbox)", orch.starts)
	}
	if len(llmFake.Requests) != 1 {
		t.Fatalf("llm requests = %d, want 1 (resume must not invoke the agent)", len(llmFake.Requests))
	}
	if fp.openPRCount != 2 {
		t.Fatalf("openPRCount = %d, want 2", fp.openPRCount)
	}

	final, loadErr := store.Load(repo.FullPath, issue.Number)
	if loadErr != nil {
		t.Fatalf("cache Load after resume: %v", loadErr)
	}
	if final != nil {
		t.Fatal("cache should be cleared after a successful PR-pending resume")
	}
}
