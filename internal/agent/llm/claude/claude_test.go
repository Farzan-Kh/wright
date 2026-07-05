package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/farzan-kh/wright/internal/agent/llm"
)

func TestCreateMessageAPIKeyAuthHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-api-key" {
			t.Fatalf("x-api-key = %q, want test-api-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty in api_key mode", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-haiku-4-5",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{
				"input_tokens":11,
				"output_tokens":7,
				"cache_creation_input_tokens":3,
				"cache_read_input_tokens":2
			}
		}`))
	}))
	defer srv.Close()

	p, err := New(Config{
		APIBaseURL: srv.URL,
		AuthMode:   AuthModeAPIKey,
		APIKey:     "test-api-key",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "claude-haiku-4-5",
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
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "ok" {
		t.Fatalf("unexpected content: %+v", resp.Message.Content)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestCreateMessageOAuthHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-access-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Fatalf("x-api-key = %q, want empty in oauth mode", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != oauthBetaHeader {
			t.Fatalf("anthropic-beta = %q, want %q", got, oauthBetaHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-haiku-4-5",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{
				"input_tokens":1,
				"output_tokens":1,
				"cache_creation_input_tokens":0,
				"cache_read_input_tokens":0
			}
		}`))
	}))
	defer srv.Close()

	p, err := New(Config{
		APIBaseURL: srv.URL,
		AuthMode:   AuthModeOAuth,
		OAuth: OAuthConfig{
			AccessToken: "oauth-access-token",
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "claude-haiku-4-5",
		MaxTokens: 64,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
}

func TestCreateMessageThinkingEffortMapsToBudgetTokens(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-5",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{
				"input_tokens":1,
				"output_tokens":1,
				"cache_creation_input_tokens":0,
				"cache_read_input_tokens":0
			}
		}`))
	}))
	defer srv.Close()

	p, err := New(Config{APIBaseURL: srv.URL, AuthMode: AuthModeAPIKey, APIKey: "test-api-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:       "claude-sonnet-5",
		MaxTokens:   4096,
		ThinkingOn:  true,
		ThinkEffort: "medium",
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	thinking, ok := reqBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking block missing or wrong type: %#v", reqBody["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type = %v, want enabled", thinking["type"])
	}
	budget, ok := thinking["budget_tokens"].(float64)
	if !ok {
		t.Fatalf("thinking.budget_tokens missing: %#v", thinking)
	}
	if budget < 1024 || budget >= 4096 {
		t.Fatalf("thinking.budget_tokens = %v, want >=1024 and <4096", budget)
	}
}

func TestThinkingBlockParsedAndReplayed(t *testing.T) {
	// The API returns a signed thinking block ahead of a tool_use; when extended
	// thinking is on, that block must be sent back unmodified on the next turn or
	// the tool_use continuation is rejected. Verify both directions.
	var reqBody map[string]any
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 2 {
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5",
			"content":[
				{"type":"thinking","thinking":"let me look","signature":"sig-abc"},
				{"type":"tool_use","id":"tu_1","name":"bash","input":{"command":"ls"}}
			],
			"stop_reason":"tool_use","stop_sequence":null,
			"usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}
		}`))
	}))
	defer srv.Close()

	p, err := New(Config{APIBaseURL: srv.URL, AuthMode: AuthModeAPIKey, APIKey: "test-api-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model: "claude-sonnet-5", MaxTokens: 4096, ThinkingOn: true,
		Messages: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	// Parsing preserves the thinking block with its signature.
	if len(resp.Message.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2: %+v", len(resp.Message.Content), resp.Message.Content)
	}
	th := resp.Message.Content[0]
	if th.Type != "thinking" || th.Text != "let me look" || th.Signature != "sig-abc" {
		t.Fatalf("thinking block not preserved: %+v", th)
	}

	// Replay the assistant turn plus a tool_result and confirm the thinking block
	// (with signature) is present on the wire.
	history := []llm.Message{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
		resp.Message,
		{Role: "user", Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "tu_1", Text: "ok"}}},
	}
	if _, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model: "claude-sonnet-5", MaxTokens: 4096, ThinkingOn: true, Messages: history,
	}); err != nil {
		t.Fatalf("CreateMessage replay: %v", err)
	}

	msgs, _ := reqBody["messages"].([]any)
	var thinking map[string]any
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		content, _ := mm["content"].([]any)
		for _, c := range content {
			cb, _ := c.(map[string]any)
			if cb["type"] == "thinking" {
				thinking = cb
			}
		}
	}
	if thinking == nil {
		t.Fatalf("replayed request dropped the thinking block: %#v", reqBody["messages"])
	}
	if thinking["signature"] != "sig-abc" || thinking["thinking"] != "let me look" {
		t.Fatalf("thinking block corrupted on replay: %#v", thinking)
	}
}

func TestCreateMessageOAuthRefreshesExpiringToken(t *testing.T) {
	refreshCalls := 0
	messageCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "refresh_token" {
				t.Fatalf("grant_type = %q, want refresh_token", got)
			}
			if got := r.Form.Get("refresh_token"); got != "refresh-token" {
				t.Fatalf("refresh_token = %q, want refresh-token", got)
			}
			if got := r.Form.Get("client_id"); got != "client-id" {
				t.Fatalf("client_id = %q, want client-id", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","expires_in":3600}`))
		case "/v1/messages":
			messageCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("Authorization = %q, want Bearer refreshed-token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"msg_1",
				"type":"message",
				"role":"assistant",
				"model":"claude-haiku-4-5",
				"content":[{"type":"text","text":"ok"}],
				"stop_reason":"end_turn",
				"stop_sequence":null,
				"usage":{
					"input_tokens":1,
					"output_tokens":1,
					"cache_creation_input_tokens":0,
					"cache_read_input_tokens":0
				}
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p, err := New(Config{
		APIBaseURL: srv.URL,
		AuthMode:   AuthModeOAuth,
		HTTPClient: srv.Client(),
		OAuth: OAuthConfig{
			AccessToken:  "stale-token",
			ExpiresAt:    time.Now().Add(30 * time.Second),
			RefreshToken: "refresh-token",
			ClientID:     "client-id",
			TokenURL:     srv.URL + "/oauth/token",
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "claude-haiku-4-5",
		MaxTokens: 64,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", refreshCalls)
	}
	if messageCalls != 1 {
		t.Fatalf("messageCalls = %d, want 1", messageCalls)
	}
}

// When refresh credentials are configured but the token's expiry couldn't be
// determined (e.g. the expiry env var was unset or unparseable), the adapter
// must refresh proactively rather than trusting a possibly-expired token for
// the life of the process.
func TestCreateMessageOAuthRefreshesWhenExpiryUnknown(t *testing.T) {
	refreshCalls := 0
	messageCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			refreshCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","expires_in":3600}`))
		case "/v1/messages":
			messageCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("Authorization = %q, want Bearer refreshed-token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"msg_1",
				"type":"message",
				"role":"assistant",
				"model":"claude-haiku-4-5",
				"content":[{"type":"text","text":"ok"}],
				"stop_reason":"end_turn",
				"stop_sequence":null,
				"usage":{
					"input_tokens":1,
					"output_tokens":1,
					"cache_creation_input_tokens":0,
					"cache_read_input_tokens":0
				}
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p, err := New(Config{
		APIBaseURL: srv.URL,
		AuthMode:   AuthModeOAuth,
		HTTPClient: srv.Client(),
		OAuth: OAuthConfig{
			AccessToken:  "stale-token",
			RefreshToken: "refresh-token",
			ClientID:     "client-id",
			TokenURL:     srv.URL + "/oauth/token",
			// ExpiresAt intentionally left zero-valued.
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := p.CreateMessage(context.Background(), llm.MessageRequest{
		Model:     "claude-haiku-4-5",
		MaxTokens: 64,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", refreshCalls)
	}
	if messageCalls != 1 {
		t.Fatalf("messageCalls = %d, want 1", messageCalls)
	}
}
