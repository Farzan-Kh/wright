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
		if gh.Budget.MaxUSD != 5.00 || gh.Budget.MaxTurns != 40 {
			t.Errorf("repo[0] budget = %+v", gh.Budget)
		}
		if gh.TokenEnv != "ACME_GH_TOKEN" {
			t.Errorf("repo[0] token_env = %q", gh.TokenEnv)
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
		{"negative_budget.yaml", "budget.max_usd must be >= 0"},
		{"empty_llm.yaml", "llm.provider must not be empty"},
		{"duplicate_repos.yaml", "duplicate of repos[0]"},
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

// The negative-budget fixture sets both max_usd and max_turns negative; confirm
// Validate joins both errors rather than stopping at the first.
func TestValidateJoinsAllErrors(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "negative_budget.yaml"))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"budget.max_usd must be >= 0", "budget.max_turns must be >= 0"} {
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
}
