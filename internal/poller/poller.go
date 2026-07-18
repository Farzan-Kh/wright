// SPDX-License-Identifier: Apache-2.0

// Package poller fetches labeled issues from a provider.
package poller

import (
	"context"
	"sort"

	"github.com/farzan-kh/wright/internal/provider"
)

// Poller performs a single provider poll for one repo.
type Poller struct {
	Provider provider.Provider
	Repo     provider.Repo
	Label    string
}

// Once lists labeled issues and returns them ascending by issue number
// (oldest first). Both GitHub's and GitLab's list APIs default to
// newest-first, which processes issues in an order that has no relation to
// when they were filed; oldest-first instead means a dependency issue -
// which, being referenced by a later one, was almost always filed and
// numbered earlier - gets a chance to be resolved (and stacked on, if
// stacking is enabled) before the issue depending on it is attempted.
func (p *Poller) Once(ctx context.Context) ([]provider.Issue, error) {
	issues, err := p.Provider.ListLabeledIssues(ctx, p.Repo, p.Label)
	if err != nil {
		return nil, err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}
