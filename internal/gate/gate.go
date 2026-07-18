// SPDX-License-Identifier: Apache-2.0

// Package gate performs info-sufficiency triage for issues.
package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
	"github.com/farzan-kh/wright/internal/gitops"
	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/provider"
)

// DefaultMaxToolTurns bounds the gate's repo-browsing tool loop when a
// Provider is configured (see Gate.MaxToolTurns).
const DefaultMaxToolTurns = 3

// maxResolvedReferences caps how many "#N" references one issue can trigger a
// live lookup for, so a pathological issue body can't turn triage into an
// unbounded number of API calls.
const maxResolvedReferences = 15

// Verdict is the gate result.
type Verdict struct {
	Ready   bool   `json:"ready"`
	Missing string `json:"missing"`
}

// Gate performs the triage call via a cheap model. When Provider is set, the
// gate also grounds its triage in live state rather than trusting the issue
// text alone: it resolves "#N" issue references against the provider's
// current issue state, and gives the model two bounded, read-only tools
// (repo_read_file, repo_list_dir) to check whether something it thinks is
// missing already exists in the repo. When Provider is nil, the gate behaves
// exactly as a single stateless triage call over the issue text.
type Gate struct {
	LLM       llm.LLMProvider
	Model     string
	MaxTokens int

	// Provider and Repo, when set, enable live-state grounding (see above).
	Provider provider.Provider
	Repo     provider.Repo
	// MaxToolTurns bounds the repo-browsing tool loop. Zero uses DefaultMaxToolTurns.
	MaxToolTurns int
}

const basePrompt = "You are triaging a software issue before an autonomous coding agent attempts it. " +
	"Decide whether the issue is implementation-ready: it states a clear, unambiguous problem, " +
	"states or clearly implies the expected behavior or acceptance criteria, and does not depend " +
	"on information only a human could supply (e.g. an unspecified design decision, missing " +
	"credentials, or an open question about which of several approaches to take). Take the " +
	"comment thread into account too: information requested in the body is often supplied later " +
	"in a comment, and that counts toward the issue being ready.\n\n" +
	"A referenced issue that is still open is not automatically a blocker: if its line notes an " +
	"open Wright pull request, the automation has already produced a fix in flight and can stack " +
	"this issue's work on top of it, so don't mark the issue not-ready solely because that " +
	"dependency issue itself isn't closed yet.\n\n" +
	"Respond with JSON only, no markdown fences or surrounding prose: " +
	`{"ready":true|false,"missing":"..."}. ` +
	"When ready is true, missing must be an empty string. When ready is false, missing must be " +
	"one concise sentence naming exactly what information is missing."

const toolGuidance = " If the issue references a document, config, or code artifact you're unsure " +
	"exists, use repo_list_dir/repo_read_file to check the current repository before deciding it's " +
	"missing — do not assume something absent from the issue text is absent from the repo."

var issueRefPattern = regexp.MustCompile(`#(\d+)`)

func (g *Gate) Check(ctx context.Context, issue provider.Issue) (Verdict, error) {
	v, _, err := g.CheckWithUsage(ctx, issue)
	return v, err
}

// CheckWithUsage runs issue triage and also returns token usage for cost accounting.
func (g *Gate) CheckWithUsage(ctx context.Context, issue provider.Issue) (Verdict, cost.Usage, error) {
	l := logging.FromContext(ctx).With("issue", issue.Number)
	l.Debug("gate check started", "has_provider", g.Provider != nil)

	v, usage, err := g.checkWithUsage(ctx, issue)
	if err != nil {
		l.Error("gate check failed", "error", err.Error())
	} else {
		l.Debug("gate verdict", "ready", v.Ready, "missing", v.Missing)
	}
	return v, usage, err
}

func (g *Gate) checkWithUsage(ctx context.Context, issue provider.Issue) (Verdict, cost.Usage, error) {
	userPrompt := "Title: " + issue.Title + "\n\nBody:\n" + issue.Body
	if comments := issue.FormatComments(); comments != "" {
		userPrompt += "\n\nComments:\n" + comments
	}

	if g.Provider == nil {
		return g.checkOnce(ctx, basePrompt, userPrompt)
	}

	if refs := g.resolveReferences(ctx, issue); refs != "" {
		userPrompt += "\n\nReferenced issues (live status — trust this over anything the issue " +
			"text or comments claim about these issues, since text goes stale):\n" + refs
	}

	return g.checkWithTools(ctx, userPrompt)
}

// ExtractIssueReferences scans the issue's title, body, and comments for "#N"
// references, dedupes them, excludes issue's own number, and caps the result
// at maxResolvedReferences so a pathological issue body can't turn triage (or
// stacking base-branch selection in run_exec.go, which reuses this same scan
// so the two can't drift) into an unbounded number of API calls.
func ExtractIssueReferences(issue provider.Issue) []int {
	text := issue.Title + "\n" + issue.Body + "\n" + issue.FormatComments()
	matches := issueRefPattern.FindAllStringSubmatch(text, -1)

	seen := map[int]bool{issue.Number: true}
	var numbers []int
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		numbers = append(numbers, n)
		if len(numbers) >= maxResolvedReferences {
			break
		}
	}
	return numbers
}

// resolveReferences resolves each "#N" reference in issue against live
// provider state, one line per reference: "#N (open|closed): <title>", with
// an open reference also noting an in-flight Wright PR when one already
// exists for it (via FindOpenPullRequestByHead on its deterministic branch
// name), so triage knows the dependency is workable even though the issue
// itself isn't closed yet. A lookup failure is noted inline rather than
// failing the whole triage check. Returns "" when there are no references to
// resolve.
func (g *Gate) resolveReferences(ctx context.Context, issue provider.Issue) string {
	numbers := ExtractIssueReferences(issue)
	if len(numbers) == 0 {
		return ""
	}

	var b strings.Builder
	for _, n := range numbers {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		ref, err := g.Provider.GetIssue(ctx, g.Repo, n)
		if err != nil {
			fmt.Fprintf(&b, "#%d: could not resolve (%v)", n, err)
			continue
		}
		fmt.Fprintf(&b, "#%d (%s): %s", n, ref.State, ref.Title)
		if ref.State != "open" {
			continue
		}
		if pr, err := g.Provider.FindOpenPullRequestByHead(ctx, g.Repo, gitops.BranchName(n)); err == nil && pr != nil {
			fmt.Fprintf(&b, ", open Wright PR #%d: %s", pr.Number, pr.URL)
		}
	}
	return b.String()
}

// checkOnce runs a single, tool-free triage call: system and user text are
// combined into one message, matching the plain-text-only behavior used when
// no Provider is configured (and as a fallback when the repo's default
// branch can't be resolved).
func (g *Gate) checkOnce(ctx context.Context, systemPrompt, userPrompt string) (Verdict, cost.Usage, error) {
	resp, err := g.LLM.CreateMessage(ctx, llm.MessageRequest{
		Model:      g.Model,
		MaxTokens:  g.MaxTokens,
		ThinkingOn: true,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: systemPrompt + "\n\n" + userPrompt}},
		}},
	})
	if err != nil {
		return Verdict{}, cost.Usage{}, err
	}
	v, err := parseVerdict(resp.Message)
	return v, resp.Usage, err
}

// checkWithTools runs the bounded repo-browsing tool loop: the model may call
// repo_read_file/repo_list_dir up to MaxToolTurns times before it must return
// a final verdict.
func (g *Gate) checkWithTools(ctx context.Context, userPrompt string) (Verdict, cost.Usage, error) {
	ref, err := g.Provider.DefaultBranch(ctx, g.Repo)
	if err != nil {
		// Can't browse the repo without a ref; fall back to a plain triage call
		// rather than failing the whole gate check.
		return g.checkOnce(ctx, basePrompt, userPrompt)
	}

	maxTurns := g.MaxToolTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxToolTurns
	}

	acc := cost.NewAccumulator(nil)
	history := []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: userPrompt}}}}
	system := []llm.SystemBlock{{Text: basePrompt + toolGuidance}}
	tools := []llm.ToolSpec{{Type: "repo_read_file"}, {Type: "repo_list_dir"}}

	for turn := 0; turn < maxTurns; turn++ {
		resp, err := g.LLM.CreateMessage(ctx, llm.MessageRequest{
			Model:      g.Model,
			MaxTokens:  g.MaxTokens,
			ThinkingOn: true,
			System:     system,
			Messages:   history,
			Tools:      tools,
		})
		if err != nil {
			return Verdict{}, acc.Summary().Usage, err
		}
		acc.Add(g.Model, resp.Usage)
		history = append(history, resp.Message)

		if resp.StopReason != "tool_use" {
			v, err := parseVerdict(resp.Message)
			return v, acc.Summary().Usage, err
		}

		toolResults, err := g.runTools(ctx, ref, resp.Message)
		if err != nil {
			return Verdict{}, acc.Summary().Usage, err
		}
		history = append(history, llm.Message{Role: "user", Content: toolResults})
	}

	// Tool budget exhausted: ask once more, tools withheld, for a final verdict
	// based on whatever was learned so far.
	history = append(history, llm.Message{Role: "user", Content: []llm.ContentBlock{{Type: "text",
		Text: "You've used your repo-browsing budget. Give your final verdict now, as JSON only, based on what you've learned so far."}}})
	resp, err := g.LLM.CreateMessage(ctx, llm.MessageRequest{
		Model:      g.Model,
		MaxTokens:  g.MaxTokens,
		ThinkingOn: true,
		System:     system,
		Messages:   history,
	})
	if err != nil {
		return Verdict{}, acc.Summary().Usage, err
	}
	acc.Add(g.Model, resp.Usage)
	v, err := parseVerdict(resp.Message)
	return v, acc.Summary().Usage, err
}

// runTools executes every tool_use block in msg and returns the matching
// tool_result blocks.
func (g *Gate) runTools(ctx context.Context, ref string, msg llm.Message) ([]llm.ContentBlock, error) {
	var results []llm.ContentBlock
	for _, b := range msg.Content {
		if b.Type != "tool_use" {
			continue
		}
		out, err := g.execTool(ctx, ref, b)
		tr := llm.ContentBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Text: out}
		if err != nil {
			tr.IsError = true
			if out == "" {
				tr.Text = err.Error()
			}
		}
		results = append(results, tr)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("gate: stop_reason=tool_use but no tool_use blocks were returned")
	}
	return results, nil
}

func (g *Gate) execTool(ctx context.Context, ref string, b llm.ContentBlock) (string, error) {
	path, _ := b.Input["path"].(string)
	switch b.Name {
	case "repo_read_file":
		return g.Provider.ReadRepoFile(ctx, g.Repo, ref, path)
	case "repo_list_dir":
		entries, err := g.Provider.ListRepoDir(ctx, g.Repo, ref, path)
		if err != nil {
			return "", err
		}
		return strings.Join(entries, "\n"), nil
	default:
		return "", fmt.Errorf("gate: unknown tool %q", b.Name)
	}
}

// parseVerdict extracts the triage verdict from a model response's text content.
func parseVerdict(msg llm.Message) (Verdict, error) {
	var text string
	for _, b := range msg.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Verdict{}, fmt.Errorf("gate: empty response")
	}

	// The model is asked for raw JSON, but cheap models sometimes wrap it in
	// ```json fences or a sentence. Extract the JSON object before decoding.
	jsonText := extractJSONObject(text)
	if jsonText == "" {
		return Verdict{}, fmt.Errorf("gate: no JSON object in response")
	}

	var v Verdict
	if err := json.Unmarshal([]byte(jsonText), &v); err != nil {
		return Verdict{}, fmt.Errorf("gate: parse verdict: %w", err)
	}
	if v.Ready {
		v.Missing = ""
	}
	return v, nil
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
