package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farzan-kh/patchr/internal/agent/llm"
)

// writeChatResponse writes a minimal non-tool-use completion response.
func writeChatResponse(w http.ResponseWriter, text, finishReason string, promptTokens, completionTokens int) {
	w.Header().Set("Content-Type", "application/json")
	resp := responseBody{
		Choices: []choice{{
			Message:      responseMessage{Role: "assistant", Content: &text},
			FinishReason: finishReason,
		}},
		Usage: usageBlock{
			PromptTokens:     int64(promptTokens),
			CompletionTokens: int64(completionTokens),
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("New with empty APIKey: expected error")
	}
}

func TestNewDefaultBaseURL(t *testing.T) {
	p, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.baseURL != defaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", p.baseURL, defaultBaseURL)
	}
}

func TestCreateMessageAPIKeyHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		writeChatResponse(w, "Hello!", "stop", 10, 5)
	}))
	defer srv.Close()

	p, err := New(Config{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "openai/gpt-4o",
		MaxTokens: 128,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "Hello!" {
		t.Fatalf("content = %+v", resp.Message.Content)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestCreateMessageSystemPrompt(t *testing.T) {
	var gotMessages []requestMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody requestBody
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		gotMessages = reqBody.Messages
		writeChatResponse(w, "ok", "stop", 1, 1)
	}))
	defer srv.Close()

	p, _ := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "openai/gpt-4o",
		MaxTokens: 64,
		System: []llm.SystemBlock{
			{Text: "You are helpful."},
			{Text: "Be concise."},
		},
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	// Expect: system + user = 2 messages.
	if len(gotMessages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(gotMessages), gotMessages)
	}
	if gotMessages[0].Role != "system" {
		t.Fatalf("msg[0].role = %q, want system", gotMessages[0].Role)
	}
	if gotMessages[0].Content == nil || !strings.Contains(*gotMessages[0].Content, "You are helpful") {
		t.Fatalf("system content = %v", gotMessages[0].Content)
	}
	if gotMessages[1].Role != "user" {
		t.Fatalf("msg[1].role = %q, want user", gotMessages[1].Role)
	}
}

func TestCreateMessageToolCallRoundTrip(t *testing.T) {
	var gotReq requestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{
				"message":{
					"role":"assistant",
					"content":null,
					"tool_calls":[{
						"id":"call_1",
						"type":"function",
						"function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":20,"completion_tokens":15}
		}`))
	}))
	defer srv.Close()

	p, _ := New(Config{APIKey: "k", BaseURL: srv.URL})
	resp, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "anthropic/claude-3-5-sonnet",
		MaxTokens: 256,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "run ls"}},
		}},
		Tools: []llm.ToolSpec{{Type: "bash_20250124"}},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q, want tool_use", resp.StopReason)
	}
	if len(resp.Message.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Message.Content))
	}
	b := resp.Message.Content[0]
	if b.Type != "tool_use" || b.Name != "bash" || b.ToolUseID != "call_1" {
		t.Fatalf("content block = %+v", b)
	}
	if cmd, _ := b.Input["command"].(string); cmd != "ls" {
		t.Fatalf("command = %q, want ls", cmd)
	}

	// Verify the tool definition was sent correctly.
	if len(gotReq.Tools) != 1 || gotReq.Tools[0].Function.Name != "bash" {
		t.Fatalf("tools = %+v", gotReq.Tools)
	}
	if gotReq.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %q, want auto", gotReq.ToolChoice)
	}
	if resp.Usage.InputTokens != 20 || resp.Usage.OutputTokens != 15 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestCreateMessageToolResultConversion(t *testing.T) {
	var gotMessages []requestMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody requestBody
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		gotMessages = reqBody.Messages
		writeChatResponse(w, "done", "stop", 5, 2)
	}))
	defer srv.Close()

	p, _ := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "openai/gpt-4o",
		MaxTokens: 128,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: []llm.ContentBlock{{Type: "text", Text: "run ls"}},
			},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "tool_use", ToolUseID: "call_1", Name: "bash", Input: map[string]any{"command": "ls"}},
				},
			},
			{
				Role: "user",
				Content: []llm.ContentBlock{
					{Type: "tool_result", ToolUseID: "call_1", Text: "file1.txt\nfile2.txt"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Expect: user → assistant (tool_calls) → tool.
	if len(gotMessages) != 3 {
		t.Fatalf("messages = %d, want 3: %+v", len(gotMessages), gotMessages)
	}
	if gotMessages[0].Role != "user" {
		t.Fatalf("msg[0].role = %q, want user", gotMessages[0].Role)
	}
	if gotMessages[1].Role != "assistant" {
		t.Fatalf("msg[1].role = %q, want assistant", gotMessages[1].Role)
	}
	if len(gotMessages[1].ToolCalls) != 1 || gotMessages[1].ToolCalls[0].Function.Name != "bash" {
		t.Fatalf("msg[1].tool_calls = %+v", gotMessages[1].ToolCalls)
	}
	// Assistant content should be nil when there is only a tool_use block.
	if gotMessages[1].Content != nil {
		t.Fatalf("msg[1].content = %v, want nil", gotMessages[1].Content)
	}
	if gotMessages[2].Role != "tool" {
		t.Fatalf("msg[2].role = %q, want tool", gotMessages[2].Role)
	}
	if gotMessages[2].ToolCallID != "call_1" {
		t.Fatalf("msg[2].tool_call_id = %q, want call_1", gotMessages[2].ToolCallID)
	}
	if gotMessages[2].Content == nil || *gotMessages[2].Content != "file1.txt\nfile2.txt" {
		t.Fatalf("msg[2].content = %v", gotMessages[2].Content)
	}
}

func TestCreateMessageUnknownTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeChatResponse(w, "ok", "stop", 1, 1)
	}))
	defer srv.Close()

	p, _ := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "openai/gpt-4o",
		MaxTokens: 64,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hi"}},
		}},
		Tools: []llm.ToolSpec{{Type: "unknown_tool_xyz"}},
	})
	if err == nil {
		t.Fatal("expected error for unknown tool type")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("error = %q, want to contain 'unknown tool'", err)
	}
}

func TestCreateMessageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","code":401}}`))
	}))
	defer srv.Close()

	p, _ := New(Config{APIKey: "bad-key", BaseURL: srv.URL})
	_, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model: "openai/gpt-4o",
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("error = %q, want to contain 'invalid api key'", err)
	}
}

func TestCreateMessageNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":0}}`))
	}))
	defer srv.Close()

	p, _ := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "openai/gpt-4o",
		MaxTokens: 64,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("error = %q, want to contain 'no choices'", err)
	}
}

func TestMapStopReason(t *testing.T) {
	cases := []struct{ in, want string }{
		{"stop", "end_turn"},
		{"tool_calls", "tool_use"},
		{"length", "max_tokens"},
		{"", "end_turn"},
		{"content_filter", "content_filter"},
	}
	for _, tc := range cases {
		if got := mapStopReason(tc.in); got != tc.want {
			t.Errorf("mapStopReason(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTextEditorToolSchema(t *testing.T) {
	def, ok := toolSchemas["text_editor_20250728"]
	if !ok {
		t.Fatal("text_editor_20250728 not in toolSchemas")
	}
	if def.Function.Name != "str_replace_based_edit_tool" {
		t.Fatalf("name = %q", def.Function.Name)
	}
	props, _ := def.Function.Parameters["properties"].(map[string]any)
	if _, ok := props["command"]; !ok {
		t.Error("missing 'command' property")
	}
	if _, ok := props["path"]; !ok {
		t.Error("missing 'path' property")
	}
}
