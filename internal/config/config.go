// SPDX-License-Identifier: Apache-2.0

// Package config defines Wright's YAML configuration format and the routines to
// load and validate it. Credentials are never part of the config file; tokens
// are resolved from environment variables (see token.go).
package config

import (
	"time"

	"github.com/farzan-kh/wright/internal/retry"
)

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

// Retry strategy identifiers accepted in retry.strategy.
const (
	RetryStrategyExponential = "exponential"
	RetryStrategyFixed       = "fixed"
)

// Defaults applied when fields are omitted.
const (
	DefaultTriggerLabel = "wright"

	DefaultLLMAuth        = "api_key"
	DefaultAgentModel     = "claude-sonnet-5"
	DefaultGateModel      = "claude-haiku-4-5"
	DefaultLLMEffort      = "high"
	DefaultSandboxImage   = "alpine/git:2.47.2"
	DefaultSandboxWorkdir = "/workspace"

	DefaultCacheDir = ".wright/cache"

	DefaultRetryStrategy    = RetryStrategyExponential
	DefaultRetryMaxAttempts = 4
	DefaultRetryBaseDelayMS = 500
	DefaultRetryMaxDelayMS  = 30_000
	DefaultRetryExponent    = 2.0
)

// Config is the top-level configuration. repos is a list from day one so that
// growing to many repos later is not a schema break; Phase 0 commands operate
// on exactly one entry.
type Config struct {
	Version int          `yaml:"version"`
	Repos   []RepoConfig `yaml:"repos"`
	// Cache configures where Wright persists partial progress from an
	// interrupted issue-resolution attempt (turn limit, sandbox fault, or a
	// failed commit/push/PR step), so the next attempt at the same issue can
	// resume instead of re-spending LLM turns from scratch. Shared across
	// all repos in this config, since it's local daemon state rather than a
	// per-repo behavior.
	Cache CacheConfig `yaml:"cache"`
}

// CacheConfig configures the resume cache. See internal/cache.
type CacheConfig struct {
	// Dir is the directory cached attempts are written under, one JSON file
	// per issue. Relative paths are resolved against the working directory
	// wright is run from. Defaults to DefaultCacheDir.
	Dir string `yaml:"dir"`
}

// RepoConfig configures Wright for a single repository (GitHub) or project
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
	// TriggerLabel is the issue label Wright acts on. Defaults to "wright".
	TriggerLabel string `yaml:"trigger_label"`
	// BaseBranch is the branch PRs target. Empty means the repo's default
	// branch, resolved via the API at run time.
	BaseBranch string `yaml:"base_branch"`
	// AutoMerge opts this repo into automatic merging. Defaults to false.
	AutoMerge bool `yaml:"auto_merge"`
	// Budget bounds per-issue agent turns.
	Budget BudgetConfig `yaml:"budget"`
	// LLM selects the models/auth used for gate + agent calls.
	LLM LLMConfig `yaml:"llm"`
	// Sandbox configures per-task execution isolation.
	Sandbox SandboxConfig `yaml:"sandbox"`
	// Verify optionally overrides auto-detected test command.
	Verify VerifyConfig `yaml:"verify"`
	// Prompt customizes the agent's system prompt. See PromptConfig.
	Prompt PromptConfig `yaml:"prompt"`
	// Retry configures backoff for connection attempts to the provider API,
	// the LLM API, and the Docker daemon.
	Retry RetryConfig `yaml:"retry"`
	// Stacking configures cross-issue dependency stacking (see
	// internal/stack). Disabled by default.
	Stacking StackingConfig `yaml:"stacking"`
}

// BudgetConfig bounds the number of agent turns spent resolving a single
// issue.
type BudgetConfig struct {
	MaxTurns int `yaml:"max_turns"`
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

// RetryConfig controls retry behavior for connection attempts to external
// services (provider APIs, the LLM API, and the Docker daemon).
type RetryConfig struct {
	// Strategy is "exponential" or "fixed" (simple time-based retries).
	// Defaults to "exponential".
	Strategy string `yaml:"strategy"`
	// MaxAttempts is the total number of tries per connection attempt,
	// including the first. 1 disables retrying. Defaults to 4.
	MaxAttempts int `yaml:"max_attempts"`
	// BaseDelayMS is the delay, in milliseconds, before the first retry (and
	// every retry under the "fixed" strategy). Defaults to 500.
	BaseDelayMS int `yaml:"base_delay_ms"`
	// MaxDelayMS caps any single retry delay, in milliseconds. Defaults to
	// 30000.
	MaxDelayMS int `yaml:"max_delay_ms"`
	// Exponent is the per-attempt delay multiplier under the "exponential"
	// strategy. Defaults to 2.
	Exponent float64 `yaml:"exponent"`
}

// ToRetryConfig converts the YAML-facing settings into internal/retry's
// runtime Config.
func (rc RetryConfig) ToRetryConfig() retry.Config {
	strategy := retry.Exponential
	if rc.Strategy == RetryStrategyFixed {
		strategy = retry.Fixed
	}
	return retry.Config{
		Strategy:    strategy,
		MaxAttempts: rc.MaxAttempts,
		BaseDelay:   time.Duration(rc.BaseDelayMS) * time.Millisecond,
		MaxDelay:    time.Duration(rc.MaxDelayMS) * time.Millisecond,
		Exponent:    rc.Exponent,
	}
}

// StackingConfig controls whether Wright stacks a new issue's work on top of
// an already-open Wright PR for a dependency it references, instead of
// blocking until a human merges that dependency. See internal/stack.
type StackingConfig struct {
	// Enabled turns on dependency stacking. Defaults to false: this changes
	// what code gets combined into a PR before human review, so it's opt-in
	// rather than a behavior change existing users see automatically.
	Enabled bool `yaml:"enabled"`
}

// PromptConfig customizes the agent's system prompt behavior text.
// SystemAppend and SystemOverride are mutually exclusive.
type PromptConfig struct {
	// SystemAppend adds repo-specific instructions after Wright's default
	// behavior guidance (e.g. "always update CHANGELOG.md"). Safe default
	// choice for most repos.
	SystemAppend string `yaml:"system_append"`
	// SystemOverride fully replaces Wright's default behavior guidance
	// (identity, scope discipline, guardrails). ADVANCED: Wright's
	// operational contract (no self-commit/push, tool/path rules) is a
	// separate, always-enforced block regardless of this setting, but
	// everything else the default guidance provides is discarded. Only use
	// this if you know exactly what you're replacing.
	SystemOverride string `yaml:"system_override"`
}

// applyDefaults fills in omitted optional fields. It is called by Load before
// validation.
func (c *Config) applyDefaults() {
	if c.Cache.Dir == "" {
		c.Cache.Dir = DefaultCacheDir
	}
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

		if rc.Retry.Strategy == "" {
			rc.Retry.Strategy = DefaultRetryStrategy
		}
		if rc.Retry.MaxAttempts == 0 {
			rc.Retry.MaxAttempts = DefaultRetryMaxAttempts
		}
		if rc.Retry.BaseDelayMS == 0 {
			rc.Retry.BaseDelayMS = DefaultRetryBaseDelayMS
		}
		if rc.Retry.MaxDelayMS == 0 {
			rc.Retry.MaxDelayMS = DefaultRetryMaxDelayMS
		}
		if rc.Retry.Exponent == 0 {
			rc.Retry.Exponent = DefaultRetryExponent
		}
	}
}
