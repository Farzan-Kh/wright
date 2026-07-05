package gate

import (
	"context"
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
	v, usage, err := g.CheckWithUsage(context.Background(), provider.Issue{Title: "Fix bug", Body: ""})
	if err != nil {
		t.Fatalf("CheckWithUsage: %v", err)
	}
	if !v.Ready {
		t.Fatalf("verdict = %+v, want ready=true", v)
	}
	if usage.InputTokens != 123 || usage.OutputTokens != 45 {
		t.Fatalf("usage = %+v, want input=123 output=45", usage)
	}
}
