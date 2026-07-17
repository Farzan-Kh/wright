// SPDX-License-Identifier: Apache-2.0

package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"

	"github.com/farzan-kh/wright/internal/retry"
)

// discardLog is used wherever a *Docker or *dockerTask is built without an
// explicit logger (e.g. existing callers, tests), so log calls are always
// safe without a nil check.
var discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// Setup-phase steps (image pull, provisioning, mkdir, git config, clone) are
// internal bookkeeping, not agent work, so each one is bounded: a wedged
// Docker daemon call or a stalled `git clone` fails fast with a clear error
// instead of blocking the pipeline forever with no visible progress.
const (
	setupStepTimeout = 2 * time.Minute
	cloneStepTimeout = 5 * time.Minute
)

// Docker orchestrates task containers through the Docker Engine API.
type Docker struct {
	cli   *client.Client
	retry retry.Config
	log   *slog.Logger
}

var _ Orchestrator = (*Docker)(nil)

// NewDocker creates a Docker orchestrator using the local Docker environment.
// retryCfg controls retries around image pulls, the orchestrator's one
// genuine network connection attempt. log receives structured logging of
// every sandbox step; pass nil to discard it.
func NewDocker(retryCfg retry.Config, log *slog.Logger) (*Docker, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("sandbox: docker client: %w", err)
	}
	if log == nil {
		log = discardLog
	}
	return &Docker{cli: cli, retry: retryCfg, log: log}, nil
}

// NewDockerWithClient creates a Docker orchestrator from a caller-provided
// client (used mainly by tests).
func NewDockerWithClient(cli *client.Client, retryCfg retry.Config, log *slog.Logger) *Docker {
	if log == nil {
		log = discardLog
	}
	return &Docker{cli: cli, retry: retryCfg, log: log}
}

// step logs a setup step's entry and returns a logger tagged with the step
// name plus the start time, for the matching stepEnd call.
func (d *Docker) step(name string, attrs ...any) (*slog.Logger, time.Time) {
	l := d.log.With(append([]any{"step", name}, attrs...)...)
	l.Debug("sandbox step started")
	return l, time.Now()
}

func stepEnd(l *slog.Logger, start time.Time, err error, resultAttrs ...any) {
	dur := time.Since(start)
	if err != nil {
		l.Error("sandbox step failed", append([]any{"duration_ms", dur.Milliseconds(), "error", err.Error()}, resultAttrs...)...)
		return
	}
	l.Debug("sandbox step ok", append([]any{"duration_ms", dur.Milliseconds()}, resultAttrs...)...)
}

// Start creates, starts, and prepares a task container. If CloneURL is set, the
// repository is cloned into RepoDir (or DefaultRepoDir when omitted).
func (d *Docker) Start(ctx context.Context, spec TaskSpec) (Task, error) {
	if d == nil || d.cli == nil {
		return nil, errors.New("sandbox: docker orchestrator is nil")
	}

	imageName := spec.Image
	if strings.TrimSpace(imageName) == "" {
		imageName = DefaultImage
	}
	workdir := spec.Workdir
	if strings.TrimSpace(workdir) == "" {
		workdir = DefaultWorkdir
	}
	repoDirName := spec.RepoDir
	if strings.TrimSpace(repoDirName) == "" {
		repoDirName = DefaultRepoDir
	}
	gitUser := spec.GitUserName
	if strings.TrimSpace(gitUser) == "" {
		gitUser = DefaultGitUser
	}
	gitEmail := spec.GitUserEmail
	if strings.TrimSpace(gitEmail) == "" {
		gitEmail = DefaultGitEmail
	}

	l, startAll := d.step("Start", "image", imageName, "workdir", workdir, "has_clone_url", strings.TrimSpace(spec.CloneURL) != "")

	if err := d.pullImage(ctx, imageName); err != nil {
		stepEnd(l, startAll, err)
		return nil, err
	}

	cs, startCreate := d.step("container_create", "image", imageName)
	resp, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image:      imageName,
		Entrypoint: []string{"sh", "-lc"},
		Cmd:        []string{"while :; do sleep 3600; done"},
	}, taskHostConfig(), nil, nil, "")
	if err != nil {
		err = fmt.Errorf("sandbox: create container: %w", err)
		stepEnd(cs, startCreate, err)
		stepEnd(l, startAll, err)
		return nil, err
	}
	stepEnd(cs, startCreate, nil, "container_id", resp.ID)

	task := &dockerTask{
		cli:         d.cli,
		containerID: resp.ID,
		repoDir:     path.Join(workdir, repoDirName),
		log:         d.log.With("container_id", resp.ID),
	}
	ready := false
	defer func() {
		if !ready {
			_ = task.Close(context.Background())
		}
	}()

	startCtx, cancelStart := context.WithTimeout(ctx, setupStepTimeout)
	err = d.cli.ContainerStart(startCtx, resp.ID, container.StartOptions{})
	cancelStart()
	if err != nil {
		err = fmt.Errorf("sandbox: start container %s: %w", resp.ID, err)
		stepEnd(l, startAll, err)
		return nil, err
	}

	// The agent's tool contract advertises a "bash" tool, but bare images like
	// the alpine/git default ship neither bash nor python3, so agent commands
	// assuming either exists silently fail. Best-effort provision them on
	// Alpine-based images only (guarded by `command -v apk`) so custom,
	// non-Alpine sandbox.image values from repo config are left untouched.
	if _, err := task.runShellTimeout(ctx, setupStepTimeout, "/", provisionCmd); err != nil {
		err = fmt.Errorf("sandbox: provision base tools: %w", err)
		stepEnd(l, startAll, err)
		return nil, err
	}

	if _, err := task.runShellTimeout(ctx, setupStepTimeout, "/", "mkdir -p "+shellQuote(workdir)); err != nil {
		err = fmt.Errorf("sandbox: create workdir: %w", err)
		stepEnd(l, startAll, err)
		return nil, err
	}
	if _, err := task.runShellTimeout(ctx, setupStepTimeout, workdir, "mkdir -p "+shellQuote(task.repoDir)); err != nil {
		err = fmt.Errorf("sandbox: create repo dir: %w", err)
		stepEnd(l, startAll, err)
		return nil, err
	}

	if strings.TrimSpace(spec.CloneURL) != "" {
		// rm -rf first so a retry after a partial clone (e.g. connection drop
		// mid-transfer) starts clean rather than failing on a non-empty
		// destination directory.
		cloneCmd := "rm -rf " + shellQuote(repoDirName) + " && git clone --depth=1"
		if strings.TrimSpace(spec.BaseBranch) != "" {
			cloneCmd += " --branch " + shellQuote(spec.BaseBranch)
		}
		cloneCmd += " " + shellQuote(spec.CloneURL) + " " + shellQuote(repoDirName)
		cs, startClone := d.step("clone", "base_branch", spec.BaseBranch)
		attempt := 0
		err := retry.Do(ctx, d.retry, nil, func(ctx context.Context) error {
			attempt++
			_, err := task.runShellTimeout(ctx, cloneStepTimeout, workdir, cloneCmd)
			if err != nil {
				cs.Warn("sandbox clone attempt failed", "attempt", attempt, "error", err.Error())
			}
			return err
		})
		stepEnd(cs, startClone, err, "attempts", attempt)
		if err != nil {
			err = fmt.Errorf("sandbox: clone repo: %w", err)
			stepEnd(l, startAll, err)
			return nil, err
		}
	}

	if _, err := task.runShellTimeout(ctx, setupStepTimeout, task.repoDir, "git config user.name "+shellQuote(gitUser)); err != nil {
		err = fmt.Errorf("sandbox: git user.name: %w", err)
		stepEnd(l, startAll, err)
		return nil, err
	}
	if _, err := task.runShellTimeout(ctx, setupStepTimeout, task.repoDir, "git config user.email "+shellQuote(gitEmail)); err != nil {
		err = fmt.Errorf("sandbox: git user.email: %w", err)
		stepEnd(l, startAll, err)
		return nil, err
	}

	ready = true
	stepEnd(l, startAll, nil)
	return task, nil
}

// Conservative resource caps for task containers: an agent-driven bash
// command is untrusted input and shouldn't be able to exhaust host CPU,
// memory, or the PID table (e.g. via a fork bomb or runaway build).
const (
	taskNanoCPUs  = 2_000_000_000 // 2 CPUs
	taskMemory    = 2 << 30       // 2 GiB
	taskPidsLimit = 512
)

// provisionCmd installs the tools an agent-issued "bash" command commonly
// assumes exist (bash, python3, curl, make, a C toolchain). It no-ops
// harmlessly on non-Alpine images, where `command -v apk` fails.
const provisionCmd = "command -v apk >/dev/null 2>&1 && apk add --no-cache bash python3 py3-pip curl make build-base >/dev/null 2>&1 || true"

func taskHostConfig() *container.HostConfig {
	pidsLimit := int64(taskPidsLimit)
	return &container.HostConfig{
		Resources: container.Resources{
			NanoCPUs:  taskNanoCPUs,
			Memory:    taskMemory,
			PidsLimit: &pidsLimit,
		},
	}
}

func (d *Docker) pullImage(ctx context.Context, imageName string) error {
	l, start := d.step("pull_image", "image", imageName)
	attempt := 0
	err := retry.Do(ctx, d.retry, nil, func(ctx context.Context) error {
		attempt++
		stepCtx, cancel := context.WithTimeout(ctx, setupStepTimeout)
		defer cancel()
		err := pullImageOnce(stepCtx, d.cli, imageName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				// Our own bound expired, not the outer ctx (see the matching
				// comment on runShellTimeout) — keep the error retryable.
				err = fmt.Errorf("sandbox: pull image %q timed out after %s", imageName, setupStepTimeout)
			}
			l.Warn("sandbox pull attempt failed", "attempt", attempt, "error", err.Error())
			return err
		}
		return nil
	})
	stepEnd(l, start, err, "attempts", attempt)
	return err
}

func pullImageOnce(ctx context.Context, cli *client.Client, imageName string) error {
	rc, err := cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("sandbox: pull image %q: %w", imageName, err)
	}
	defer func() { _ = rc.Close() }()
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return fmt.Errorf("sandbox: read image pull stream for %q: %w", imageName, err)
	}
	return nil
}

type dockerTask struct {
	cli         *client.Client
	containerID string
	repoDir     string
	log         *slog.Logger
}

var _ Task = (*dockerTask)(nil)

func (t *dockerTask) RepoDir() string {
	return t.repoDir
}

func (t *dockerTask) Close(ctx context.Context) error {
	if t == nil || t.cli == nil || t.containerID == "" {
		return nil
	}
	return t.cli.ContainerRemove(ctx, t.containerID, container.RemoveOptions{Force: true})
}

func (t *dockerTask) Bash(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("sandbox: bash command is empty")
	}
	return t.runShell(ctx, t.repoDir, command)
}

func (t *dockerTask) ReadFile(ctx context.Context, rel string) (string, error) {
	abs, err := t.absRepoPath(rel)
	if err != nil {
		return "", err
	}

	rc, _, err := t.cli.CopyFromContainer(ctx, t.containerID, abs)
	if err != nil {
		return "", fmt.Errorf("sandbox: read %q: %w", rel, err)
	}
	defer func() { _ = rc.Close() }()

	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	if err != nil {
		return "", fmt.Errorf("sandbox: read %q: %w", rel, err)
	}
	if hdr.FileInfo().IsDir() {
		return "", fmt.Errorf("sandbox: %q is a directory", rel)
	}

	b, err := io.ReadAll(tr)
	if err != nil {
		return "", fmt.Errorf("sandbox: read %q: %w", rel, err)
	}
	return string(b), nil
}

func (t *dockerTask) WriteFile(ctx context.Context, rel, content string) error {
	abs, err := t.absRepoPath(rel)
	if err != nil {
		return err
	}
	parent := path.Dir(abs)
	if _, err := t.runShell(ctx, t.repoDir, "mkdir -p "+shellQuote(parent)); err != nil {
		return fmt.Errorf("sandbox: mkdir for %q: %w", rel, err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: path.Base(abs),
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("sandbox: write %q tar header: %w", rel, err)
	}
	if _, err := io.Copy(tw, strings.NewReader(content)); err != nil {
		return fmt.Errorf("sandbox: write %q tar content: %w", rel, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("sandbox: write %q tar close: %w", rel, err)
	}

	if err := t.cli.CopyToContainer(ctx, t.containerID, parent, &buf, container.CopyToContainerOptions{AllowOverwriteDirWithFile: true}); err != nil {
		return fmt.Errorf("sandbox: write %q: %w", rel, err)
	}
	return nil
}

func (t *dockerTask) ReplaceText(ctx context.Context, rel, oldText, newText string) error {
	current, err := t.ReadFile(ctx, rel)
	if err != nil {
		return err
	}
	updated, err := replaceUnique(current, oldText, newText)
	if err != nil {
		return fmt.Errorf("%w in %q", err, rel)
	}
	return t.WriteFile(ctx, rel, updated)
}

func (t *dockerTask) Exists(ctx context.Context, rel string) (bool, error) {
	abs, err := t.absRepoPath(rel)
	if err != nil {
		return false, err
	}
	_, code, err := t.exec(ctx, t.repoDir, []string{"sh", "-lc", "test -e " + shellQuote(abs)})
	if err != nil {
		return false, err
	}
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("sandbox: exists check %q exited with code %d", rel, code)
	}
}

func (t *dockerTask) absRepoPath(rel string) (string, error) {
	clean, err := cleanRelativePath(rel)
	if err != nil {
		return "", err
	}
	return path.Join(t.repoDir, clean), nil
}

func cleanRelativePath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("sandbox: path is empty")
	}
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("sandbox: absolute paths are not allowed: %q", rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("sandbox: invalid path %q", rel)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("sandbox: path escapes repo root: %q", rel)
	}
	return clean, nil
}

func (t *dockerTask) runShell(ctx context.Context, workdir, command string) (string, error) {
	out, code, err := t.exec(ctx, workdir, []string{"sh", "-lc", command})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return out, fmt.Errorf("sandbox: command failed with exit code %d", code)
	}
	return out, nil
}

// runShellTimeout is runShell bounded by timeout, for internal setup steps
// (provisioning, clone, git config) that must fail fast rather than block the
// pipeline indefinitely if the container or the network wedges.
func (t *dockerTask) runShellTimeout(ctx context.Context, timeout time.Duration, workdir, command string) (string, error) {
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := t.runShell(stepCtx, workdir, command)
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		// stepCtx's own bound expired while the caller's ctx is still live:
		// this is our timeout, not a real cancellation. Callers that retry
		// around this (e.g. the clone step) key off context.DeadlineExceeded
		// to mean "the outer context is dead, stop retrying" — report a
		// plain error instead so a single wedged attempt doesn't cancel the
		// rest of the retry budget.
		return out, fmt.Errorf("sandbox: step %q timed out after %s", command, timeout)
	}
	return out, err
}

// exec runs cmd in the container and returns its combined output and exit
// code. The output read is raced against ctx so a command that never returns
// (e.g. a wedged network call) surfaces as a clear timeout/cancellation error
// instead of blocking the caller forever; closing attach unblocks the reader
// goroutine, which is left to drain into the buffered channel on its own.
func (t *dockerTask) exec(ctx context.Context, workdir string, cmd []string) (string, int, error) {
	l := t.log
	if l == nil {
		l = discardLog
	}
	l = l.With("workdir", workdir, "command", strings.Join(cmd, " "))
	start := time.Now()
	l.Debug("sandbox exec started")

	execResp, err := t.cli.ContainerExecCreate(ctx, t.containerID, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          cmd,
		WorkingDir:   workdir,
	})
	if err != nil {
		err = fmt.Errorf("sandbox: create exec: %w", err)
		l.Error("sandbox exec failed", "phase", "create", "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return "", 0, err
	}
	attach, err := t.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		err = fmt.Errorf("sandbox: attach exec: %w", err)
		l.Error("sandbox exec failed", "phase", "attach", "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return "", 0, err
	}

	type readResult struct {
		out []byte
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		out, err := io.ReadAll(attach.Reader)
		done <- readResult{out, err}
	}()

	var outBytes []byte
	select {
	case <-ctx.Done():
		attach.Close()
		err := fmt.Errorf("sandbox: exec %q: %w", strings.Join(cmd, " "), ctx.Err())
		l.Error("sandbox exec timed out", "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return "", 0, err
	case res := <-done:
		attach.Close()
		if res.err != nil {
			err := fmt.Errorf("sandbox: read exec output: %w", res.err)
			l.Error("sandbox exec failed", "phase", "read", "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
			return "", 0, err
		}
		outBytes = res.out
	}

	info, err := t.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		err = fmt.Errorf("sandbox: inspect exec: %w", err)
		l.Error("sandbox exec failed", "phase", "inspect", "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return "", 0, err
	}
	l.Debug("sandbox exec ok", "duration_ms", time.Since(start).Milliseconds(), "exit_code", info.ExitCode, "output_bytes", len(outBytes))
	return string(outBytes), info.ExitCode, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
