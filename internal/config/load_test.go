package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValid(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		c, err := Load(filepath.Join("testdata", "valid_full.yaml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := len(c.Repos); got != 2 {
			t.Fatalf("repos: got %d, want 2", got)
		}
		gh := c.Repos[0]
		if gh.Provider != ProviderGitHub || gh.Repo != "acme/widgets" {
			t.Errorf("repo[0] provider/repo = %q/%q", gh.Provider, gh.Repo)
		}
		if gh.TriggerLabel != "fixme" {
			t.Errorf("repo[0] trigger_label = %q, want fixme", gh.TriggerLabel)
		}
		if gh.BaseBranch != "develop" || !gh.AutoMerge {
			t.Errorf("repo[0] base_branch=%q auto_merge=%v", gh.BaseBranch, gh.AutoMerge)
		}
		if gh.Budget.MaxTurns != 40 {
			t.Errorf("repo[0] budget = %+v", gh.Budget)
		}
		if gh.TokenEnv != "ACME_GH_TOKEN" {
			t.Errorf("repo[0] token_env = %q", gh.TokenEnv)
		}
		if gh.LLM.Auth != DefaultLLMAuth || gh.LLM.AgentModel != "claude-sonnet-4-5" || gh.LLM.GateModel != DefaultGateModel {
			t.Errorf("repo[0] llm defaults/legacy model mapping = %+v", gh.LLM)
		}
		gl := c.Repos[1]
		if gl.Provider != ProviderGitLab || gl.Repo != "acme/group/subgroup/service" {
			t.Errorf("repo[1] provider/repo = %q/%q", gl.Provider, gl.Repo)
		}
	})

	t.Run("minimal_applies_defaults", func(t *testing.T) {
		c, err := Load(filepath.Join("testdata", "minimal.yaml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		rc := c.Repos[0]
		if rc.TriggerLabel != DefaultTriggerLabel {
			t.Errorf("trigger_label = %q, want default %q", rc.TriggerLabel, DefaultTriggerLabel)
		}
		if rc.AutoMerge {
			t.Errorf("auto_merge defaulted to true")
		}
		if rc.BaseBranch != "" {
			t.Errorf("base_branch = %q, want empty (resolved at run time)", rc.BaseBranch)
		}
		if rc.LLM.AgentModel != "claude-sonnet-4-5" || rc.LLM.GateModel != DefaultGateModel || rc.LLM.Effort != DefaultLLMEffort {
			t.Errorf("llm defaults = %+v", rc.LLM)
		}
		if rc.Sandbox.Image != DefaultSandboxImage || rc.Sandbox.Workdir != DefaultSandboxWorkdir {
			t.Errorf("sandbox defaults = %+v", rc.Sandbox)
		}
	})

	t.Run("llm_api_key_mode", func(t *testing.T) {
		c, err := Load(filepath.Join("testdata", "valid_llm_api_key.yaml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		rc := c.Repos[0]
		if rc.LLM.Auth != "api_key" || rc.LLM.APIKeyEnv != "MY_ANTHROPIC_KEY" {
			t.Fatalf("unexpected llm api key config: %+v", rc.LLM)
		}
	})

	t.Run("llm_oauth_mode", func(t *testing.T) {
		c, err := Load(filepath.Join("testdata", "valid_llm_oauth.yaml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		rc := c.Repos[0]
		if rc.LLM.Auth != "oauth" || rc.LLM.OAuth.AccessTokenEnv == "" || rc.LLM.OAuth.TokenURL == "" {
			t.Fatalf("unexpected llm oauth config: %+v", rc.LLM)
		}
	})

	t.Run("prompt_append_only", func(t *testing.T) {
		c, err := Load(filepath.Join("testdata", "prompt_append_only.yaml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		rc := c.Repos[0]
		if rc.Prompt.SystemAppend != "Always update CHANGELOG.md." {
			t.Fatalf("prompt.system_append = %q", rc.Prompt.SystemAppend)
		}
		if rc.Prompt.SystemOverride != "" {
			t.Fatalf("prompt.system_override = %q, want empty", rc.Prompt.SystemOverride)
		}
	})

	t.Run("llm_openrouter_mode", func(t *testing.T) {
		c, err := Load(filepath.Join("testdata", "valid_llm_openrouter.yaml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		rc := c.Repos[0]
		if rc.LLM.Provider != LLMProviderOpenRouter {
			t.Fatalf("provider = %q, want openrouter", rc.LLM.Provider)
		}
		if rc.LLM.APIKeyEnv != "MY_OPENROUTER_KEY" {
			t.Fatalf("api_key_env = %q, want MY_OPENROUTER_KEY", rc.LLM.APIKeyEnv)
		}
		if rc.LLM.AgentModel != "anthropic/claude-3-5-sonnet" {
			t.Fatalf("agent_model = %q", rc.LLM.AgentModel)
		}
		if rc.LLM.GateModel != "openai/gpt-4o-mini" {
			t.Fatalf("gate_model = %q", rc.LLM.GateModel)
		}
	})
}

func TestLoadInvalid(t *testing.T) {
	cases := []struct {
		file string
		want string // substring expected in the error
	}{
		{"unknown_field.yaml", "field triger_label not found"},
		{"bad_version.yaml", "version must be 1"},
		{"no_repos.yaml", "at least one repo"},
		{"bad_provider.yaml", "is not github|gitlab"},
		{"bad_repo_path.yaml", "owner/name"},
		{"empty_trigger.yaml", "trigger_label must not be empty"},
		{"negative_budget.yaml", "budget.max_turns must be >= 0"},
		{"empty_llm.yaml", "llm.provider must not be empty"},
		{"bad_llm_auth.yaml", "llm.auth"},
		{"oauth_missing_access_token_env.yaml", "llm.oauth.access_token_env"},
		{"duplicate_repos.yaml", "duplicate of repos[0]"},
		{"openrouter_oauth_invalid.yaml", "oauth is not supported for openrouter"},
		{"prompt_append_and_override.yaml", "prompt.system_append and prompt.system_override are mutually exclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			_, err := Load(filepath.Join("testdata", tc.file))
			if err == nil {
				t.Fatalf("Load(%s): expected error, got nil", tc.file)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load(%s) error = %q, want substring %q", tc.file, err, tc.want)
			}
		})
	}
}

// The negative-budget fixture sets an invalid provider and a negative
// max_turns; confirm Validate joins both errors rather than stopping at the
// first.
func TestValidateJoinsAllErrors(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "negative_budget.yaml"))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"is not github|gitlab", "budget.max_turns must be >= 0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}

// The committed example config must round-trip through Load unchanged, so the
// documented example never drifts from what the loader accepts.
func TestExampleConfigLoads(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "patchr.example.yaml"))
	if err != nil {
		t.Fatalf("Load(patchr.example.yaml): %v", err)
	}
	if len(c.Repos) != 1 || c.Repos[0].Provider != ProviderGitHub {
		t.Fatalf("unexpected example contents: %+v", c.Repos)
	}
	if c.Repos[0].LLM.AgentModel != "claude-sonnet-5" {
		t.Fatalf("example should default to sonnet-5, got %q", c.Repos[0].LLM.AgentModel)
	}
}

func TestSelectRepo(t *testing.T) {
	c := &Config{Repos: []RepoConfig{
		{Provider: ProviderGitHub, Repo: "a/one"},
		{Provider: ProviderGitLab, Repo: "a/two"},
	}}

	if _, err := c.SelectRepo(""); err == nil {
		t.Error("SelectRepo(\"\") with 2 repos: expected error")
	}
	rc, err := c.SelectRepo("a/two")
	if err != nil || rc.Repo != "a/two" {
		t.Errorf("SelectRepo(a/two) = %+v, %v", rc, err)
	}
	if _, err := c.SelectRepo("a/missing"); err == nil {
		t.Error("SelectRepo(a/missing): expected error")
	}

	single := &Config{Repos: []RepoConfig{{Provider: ProviderGitHub, Repo: "a/one"}}}
	rc, err = single.SelectRepo("")
	if err != nil || rc.Repo != "a/one" {
		t.Errorf("SelectRepo(\"\") with 1 repo = %+v, %v", rc, err)
	}
}

func TestTokenResolution(t *testing.T) {
	t.Run("candidates_order", func(t *testing.T) {
		gh := &RepoConfig{Provider: ProviderGitHub, TokenEnv: "CUSTOM"}
		want := []string{"CUSTOM", "PATCHR_GITHUB_TOKEN", "GITHUB_TOKEN"}
		got := gh.TokenEnvCandidates()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("github candidates = %v, want %v", got, want)
		}
		gl := &RepoConfig{Provider: ProviderGitLab}
		wantGL := []string{"PATCHR_GITLAB_TOKEN", "GITLAB_TOKEN"}
		if strings.Join(gl.TokenEnvCandidates(), ",") != strings.Join(wantGL, ",") {
			t.Errorf("gitlab candidates = %v, want %v", gl.TokenEnvCandidates(), wantGL)
		}
	})

	t.Run("explicit_wins", func(t *testing.T) {
		t.Setenv("CUSTOM_TOK", "secret-custom")
		t.Setenv("PATCHR_GITHUB_TOKEN", "secret-patchr")
		rc := &RepoConfig{Provider: ProviderGitHub, TokenEnv: "CUSTOM_TOK"}
		tok, name, ok := rc.ResolveToken()
		if !ok || tok != "secret-custom" || name != "CUSTOM_TOK" {
			t.Fatalf("ResolveToken = %q, %q, %v", tok, name, ok)
		}
	})

	t.Run("patchr_var_before_conventional", func(t *testing.T) {
		t.Setenv("PATCHR_GITLAB_TOKEN", "patchr-gl")
		t.Setenv("GITLAB_TOKEN", "plain-gl")
		rc := &RepoConfig{Provider: ProviderGitLab}
		tok, name, ok := rc.ResolveToken()
		if !ok || tok != "patchr-gl" || name != "PATCHR_GITLAB_TOKEN" {
			t.Fatalf("ResolveToken = %q, %q, %v", tok, name, ok)
		}
	})

	t.Run("none_set", func(t *testing.T) {
		// Ensure the vars this case relies on are empty within the test process.
		t.Setenv("PATCHR_GITHUB_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		rc := &RepoConfig{Provider: ProviderGitHub}
		if _, _, ok := rc.ResolveToken(); ok {
			t.Fatal("ResolveToken: expected ok=false when no vars set")
		}
	})

	t.Run("llm_api_key", func(t *testing.T) {
		t.Setenv("PATCHR_ANTHROPIC_API_KEY", "patchr-anthropic")
		cfg := LLMConfig{}
		tok, name, ok := cfg.ResolveAPIKey()
		if !ok || tok != "patchr-anthropic" || name != "PATCHR_ANTHROPIC_API_KEY" {
			t.Fatalf("ResolveAPIKey = %q, %q, %v", tok, name, ok)
		}
	})

	t.Run("openrouter_api_key_candidates", func(t *testing.T) {
		cfg := LLMConfig{Provider: LLMProviderOpenRouter}
		want := []string{"PATCHR_OPENROUTER_API_KEY", "OPENROUTER_API_KEY"}
		got := cfg.APIKeyEnvCandidates()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("openrouter candidates = %v, want %v", got, want)
		}
	})

	t.Run("openrouter_api_key_explicit_env", func(t *testing.T) {
		t.Setenv("PATCHR_OPENROUTER_API_KEY", "or-key")
		cfg := LLMConfig{Provider: LLMProviderOpenRouter}
		tok, name, ok := cfg.ResolveAPIKey()
		if !ok || tok != "or-key" || name != "PATCHR_OPENROUTER_API_KEY" {
			t.Fatalf("ResolveAPIKey = %q, %q, %v", tok, name, ok)
		}
	})

	t.Run("llm_oauth", func(t *testing.T) {
		t.Setenv("PATCHR_CLAUDE_OAUTH_TOKEN", "oauth-token")
		t.Setenv("PATCHR_CLAUDE_OAUTH_EXPIRES_AT", "2026-12-31T00:00:00Z")
		cfg := LLMConfig{OAuth: OAuthConfig{
			AccessTokenEnv:       "PATCHR_CLAUDE_OAUTH_TOKEN",
			AccessTokenExpiryEnv: "PATCHR_CLAUDE_OAUTH_EXPIRES_AT",
		}}
		tok, exp, name, ok := cfg.ResolveOAuthAccessToken()
		if !ok || tok != "oauth-token" || name != "PATCHR_CLAUDE_OAUTH_TOKEN" {
			t.Fatalf("ResolveOAuthAccessToken = %q, %v, %q, %v", tok, exp, name, ok)
		}
		if exp.IsZero() {
			t.Fatalf("expected parsed expiry, got zero")
		}
	})
}
