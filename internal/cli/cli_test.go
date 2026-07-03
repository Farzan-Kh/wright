package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/farzan-kh/patchr/internal/version"
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
      model: claude-sonnet-4-5
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "patchr.yaml")
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
		t.Setenv("PATCHR_GITHUB_TOKEN", "dummy")
		out, err := run("validate", "--config", path)
		if err != nil {
			t.Fatalf("validate: %v (out: %s)", err, out)
		}
		if !strings.Contains(out, "is valid") {
			t.Errorf("output missing validity line: %s", out)
		}
	})

	t.Run("missing_token", func(t *testing.T) {
		t.Setenv("PATCHR_GITHUB_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
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
