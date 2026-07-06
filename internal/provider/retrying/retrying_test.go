package retrying

import (
	"context"
	"testing"
	"time"

	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/retry"
)

// spy embeds provider.Provider (left nil) so it only needs to implement the
// methods a given test exercises; anything else would panic if called, which
// would itself be a test failure worth surfacing.
type spy struct {
	provider.Provider
	calls     int
	failUntil int // fail this many times before succeeding
	err       error
}

func (s *spy) DefaultBranch(ctx context.Context, repo provider.Repo) (string, error) {
	s.calls++
	if s.calls <= s.failUntil {
		return "", s.err
	}
	return "main", nil
}

func (s *spy) CreateBranch(ctx context.Context, repo provider.Repo, branch, fromRef string) error {
	s.calls++
	if s.calls <= s.failUntil {
		return s.err
	}
	return nil
}

func testConfig() retry.Config {
	return retry.Config{Strategy: retry.Fixed, MaxAttempts: 5, BaseDelay: time.Millisecond}
}

func TestRetriesTransientErrorUntilSuccess(t *testing.T) {
	inner := &spy{failUntil: 2, err: errTransient}
	p := New(inner, testConfig())

	branch, err := p.DefaultBranch(context.Background(), provider.Repo{FullPath: "acme/widgets"})
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}
	if inner.calls != 3 {
		t.Errorf("calls = %d, want 3", inner.calls)
	}
}

func TestDoesNotRetryNotFound(t *testing.T) {
	inner := &spy{failUntil: 10, err: provider.ErrNotFound}
	p := New(inner, testConfig())

	_, err := p.DefaultBranch(context.Background(), provider.Repo{FullPath: "acme/widgets"})
	if err == nil {
		t.Fatal("expected error")
	}
	if inner.calls != 1 {
		t.Errorf("calls = %d, want 1 (ErrNotFound must not retry)", inner.calls)
	}
}

func TestDoesNotRetryAuthOrAlreadyExists(t *testing.T) {
	for _, sentinel := range []error{provider.ErrAuth, provider.ErrAlreadyExists, provider.ErrInvalidRequest} {
		inner := &spy{failUntil: 10, err: sentinel}
		p := New(inner, testConfig())

		err := p.CreateBranch(context.Background(), provider.Repo{FullPath: "acme/widgets"}, "feature", "main")
		if err == nil {
			t.Fatal("expected error")
		}
		if inner.calls != 1 {
			t.Errorf("sentinel %v: calls = %d, want 1", sentinel, inner.calls)
		}
	}
}

func TestExhaustsMaxAttemptsOnPersistentTransientError(t *testing.T) {
	inner := &spy{failUntil: 100, err: errTransient}
	p := New(inner, testConfig())

	_, err := p.DefaultBranch(context.Background(), provider.Repo{FullPath: "acme/widgets"})
	if err == nil {
		t.Fatal("expected error")
	}
	if inner.calls != 5 {
		t.Errorf("calls = %d, want 5 (MaxAttempts)", inner.calls)
	}
}

var errTransient = &transientErr{}

type transientErr struct{}

func (*transientErr) Error() string { return "connection reset" }
