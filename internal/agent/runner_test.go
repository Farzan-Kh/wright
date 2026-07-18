// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/sandbox"
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
		Cfg:  Config{Model: "claude-haiku-4-5", MaxTurns: 5},
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
		Cfg:  Config{Model: "claude-haiku-4-5", MaxTurns: 1},
	}
	result, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:   []llm.ToolSpec{{Type: "bash_20250124"}},
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if result.BudgetReason != "max_turns" {
		t.Fatalf("BudgetReason = %q, want max_turns", result.BudgetReason)
	}
}

func TestRunStopsAtMaxTotalTokens(t *testing.T) {
	// Each response uses 100 input + 100 output tokens = 200 total.
	// With MaxTotalTokens=250, the runner stops after two completed turns
	// (total=400) before making a third API call.
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
				Type: "tool_use", ToolUseID: "t1", Name: "bash",
				Input: map[string]any{"command": "echo ok"},
			}}},
			StopReason: "tool_use",
			Usage:      cost.Usage{InputTokens: 100, OutputTokens: 100},
		},
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
				Type: "tool_use", ToolUseID: "t2", Name: "bash",
				Input: map[string]any{"command": "echo ok"},
			}}},
			StopReason: "tool_use",
			Usage:      cost.Usage{InputTokens: 100, OutputTokens: 100},
		},
	}}
	r := &Runner{
		LLM:  fake,
		Exec: &sandbox.FakeExec{},
		Cfg:  Config{Model: "claude-haiku-4-5", MaxTotalTokens: 250},
	}
	result, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:   []llm.ToolSpec{{Type: "bash_20250124"}},
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if result.BudgetReason != "max_total_tokens" {
		t.Fatalf("BudgetReason = %q, want max_total_tokens", result.BudgetReason)
	}
}

func TestRunStopsAtMaxUSD(t *testing.T) {
	// Rates: $8/MTok input, $24/MTok output (Claude Sonnet 4.5 approximate).
	// Each response uses 100 input + 100 output tokens.
	// Per-turn cost: (100/1e6 * 8) + (100/1e6 * 24) = 0.0008 + 0.0024 = 0.0032 USD.
	// With MaxUSD=0.004 and MaxTurns=0 (unlimited), the runner stops after two
	// completed turns (cumulative 0.0064 USD) before making a third API call.
	rates := cost.RateTable{
		"claude-haiku-4-5": {InputPerMTok: 8, OutputPerMTok: 24},
	}
	fake := &llm.FakeProvider{Responses: []llm.MessageResponse{
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
				Type: "tool_use", ToolUseID: "t1", Name: "bash",
				Input: map[string]any{"command": "echo ok"},
			}}},
			StopReason: "tool_use",
			Usage:      cost.Usage{InputTokens: 100, OutputTokens: 100},
		},
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{
				Type: "tool_use", ToolUseID: "t2", Name: "bash",
				Input: map[string]any{"command": "echo ok"},
			}}},
			StopReason: "tool_use",
			Usage:      cost.Usage{InputTokens: 100, OutputTokens: 100},
		},
	}}
	r := &Runner{
		LLM:  fake,
		Exec: &sandbox.FakeExec{},
		Cfg:  Config{Model: "claude-haiku-4-5", MaxUSD: 0.004, Rates: rates},
	}
	result, err := r.Run(context.Background(), RunRequest{
		History: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:   []llm.ToolSpec{{Type: "bash_20250124"}},
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if result.BudgetReason != "max_usd" {
		t.Fatalf("BudgetReason = %q, want max_usd", result.BudgetReason)
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
		Cfg:  Config{Model: "claude-haiku-4-5", MaxTurns: 5},
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
