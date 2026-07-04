// Package gate performs info-sufficiency triage for issues.
package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/farzan-kh/patchr/internal/agent/llm"
	"github.com/farzan-kh/patchr/internal/cost"
	"github.com/farzan-kh/patchr/internal/provider"
)

// Verdict is the gate result.
type Verdict struct {
	Ready   bool   `json:"ready"`
	Missing string `json:"missing"`
}

// Gate performs the triage call via a cheap model.
type Gate struct {
	LLM       llm.LLMProvider
	Model     string
	MaxTokens int
}

func (g *Gate) Check(ctx context.Context, issue provider.Issue) (Verdict, error) {
	v, _, err := g.CheckWithUsage(ctx, issue)
	return v, err
}

// CheckWithUsage runs issue triage and also returns token usage for cost accounting.
func (g *Gate) CheckWithUsage(ctx context.Context, issue provider.Issue) (Verdict, cost.Usage, error) {
	prompt := "Decide if this software issue is implementation-ready. Return JSON only: " +
		`{"ready":true|false,"missing":"..."}. ` +
		"Set missing to empty when ready=true.\n\n" +
		"Title: " + issue.Title + "\n\nBody:\n" + issue.Body

	resp, err := g.LLM.CreateMessage(ctx, llm.MessageRequest{
		Model:     g.Model,
		MaxTokens: g.MaxTokens,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: prompt}},
		}},
	})
	if err != nil {
		return Verdict{}, cost.Usage{}, err
	}

	var text string
	for _, b := range resp.Message.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Verdict{}, resp.Usage, fmt.Errorf("gate: empty response")
	}

	// The model is asked for raw JSON, but cheap models sometimes wrap it in
	// ```json fences or a sentence. Extract the JSON object before decoding.
	jsonText := extractJSONObject(text)
	if jsonText == "" {
		return Verdict{}, resp.Usage, fmt.Errorf("gate: no JSON object in response")
	}

	var v Verdict
	if err := json.Unmarshal([]byte(jsonText), &v); err != nil {
		return Verdict{}, resp.Usage, fmt.Errorf("gate: parse verdict: %w", err)
	}
	if v.Ready {
		v.Missing = ""
	}
	return v, resp.Usage, nil
}

// extractJSONObject returns the substring from the first '{' to the last '}',
// tolerating markdown fences or prose around the JSON. It returns "" when no
// braces are present.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}
