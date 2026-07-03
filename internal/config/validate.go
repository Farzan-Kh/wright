package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Validate checks the whole config and returns every problem found, joined
// together (errors.Join), so a single run surfaces all issues rather than just
// the first. Defaults are assumed already applied.
func (c *Config) Validate() error {
	var errs []error

	if c.Version != 1 {
		errs = append(errs, fmt.Errorf("version must be 1, got %d", c.Version))
	}
	if len(c.Repos) == 0 {
		errs = append(errs, errors.New("at least one repo is required"))
	}

	seen := make(map[string]int) // "provider\x00repo" -> first index
	for i := range c.Repos {
		rc := &c.Repos[i]
		errs = append(errs, rc.validate(i)...)

		if rc.Provider != "" && rc.Repo != "" {
			key := rc.Provider + "\x00" + rc.Repo
			if first, ok := seen[key]; ok {
				errs = append(errs, fmt.Errorf("repos[%d]: duplicate of repos[%d] (%s %s)", i, first, rc.Provider, rc.Repo))
			} else {
				seen[key] = i
			}
		}
	}

	return errors.Join(errs...)
}

// validate checks a single repo entry, returning all problems found. idx is the
// entry's index, used for readable error messages.
func (rc *RepoConfig) validate(idx int) []error {
	var errs []error
	p := func(format string, args ...any) error {
		return fmt.Errorf("repos[%d]: "+format, append([]any{idx}, args...)...)
	}

	switch rc.Provider {
	case ProviderGitHub, ProviderGitLab:
	case "":
		errs = append(errs, p("provider is required (github|gitlab)"))
	default:
		errs = append(errs, p("provider %q is not github|gitlab", rc.Provider))
	}

	if err := validateRepoPath(rc.Repo); err != nil {
		errs = append(errs, p("repo %q: %v", rc.Repo, err))
	}

	if strings.TrimSpace(rc.TriggerLabel) == "" {
		errs = append(errs, p("trigger_label must not be empty"))
	}

	if rc.Budget.MaxUSD < 0 {
		errs = append(errs, p("budget.max_usd must be >= 0, got %v", rc.Budget.MaxUSD))
	}
	if rc.Budget.MaxTurns < 0 {
		errs = append(errs, p("budget.max_turns must be >= 0, got %d", rc.Budget.MaxTurns))
	}

	if strings.TrimSpace(rc.LLM.Provider) == "" {
		errs = append(errs, p("llm.provider must not be empty"))
	}
	if strings.TrimSpace(rc.LLM.Model) == "" {
		errs = append(errs, p("llm.model must not be empty"))
	}

	return errs
}

// validateRepoPath checks the shape of a repo/project path. It accepts
// "owner/name" and deeper GitLab paths ("group/subgroup/name"), rejecting empty
// paths, missing separators, leading/trailing slashes, empty segments, and
// whitespace.
func validateRepoPath(path string) error {
	if path == "" {
		return errors.New("must not be empty")
	}
	if strings.ContainsAny(path, " \t\n") {
		return errors.New("must not contain whitespace")
	}
	if !strings.Contains(path, "/") {
		return errors.New("must be of the form owner/name")
	}
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return errors.New("must not start or end with '/'")
	}
	if slices.Contains(strings.Split(path, "/"), "") {
		return errors.New("must not contain empty path segments")
	}
	return nil
}
