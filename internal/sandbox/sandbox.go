// Package sandbox defines the isolated tool-execution abstraction used by the
// agent and verifier. Docker-backed execution is implemented in this package.
package sandbox

import (
	"context"
	"errors"
	"strings"
)

const (
	DefaultImage    = "alpine/git:2.47.2"
	DefaultWorkdir  = "/workspace"
	DefaultRepoDir  = "repo"
	DefaultGitUser  = "patchr"
	DefaultGitEmail = "patchr@local"
)

// replaceUnique replaces the single occurrence of oldText in current with
// newText. It mirrors the str_replace_based_edit_tool contract: oldText must
// match exactly once, otherwise the edit is ambiguous and is rejected rather
// than silently applied to the first match.
func replaceUnique(current, oldText, newText string) (string, error) {
	if oldText == "" {
		return "", errors.New("sandbox: replace: old text is empty")
	}
	switch strings.Count(current, oldText) {
	case 0:
		return "", errors.New("sandbox: replace: old text not found")
	case 1:
		return strings.Replace(current, oldText, newText, 1), nil
	default:
		return "", errors.New("sandbox: replace: old text is not unique; include more surrounding context")
	}
}

// ToolExec executes Claude tool calls inside an isolated repo workspace.
type ToolExec interface {
	Bash(ctx context.Context, command string) (string, error)
	ReadFile(ctx context.Context, path string) (string, error)
	WriteFile(ctx context.Context, path, content string) error
	ReplaceText(ctx context.Context, path, oldText, newText string) error
	Exists(ctx context.Context, path string) (bool, error)
}

// Task is a running sandbox task.
type Task interface {
	ToolExec
	RepoDir() string
	Close(ctx context.Context) error
}

// Orchestrator provisions per-issue isolated tasks.
type Orchestrator interface {
	Start(ctx context.Context, spec TaskSpec) (Task, error)
}

// TaskSpec configures one sandbox task.
type TaskSpec struct {
	Image      string
	Workdir    string
	RepoDir    string
	CloneURL   string
	BaseBranch string

	GitUserName  string
	GitUserEmail string
}
