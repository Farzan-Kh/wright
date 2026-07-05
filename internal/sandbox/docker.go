package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"

	"github.com/farzan-kh/wright/internal/retry"
)

// Docker orchestrates task containers through the Docker Engine API.
type Docker struct {
	cli   *client.Client
	retry retry.Config
}

var _ Orchestrator = (*Docker)(nil)

// NewDocker creates a Docker orchestrator using the local Docker environment.
// retryCfg controls retries around image pulls, the orchestrator's one
// genuine network connection attempt.
func NewDocker(retryCfg retry.Config) (*Docker, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("sandbox: docker client: %w", err)
	}
	return &Docker{cli: cli, retry: retryCfg}, nil
}

// NewDockerWithClient creates a Docker orchestrator from a caller-provided
// client (used mainly by tests).
func NewDockerWithClient(cli *client.Client, retryCfg retry.Config) *Docker {
	return &Docker{cli: cli, retry: retryCfg}
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

	if err := d.pullImage(ctx, imageName); err != nil {
		return nil, err
	}

	resp, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image:      imageName,
		Entrypoint: []string{"sh", "-lc"},
		Cmd:        []string{"while :; do sleep 3600; done"},
	}, taskHostConfig(), nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("sandbox: create container: %w", err)
	}

	task := &dockerTask{
		cli:         d.cli,
		containerID: resp.ID,
		repoDir:     path.Join(workdir, repoDirName),
	}
	ready := false
	defer func() {
		if !ready {
			_ = task.Close(context.Background())
		}
	}()

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("sandbox: start container %s: %w", resp.ID, err)
	}

	// The agent's tool contract advertises a "bash" tool, but bare images like
	// the alpine/git default ship neither bash nor python3, so agent commands
	// assuming either exists silently fail. Best-effort provision them on
	// Alpine-based images only (guarded by `command -v apk`) so custom,
	// non-Alpine sandbox.image values from repo config are left untouched.
	if _, err := task.runShell(ctx, "/", provisionCmd); err != nil {
		return nil, fmt.Errorf("sandbox: provision base tools: %w", err)
	}

	if _, err := task.runShell(ctx, "/", "mkdir -p "+shellQuote(workdir)); err != nil {
		return nil, fmt.Errorf("sandbox: create workdir: %w", err)
	}
	if _, err := task.runShell(ctx, workdir, "mkdir -p "+shellQuote(task.repoDir)); err != nil {
		return nil, fmt.Errorf("sandbox: create repo dir: %w", err)
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
		if err := retry.Do(ctx, d.retry, nil, func(ctx context.Context) error {
			_, err := task.runShell(ctx, workdir, cloneCmd)
			return err
		}); err != nil {
			return nil, fmt.Errorf("sandbox: clone repo: %w", err)
		}
	}

	if _, err := task.runShell(ctx, task.repoDir, "git config user.name "+shellQuote(gitUser)); err != nil {
		return nil, fmt.Errorf("sandbox: git user.name: %w", err)
	}
	if _, err := task.runShell(ctx, task.repoDir, "git config user.email "+shellQuote(gitEmail)); err != nil {
		return nil, fmt.Errorf("sandbox: git user.email: %w", err)
	}

	ready = true
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
	return retry.Do(ctx, d.retry, nil, func(ctx context.Context) error {
		rc, err := d.cli.ImagePull(ctx, imageName, image.PullOptions{})
		if err != nil {
			return fmt.Errorf("sandbox: pull image %q: %w", imageName, err)
		}
		defer func() { _ = rc.Close() }()
		if _, err := io.Copy(io.Discard, rc); err != nil {
			return fmt.Errorf("sandbox: read image pull stream for %q: %w", imageName, err)
		}
		return nil
	})
}

type dockerTask struct {
	cli         *client.Client
	containerID string
	repoDir     string
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

func (t *dockerTask) exec(ctx context.Context, workdir string, cmd []string) (string, int, error) {
	execResp, err := t.cli.ContainerExecCreate(ctx, t.containerID, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          cmd,
		WorkingDir:   workdir,
	})
	if err != nil {
		return "", 0, fmt.Errorf("sandbox: create exec: %w", err)
	}
	attach, err := t.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("sandbox: attach exec: %w", err)
	}
	defer attach.Close()

	outBytes, err := io.ReadAll(attach.Reader)
	if err != nil {
		return "", 0, fmt.Errorf("sandbox: read exec output: %w", err)
	}
	info, err := t.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return "", 0, fmt.Errorf("sandbox: inspect exec: %w", err)
	}
	return string(outBytes), info.ExitCode, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
