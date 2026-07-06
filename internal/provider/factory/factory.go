// Package factory constructs a provider.Provider from a repo's config entry.
// It lives in its own package (rather than in internal/provider) because it
// depends on the concrete github and gitlab adapters, which in turn depend on
// internal/provider — putting the factory there would create an import cycle.
package factory

import (
	"fmt"
	"log/slog"

	"github.com/farzan-kh/wright/internal/config"
	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/provider/github"
	"github.com/farzan-kh/wright/internal/provider/gitlab"
	"github.com/farzan-kh/wright/internal/provider/logging"
	"github.com/farzan-kh/wright/internal/provider/retrying"
)

// New constructs the Provider for a repo entry, authenticated with token. It
// switches on rc.Provider; the config layer has already validated that value.
// The returned Provider logs every call to log (see internal/provider/logging;
// pass a discarding logger, e.g. from internal/logging, to disable that) and
// retries connection attempts per rc.Retry. Logging wraps the raw adapter
// rather than the retry layer, so every retry attempt is logged individually,
// not just the final one.
func New(rc config.RepoConfig, token string, log *slog.Logger) (provider.Provider, error) {
	var (
		c   provider.Provider
		err error
	)
	switch rc.Provider {
	case config.ProviderGitHub:
		c, err = github.New(token, rc.APIBaseURL)
	case config.ProviderGitLab:
		c, err = gitlab.New(token, rc.APIBaseURL)
	default:
		return nil, fmt.Errorf("provider: unknown provider %q", rc.Provider)
	}
	if err != nil {
		return nil, err
	}
	return retrying.New(logging.New(c, log), rc.Retry.ToRetryConfig()), nil
}
