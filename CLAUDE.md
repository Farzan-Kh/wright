# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Wright is

Wright is a self-hosted daemon that turns labeled GitHub/GitLab issues into
verified pull requests, unattended. It polls for issues carrying a trigger
label, runs a cheap-model triage gate, executes an LLM coding agent inside a
Docker sandbox, verifies the result with the target repo's own test suite
(retrying on failure), and opens a PR. Cost-per-resolved-issue is the core
design constraint: LLM calls are used only for understanding the issue and
writing code — polling, gating grunt work, sandboxing, git operations, and
retries are deterministic Go.

Status: pre-alpha (Phase 1) — see `README.md` for what's implemented.
`docs/ARCHITECTURE.md`, `docs/PROJECT_BRIEF.md`, and `docs/ROADMAP.md` (one
level up, in the parent `Wright-Project` directory) hold the fuller design
rationale and phased plan if you need more context than this file gives.

## Commands

```bash
make build   # go build -ldflags "-X .../internal/version.Version=$(VERSION)" -o wright ./cmd/wright
make test    # go test ./...          (zero live API calls — see Testing rules)
make lint    # golangci-lint run       (pinned to v2.12.2 in CI)
make tidy    # go mod tidy
```

Run a single test or package:

```bash
go test ./internal/gate/...
go test ./internal/gate/... -run TestGate_CheckWithTools
go test -race ./...          # what CI actually runs, in addition to go vet
```

Manual, end-to-end verification against a real repo (never add live calls to
the test suite for this):

```bash
export WRIGHT_GITHUB_TOKEN=ghp_...      # or WRIGHT_GITLAB_TOKEN=glpat-...
./wright smoke --config wright.yaml --repo you/scratch-repo   # real write path: branch, commit, PR, comment, label
./wright validate --config wright.yaml   # offline config + token env check
./wright once --config wright.yaml       # prove provider access, list labeled issues
./wright run --config wright.yaml        # one full pipeline pass for one repo
```

Requires Go 1.26+ and Docker (sandbox + its tests).

### Testing rules

- **No live API calls, ever** in `go test ./...`. `internal/provider/github`
  and `internal/provider/gitlab` are tested against `httptest` servers with
  canned fixtures — follow that pattern for new adapter code, and reuse the
  shared assertions in `internal/provider/providertest` so both adapters stay
  honest against the same contract.
- Tests must pass under `-race`.
- `internal/agent/llm/fake.go` and `internal/sandbox/fake.go` provide in-memory
  fakes for the LLM provider and sandbox tool-exec interfaces — use these for
  agent/gate/pipeline tests instead of hitting a real model or container.

## Architecture

The pipeline is a straight chain of narrow, single-purpose packages, each
behind an interface so the concrete implementation (GitHub vs GitLab, Claude
vs OpenRouter, Docker sandbox vs fake) is swappable without touching callers:

```
Poller → Gate → (executor: Sandbox → Agent Runner → Verifier → loop) → Git Ops → PR
```

- **`internal/provider`** — leaf package: the `Provider` interface (list/get
  issues, comment, label, branch, push, PR, merge) plus domain types and
  sentinel errors. Depends on nothing internal; everything else depends on it.
  `internal/provider/github` and `internal/provider/gitlab` are the two
  adapters. `internal/provider/factory` builds one from config (it's a
  separate package from `provider` itself specifically to avoid an import
  cycle with the concrete adapters). Every `Provider` is wrapped with
  `internal/provider/retrying` (connection retry) and `internal/provider/logging`
  (verbose call logging) — logging wraps the raw adapter so each retry attempt
  is logged individually, not just the final one. `internal/provider/sanitize.go`
  sanitizes any agent-produced text/refs before it's posted back to GitHub/GitLab.

- **`internal/poller`** — deterministic, no LLM calls. Lists issues carrying
  the trigger label, fetching each issue's comment thread alongside its body
  (downstream steps need clarifications/decisions that often only show up in
  comments).

- **`internal/gate`** (`Gate.CheckWithUsage`) — a cheap-model triage pass
  deciding whether an issue has enough information to attempt. Grounds the
  triage in live state rather than trusting issue text alone: resolves `#N`
  references (via the exported `ExtractIssueReferences`, also reused by
  `run_exec.go`'s stacking base-branch selection so the two scans can't
  drift) against the provider's current issue state, noting when a
  referenced-but-still-open issue already has an open Wright PR (a
  stackable, not blocking, dependency — see `internal/stack`), and — when
  `Gate.Provider` is set — gives the model two bounded, read-only tools
  (`repo_read_file`, `repo_list_dir`, capped at `MaxToolTurns`, default 3) to
  check whether something it thinks is missing already exists in the repo.
  Both are best-effort: a failed lookup falls back to a plain triage call
  rather than failing the whole check.

- **`internal/pipeline`** (`Pipeline.RunOnce`) — wires poll → gate → the
  `ReadyHandler` callback for gate-approved issues → failure/skip reporting.
  The actual "do the work" step is injected as `OnReady`; the concrete
  implementation is `issueExecutor.Handle` in `internal/cli/run_exec.go`, kept
  in `cli` rather than `pipeline` because it needs to reach into config,
  sandbox, agent, gitops, and verifier all at once — `pipeline` itself stays
  free of those dependencies.

- **`internal/sandbox`** — the `ToolExec`/`Task`/`Orchestrator` interfaces for
  isolated per-issue execution. `docker.go` is the real Docker-backed
  implementation (fresh clone, scoped credentials, torn down on task end
  regardless of outcome); `fake.go` is the in-memory test double. Both the
  agent and verifier operate purely through `ToolExec` (`Bash`, `ReadFile`,
  `WriteFile`, `ReplaceText`, `Exists`) — neither talks to Docker directly.

- **`internal/agent`** (`Runner.Run`) — the hand-written tool-use loop against
  an `llm.LLMProvider`. Bounded by `Cfg.MaxTurns`; exposes exactly two tools to
  the model, `bash` and `str_replace_based_edit_tool` (view/create/str_replace),
  both executed through `sandbox.ToolExec`. `internal/agent/llm` defines the
  model-agnostic request/response contract (`MessageRequest`/`MessageResponse`,
  provider-agnostic `ContentBlock` covering text/thinking/tool_use/tool_result);
  `internal/agent/llm/claude` and `internal/agent/llm/openrouter` are the two
  adapters, wrapped in `internal/agent/llm/retrying` and `internal/agent/llm/logging`
  the same way providers are. `internal/agent/llm/fake.go` is the test double.

- **`internal/verifier`** — deliberately not an LLM step. Auto-detects the
  target repo's test command from repo markers (`go.mod` → `go test ./...`,
  `package.json` with a `scripts.test` entry → `npm test`, `pytest.ini` /
  `pyproject.toml` mentioning pytest → `pytest`, `Makefile` with a `test:`
  target → `make test`), or takes an explicit override from config. The
  executor (`run_exec.go`) retries the agent against verify failures up to
  `maxVerifyAttempts` (3), feeding back truncated command output each time —
  this retry loop is the main reliability mechanism and costs no extra tokens
  beyond the retry turn itself.

- **`internal/gitops`** (`Ops.CommitAndPush`, `Ops.OpenPR`) — deterministic git
  operations run *inside the sandbox clone* via `ToolExec.Bash` (checkout
  branch, add, commit, push with an injected-credential HTTPS remote URL), then
  opens the PR through the same `Provider` used by the poller. The agent itself
  is explicitly forbidden (in its system prompt's operational contract, see
  below) from committing or pushing — it leaves uncommitted edits in the
  working tree and `gitops` does the git work afterward, which is also why an
  agent-made commit would silently break the harness.

- **`internal/cache`** (`Store`, `FileStore`) — persists partial progress from
  an interrupted issue-resolution attempt (turn limit, verify exhaustion, or a
  failed commit/push/PR step) as one JSON file per issue, so the next attempt
  at the same issue resumes instead of re-spending LLM turns from scratch.
  `Entry.Stage` records how far a cached attempt got and drives how
  `run_exec.go` resumes it: `agent_incomplete` reapplies the cached diff into
  a fresh sandbox and continues the cached agent conversation;
  `verified_unpushed` reapplies the diff and redoes commit+push+PR without
  re-invoking the agent; `pr_pending` needs no sandbox or agent at all — it
  just retries the PR-open call against the already-pushed branch. This also
  fixes what used to be a dead end: a PR-creation failure left a pushed branch
  that the idempotency check in `run_exec.go` would otherwise skip forever.

- **`internal/stack`** (`Store`, `FileStore`, `Reconcile`) — opt-in
  (`RepoConfig.Stacking.Enabled`, default false) tracking for PRs Wright
  stacked on an in-flight dependency PR instead of blocking until a human
  merges it. `run_exec.go` picks the stacking base branch deterministically
  (never the gate's LLM call): when an issue references a dependency that
  already has an open Wright PR, it branches on that PR's head branch
  instead of the resolved base branch, notes the relationship in the new
  PR's body, and — after a successful `OpenPR` — records a `stack.Entry`
  (dependency PR number, the *real* base branch it would have used
  otherwise). `Reconcile` runs once per poll cycle, deterministic and
  LLM-free: once a tracked dependency PR merges, it retargets the stacked
  PR onto the real base branch and comments that CI/tests should be
  rechecked (no automatic re-verification in v1); if the dependency PR
  closes unmerged, it comments and drops the entry instead of checking it
  forever.

- **`internal/config`** — YAML schema (`wright.yaml`) and defaults
  (`applyDefaults`) / validation. `repos` is a list from day one (Phase 1 CLI
  commands operate on one entry) so adding multi-repo support later isn't a
  schema break. Credentials are never in the config file — `internal/config/token.go`
  resolves the provider token from env vars in order: `token_env` override →
  `WRIGHT_{GITHUB,GITLAB}_TOKEN` → `{GITHUB,GITLAB}_TOKEN`.

- **`internal/cli`** — cobra commands (`validate`, `once`, `run`, `smoke`,
  `version`). `run_exec.go` is where the executor lives, including the two
  system-prompt blocks given to the coding agent:
  - `defaultAgentBehaviorPrompt` — identity/scope guardrails, replaceable
    wholesale by `RepoConfig.Prompt.SystemOverride` or extended by
    `RepoConfig.Prompt.SystemAppend`.
  - `agentOperationalContract` — harness mechanics (don't self-commit/push,
    tool path rules, "harness runs verify after you stop") that are **never**
    overridden or extended by repo config, because getting them wrong breaks
    the harness rather than just producing worse code.

- **`internal/retry`** — shared exponential/fixed backoff used by provider
  clients, the LLM client, and `gitops`' push retry. Configured per-repo via
  `RepoConfig.Retry` → `RetryConfig.ToRetryConfig()`.

- **`internal/cost`** — provider-agnostic token usage (`Usage`) and an
  `Accumulator`/`Summary` for turn/token accounting, aggregated per issue
  across gate + agent turns in `pipeline.mergeCost`.

- **`internal/logging`** — optional diagnostic logger, off by default (only
  `-v`/`--verbose` turns it on, writing to `--log-file` rather than
  stdout/stderr). Pulled out of `context.Context`; a discarding logger is used
  everywhere logging isn't explicitly enabled, so call sites never need a nil
  check.

### The trust/security boundary

The trigger label is the actual security boundary, not the issue content —
Wright feeds untrusted issue text (from anyone who can file an issue) to an
LLM agent with file-edit tools. Sandbox isolation, the fixed operational
contract, the gate's triage pass, `auto_merge: false` by default, and output
sanitization at the provider boundary are the layered mitigations; see
`SECURITY.md` for the full threat model and token-scope guidance before
touching anything in `internal/provider/sanitize.go`, the operational
contract in `run_exec.go`, or sandbox egress/isolation in `internal/sandbox`.

### A note on `PushCommits`

`wright run` uses real git inside the sandbox clone (`gitops`, checkout /
commit / push) — `Provider.PushCommits` (the provider commit API path) is not
on the `wright run` hot path. It remains for deterministic provider-level
smoke and adapter testing.

## Conventions

- Every source file starts with `// SPDX-License-Identifier: Apache-2.0`.
- Package doc comments explain *why* the package exists and its place in the
  dependency graph (see `internal/provider/provider.go`, `internal/agent/llm/llm.go`)
  — match that style for new packages rather than a one-line description.
- Commit messages: plain, descriptive, imperative mood (`Fix pagination in
  GitHub issue comments`, not `Fixed`/`Fixes`); no enforced prefix convention.
- For anything beyond a small fix, open an issue first to discuss approach
  before investing real time (see `CONTRIBUTING.md`) — interfaces are still
  moving pre-alpha.
- PRs: keep scoped to one change, include a regression test (fails without
  the fix, passes with it), update `docs/CONFIGURATION.md`/README when
  user-facing config or behavior changes, and describe *why* in the
  description since the diff already shows *what*.
