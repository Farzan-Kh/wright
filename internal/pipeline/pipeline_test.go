package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/farzan-kh/patchr/internal/agent/llm"
	"github.com/farzan-kh/patchr/internal/cost"
	"github.com/farzan-kh/patchr/internal/gate"
	"github.com/farzan-kh/patchr/internal/poller"
	"github.com/farzan-kh/patchr/internal/provider"
)

type fakeProvider struct {
	issues        []provider.Issue
	comments      []string
	removedLabels []string
	addedLabels   []string
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) ListLabeledIssues(_ context.Context, _ provider.Repo, _ string) ([]provider.Issue, error) {
	return append([]provider.Issue(nil), f.issues...), nil
}

func (f *fakeProvider) CommentOnIssue(_ context.Context, _ provider.Repo, issueNumber int, body string) error {
	f.comments = append(f.comments, fmt.Sprintf("%d:%s", issueNumber, body))
	return nil
}

func (f *fakeProvider) AddIssueLabel(_ context.Context, _ provider.Repo, issueNumber int, label string) error {
	f.addedLabels = append(f.addedLabels, fmt.Sprintf("%d:%s", issueNumber, label))
	return nil
}

func (f *fakeProvider) RemoveIssueLabel(_ context.Context, _ provider.Repo, issueNumber int, label string) error {
	f.removedLabels = append(f.removedLabels, fmt.Sprintf("%d:%s", issueNumber, label))
	return nil
}

func (f *fakeProvider) CommentOnPullRequest(context.Context, provider.Repo, int, string) error {
	return nil
}
func (f *fakeProvider) DefaultBranch(context.Context, provider.Repo) (string, error) {
	return "main", nil
}
func (f *fakeProvider) CreateBranch(context.Context, provider.Repo, string, string) error {
	return nil
}
func (f *fakeProvider) DeleteBranch(context.Context, provider.Repo, string) error { return nil }
func (f *fakeProvider) PushCommits(context.Context, provider.Repo, string, []provider.Commit) (string, error) {
	return "", nil
}
func (f *fakeProvider) FindOpenPullRequestByHead(context.Context, provider.Repo, string) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeProvider) OpenPullRequest(context.Context, provider.Repo, provider.PullRequestSpec) (*provider.PullRequest, error) {
	return nil, nil
}
func (f *fakeProvider) MergePullRequest(context.Context, provider.Repo, int, provider.MergeOptions) error {
	return nil
}
func (f *fakeProvider) ClosePullRequest(context.Context, provider.Repo, int) error { return nil }

func TestRunOnceNeedsInfoIncludesGateCost(t *testing.T) {
	fp := &fakeProvider{issues: []provider.Issue{{Number: 101, Title: "Need detail", Body: ""}}}
	gLLM := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: `{"ready":false,"missing":"repro steps"}`}}},
		Usage:   cost.Usage{InputTokens: 1_000_000},
	}}}

	pl := &Pipeline{
		Provider:        fp,
		Repo:            provider.Repo{FullPath: "acme/widgets"},
		TriggerLabel:    "patchr",
		NeedsHumanLabel: "needs-human",
		USDApplicable:   true,
		Poller:          &poller.Poller{Provider: fp, Repo: provider.Repo{FullPath: "acme/widgets"}, Label: "patchr"},
		Gate:            &gate.Gate{LLM: gLLM, Model: "claude-haiku-4-5", MaxTokens: 256},
	}

	reports, err := pl.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	r := reports[0]
	if r.Status != "needs-info" {
		t.Fatalf("status = %q, want needs-info", r.Status)
	}
	if r.Cost.Turns != 1 || r.Cost.Usage.InputTokens != 1_000_000 {
		t.Fatalf("cost = %+v", r.Cost)
	}
	if r.Cost.USD != 1.0 {
		t.Fatalf("USD = %v, want 1.0", r.Cost.USD)
	}
	if len(fp.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(fp.comments))
	}
	if len(fp.removedLabels) != 1 || fp.removedLabels[0] != "101:patchr" {
		t.Fatalf("removedLabels = %v, want [101:patchr]", fp.removedLabels)
	}
}

func TestRunOnceCompletedMergesGateAndExecutionCost(t *testing.T) {
	fp := &fakeProvider{issues: []provider.Issue{{Number: 7, Title: "Fix", Body: "details"}}}
	gLLM := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: `{"ready":true,"missing":""}`}}},
		Usage:   cost.Usage{InputTokens: 10, OutputTokens: 5},
	}}}

	pl := &Pipeline{
		Provider:      fp,
		Repo:          provider.Repo{FullPath: "acme/widgets"},
		TriggerLabel:  "patchr",
		USDApplicable: true,
		Poller:        &poller.Poller{Provider: fp, Repo: provider.Repo{FullPath: "acme/widgets"}, Label: "patchr"},
		Gate:          &gate.Gate{LLM: gLLM, Model: "claude-haiku-4-5", MaxTokens: 256},
		OnReady: func(_ context.Context, _ provider.Issue) (cost.Summary, error) {
			return cost.Summary{
				Turns:         2,
				Usage:         cost.Usage{InputTokens: 20, OutputTokens: 30},
				USD:           0.25,
				USDApplicable: true,
			}, nil
		},
	}

	reports, err := pl.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	r := reports[0]
	if r.Status != "completed" {
		t.Fatalf("status = %q, want completed", r.Status)
	}
	if r.Cost.Turns != 3 {
		t.Fatalf("turns = %d, want 3", r.Cost.Turns)
	}
	if r.Cost.Usage.InputTokens != 30 || r.Cost.Usage.OutputTokens != 35 {
		t.Fatalf("usage = %+v, want in=30 out=35", r.Cost.Usage)
	}
}

func TestRunOnceNeedsHumanAddsLabel(t *testing.T) {
	fp := &fakeProvider{issues: []provider.Issue{{Number: 42, Title: "Fix", Body: "details"}}}
	gLLM := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: `{"ready":true,"missing":""}`}}},
		Usage:   cost.Usage{InputTokens: 10, OutputTokens: 5},
	}}}

	pl := &Pipeline{
		Provider:        fp,
		Repo:            provider.Repo{FullPath: "acme/widgets"},
		TriggerLabel:    "patchr",
		NeedsHumanLabel: "needs-human",
		USDApplicable:   true,
		Poller:          &poller.Poller{Provider: fp, Repo: provider.Repo{FullPath: "acme/widgets"}, Label: "patchr"},
		Gate:            &gate.Gate{LLM: gLLM, Model: "claude-haiku-4-5", MaxTokens: 256},
		OnReady: func(_ context.Context, _ provider.Issue) (cost.Summary, error) {
			return cost.Summary{Turns: 2, Usage: cost.Usage{InputTokens: 100}, USDApplicable: true}, errors.New("verify failed")
		},
	}

	reports, err := pl.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	r := reports[0]
	if r.Status != "needs-human" {
		t.Fatalf("status = %q, want needs-human", r.Status)
	}
	if r.Cost.Turns != 3 {
		t.Fatalf("turns = %d, want 3", r.Cost.Turns)
	}
	if len(fp.removedLabels) != 1 || fp.removedLabels[0] != "42:patchr" {
		t.Fatalf("removedLabels = %v", fp.removedLabels)
	}
	if len(fp.addedLabels) != 1 || fp.addedLabels[0] != "42:needs-human" {
		t.Fatalf("addedLabels = %v", fp.addedLabels)
	}
	if len(fp.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(fp.comments))
	}
}
