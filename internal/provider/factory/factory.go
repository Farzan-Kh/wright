// Package factory constructs a provider.Provider from a repo's config entry.
// It lives in its own package (rather than in internal/provider) because it
// depends on the concrete github and gitlab adapters, which in turn depend on
// internal/provider — putting the factory there would create an import cycle.
package factory

import (
	"fmt"

	"github.com/farzan-kh/patchr/internal/config"
	"github.com/farzan-kh/patchr/internal/provider"
	"github.com/farzan-kh/patchr/internal/provider/github"
	"github.com/farzan-kh/patchr/internal/provider/gitlab"
)

// New constructs the Provider for a repo entry, authenticated with token. It
// switches on rc.Provider; the config layer has already validated that value.
func New(rc config.RepoConfig, token string) (provider.Provider, error) {
	switch rc.Provider {
	case config.ProviderGitHub:
		c, err := github.New(token, rc.APIBaseURL)
		if err != nil {
			return nil, err
		}
		return c, nil
	case config.ProviderGitLab:
		c, err := gitlab.New(token, rc.APIBaseURL)
		if err != nil {
			return nil, err
		}
		return c, nil
	default:
		return nil, fmt.Errorf("provider: unknown provider %q", rc.Provider)
	}
}
