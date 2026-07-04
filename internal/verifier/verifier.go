// Package verifier detects and runs a repository's native test command.
package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/farzan-kh/patchr/internal/sandbox"
)

// ErrNoCommand indicates no test command could be detected.
var ErrNoCommand = errors.New("verifier: no test command detected")

// Verifier runs the detected test command via sandbox tools.
type Verifier struct {
	OverrideCommand string
}

// DetectCommand chooses a test command from common repo markers.
func (v *Verifier) DetectCommand(ctx context.Context, exec sandbox.ToolExec) (string, error) {
	if strings.TrimSpace(v.OverrideCommand) != "" {
		return v.OverrideCommand, nil
	}

	if ok, _ := exec.Exists(ctx, "go.mod"); ok {
		return "go test ./...", nil
	}
	if ok, _ := exec.Exists(ctx, "package.json"); ok {
		pkg, err := exec.ReadFile(ctx, "package.json")
		if err == nil && hasNPMTestScript(pkg) {
			return "npm test", nil
		}
	}
	if ok, _ := exec.Exists(ctx, "pytest.ini"); ok {
		return "pytest", nil
	}
	if ok, _ := exec.Exists(ctx, "pyproject.toml"); ok {
		pyproject, err := exec.ReadFile(ctx, "pyproject.toml")
		if err == nil && strings.Contains(pyproject, "pytest") {
			return "pytest", nil
		}
	}
	if ok, _ := exec.Exists(ctx, "Makefile"); ok {
		mk, err := exec.ReadFile(ctx, "Makefile")
		if err == nil && hasMakeTestTarget(mk) {
			return "make test", nil
		}
	}
	return "", ErrNoCommand
}

// Verify executes the detected or overridden command and returns combined output.
func (v *Verifier) Verify(ctx context.Context, exec sandbox.ToolExec) (string, error) {
	cmd, err := v.DetectCommand(ctx, exec)
	if err != nil {
		return "", err
	}
	out, err := exec.Bash(ctx, cmd)
	if err != nil {
		return out, fmt.Errorf("verifier: %s: %w", cmd, err)
	}
	return out, nil
}

// hasNPMTestScript reports whether package.json declares a "test" entry under
// "scripts". Parses the JSON rather than substring-matching so unrelated
// fields (e.g. a "test" value under "keywords" or NODE_ENV) can't false-positive.
func hasNPMTestScript(pkgJSON string) bool {
	var parsed struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(pkgJSON), &parsed); err != nil {
		return false
	}
	_, ok := parsed.Scripts["test"]
	return ok
}

func hasMakeTestTarget(mk string) bool {
	for line := range strings.SplitSeq(mk, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "test:") {
			return true
		}
	}
	return false
}
