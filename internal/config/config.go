// Package config defines Patchr's YAML configuration format and the routines to
// load and validate it. Credentials are never part of the config file; tokens
// are resolved from environment variables (see token.go).
package config

// Provider identifiers accepted in the config.
const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
)

// DefaultTriggerLabel is applied when a repo entry omits trigger_label.
const DefaultTriggerLabel = "patchr"

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
	// LLM selects the model that resolves issues for this repo.
	LLM LLMConfig `yaml:"llm"`
}

// BudgetConfig bounds the cost of resolving a single issue. The unit of MaxUSD
// is still an open question (see PROJECT_BRIEF.md); the schema holds it now and
// enforcement lands in Phase 1.
type BudgetConfig struct {
	MaxUSD   float64 `yaml:"max_usd"`
	MaxTurns int     `yaml:"max_turns"`
}

// LLMConfig selects the LLM provider and model.
type LLMConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// applyDefaults fills in omitted optional fields. It is called by Load before
// validation.
func (c *Config) applyDefaults() {
	for i := range c.Repos {
		if c.Repos[i].TriggerLabel == "" {
			c.Repos[i].TriggerLabel = DefaultTriggerLabel
		}
	}
}
