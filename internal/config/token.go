package config

import (
	"os"
	"time"
)

// TokenEnvCandidates returns the ordered list of environment variable names
// consulted for this repo's token, most specific first:
//
//  1. the explicit TokenEnv, if set;
//  2. the Wright-specific, provider-scoped var (WRIGHT_GITHUB_TOKEN / WRIGHT_GITLAB_TOKEN);
//  3. the conventional provider var (GITHUB_TOKEN / GITLAB_TOKEN).
//
// Credentials themselves never live in the config file — only the names of the
// variables to read them from.
func (rc *RepoConfig) TokenEnvCandidates() []string {
	var cands []string
	if rc.TokenEnv != "" {
		cands = append(cands, rc.TokenEnv)
	}
	switch rc.Provider {
	case ProviderGitHub:
		cands = append(cands, "WRIGHT_GITHUB_TOKEN", "GITHUB_TOKEN")
	case ProviderGitLab:
		cands = append(cands, "WRIGHT_GITLAB_TOKEN", "GITLAB_TOKEN")
	}
	return cands
}

// ResolveToken returns the token from the first candidate variable that is set
// to a non-empty value, along with the variable name it came from. If none are
// set, ok is false and the caller can report TokenEnvCandidates as the vars it
// looked for.
func (rc *RepoConfig) ResolveToken() (token, envVar string, ok bool) {
	for _, name := range rc.TokenEnvCandidates() {
		if v := os.Getenv(name); v != "" {
			return v, name, true
		}
	}
	return "", "", false
}

// APIKeyEnvCandidates returns LLM API-key env var candidates, ordered most
// specific first. The list is provider-aware so that each provider's
// conventional env var names are consulted when no explicit api_key_env is set.
func (lc *LLMConfig) APIKeyEnvCandidates() []string {
	var cands []string
	if lc.APIKeyEnv != "" {
		cands = append(cands, lc.APIKeyEnv)
	}
	switch lc.Provider {
	case LLMProviderOpenRouter:
		cands = append(cands, "WRIGHT_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
	default:
		// "claude" and any unrecognised provider fall back to Anthropic vars.
		cands = append(cands, "WRIGHT_ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY")
	}
	return cands
}

// ResolveAPIKey resolves the first non-empty API key env var.
func (lc *LLMConfig) ResolveAPIKey() (token, envVar string, ok bool) {
	for _, name := range lc.APIKeyEnvCandidates() {
		if v := os.Getenv(name); v != "" {
			return v, name, true
		}
	}
	return "", "", false
}

// ResolveOAuthAccessToken resolves the OAuth access token and optional expiry.
func (lc *LLMConfig) ResolveOAuthAccessToken() (token string, expiresAt time.Time, envVar string, ok bool) {
	name := lc.OAuth.AccessTokenEnv
	if name == "" {
		name = "ANTHROPIC_OAUTH_ACCESS_TOKEN"
	}
	token = os.Getenv(name)
	if token == "" {
		return "", time.Time{}, name, false
	}
	if lc.OAuth.AccessTokenExpiryEnv != "" {
		if raw := os.Getenv(lc.OAuth.AccessTokenExpiryEnv); raw != "" {
			if ts, err := time.Parse(time.RFC3339, raw); err == nil {
				expiresAt = ts
			}
		}
	}
	return token, expiresAt, name, true
}

// ResolveOAuthRefresh returns refresh fields from env if configured.
func (lc *LLMConfig) ResolveOAuthRefresh() (refreshToken, clientID, tokenURL string, ok bool) {
	if lc.OAuth.RefreshTokenEnv == "" || lc.OAuth.ClientIDEnv == "" || lc.OAuth.TokenURL == "" {
		return "", "", "", false
	}
	refreshToken = os.Getenv(lc.OAuth.RefreshTokenEnv)
	clientID = os.Getenv(lc.OAuth.ClientIDEnv)
	if refreshToken == "" || clientID == "" {
		return "", "", "", false
	}
	return refreshToken, clientID, lc.OAuth.TokenURL, true
}
