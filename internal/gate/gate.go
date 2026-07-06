// Package gate performs info-sufficiency triage for issues.
package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/provider"
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
	prompt := "You are triaging a software issue before an autonomous coding agent attempts it. " +
		"Decide whether the issue is implementation-ready: it states a clear, unambiguous problem, " +
		"states or clearly implies the expected behavior or acceptance criteria, and does not depend " +
		"on information only a human could supply (e.g. an unspecified design decision, missing " +
		"credentials, or an open question about which of several approaches to take). Take the " +
		"comment thread into account too: information requested in the body is often supplied later " +
		"in a comment, and that counts toward the issue being ready.\n\n" +
		"Respond with JSON only, no markdown fences or surrounding prose: " +
		`{"ready":true|false,"missing":"..."}. ` +
		"When ready is true, missing must be an empty string. When ready is false, missing must be " +
		"one concise sentence naming exactly what information is missing.\n\n" +
		"Title: " + issue.Title + "\n\nBody:\n" + issue.Body

	if comments := issue.FormatComments(); comments != "" {
		prompt += "\n\nComments:\n" + comments
	}

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
