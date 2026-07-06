// Package logging sets up wright's optional diagnostic logger. It is off by
// default: only the "-v"/"--verbose" CLI flag turns it on, and only then does
// anything get written, to a file rather than stdout/stderr (which are
// reserved for normal command output). Nothing in this package prints
// anywhere on its own; callers pull the logger out of a context.Context and
// log through it explicitly.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// discard is returned wherever a logger is needed but logging is disabled or
// no logger has been attached to the context. Debug/Info/Warn/Error calls on
// it are cheap no-ops, so callers never need a nil check.
var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// New opens path for append (creating it if necessary) and returns a
// Debug-level logger writing to it, plus a closer the caller must invoke when
// done to release the file. When verbose is false, path is never touched: New
// returns a discarding logger and a no-op closer.
func New(verbose bool, path string) (*slog.Logger, func() error, error) {
	if !verbose {
		return discard, func() error { return nil }, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: open %q: %w", path, err)
	}
	logger := slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, f.Close, nil
}

// ctxKey is an unexported type so context values under this key can't
// collide with keys set by other packages.
type ctxKey struct{}

// WithLogger returns a copy of ctx carrying logger, retrievable with
// FromContext.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext returns the logger attached to ctx by WithLogger, or a
// discarding logger if none was attached (e.g. in tests that build a context
// directly).
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return discard
}
