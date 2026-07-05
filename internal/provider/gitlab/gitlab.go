// Package gitlab implements provider.Provider against the GitLab REST API using
// the official gitlab.com/gitlab-org/api/client-go (the successor to the
// archived xanzy/go-gitlab). The client-go package is imported under the alias
// gl to avoid colliding with this package's own name.
//
// GitLab vocabulary maps onto the GitHub-flavored domain types: a merge
// request's IID becomes provider.PullRequest.Number, a note becomes a comment,
// and the project full path is used directly as the project ID.
//
// Phase 0 note: PushCommits writes through the Commits API rather than a local
// clone. See the package README section on PushCommits for the Phase 1 caveat.
package gitlab

import (
	"context"
	"errors"
	"net/http"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/farzan-kh/wright/internal/provider"
)

// providerName is the identifier returned by Name and matched by the factory.
const providerName = "gitlab"

// Client adapts the GitLab API to provider.Provider.
type Client struct {
	gl *gl.Client
}

var _ provider.Provider = (*Client)(nil)

// New builds a GitLab-backed provider authenticated with token. When
// apiBaseURL is non-empty it targets a self-managed GitLab instance; otherwise
// it targets gitlab.com.
func New(token, apiBaseURL string) (*Client, error) {
	var opts []gl.ClientOptionFunc
	if apiBaseURL != "" {
		opts = append(opts, gl.WithBaseURL(apiBaseURL))
	}
	c, err := gl.NewClient(token, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{gl: c}, nil
}

// Name implements provider.Provider.
func (c *Client) Name() string { return providerName }

// pid returns the project ID used by client-go calls: the full project path,
// passed through directly (client-go URL-escapes it).
func pid(repo provider.Repo) string { return repo.FullPath }

// classify maps a client-go transport error onto one of provider's sentinel
// errors, returning the original error unchanged when nothing matches.
func classify(err error) error {
	if err == nil {
		return nil
	}
	// client-go returns its own sentinel (not an *ErrorResponse) for 404s.
	if errors.Is(err, gl.ErrNotFound) {
		return provider.ErrNotFound
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

// statusCode extracts the HTTP status from a client-go ErrorResponse.
func statusCode(err error) (int, bool) {
	var er *gl.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		return er.Response.StatusCode, true
	}
	return 0, false
}

// isAlreadyExists reports whether err indicates the target already exists.
// GitLab answers branch-creation conflicts with 400 and a message rather than a
// dedicated status, so the message is inspected too.
func isAlreadyExists(err error) bool {
	var er *gl.ErrorResponse
	if errors.As(err, &er) {
		if strings.Contains(strings.ToLower(er.Message), "already exists") {
			return true
		}
	}
	if code, ok := statusCode(err); ok {
		return code == http.StatusConflict
	}
	return false
}

// ctx wraps a context as a client-go request option.
func ctxOpt(ctx context.Context) gl.RequestOptionFunc { return gl.WithContext(ctx) }
