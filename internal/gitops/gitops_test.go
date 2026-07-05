package gitops

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farzan-kh/wright/internal/provider"
	"github.com/farzan-kh/wright/internal/retry"
	"github.com/farzan-kh/wright/internal/sandbox"
)

func TestBranchName(t *testing.T) {
	if got := BranchName(42); got != "wright/issue-42" {
		t.Fatalf("BranchName = %q, want wright/issue-42", got)
	}
}

func TestInjectTokenIntoRemoteURL(t *testing.T) {
	got, err := InjectTokenIntoRemoteURL("https://github.com/acme/widgets.git", "tok")
	if err != nil {
		t.Fatalf("InjectTokenIntoRemoteURL: %v", err)
	}
	want := "https://x-access-token:tok@github.com/acme/widgets.git"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInjectCredentialIntoRemoteURL(t *testing.T) {
	got, err := InjectCredentialIntoRemoteURL("https://gitlab.com/group/app.git", "oauth2", "tok")
	if err != nil {
		t.Fatalf("InjectCredentialIntoRemoteURL: %v", err)
	}
	want := "https://oauth2:tok@gitlab.com/group/app.git"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInjectTokenIntoRemoteURLRejectsNonHTTPS(t *testing.T) {
	if _, err := InjectTokenIntoRemoteURL("ssh://git@github.com/acme/widgets.git", "tok"); err == nil {
		t.Fatal("expected error for non-https remote")
	}
}

func fakeExecForPush(pushErrUntil func() error) *sandbox.FakeExec {
	var mu sync.Mutex
	pushCalls := 0
	return &sandbox.FakeExec{
		BashFn: func(command string) (string, error) {
			switch {
			case strings.HasPrefix(command, "git diff"):
				return " 1 file changed", nil
			case strings.HasPrefix(command, "git push"):
				mu.Lock()
				pushCalls++
				mu.Unlock()
				if pushErrUntil != nil {
					if err := pushErrUntil(); err != nil {
						return "", err
					}
				}
				return "", nil
			default:
				return "", nil
			}
		},
	}
}

func TestCommitAndPushRetriesTransientPushFailure(t *testing.T) {
	calls := 0
	exec := fakeExecForPush(func() error {
		calls++
		if calls < 3 {
			return errors.New("connection reset")
		}
		return nil
	})
	ops := &Ops{
		Exec:  exec,
		Repo:  provider.Repo{FullPath: "acme/widgets"},
		Retry: retry.Config{Strategy: retry.Fixed, MaxAttempts: 5, BaseDelay: time.Millisecond},
	}
	branch, diff, err := ops.CommitAndPush(context.Background(), 7, "https://github.com/acme/widgets.git", "tok", "wright: fix")
	if err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}
	if branch != "wright/issue-7" {
		t.Errorf("branch = %q, want wright/issue-7", branch)
	}
	if diff == "" {
		t.Errorf("diffSummary is empty")
	}
	if calls != 3 {
		t.Errorf("push calls = %d, want 3", calls)
	}
}

func TestCommitAndPushExhaustsRetriesOnPersistentFailure(t *testing.T) {
	calls := 0
	exec := fakeExecForPush(func() error {
		calls++
		return errors.New("connection reset")
	})
	ops := &Ops{
		Exec:  exec,
		Repo:  provider.Repo{FullPath: "acme/widgets"},
		Retry: retry.Config{Strategy: retry.Fixed, MaxAttempts: 3, BaseDelay: time.Millisecond},
	}
	_, _, err := ops.CommitAndPush(context.Background(), 7, "https://github.com/acme/widgets.git", "tok", "wright: fix")
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 {
		t.Errorf("push calls = %d, want 3 (MaxAttempts)", calls)
	}
}
