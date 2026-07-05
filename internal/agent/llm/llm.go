// Package llm defines Wright's model-agnostic LLM contract.
package llm

import (
	"context"

	"github.com/farzan-kh/wright/internal/cost"
)

// LLMProvider is the model provider abstraction used by the agent and gate.
type LLMProvider interface {
	CreateMessage(ctx context.Context, req MessageRequest) (MessageResponse, error)
}

// MessageRequest is one call to an LLM chat/messages API.
type MessageRequest struct {
	Model       string
	MaxTokens   int
	System      []SystemBlock
	Messages    []Message
	Tools       []ToolSpec
	ThinkingOn  bool
	ThinkEffort string
}

// SystemBlock is one system prompt block.
type SystemBlock struct {
	Text        string
	CachePrompt bool
}

// ToolSpec identifies a tool definition available to the model.
type ToolSpec struct {
	Type string // e.g. bash_20250124, text_editor_20250728
}

// Message is one role/content turn.
type Message struct {
	Role    string // "user" or "assistant"
	Content []ContentBlock
}

// ContentBlock is a provider-agnostic content block.
type ContentBlock struct {
	Type string // text | thinking | redacted_thinking | tool_use | tool_result

	// text/tool_result payload; also the thinking text for a thinking block.
	Text string

	// Signature accompanies a thinking block. It must be preserved and sent back
	// unmodified: when extended thinking is enabled, the API requires the signed
	// thinking block that precedes a tool_use to be replayed with the tool_result.
	Signature string
	// Data carries a redacted_thinking block's opaque payload, replayed verbatim.
	Data string

	// tool_use/tool_result linkage
	ToolUseID string
	Name      string
	Input     map[string]any
	IsError   bool
}

// MessageResponse is one model response.
type MessageResponse struct {
	Message    Message
	StopReason string
	Usage      cost.Usage
}
