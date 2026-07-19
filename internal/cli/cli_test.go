// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/farzan-kh/wright/internal/config"
	"github.com/farzan-kh/wright/internal/logging"
	"github.com/farzan-kh/wright/internal/version"
)

// run executes the root command with args, capturing combined output.
func run(args ...string) (string, error) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

const sampleConfig = `version: 1
repos:
  - provider: github
    repo: acme/widgets
    llm:
      provider: claude
      model: claude-sonnet-5
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wright.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVersionCommand(t *testing.T) {
	out, err := run("version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(out) != version.Version {
		t.Errorf("version output = %q, want %q", strings.TrimSpace(out), version.Version)
	}
}

func TestValidateCommand(t *testing.T) {
	path := writeConfig(t, sampleConfig)

	t.Run("ok_with_token", func(t *testing.T) {
		t.Setenv("WRIGHT_GITHUB_TOKEN", "dummy")
		t.Setenv("ANTHROPIC_API_KEY", "dummy-llm")
		out, err := run("validate", "--config", path)
		if err != nil {
			t.Fatalf("validate: %v (out: %s)", err, out)
		}
		if !strings.Contains(out, "is valid") {
			t.Errorf("output missing validity line: %s", out)
		}
	})

	t.Run("missing_token", func(t *testing.T) {
		t.Setenv("WRIGHT_GITHUB_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("WRIGHT_ANTHROPIC_API_KEY", "")
		out, err := run("validate", "--config", path)
		if err == nil {
			t.Fatalf("validate: expected error for missing token; out: %s", out)
		}
		if !strings.Contains(err.Error(), "missing token") {
			t.Errorf("error = %v, want mention of missing token", err)
		}
	})

	t.Run("bad_config", func(t *testing.T) {
		bad := writeConfig(t, "version: 2\nrepos: []\n")
		if _, err := run("validate", "--config", bad); err == nil {
			t.Fatal("expected error for invalid config")
		}
	})

	t.Run("oauth_not_supported_in_phase1", func(t *testing.T) {
		oauthConfig := writeConfig(t, `version: 1
repos:
  - provider: github
    repo: acme/widgets
    llm:
      provider: claude
      auth: oauth
      oauth:
        access_token_env: ANTHROPIC_OAUTH_ACCESS_TOKEN
`)
		t.Setenv("WRIGHT_GITHUB_TOKEN", "dummy")
		t.Setenv("ANTHROPIC_OAUTH_ACCESS_TOKEN", "dummy-oauth-token")
		out, err := run("validate", "--config", oauthConfig)
		if err == nil {
			t.Fatalf("validate: expected error for oauth in Phase 1; out: %s", out)
		}
		if !strings.Contains(err.Error(), "not supported in Phase 1") {
			t.Errorf("error = %v, want mention of Phase 1 oauth restriction", err)
		}
	})
}

// smoke must refuse to touch a repo unless --repo is given explicitly. This
// guard runs before any config load or network call.
func TestSmokeRequiresRepo(t *testing.T) {
	path := writeConfig(t, sampleConfig)
	_, err := run("smoke", "--config", path)
	if err == nil {
		t.Fatal("smoke without --repo should error")
	}
	if !strings.Contains(err.Error(), "--repo is required") {
		t.Errorf("error = %v, want '--repo is required'", err)
	}
}

func TestBuildLLMRejectsOAuthInPhase1(t *testing.T) {
	rc := &config.RepoConfig{
		LLM: config.LLMConfig{Provider: config.LLMProviderClaude, Auth: "oauth"},
	}
	_, err := buildLLM(rc, logging.FromContext(context.Background()))
	if err == nil {
		t.Fatal("buildLLM(oauth) = nil error, want a Phase 1 not-supported error")
	}
	if !strings.Contains(err.Error(), "not supported in Phase 1") || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error = %q, want it to mention Phase 1 and api_key", err)
	}
}

func TestRunCommandHelp(t *testing.T) {
	out, err := run("run", "--help")
	if err != nil {
		t.Fatalf("run --help: %v", err)
	}
	if !strings.Contains(out, "sandbox, agent, verifier") {
		t.Fatalf("run help output missing expected text: %s", out)
	}
}
