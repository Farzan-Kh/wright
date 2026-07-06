// Package openrouter implements an llm.LLMProvider backed by OpenRouter's
// OpenAI-compatible Chat Completions API (https://openrouter.ai/api/v1).
//
// OpenRouter uses only API-key auth. The OpenAI message and tool-call formats
// are translated to/from the provider-agnostic llm types so the rest of
// Wright (agent runner, gate) works without modification.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

// Config configures the OpenRouter adapter.
type Config struct {
	// APIKey is the OpenRouter API key (required).
	APIKey string
	// BaseURL overrides the default OpenRouter endpoint. Useful in tests.
	BaseURL string
	// HTTPClient overrides the HTTP client. Defaults to http.DefaultClient.
	HTTPClient *http.Client
}

// Provider implements llm.LLMProvider using OpenRouter's OpenAI-compatible
// /chat/completions endpoint.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

var _ llm.LLMProvider = (*Provider)(nil)

// New creates a Provider from cfg.
func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("openrouter: API key is required")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Provider{apiKey: cfg.APIKey, baseURL: baseURL, httpClient: hc}, nil
}

// CreateMessage calls the OpenRouter /chat/completions endpoint and returns a
// provider-agnostic response.
func (p *Provider) CreateMessage(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	chatReq := &requestBody{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Messages:  toOpenAIMessages(req.System, req.Messages),
	}

	for _, t := range req.Tools {
		tool, err := toOpenAITool(t)
		if err != nil {
			return llm.MessageResponse{}, err
		}
		chatReq.Tools = append(chatReq.Tools, tool)
	}
	if len(chatReq.Tools) > 0 {
		chatReq.ToolChoice = "auto"
	}

	// Pass reasoning effort for models that support it (e.g. DeepSeek R1, o1).
	// Models that do not support it will silently ignore the field.
	if req.ThinkingOn && req.ThinkEffort != "" {
		chatReq.Reasoning = &reasoning{Effort: req.ThinkEffort}
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return llm.MessageResponse{}, fmt.Errorf("openrouter: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return llm.MessageResponse{}, fmt.Errorf("openrouter: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return llm.MessageResponse{}, fmt.Errorf("openrouter: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var chatResp responseBody
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return llm.MessageResponse{}, fmt.Errorf("openrouter: decode response: %w", err)
	}

	// OpenRouter can return an error body with a 2xx status (e.g. upstream
	// provider or moderation errors), so surface a present error regardless of
	// the HTTP status code.
	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return llm.MessageResponse{}, fmt.Errorf("openrouter: %s", chatResp.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llm.MessageResponse{}, fmt.Errorf("openrouter: HTTP %d", resp.StatusCode)
	}

	return fromOpenAIResponse(chatResp)
}

// ── wire types ───────────────────────────────────────────────────────────────

type requestBody struct {
	Model      string           `json:"model"`
	Messages   []requestMessage `json:"messages"`
	MaxTokens  int              `json:"max_tokens,omitempty"`
	Tools      []tool           `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
	Reasoning  *reasoning       `json:"reasoning,omitempty"`
}

// requestMessage uses *string for Content so nil marshals as JSON null, which
// is required for assistant messages that contain only tool_calls.
type requestMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"` // always "function"
	Function funcCall `json:"function"`
}

type funcCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded object
}

type tool struct {
	Type     string   `json:"type"` // always "function"
	Function toolFunc `json:"function"`
}

type toolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type reasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responseBody struct {
	Choices []choice   `json:"choices"`
	Usage   usageBlock `json:"usage"`
	Error   *apiError  `json:"error,omitempty"`
}

type choice struct {
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type responseMessage struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []toolCall `json:"tool_calls"`
}

type usageBlock struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// UnmarshalJSON accepts the "error" field in either of the shapes OpenRouter and
// its upstream proxies emit: an object ({"message":...,"code":...}) or a bare
// string ("something went wrong"). The Code field also tolerates being a JSON
// string rather than a number. Without this, a string error body fails the whole
// response decode, masking the real upstream message.
func (e *apiError) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}

	// String form: {"error":"..."}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		e.Message = s
		return nil
	}

	// Object form. Decode into an alias with a flexible code so a string code
	// (e.g. "invalid_request_error") does not fail the decode.
	var obj struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return err
	}
	e.Message = obj.Message
	if len(obj.Code) > 0 && string(obj.Code) != "null" {
		// Best-effort: numeric codes populate Code; string codes are ignored.
		_ = json.Unmarshal(obj.Code, &e.Code)
	}
	return nil
}

// ── tool definitions ─────────────────────────────────────────────────────────

// toolSchemas maps Anthropic-style tool-type identifiers to their OpenAI
// function equivalents. Only the tool types used by Wright are listed.
var toolSchemas = map[string]tool{
	"bash_20250124": {
		Type: "function",
		Function: toolFunc{
			Name:        "bash",
			Description: "Execute a bash command in the sandbox environment.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to execute.",
					},
				},
				"required": []string{"command"},
			},
		},
	},
	"text_editor_20250728": {
		Type: "function",
		Function: toolFunc{
			Name:        "str_replace_based_edit_tool",
			Description: "View and edit files. command=view reads a file; command=create writes a new file; command=str_replace replaces a specific substring.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"enum":        []string{"view", "create", "str_replace"},
						"description": "Editor operation: view, create, or str_replace.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Absolute path to the target file.",
					},
					"file_text": map[string]any{
						"type":        "string",
						"description": "Full file content for command=create.",
					},
					"old_str": map[string]any{
						"type":        "string",
						"description": "Exact text to replace for command=str_replace.",
					},
					"new_str": map[string]any{
						"type":        "string",
						"description": "Replacement text for command=str_replace.",
					},
				},
				"required": []string{"command", "path"},
			},
		},
	},
	"repo_read_file": {
		Type: "function",
		Function: toolFunc{
			Name:        "repo_read_file",
			Description: "Read a file from the repository at its current default-branch state.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Repo-relative file path to read.",
					},
				},
				"required": []string{"path"},
			},
		},
	},
	"repo_list_dir": {
		Type: "function",
		Function: toolFunc{
			Name:        "repo_list_dir",
			Description: `List a directory in the repository at its current default-branch state. Directory entries end with "/".`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": `Repo-relative directory path to list ("" for the repo root).`,
					},
				},
				"required": []string{"path"},
			},
		},
	},
}

func toOpenAITool(t llm.ToolSpec) (tool, error) {
	def, ok := toolSchemas[t.Type]
	if !ok {
		return tool{}, fmt.Errorf("openrouter: unknown tool %q", t.Type)
	}
	return def, nil
}

// ── message conversion ────────────────────────────────────────────────────────

// toOpenAIMessages converts llm system blocks and Anthropic-style messages to
// the OpenAI chat format:
//   - System blocks are joined into a single "system" message.
//   - User messages whose content is entirely tool_result blocks are expanded
//     into one "tool" message per result (OpenAI requires separate messages).
//   - Assistant messages with tool_use blocks become messages with tool_calls.
func toOpenAIMessages(system []llm.SystemBlock, msgs []llm.Message) []requestMessage {
	var out []requestMessage

	if len(system) > 0 {
		var parts []string
		for _, s := range system {
			if t := strings.TrimSpace(s.Text); t != "" {
				parts = append(parts, t)
			}
		}
		if len(parts) > 0 {
			content := strings.Join(parts, "\n\n")
			out = append(out, requestMessage{Role: "system", Content: &content})
		}
	}

	for _, m := range msgs {
		switch strings.ToLower(m.Role) {
		case "user":
			out = append(out, convertUserMessage(m)...)
		case "assistant":
			out = append(out, convertAssistantMessage(m))
		}
	}
	return out
}

// convertUserMessage returns one or more OpenAI messages from a user turn.
// When all content blocks are tool_result, each becomes a "tool" role message.
func convertUserMessage(m llm.Message) []requestMessage {
	if len(m.Content) == 0 {
		empty := ""
		return []requestMessage{{Role: "user", Content: &empty}}
	}

	allToolResults := true
	for _, b := range m.Content {
		if b.Type != "tool_result" {
			allToolResults = false
			break
		}
	}

	if allToolResults {
		out := make([]requestMessage, 0, len(m.Content))
		for _, b := range m.Content {
			text := b.Text
			if b.IsError && text == "" {
				text = "error"
			}
			out = append(out, requestMessage{
				Role:       "tool",
				Content:    &text,
				ToolCallID: b.ToolUseID,
			})
		}
		return out
	}

	// Regular text message — concatenate all text blocks.
	var sb strings.Builder
	first := true
	for _, b := range m.Content {
		if b.Type == "text" {
			if !first {
				sb.WriteString("\n")
			}
			sb.WriteString(b.Text)
			first = false
		}
	}
	text := sb.String()
	return []requestMessage{{Role: "user", Content: &text}}
}

// convertAssistantMessage converts an assistant llm.Message into an OpenAI
// assistant message, translating tool_use content blocks to tool_calls.
func convertAssistantMessage(m llm.Message) requestMessage {
	msg := requestMessage{Role: "assistant"}
	var textParts []string

	for _, b := range m.Content {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "tool_use":
			args, _ := json.Marshal(b.Input)
			msg.ToolCalls = append(msg.ToolCalls, toolCall{
				ID:   b.ToolUseID,
				Type: "function",
				Function: funcCall{
					Name:      b.Name,
					Arguments: string(args),
				},
			})
		}
	}

	if len(textParts) > 0 {
		text := strings.Join(textParts, "\n")
		msg.Content = &text
	}
	// Content stays nil (→ JSON null) when there is no text alongside tool_calls.
	return msg
}

// ── response conversion ───────────────────────────────────────────────────────

// fromOpenAIResponse converts an OpenRouter response to llm.MessageResponse,
// mapping OpenAI finish_reason values to the Anthropic-style stop_reason strings
// the rest of Wright expects ("end_turn", "tool_use", "max_tokens").
func fromOpenAIResponse(r responseBody) (llm.MessageResponse, error) {
	if len(r.Choices) == 0 {
		return llm.MessageResponse{}, errors.New("openrouter: response has no choices")
	}

	ch := r.Choices[0]
	msg := ch.Message

	out := llm.MessageResponse{
		Message:    llm.Message{Role: "assistant"},
		StopReason: mapStopReason(ch.FinishReason),
		Usage: cost.Usage{
			InputTokens:  r.Usage.PromptTokens,
			OutputTokens: r.Usage.CompletionTokens,
		},
	}

	if msg.Content != nil && *msg.Content != "" {
		out.Message.Content = append(out.Message.Content,
			llm.ContentBlock{Type: "text", Text: *msg.Content})
	}

	for _, tc := range msg.ToolCalls {
		var input map[string]any
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				return llm.MessageResponse{}, fmt.Errorf("openrouter: parse tool arguments for %q: %w", tc.Function.Name, err)
			}
		}
		out.Message.Content = append(out.Message.Content, llm.ContentBlock{
			Type:      "tool_use",
			ToolUseID: tc.ID,
			Name:      tc.Function.Name,
			Input:     input,
		})
	}

	return out, nil
}

// mapStopReason translates OpenAI finish_reason values to the Anthropic-style
// stop_reason strings expected by the agent runner and gate.
func mapStopReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "stop", "":
		return "end_turn"
	case "length":
		return "max_tokens"
	default:
		return reason
	}
}
