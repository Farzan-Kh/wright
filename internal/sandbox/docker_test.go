//go:build docker

package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/farzan-kh/patchr/internal/retry"
)

func TestDockerTaskToolExec(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	o, err := NewDocker(retry.Config{})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}

	task, err := o.Start(ctx, TaskSpec{
		Image:   "alpine/git:2.47.2",
		Workdir: "/workspace",
		RepoDir: "repo",
	})
	if err != nil {
		t.Skipf("docker daemon/image unavailable: %v", err)
	}
	defer func() {
		_ = task.Close(context.Background())
	}()

	if err := task.WriteFile(ctx, "a/b.txt", "hello\nworld\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	content, err := task.ReadFile(ctx, "a/b.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if content != "hello\nworld\n" {
		t.Fatalf("content = %q", content)
	}

	if err := task.ReplaceText(ctx, "a/b.txt", "world", "patchr"); err != nil {
		t.Fatalf("ReplaceText: %v", err)
	}
	content, err = task.ReadFile(ctx, "a/b.txt")
	if err != nil {
		t.Fatalf("ReadFile after ReplaceText: %v", err)
	}
	if content != "hello\npatchr\n" {
		t.Fatalf("content after replace = %q", content)
	}

	exists, err := task.Exists(ctx, "a/b.txt")
	if err != nil {
		t.Fatalf("Exists existing: %v", err)
	}
	if !exists {
		t.Fatal("Exists(existing) = false, want true")
	}
	exists, err = task.Exists(ctx, "a/missing.txt")
	if err != nil {
		t.Fatalf("Exists missing: %v", err)
	}
	if exists {
		t.Fatal("Exists(missing) = true, want false")
	}

	out, err := task.Bash(ctx, "pwd")
	if err != nil {
		t.Fatalf("Bash pwd: %v", err)
	}
	if !strings.Contains(out, "/workspace/repo") {
		t.Fatalf("pwd output = %q, want path to /workspace/repo", out)
	}
}
