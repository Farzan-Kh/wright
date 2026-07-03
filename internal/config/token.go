package config

import "os"

// TokenEnvCandidates returns the ordered list of environment variable names
// consulted for this repo's token, most specific first:
//
//  1. the explicit TokenEnv, if set;
//  2. the Patchr-specific, provider-scoped var (PATCHR_GITHUB_TOKEN / PATCHR_GITLAB_TOKEN);
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
		cands = append(cands, "PATCHR_GITHUB_TOKEN", "GITHUB_TOKEN")
	case ProviderGitLab:
		cands = append(cands, "PATCHR_GITLAB_TOKEN", "GITLAB_TOKEN")
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
