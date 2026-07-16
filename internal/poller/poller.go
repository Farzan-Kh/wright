// SPDX-License-Identifier: Apache-2.0

// Package poller fetches labeled issues from a provider.
package poller

import (
	"context"

	"github.com/farzan-kh/wright/internal/provider"
)

// Poller performs a single provider poll for one repo.
type Poller struct {
	Provider provider.Provider
	Repo     provider.Repo
	Label    string
}

func (p *Poller) Once(ctx context.Context) ([]provider.Issue, error) {
	return p.Provider.ListLabeledIssues(ctx, p.Repo, p.Label)
}
