// SPDX-License-Identifier: Apache-2.0

package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/provider"
)

func TestCheckReady(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type: "text", Text: `{"ready":true,"missing":""}`,
		}}},
	}}}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256}
	v, err := g.Check(context.Background(), provider.Issue{Title: "Fix bug", Body: "Steps"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !v.Ready || v.Missing != "" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestCheckNotReady(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type: "text", Text: `{"ready":false,"missing":"acceptance criteria"}`,
		}}},
	}}}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256}
	v, err := g.Check(context.Background(), provider.Issue{Title: "Fix bug", Body: ""})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if v.Ready || v.Missing == "" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestCheckToleratesFencedJSON(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type: "text", Text: "Here is my verdict:\n```json\n{\"ready\":false,\"missing\":\"repro steps\"}\n```",
		}}},
	}}}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256}
	v, err := g.Check(context.Background(), provider.Issue{Title: "Bug", Body: ""})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if v.Ready || v.Missing != "repro steps" {
		t.Fatalf("verdict = %+v, want not-ready with missing=repro steps", v)
	}
}

func TestCheckWithUsage(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type: "text", Text: `{"ready":true,"missing":""}`,
		}}},
		Usage: cost.Usage{InputTokens: 123, OutputTokens: 45},
	}}}

	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256}
	v, summary, err := g.CheckWithUsage(context.Background(), provider.Issue{Title: "Fix bug", Body: ""})
	if err != nil {
		t.Fatalf("CheckWithUsage: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}
	if summary.Turns != 1 {
		t.Fatalf("turns = %d, want 1", summary.Turns)
	}
	if summary.Usage.InputTokens != 123 || summary.Usage.OutputTokens != 45 {
		t.Fatalf("usage = %+v, want input=123 output=45", summary.Usage)
	}
	if summary.USDKnown {
		t.Fatalf("USDKnown should be false with nil Rates")
	}
}

// TestCheckWithUsageWithRates verifies that the gate returns a Summary with
// populated USD when Rates is set.
func TestCheckWithUsageWithRates(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type: "text", Text: `{"ready":true,"missing":""}`,
		}}},
		Usage: cost.Usage{InputTokens: 1_000_000, OutputTokens: 500_000},
	}}}

	g := &Gate{
		LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256,
		Rates: cost.RateTable{
			"claude-haiku-4-5": {InputPerMTok: 0.80, OutputPerMTok: 4.00},
		},
	}
	v, summary, err := g.CheckWithUsage(context.Background(), provider.Issue{Title: "Fix bug", Body: ""})
	if err != nil {
		t.Fatalf("CheckWithUsage: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}
	if !summary.USDKnown {
		t.Fatalf("USDKnown should be true when Rates is set")
	}
	// 1M input @ $0.80/MTok = $0.80, 500k output @ $4.00/MTok = $2.00, total = $2.80
	if summary.USD != 2.80 {
		t.Fatalf("USD = %f, want 2.80", summary.USD)
	}
}

// fakeRepoProvider is a minimal provider.Provider for the gate's live-state
// grounding tests: only GetIssue, ReadRepoFile, ListRepoDir, and DefaultBranch
// carry real behavior, everything else is an unused no-op.
type fakeRepoProvider struct {
	issues           map[int]provider.Issue
	files            map[string]string   // "ref:path" -> content
	dirs             map[string][]string // "ref:path" -> entries
	defaultBranch    string
	defaultBranchErr error
	openPRs          map[string]*provider.PullRequest // head branch -> open PR
}

var _ provider.Provider = (*fakeRepoProvider)(nil)

func (f *fakeRepoProvider) Name() string { return "fake" }
func (f *fakeRepoProvider) ListLabeledIssues(context.Context, provider.Repo, string) ([]provider.Issue, error) {
	return nil, nil
}
func (f *fakeRepoProvider) GetIssue(_ context.Context, _ provider.Repo, number int) (*provider.Issue, error) {
	iss, ok := f.issues[number]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return &iss, nil
}
func (f *fakeRepoProvider) ReadRepoFile(_ context.Context, _ provider.Repo, ref, path string) (string, error) {
	c, ok := f.files[ref+":"+path]
	if !ok {
		return "", provider.ErrNotFound
	}
	return c, nil
}
func (f *fakeRepoProvider) ListRepoDir(_ context.Context, _ provider.Repo, ref, path string) ([]string, error) {
	e, ok := f.dirs[ref+":"+path]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return e, nil
}
func (f *fakeRepoProvider) CommentOnIssue(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeRepoProvider) AddIssueLabel(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeRepoProvider) RemoveIssueLabel(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeRepoProvider) CommentOnPullRequest(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeRepoProvider) DefaultBranch(context.Context, provider.Repo) (string, error) {
	if f.defaultBranchErr != nil {
		return "", f.defaultBranchErr
	}
	if f.defaultBranch == "" {
		return "main", nil
	}
	return f.defaultBranch, nil
}
func (f *fakeRepoProvider) CreateBranch(context.Context, provider.Repo, string, string) error {
	return nil
}
func (f *fakeRepoProvider) DeleteBranch(context.Context, provider.Repo, string) error { return nil }
func (f *fakeRepoProvider) PushCommits(context.Context, provider.Repo, string, []provider.Commit) (string, error) {
	return "", nil
}
func (f *fakeRepoProvider) FindOpenPullRequestByHead(_ context.Context, _ provider.Repo, headBranch string) (*provider.PullRequest, error) {
	return f.openPRs[headBranch], nil
}
func (f *fakeRepoProvider) OpenPullRequest(context.Context, provider.Repo, provider.PullRequestSpec) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeRepoProvider) GetPullRequest(context.Context, provider.Repo, int) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeRepoProvider) UpdatePullRequestBase(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeRepoProvider) MergePullRequest(context.Context, provider.Repo, int, provider.MergeOptions) error {
	return nil
}
func (f *fakeRepoProvider) ClosePullRequest(context.Context, provider.Repo, int) error { return nil }

func readyResponse() llm.MessageResponse {
	return llm.MessageResponse{Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
		Type: "text", Text: `{"ready":true,"missing":""}`,
	}}}}
}

func TestReferenceResolutionInjectsLiveStatus(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{readyResponse()}}
	p := &fakeRepoProvider{issues: map[int]provider.Issue{
		12: {Number: 12, Title: "Add ADR 007", State: "closed"},
		14: {Number: 14, Title: "Wire up ADR 007", State: "open"},
	}}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256, Provider: p, Repo: provider.Repo{FullPath: "acme/widgets"}}

	v, err := g.Check(context.Background(), provider.Issue{Number: 20, Title: "Implement X", Body: "Blocked by #12 and #14."})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}

	if len(f.Requests) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(f.Requests))
	}
	prompt := requestText(f.Requests[0])
	if !strings.Contains(prompt, "#12 (closed): Add ADR 007") {
		t.Errorf("prompt missing resolved #12 status:\n%s", prompt)
	}
	if !strings.Contains(prompt, "#14 (open): Wire up ADR 007") {
		t.Errorf("prompt missing resolved #14 status:\n%s", prompt)
	}
}

// TestReferenceResolutionNotesStackableOpenPR is the regression test for the
// stacked-PR feature's gate-side half: an open referenced issue with an
// already-open Wright PR must be surfaced to the triage model as a workable
// (stackable) dependency, not silently indistinguishable from a plain open
// blocker.
func TestReferenceResolutionNotesStackableOpenPR(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{readyResponse()}}
	p := &fakeRepoProvider{
		issues: map[int]provider.Issue{
			13: {Number: 13, Title: "Add config parsing", State: "open"},
		},
		openPRs: map[string]*provider.PullRequest{
			"wright/issue-13": {Number: 45, URL: "https://example.com/pr/45", HeadBranch: "wright/issue-13", State: "open"},
		},
	}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256, Provider: p, Repo: provider.Repo{FullPath: "acme/widgets"}}

	v, err := g.Check(context.Background(), provider.Issue{Number: 14, Title: "Use config", Body: "Requires #13."})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}

	prompt := requestText(f.Requests[0])
	if !strings.Contains(prompt, "#13 (open): Add config parsing, open Wright PR #45: https://example.com/pr/45") {
		t.Errorf("prompt missing stackable-PR annotation for #13:\n%s", prompt)
	}
}

func TestToolUseRoundTrip(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			StopReason: "tool_use",
			Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
				Type: "tool_use", ToolUseID: "t1", Name: "repo_read_file",
				Input: map[string]any{"path": "docs/adr/007.md"},
			}}},
		},
		readyResponse(),
	}}
	p := &fakeRepoProvider{files: map[string]string{"main:docs/adr/007.md": "# ADR 007\naccepted\n"}}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256, Provider: p, Repo: provider.Repo{FullPath: "acme/widgets"}}

	v, err := g.Check(context.Background(), provider.Issue{Title: "Implement per ADR 007", Body: "See docs/adr/007.md"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}
	if len(f.Requests) != 2 {
		t.Fatalf("LLM calls = %d, want 2", len(f.Requests))
	}

	// The second call must carry the tool result back so the model can see the
	// file content it asked for.
	last := f.Requests[1].Messages[len(f.Requests[1].Messages)-1]
	if len(last.Content) != 1 || last.Content[0].Type != "tool_result" || last.Content[0].ToolUseID != "t1" {
		t.Fatalf("tool_result message = %+v", last)
	}
	if !strings.Contains(last.Content[0].Text, "# ADR 007") {
		t.Errorf("tool_result content = %q, want file content", last.Content[0].Text)
	}
}

func TestMaxToolTurnsCutoff(t *testing.T) {
	toolUseResponse := llm.MessageResponse{
		StopReason: "tool_use",
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type: "tool_use", ToolUseID: "t1", Name: "repo_list_dir", Input: map[string]any{"path": ""},
		}}},
	}
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{toolUseResponse, toolUseResponse, readyResponse()}}
	p := &fakeRepoProvider{dirs: map[string][]string{"main:": {"docs/", "go.mod"}}}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256, Provider: p, Repo: provider.Repo{FullPath: "acme/widgets"}, MaxToolTurns: 2}

	v, err := g.Check(context.Background(), provider.Issue{Title: "Fix", Body: "Body"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}
	if len(f.Requests) != 3 {
		t.Fatalf("LLM calls = %d, want 3 (2 tool turns + 1 final)", len(f.Requests))
	}
	if len(f.Requests[2].Tools) != 0 {
		t.Errorf("final request Tools = %+v, want none (tool budget withheld)", f.Requests[2].Tools)
	}
}

func TestCheckWithToolsFallsBackWhenDefaultBranchFails(t *testing.T) {
	f := &llm.FakeProvider{Responses: []llm.MessageResponse{readyResponse()}}
	p := &fakeRepoProvider{defaultBranchErr: errors.New("boom")}
	g := &Gate{LLM: f, Model: "claude-haiku-4-5", MaxTokens: 256, Provider: p, Repo: provider.Repo{FullPath: "acme/widgets"}}

	v, err := g.Check(context.Background(), provider.Issue{Title: "Fix", Body: "Body"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}
	if len(f.Requests) != 1 {
		t.Fatalf("LLM calls = %d, want 1 (plain fallback, no tool loop)", len(f.Requests))
	}
}

// requestText concatenates every text content block across req's messages.
func requestText(req llm.MessageRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		for _, c := range m.Content {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}
