// Package retrying decorates an llm.LLMProvider with configurable retries
// around each connection attempt to the model API.
package retrying

import (
	"context"

	"github.com/farzan-kh/patchr/internal/agent/llm"
	"github.com/farzan-kh/patchr/internal/retry"
)

// Provider wraps an inner llm.LLMProvider, retrying CreateMessage per Config
// until it succeeds, exhausts its attempts, or the context is done.
//
// Unlike the provider.Provider decorator, this one has no repo-owned sentinel
// errors to distinguish permanent failures (bad request, bad API key) from
// transient ones (timeouts, rate limiting, 5xx) across every backend, so it
// retries any error the same way. MaxAttempts bounds the cost of retrying a
// permanent failure.
type Provider struct {
	inner  llm.LLMProvider
	config retry.Config
}

var _ llm.LLMProvider = (*Provider)(nil)

// New wraps inner with retry behavior controlled by cfg.
func New(inner llm.LLMProvider, cfg retry.Config) *Provider {
	return &Provider{inner: inner, config: cfg}
}

func (p *Provider) CreateMessage(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error) {
	return retry.Value(ctx, p.config, nil, func(ctx context.Context) (llm.MessageResponse, error) {
		return p.inner.CreateMessage(ctx, req)
	})
}
