// SPDX-License-Identifier: Apache-2.0

// Package logging decorates an llm.LLMProvider with structured logging of
// every call: the request shape on entry, and the duration plus outcome (a
// brief usage summary, or the full error chain) on exit.
//
// It wraps the innermost (claude/openrouter) client rather than the outer
// retrying.Provider, so every retry attempt is logged individually — that
// visibility into what each attempt actually failed with is the point: the
// error a caller ultimately sees is often just the last attempt's, which can
// hide what was going wrong across earlier ones.
package logging

import (
	"context"
	"log/slog"
	"time"

	"github.com/farzan-kh/wright/internal/agent/llm"
)

// Provider wraps an inner llm.LLMProvider, logging every call to log.
type Provider struct {
	inner llm.LLMProvider
	log   *slog.Logger
}

var _ llm.LLMProvider = (*Provider)(nil)

// New wraps inner with call logging, tagging every line with name (e.g.
// "claude" or "openrouter", since llm.LLMProvider has no Name method of its
// own). log must not be nil; pass a discarding logger (see internal/logging)
// to disable output.
func New(inner llm.LLMProvider, log *slog.Logger, name string) *Provider {
	return &Provider{inner: inner, log: log.With("llm", name)}
}

func (p *Provider) CreateMessage(ctx context.Context, req llm.MessageRequest) (llm.MessageResponse, error) {
	l := p.log.With(
		"method", "CreateMessage",
		"model", req.Model,
		"max_tokens", req.MaxTokens,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
		"thinking_on", req.ThinkingOn,
	)
	l.Debug("llm call started")
	start := time.Now()

	resp, err := p.inner.CreateMessage(ctx, req)
	dur := time.Since(start)
	if err != nil {
		l.Error("llm call failed", "duration_ms", dur.Milliseconds(), "error", err.Error())
		return resp, err
	}
	l.Debug("llm call ok",
		"duration_ms", dur.Milliseconds(),
		"stop_reason", resp.StopReason,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
	)
	return resp, nil
}
