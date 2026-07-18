// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/sandbox"
)

var ErrTurnLimit = errors.New("agent: max turns reached")

// Config configures one bounded agent run.
type Config struct {
	Model       string
	MaxTokens   int
	MaxTurns    int
	// MaxTotalTokens caps the total LLM tokens (input + output + cache)
	// across all turns. 0 = unlimited.
	MaxTotalTokens int64
	// MaxUSD caps the total USD cost across all turns. 0 = not enforced.
	// When > 0, RateTable must have entries for Cfg.Model.
	MaxUSD float64
	// RateTable maps model id -> per-model pricing for USD tracking.
	// Nil disables USD tracking.
	RateTable   cost.RateTable
	ThinkEffort string
}

// Runner is the hand-written tool-use loop.
type Runner struct {
	LLM  llm.LLMProvider
	Exec sandbox.ToolExec
	Cfg  Config
}

// RunRequest carries the conversation setup.
type RunRequest struct {
	System  []llm.SystemBlock
	History []llm.Message
	Tools   []llm.ToolSpec
}

// RunResult captures the end state.
type RunResult struct {
	FinalMessage    llm.Message
	History         []llm.Message
	StopReason      string
	BudgetExceeded  bool
	BudgetReason    string
	UsageAndCost    cost.Summary
	ToolResultCount int
}

// Run executes the manual loop until end_turn, a hard limit, or an error.
func (r *Runner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	acc := cost.NewAccumulator(r.Cfg.RateTable)
	history := append([]llm.Message(nil), req.History...)
	result := RunResult{History: history}

	for {
		s := acc.Summary()
		if r.Cfg.MaxTurns > 0 && s.Turns >= r.Cfg.MaxTurns {
			result.BudgetExceeded = true
			result.BudgetReason = "max_turns"
			result.UsageAndCost = s
			return result, ErrTurnLimit
		}
		if r.Cfg.MaxTotalTokens > 0 {
			totalTokens := s.Usage.InputTokens + s.Usage.OutputTokens +
				s.Usage.CacheCreationInputTokens + s.Usage.CacheReadInputTokens
			if totalTokens >= r.Cfg.MaxTotalTokens {
				result.BudgetExceeded = true
				result.BudgetReason = "max_total_tokens"
				result.UsageAndCost = s
				return result, fmt.Errorf("agent: max total tokens reached (%d >= %d)", totalTokens, r.Cfg.MaxTotalTokens)
			}
		}
		if r.Cfg.MaxUSD > 0 && s.USDKnown && s.USD >= r.Cfg.MaxUSD {
			result.BudgetExceeded = true
			result.BudgetReason = "max_usd"
			result.UsageAndCost = s
			return result, fmt.Errorf("agent: max USD reached ($%.4f >= $%.4f)", s.USD, r.Cfg.MaxUSD)
		}

		effort := r.Cfg.ThinkEffort
		if strings.TrimSpace(effort) == "" {
			effort = "high"
		}
		resp, err := r.LLM.CreateMessage(ctx, llm.MessageRequest{
			Model:       r.Cfg.Model,
			MaxTokens:   r.Cfg.MaxTokens,
			System:      req.System,
			Messages:    history,
			Tools:       req.Tools,
			ThinkingOn:  true,
			ThinkEffort: effort,
		})
		if err != nil {
			return result, err
		}

		acc.Add(r.Cfg.Model, resp.Usage)
		s = acc.Summary()

		// Record the turn before any budget decision: the model's output (and its
		// signed thinking blocks) belongs in history regardless of cost, and a turn
		// that already completed must not be reported as a budget failure.
		history = append(history, resp.Message)
		result.History = history
		result.FinalMessage = resp.Message
		result.StopReason = resp.StopReason
		result.UsageAndCost = s

		if resp.StopReason != "tool_use" {
			return result, nil
		}

		toolResults := make([]llm.ContentBlock, 0)
		for _, b := range resp.Message.Content {
			if b.Type != "tool_use" {
				continue
			}
			out, err := r.execTool(ctx, b)
			tr := llm.ContentBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Text: out}
			if err != nil {
				tr.IsError = true
				if out == "" {
					tr.Text = err.Error()
				}
			}
			toolResults = append(toolResults, tr)
		}
		if len(toolResults) == 0 {
			return result, fmt.Errorf("agent: stop_reason=tool_use but no tool_use blocks were returned")
		}
		result.ToolResultCount += len(toolResults)
		history = append(history, llm.Message{Role: "user", Content: toolResults})
		result.History = history
	}
}

func (r *Runner) execTool(ctx context.Context, b llm.ContentBlock) (string, error) {
	switch b.Name {
	case "bash":
		cmd, _ := b.Input["command"].(string)
		if strings.TrimSpace(cmd) == "" {
			return "", errors.New("bash tool: missing command")
		}
		return r.Exec.Bash(ctx, cmd)
	case "str_replace_based_edit_tool":
		return r.execTextEditor(ctx, b.Input)
	default:
		return "", fmt.Errorf("unknown tool %q", b.Name)
	}
}

func (r *Runner) execTextEditor(ctx context.Context, input map[string]any) (string, error) {
	cmd, _ := input["command"].(string)
	path, _ := input["path"].(string)

	switch cmd {
	case "view":
		if path == "" {
			return "", errors.New("text editor: missing path")
		}
		return r.Exec.ReadFile(ctx, path)
	case "create":
		text, _ := input["file_text"].(string)
		if text == "" {
			text, _ = input["content"].(string)
		}
		if path == "" {
			return "", errors.New("text editor: missing path")
		}
		if err := r.Exec.WriteFile(ctx, path, text); err != nil {
			return "", err
		}
		return "ok", nil
	case "str_replace":
		oldText, _ := input["old_str"].(string)
		newText, _ := input["new_str"].(string)
		if oldText == "" {
			oldText, _ = input["old_text"].(string)
		}
		if newText == "" {
			newText, _ = input["new_text"].(string)
		}
		if path == "" {
			return "", errors.New("text editor: missing path")
		}
		if err := r.Exec.ReplaceText(ctx, path, oldText, newText); err != nil {
			return "", err
		}
		return "ok", nil
	default:
		return "", fmt.Errorf("unsupported text editor command %q", cmd)
	}
}
