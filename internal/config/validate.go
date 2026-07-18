// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"net/url"
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

	if rc.Budget.MaxTurns < 0 {
		errs = append(errs, p("budget.max_turns must be >= 0, got %d", rc.Budget.MaxTurns))
	}
	if rc.Budget.MaxTotalTokens < 0 {
		errs = append(errs, p("budget.max_total_tokens must be >= 0, got %d", rc.Budget.MaxTotalTokens))
	}
	if rc.Budget.MaxUSD*0 != 0 {
		errs = append(errs, p("budget.max_usd must be a finite number, got %v", rc.Budget.MaxUSD))
	} else if rc.Budget.MaxUSD < 0 {
		errs = append(errs, p("budget.max_usd must be >= 0, got %v", rc.Budget.MaxUSD))
	}

	if strings.TrimSpace(rc.LLM.Provider) == "" {
		errs = append(errs, p("llm.provider must not be empty"))
	}
	switch rc.LLM.Auth {
	case "api_key", "oauth":
	case "":
		errs = append(errs, p("llm.auth must not be empty (api_key|oauth)"))
	default:
		errs = append(errs, p("llm.auth %q is not api_key|oauth", rc.LLM.Auth))
	}
	if strings.TrimSpace(rc.LLM.AgentModel) == "" {
		errs = append(errs, p("llm.agent_model must not be empty"))
	}
	if strings.TrimSpace(rc.LLM.GateModel) == "" {
		errs = append(errs, p("llm.gate_model must not be empty"))
	}
	switch rc.LLM.Effort {
	case "low", "medium", "high":
	case "":
		errs = append(errs, p("llm.effort must not be empty (low|medium|high)"))
	default:
		errs = append(errs, p("llm.effort %q is not low|medium|high", rc.LLM.Effort))
	}

	if rc.LLM.Provider == LLMProviderOpenRouter && rc.LLM.Auth == "oauth" {
		errs = append(errs, p("llm.auth oauth is not supported for openrouter; use api_key"))
	}

	// Validate rates: all values must be non-negative and finite.
	for modelID, r := range rc.LLM.Rates {
		if r.InputPerMTok*0 != 0 {
			errs = append(errs, p("llm.rates[%q].input_per_mtok must be finite, got %v", modelID, r.InputPerMTok))
		} else if r.InputPerMTok < 0 {
			errs = append(errs, p("llm.rates[%q].input_per_mtok must be >= 0, got %v", modelID, r.InputPerMTok))
		}
		if r.OutputPerMTok*0 != 0 {
			errs = append(errs, p("llm.rates[%q].output_per_mtok must be finite, got %v", modelID, r.OutputPerMTok))
		} else if r.OutputPerMTok < 0 {
			errs = append(errs, p("llm.rates[%q].output_per_mtok must be >= 0, got %v", modelID, r.OutputPerMTok))
		}
		if r.CacheReadPerMTok*0 != 0 {
			errs = append(errs, p("llm.rates[%q].cache_read_per_mtok must be finite, got %v", modelID, r.CacheReadPerMTok))
		} else if r.CacheReadPerMTok < 0 {
			errs = append(errs, p("llm.rates[%q].cache_read_per_mtok must be >= 0, got %v", modelID, r.CacheReadPerMTok))
		}
		if r.CacheWritePerMTok*0 != 0 {
			errs = append(errs, p("llm.rates[%q].cache_write_per_mtok must be finite, got %v", modelID, r.CacheWritePerMTok))
		} else if r.CacheWritePerMTok < 0 {
			errs = append(errs, p("llm.rates[%q].cache_write_per_mtok must be >= 0, got %v", modelID, r.CacheWritePerMTok))
		}
	}

	// When max_usd > 0, rates must be configured for both agent_model and gate_model.
	if rc.Budget.MaxUSD > 0 {
		if rc.LLM.Rates == nil {
			errs = append(errs, p("budget.max_usd > 0 requires llm.rates, but none are configured"))
		} else {
			if _, ok := rc.LLM.Rates[rc.LLM.AgentModel]; !ok {
				errs = append(errs, p("budget.max_usd > 0 requires a rates entry for llm.agent_model %q", rc.LLM.AgentModel))
			}
			if _, ok := rc.LLM.Rates[rc.LLM.GateModel]; !ok {
				errs = append(errs, p("budget.max_usd > 0 requires a rates entry for llm.gate_model %q", rc.LLM.GateModel))
			}
		}
	}

	if strings.TrimSpace(rc.Prompt.SystemAppend) != "" && strings.TrimSpace(rc.Prompt.SystemOverride) != "" {
		errs = append(errs, p("prompt.system_append and prompt.system_override are mutually exclusive"))
	}

	switch rc.Retry.Strategy {
	case RetryStrategyExponential, RetryStrategyFixed:
	default:
		errs = append(errs, p("retry.strategy %q is not exponential|fixed", rc.Retry.Strategy))
	}
	if rc.Retry.MaxAttempts < 0 {
		errs = append(errs, p("retry.max_attempts must be >= 0, got %d", rc.Retry.MaxAttempts))
	}
	if rc.Retry.BaseDelayMS < 0 {
		errs = append(errs, p("retry.base_delay_ms must be >= 0, got %d", rc.Retry.BaseDelayMS))
	}
	if rc.Retry.MaxDelayMS < 0 {
		errs = append(errs, p("retry.max_delay_ms must be >= 0, got %d", rc.Retry.MaxDelayMS))
	}
	if rc.Retry.Exponent < 0 {
		errs = append(errs, p("retry.exponent must be >= 0, got %v", rc.Retry.Exponent))
	}

	if rc.LLM.Auth == "oauth" {
		if strings.TrimSpace(rc.LLM.OAuth.AccessTokenEnv) == "" {
			errs = append(errs, p("llm.oauth.access_token_env is required in oauth mode"))
		}
		if rc.LLM.OAuth.RefreshTokenEnv != "" || rc.LLM.OAuth.ClientIDEnv != "" || rc.LLM.OAuth.TokenURL != "" {
			if rc.LLM.OAuth.RefreshTokenEnv == "" || rc.LLM.OAuth.ClientIDEnv == "" || rc.LLM.OAuth.TokenURL == "" {
				errs = append(errs, p("llm.oauth refresh requires refresh_token_env, client_id_env, and token_url together"))
			}
			if rc.LLM.OAuth.TokenURL != "" {
				u, err := url.Parse(rc.LLM.OAuth.TokenURL)
				if err != nil || u.Scheme == "" || u.Host == "" {
					errs = append(errs, p("llm.oauth.token_url %q is not a valid absolute URL", rc.LLM.OAuth.TokenURL))
				}
			}
		}
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
