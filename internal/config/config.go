// Package config defines Patchr's YAML configuration format and the routines to
// load and validate it. Credentials are never part of the config file; tokens
// are resolved from environment variables (see token.go).
package config

// Provider identifiers accepted in the config.
const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
)

// LLM provider identifiers accepted in llm.provider.
const (
	LLMProviderClaude     = "claude"
	LLMProviderOpenRouter = "openrouter"
)

// Defaults applied when fields are omitted.
const (
	DefaultTriggerLabel = "patchr"

	DefaultLLMAuth        = "api_key"
	DefaultAgentModel     = "claude-sonnet-5"
	DefaultGateModel      = "claude-haiku-4-5"
	DefaultLLMEffort      = "high"
	DefaultSandboxImage   = "alpine/git:2.47.2"
	DefaultSandboxWorkdir = "/workspace"
)

// Config is the top-level configuration. repos is a list from day one so that
// growing to many repos later is not a schema break; Phase 0 commands operate
// on exactly one entry.
type Config struct {
	Version int          `yaml:"version"`
	Repos   []RepoConfig `yaml:"repos"`
}

// RepoConfig configures Patchr for a single repository (GitHub) or project
// (GitLab).
type RepoConfig struct {
	// Provider is "github" or "gitlab".
	Provider string `yaml:"provider"`
	// Repo is the full path: "owner/name" on GitHub, or a full project path
	// like "group/subgroup/name" on GitLab.
	Repo string `yaml:"repo"`
	// APIBaseURL points at a self-hosted instance (GitHub Enterprise or a
	// self-managed GitLab). Empty means the provider's public SaaS.
	APIBaseURL string `yaml:"api_base_url"`
	// TokenEnv names an environment variable to read the token from, taking
	// precedence over the provider-based defaults (see token.go).
	TokenEnv string `yaml:"token_env"`
	// TriggerLabel is the issue label Patchr acts on. Defaults to "patchr".
	TriggerLabel string `yaml:"trigger_label"`
	// BaseBranch is the branch PRs target. Empty means the repo's default
	// branch, resolved via the API at run time.
	BaseBranch string `yaml:"base_branch"`
	// AutoMerge opts this repo into automatic merging. Defaults to false.
	AutoMerge bool `yaml:"auto_merge"`
	// Budget bounds per-issue spend. Held in the schema from Phase 0; actually
	// enforced in Phase 1.
	Budget BudgetConfig `yaml:"budget"`
	// LLM selects the models/auth used for gate + agent calls.
	LLM LLMConfig `yaml:"llm"`
	// Sandbox configures per-task execution isolation.
	Sandbox SandboxConfig `yaml:"sandbox"`
	// Verify optionally overrides auto-detected test command.
	Verify VerifyConfig `yaml:"verify"`
}

// BudgetConfig bounds the cost of resolving a single issue. The unit of MaxUSD
// is still an open question (see PROJECT_BRIEF.md); the schema holds it now and
// enforcement lands in Phase 1.
type BudgetConfig struct {
	MaxUSD   float64 `yaml:"max_usd"`
	MaxTurns int     `yaml:"max_turns"`
}

// LLMConfig selects the LLM provider, auth, and models.
type LLMConfig struct {
	Provider string `yaml:"provider"`

	// Auth is one of: api_key | oauth.
	Auth string `yaml:"auth"`
	// APIKeyEnv is the env var name for API-key auth credentials.
	APIKeyEnv string `yaml:"api_key_env"`

	// Model is a legacy alias for agent_model kept for compatibility.
	Model string `yaml:"model"`

	AgentModel string `yaml:"agent_model"`
	GateModel  string `yaml:"gate_model"`
	Effort     string `yaml:"effort"`

	OAuth OAuthConfig `yaml:"oauth"`
}

// OAuthConfig configures OAuth/subscription auth material.
type OAuthConfig struct {
	AccessTokenEnv       string `yaml:"access_token_env"`
	AccessTokenExpiryEnv string `yaml:"access_token_expiry_env"`
	RefreshTokenEnv      string `yaml:"refresh_token_env"`
	ClientIDEnv          string `yaml:"client_id_env"`
	TokenURL             string `yaml:"token_url"`
}

// SandboxConfig configures task containers.
type SandboxConfig struct {
	Image   string `yaml:"image"`
	Workdir string `yaml:"workdir"`
}

// VerifyConfig configures verification.
type VerifyConfig struct {
	Command string `yaml:"command"`
}

// applyDefaults fills in omitted optional fields. It is called by Load before
// validation.
func (c *Config) applyDefaults() {
	for i := range c.Repos {
		rc := &c.Repos[i]
		if rc.TriggerLabel == "" {
			rc.TriggerLabel = DefaultTriggerLabel
		}

		if rc.LLM.Auth == "" {
			rc.LLM.Auth = DefaultLLMAuth
		}
		if rc.LLM.AgentModel == "" {
			if rc.LLM.Model != "" { // compatibility with Phase 0 schema.
				rc.LLM.AgentModel = rc.LLM.Model
			} else {
				rc.LLM.AgentModel = DefaultAgentModel
			}
		}
		if rc.LLM.GateModel == "" {
			rc.LLM.GateModel = DefaultGateModel
		}
		if rc.LLM.Effort == "" {
			rc.LLM.Effort = DefaultLLMEffort
		}
		if rc.Sandbox.Image == "" {
			rc.Sandbox.Image = DefaultSandboxImage
		}
		if rc.Sandbox.Workdir == "" {
			rc.Sandbox.Workdir = DefaultSandboxWorkdir
		}
	}
}
