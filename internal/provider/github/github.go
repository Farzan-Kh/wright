// Package github implements provider.Provider against the GitHub REST API using
// google/go-github. The go-github package is imported under the alias gh to
// avoid colliding with this package's own name.
//
// Phase 0 note: PushCommits writes through the Git Data API rather than a local
// clone. See the package README section on PushCommits for the Phase 1 caveat.
package github

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v78/github"

	"github.com/farzan-kh/wright/internal/provider"
)

// providerName is the identifier returned by Name and matched by the factory.
const providerName = "github"

// Client adapts the GitHub API to provider.Provider.
type Client struct {
	gh *gh.Client
}

// Ensure Client satisfies the interface.
var _ provider.Provider = (*Client)(nil)

// New builds a GitHub-backed provider authenticated with token. When
// apiBaseURL is non-empty it targets a GitHub Enterprise Server instance;
// otherwise it targets github.com.
func New(token, apiBaseURL string) (*Client, error) {
	client := gh.NewClient(nil).WithAuthToken(token)
	if apiBaseURL != "" {
		var err error
		client, err = client.WithEnterpriseURLs(apiBaseURL, apiBaseURL)
		if err != nil {
			return nil, fmt.Errorf("github: configure enterprise URLs %q: %w", apiBaseURL, err)
		}
	}
	return &Client{gh: client}, nil
}

// Name implements provider.Provider.
func (c *Client) Name() string { return providerName }

// splitRepo splits an "owner/name" full path into its two parts. GitHub repos
// are always exactly owner/name.
func splitRepo(repo provider.Repo) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repo.FullPath, "/")
	if !ok || owner == "" || name == "" {
		return "", "", fmt.Errorf("github: invalid repo path %q, want owner/name", repo.FullPath)
	}
	return owner, name, nil
}

// classify maps a go-github transport error onto one of provider's sentinel
// errors, returning the original error unchanged when nothing matches. Callers
// wrap the result with per-operation context using %w.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var rle *gh.RateLimitError
	var arle *gh.AbuseRateLimitError
	if errors.As(err, &rle) || errors.As(err, &arle) {
		return provider.ErrRateLimited
	}
	if code, ok := statusCode(err); ok {
		switch code {
		case http.StatusNotFound:
			return provider.ErrNotFound
		case http.StatusUnauthorized, http.StatusForbidden:
			return provider.ErrAuth
		case http.StatusTooManyRequests:
			return provider.ErrRateLimited
		}
	}
	return err
}

// statusCode extracts the HTTP status from a go-github ErrorResponse.
func statusCode(err error) (int, bool) {
	var er *gh.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		return er.Response.StatusCode, true
	}
	return 0, false
}
