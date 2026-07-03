package provider

import "errors"

// Sentinel errors that adapters map provider HTTP statuses onto. Callers check
// them with errors.Is; adapters wrap them with context using %w, e.g.
//
//	fmt.Errorf("github: create branch %q in %s: %w", branch, repo.FullPath, provider.ErrAlreadyExists)
var (
	// ErrNotFound is returned when a repo, issue, branch, or PR does not exist
	// (HTTP 404).
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists is returned when creating something that already exists,
	// e.g. a branch (HTTP 409/422 depending on provider).
	ErrAlreadyExists = errors.New("already exists")

	// ErrAuth is returned for authentication or authorization failures
	// (HTTP 401/403 that are not rate limiting).
	ErrAuth = errors.New("authentication failed")

	// ErrRateLimited is returned when the provider reports rate limiting
	// (HTTP 429, or GitHub's 403 with a rate-limit signal).
	ErrRateLimited = errors.New("rate limited")
)
