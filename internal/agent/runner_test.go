package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/farzan-kh/patchr/internal/agent/llm"
	"github.com/farzan-kh/patchr/internal/cost"
	"github.com/farzan-kh/patchr/internal/sandbox"
)

func TestRunStopsOnEndTurn(t *testing.T) {
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage:      cost.Usage{InputTokens: 10, OutputTokens: 20},
	}}}
	r := &Runner{
		LLM:  fake,
		Exec: &sandbox.FakeExec{},
		Cfg:  Config{Model: "claude-haiku-4-5", MaxTurns: 5, USDApplicable: true},
	}
	got, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", got.StopReason)
	}
	if got.UsageAndCost.Turns != 1 {
		t.Fatalf("turns = %d, want 1", got.UsageAndCost.Turns)
	}
}

func TestRunStopsAtTurnCap(t *testing.T) {
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type:      "tool_use",
			ToolUseID: "toolu_1",
			Name:      "bash",
			Input:     map[string]any{"command": "echo ok"},
		}}},
		StopReason: "tool_use",
		Usage:      cost.Usage{InputTokens: 1, OutputTokens: 1},
	}}}
	r := &Runner{
		LLM:  fake,
		Exec: &sandbox.FakeExec{},
		Cfg:  Config{Model: "claude-haiku-4-5", MaxTurns: 1, USDApplicable: true},
	}
	_, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:   []llm.ToolSpec{{Type: "bash_20250124"}},
	})
	if !errors.Is(err, ErrTurnLimit) {
		t.Fatalf("err = %v, want ErrTurnLimit", err)
	}
}

// toolUseResp is an assistant turn that wants to run a tool, so the runner will
// try to continue — the point at which the USD ceiling is enforced.
func toolUseResp(u cost.Usage) llm.MessageResponse {
	return llm.MessageResponse{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
			Type: "tool_use", ToolUseID: "t1", Name: "bash", Input: map[string]any{"command": "ls"},
		}}},
		StopReason: "tool_use",
		Usage:      u,
	}
}

func TestRunStopsAtUSDCap(t *testing.T) {
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{
		toolUseResp(cost.Usage{InputTokens: 1_000_000}), // $2.00, over the cap
	}}
	r := &Runner{
		LLM:  fake,
		Exec: &sandbox.FakeExec{},
		Cfg:  Config{Model: "claude-sonnet-5", MaxTurns: 5, MaxUSD: 0.5, USDApplicable: true},
	}
	_, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if !errors.Is(err, ErrUSDLimit) {
		t.Fatalf("err = %v, want ErrUSDLimit", err)
	}
}

func TestRunStopsAtUSDCapOnExactBoundary(t *testing.T) {
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{
		toolUseResp(cost.Usage{InputTokens: 1_000_000}), // sonnet-5 input: exactly $2.00 (intro)
	}}
	r := &Runner{
		LLM:  fake,
		Exec: &sandbox.FakeExec{},
		Cfg:  Config{Model: "claude-sonnet-5", MaxTurns: 5, MaxUSD: 2.0, USDApplicable: true},
	}
	_, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if !errors.Is(err, ErrUSDLimit) {
		t.Fatalf("err = %v, want ErrUSDLimit at exact budget boundary", err)
	}
}

// A turn that completes (end_turn) is a success even if it pushes usage over the
// budget — the ceiling only prevents spending on a *further* turn. The finishing
// message must also survive in the result.
func TestRunCompletesWhenFinalTurnCrossesUSD(t *testing.T) {
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{{
		Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage:      cost.Usage{InputTokens: 1_000_000}, // $2.00, over the $1.00 cap
	}}}
	r := &Runner{
		LLM:  fake,
		Exec: &sandbox.FakeExec{},
		Cfg:  Config{Model: "claude-sonnet-5", MaxTurns: 5, MaxUSD: 1.0, USDApplicable: true},
	}
	res, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil (completed turn is a success)", err)
	}
	if res.BudgetExceeded {
		t.Fatalf("BudgetExceeded = true, want false for a completed turn")
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", res.StopReason)
	}
	if len(res.FinalMessage.Content) != 1 || res.FinalMessage.Content[0].Text != "done" {
		t.Fatalf("final message not preserved: %+v", res.FinalMessage)
	}
}

func TestRunRoundTripsToolResults(t *testing.T) {
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
				Type:      "tool_use",
				ToolUseID: "toolu_1",
				Name:      "bash",
				Input:     map[string]any{"command": "echo ok"},
			}}},
			StopReason: "tool_use",
			Usage:      cost.Usage{InputTokens: 1, OutputTokens: 1},
		},
		{
			Message:    llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
			Usage:      cost.Usage{InputTokens: 1, OutputTokens: 1},
		},
	}}
	exec := &sandbox.FakeExec{BashFn: func(command string) (string, error) {
		if command != "echo ok" {
			t.Fatalf("command = %q, want echo ok", command)
		}
		return "ok\n", nil
	}}
	r := &Runner{
		LLM:  fake,
		Exec: exec,
		Cfg:  Config{Model: "claude-haiku-4-5", MaxTurns: 5, USDApplicable: true},
	}
	got, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:   []llm.ToolSpec{{Type: "bash_20250124"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.ToolResultCount != 1 {
		t.Fatalf("ToolResultCount = %d, want 1", got.ToolResultCount)
	}

	if len(fake.Requests) < 2 {
		t.Fatalf("expected 2 llm requests, got %d", len(fake.Requests))
	}
	msgs := fake.Requests[1].Messages
	if len(msgs) == 0 {
		t.Fatal("second request has no messages")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || len(last.Content) != 1 || last.Content[0].Type != "tool_result" {
		t.Fatalf("last message = %+v, want user tool_result", last)
	}
	if last.Content[0].Text != "ok\n" {
		t.Fatalf("tool_result text = %q, want ok\\n", last.Content[0].Text)
	}
}
