package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/farzan-kh/wright/internal/agent/llm"
	"github.com/farzan-kh/wright/internal/cost"
)

const oauthBetaHeader = "oauth-2025-04-20"

// AuthMode selects Claude authentication mode.
type AuthMode string

const (
	AuthModeAPIKey AuthMode = "api_key"
	AuthModeOAuth  AuthMode = "oauth"
)

// OAuthConfig holds OAuth access/refresh state.
type OAuthConfig struct {
	AccessToken  string
	ExpiresAt    time.Time
	RefreshToken string
	ClientID     string
	TokenURL     string
}

// Config configures the Claude adapter.
type Config struct {
	APIBaseURL string

	AuthMode AuthMode
	APIKey   string
	OAuth    OAuthConfig

	HTTPClient *http.Client
}

// Provider is an llm.LLMProvider backed by Anthropic's Messages API.
type Provider struct {
	client anthropic.Client
	mode   AuthMode
	oauth  *oauthState
}

var _ llm.LLMProvider = (*Provider)(nil)

func New(cfg Config) (*Provider, error) {
	opts := []option.RequestOption{}
	if cfg.APIBaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.APIBaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}

	switch cfg.AuthMode {
	case AuthModeAPIKey:
		if cfg.APIKey == "" {
			return nil, errors.New("claude: api_key auth selected but API key is empty")
		}
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
		c := anthropic.NewClient(opts...)
		return &Provider{client: c, mode: AuthModeAPIKey}, nil
	case AuthModeOAuth:
		if cfg.APIKey != "" {
			return nil, errors.New("claude: oauth mode must not set api key")
		}
		if cfg.OAuth.AccessToken == "" {
			return nil, errors.New("claude: oauth auth selected but access token is empty")
		}
		c := anthropic.NewClient(opts...)
		return &Provider{
			client: c,
			mode:   AuthModeOAuth,
			oauth: &oauthState{
				accessToken:  cfg.OAuth.AccessToken,
				expiresAt:    cfg.OAuth.ExpiresAt,
				refreshToken: cfg.OAuth.RefreshToken,
				clientID:     cfg.OAuth.ClientID,
				tokenURL:     cfg.OAuth.TokenURL,
				httpClient:   cfg.HTTPClient,
			},
		}, nil
	default:
		return nil, fmt.Errorf("claude: unsupported auth mode %q", cfg.AuthMode)
	}
}

func (p *Provider) CreateMessage(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error) {
	params, err := toAnthropicRequest(req)
	if err != nil {
		return llm.MessageResponse{}, err
	}

	callOpts := []option.RequestOption{}
	if p.mode == AuthModeOAuth {
		tok, err := p.oauth.token(ctx)
		if err != nil {
			return llm.MessageResponse{}, err
		}
		callOpts = append(callOpts,
			option.WithAuthToken(tok),
			option.WithHeader("anthropic-beta", oauthBetaHeader),
		)
	}

	resp, err := p.client.Messages.New(ctx, params, callOpts...)
	if err != nil {
		return llm.MessageResponse{}, err
	}
	return fromAnthropicResponse(resp)
}

func toAnthropicRequest(req llm.MessageRequest) (anthropic.MessageNewParams, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	out := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(maxTokens),
	}

	for _, sb := range req.System {
		block := anthropic.TextBlockParam{Text: sb.Text}
		if sb.CachePrompt {
			block.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		out.System = append(out.System, block)
	}

	if req.ThinkingOn {
		out.Thinking = thinkingConfig(maxTokens, req.ThinkEffort)
	}

	for _, m := range req.Messages {
		msg, err := toAnthropicMessage(m)
		if err != nil {
			return anthropic.MessageNewParams{}, err
		}
		out.Messages = append(out.Messages, msg)
	}

	for _, t := range req.Tools {
		switch t.Type {
		case "bash_20250124":
			out.Tools = append(out.Tools, anthropic.ToolUnionParam{OfBashTool20250124: &anthropic.ToolBash20250124Param{}})
		case "text_editor_20250728":
			out.Tools = append(out.Tools, anthropic.ToolUnionParam{OfTextEditor20250728: &anthropic.ToolTextEditor20250728Param{}})
		case "repo_read_file":
			tu := anthropic.ToolUnionParamOfTool(repoPathSchema("Repo-relative file path to read."), "repo_read_file")
			tu.OfTool.Description = param.NewOpt("Read a file from the repository at its current default-branch state.")
			out.Tools = append(out.Tools, tu)
		case "repo_list_dir":
			tu := anthropic.ToolUnionParamOfTool(repoPathSchema(`Repo-relative directory path to list ("" for the repo root).`), "repo_list_dir")
			tu.OfTool.Description = param.NewOpt("List a directory in the repository at its current default-branch state. Directory entries end with \"/\".")
			out.Tools = append(out.Tools, tu)
		default:
			return anthropic.MessageNewParams{}, fmt.Errorf("claude: unknown tool %q", t.Type)
		}
	}

	return out, nil
}

// repoPathSchema is the shared input schema for the repo_read_file and
// repo_list_dir tools: a single required "path" string.
func repoPathSchema(pathDescription string) anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": pathDescription,
			},
		},
		Required: []string{"path"},
	}
}

func thinkingConfig(maxTokens int, effort string) anthropic.ThinkingConfigParamUnion {
	// ThinkingConfigEnabled requires budget_tokens >= 1024 and < max_tokens.
	if maxTokens <= 1024 {
		return anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}
	}

	ratio := 0.7 // "high", "", and any unrecognized value
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		ratio = 0.25
	case "medium":
		ratio = 0.45
	}

	budget := max(int64(float64(maxTokens)*ratio), 1024)
	maxAllowed := int64(maxTokens - 1)
	budget = min(budget, maxAllowed)
	if budget < 1024 {
		return anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}
	}
	return anthropic.ThinkingConfigParamUnion{OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: budget}}
}

func toAnthropicMessage(m llm.Message) (anthropic.MessageParam, error) {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Content))
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			blocks = append(blocks, anthropic.NewTextBlock(b.Text))
		case "thinking":
			blocks = append(blocks, anthropic.NewThinkingBlock(b.Signature, b.Text))
		case "redacted_thinking":
			blocks = append(blocks, anthropic.NewRedactedThinkingBlock(b.Data))
		case "tool_result":
			blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolUseID, b.Text, b.IsError))
		case "tool_use":
			blocks = append(blocks, anthropic.NewToolUseBlock(b.ToolUseID, b.Input, b.Name))
		default:
			return anthropic.MessageParam{}, fmt.Errorf("claude: unsupported content block %q", b.Type)
		}
	}

	switch strings.ToLower(m.Role) {
	case "assistant":
		return anthropic.NewAssistantMessage(blocks...), nil
	case "user", "":
		return anthropic.NewUserMessage(blocks...), nil
	default:
		return anthropic.MessageParam{}, fmt.Errorf("claude: unsupported message role %q", m.Role)
	}
}

func fromAnthropicResponse(msg *anthropic.Message) (llm.MessageResponse, error) {
	if msg == nil {
		return llm.MessageResponse{}, errors.New("claude: nil message response")
	}
	out := llm.MessageResponse{
		Message: llm.Message{
			Role: "assistant",
		},
		StopReason: string(msg.StopReason),
		Usage: cost.Usage{
			InputTokens:              msg.Usage.InputTokens,
			OutputTokens:             msg.Usage.OutputTokens,
			CacheCreationInputTokens: msg.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     msg.Usage.CacheReadInputTokens,
		},
	}

	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			out.Message.Content = append(out.Message.Content, llm.ContentBlock{Type: "text", Text: b.Text})
		case "thinking":
			// Preserve the signed thinking block so it can be replayed on the next
			// turn — the API rejects a tool_use continuation with it stripped.
			out.Message.Content = append(out.Message.Content, llm.ContentBlock{
				Type:      "thinking",
				Text:      b.Thinking,
				Signature: b.Signature,
			})
		case "redacted_thinking":
			out.Message.Content = append(out.Message.Content, llm.ContentBlock{
				Type: "redacted_thinking",
				Data: b.Data,
			})
		case "tool_use":
			var input map[string]any
			if len(b.Input) > 0 {
				if err := json.Unmarshal(b.Input, &input); err != nil {
					return llm.MessageResponse{}, fmt.Errorf("claude: decode tool_use input: %w", err)
				}
			}
			out.Message.Content = append(out.Message.Content, llm.ContentBlock{
				Type:      "tool_use",
				ToolUseID: b.ID,
				Name:      b.Name,
				Input:     input,
			})
		}
	}

	return out, nil
}

type oauthState struct {
	mu sync.Mutex

	accessToken string
	expiresAt   time.Time

	refreshToken string
	clientID     string
	tokenURL     string
	httpClient   *http.Client
}

func (o *oauthState) token(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.accessToken == "" {
		return "", errors.New("claude: oauth access token is empty")
	}
	if o.needsRefresh() {
		if err := o.refreshLocked(ctx); err != nil {
			return "", err
		}
	}
	return o.accessToken, nil
}

func (o *oauthState) needsRefresh() bool {
	if o.refreshToken == "" || o.tokenURL == "" {
		return false
	}
	if o.expiresAt.IsZero() {
		// Expiry is unknown (e.g. the configured expiry env var is unset or
		// unparseable). Refresh now rather than silently trusting a token
		// that may already be expired for the life of the process.
		return true
	}
	return time.Until(o.expiresAt) <= 2*time.Minute
}

func (o *oauthState) refreshLocked(ctx context.Context) error {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", o.refreshToken)
	if o.clientID != "" {
		values.Set("client_id", o.clientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("claude: build oauth refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	hc := o.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("claude: oauth token refresh request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("claude: oauth token refresh failed: status %s", resp.Status)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return fmt.Errorf("claude: parse oauth refresh response: %w", err)
	}
	if tr.AccessToken == "" {
		return errors.New("claude: oauth refresh response missing access_token")
	}
	o.accessToken = tr.AccessToken
	if tr.ExpiresIn > 0 {
		o.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return nil
}
