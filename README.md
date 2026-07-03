# Patchr

Patchr is a self-hosted Go daemon that resolves labeled, well-scoped GitHub and
GitLab issues with an LLM agent and opens pull requests — built from the ground
up to minimize token cost per resolved issue.

> **Status: pre-alpha (Phase 0).** This is the foundation only: the provider
> abstraction, config format, and a minimal run-once CLI. There is no poller,
> sandbox, agent, or verifier yet — those arrive in Phase 1. Nothing here
> resolves an issue on its own. Interfaces and config may still change.

## What works today

- A `Provider` interface with **GitHub** and **GitLab** adapters covering the
  write path: list labeled issues, comment, create/delete branches, push
  commits (via each provider's commit API — no local clone), and open/merge/
  close pull requests.
- A YAML config format (`patchr.yaml`) describing one or more repos.
- A CLI:
  - `patchr validate` — load and validate the config fully offline, and confirm
    the resolved token env vars are set.
  - `patchr once` — construct the provider for one repo, prove auth, and list
    that repo's open issues carrying the trigger label.
  - `patchr smoke` — exercise the full write path (branch → commit → PR →
    comment → cleanup) against a scratch repo you designate. Refuses to run
    without an explicit `--repo`.
  - `patchr version` — print the build version.

## Configuration

Patchr reads a `patchr.yaml`. See [`patchr.example.yaml`](patchr.example.yaml)
for a fully commented example.

```yaml
version: 1
repos:
  - provider: github            # "github" | "gitlab"
    repo: your-org/your-repo    # GitLab: full project path, nested groups OK
    # api_base_url: https://gitlab.example.com   # GHE / self-hosted GitLab only
    # token_env: MY_TOKEN_VAR                     # env var NAME; see table below
    trigger_label: patchr       # default "patchr"
    # base_branch: main         # default: repo's default branch via API
    auto_merge: false           # default false — explicit opt-in
    budget:                     # enforced in Phase 1; unit still open
      max_usd: 2.00
      max_turns: 30
    llm:
      provider: claude
      model: claude-sonnet-4-5
```

Credentials are **never** stored in the config file. Tokens are read from
environment variables, resolved in this order:

| Order | Source                        | Notes                                  |
|-------|-------------------------------|----------------------------------------|
| 1     | `token_env` from the repo entry | Explicit override; you name the var. |
| 2     | `PATCHR_GITHUB_TOKEN` / `PATCHR_GITLAB_TOKEN` | Patchr-specific, by provider. |
| 3     | `GITHUB_TOKEN` / `GITLAB_TOKEN` | Conventional fallback, by provider.  |

## Quick start

```bash
make build
export PATCHR_GITHUB_TOKEN=ghp_...      # or PATCHR_GITLAB_TOKEN=glpat-...
cp patchr.example.yaml patchr.yaml      # then edit for your repo
./patchr validate --config patchr.yaml
./patchr once --config patchr.yaml
```

## Development

```bash
make build   # compile ./cmd/patchr with version ldflags
make test    # go test ./...   (no live API calls)
make lint    # golangci-lint run
make tidy    # go mod tidy
```

The test suite makes **zero live API calls** — adapters are tested against
`httptest` servers with canned fixtures. Live verification is done manually via
`patchr smoke` against your own scratch repos.

### A note on `PushCommits`

In Phase 0, `PushCommits` writes through each provider's commit API (GitHub Git
Data API, GitLab Commits API) rather than a local git clone. This is the
simplest honest implementation and lets `smoke` exercise the real write
plumbing. In Phase 1, the sandbox will likely push via real git from a working
clone, at which point `PushCommits` may become a smoke/bot-commit utility rather
than the primary write path.

## Planned package map (Phase 1+)

Phase 0 ships only `internal/{cli,config,provider,version}`. Future phases slot
in additively — everything new depends on `internal/provider` (domain types) and
`internal/config`, never the reverse:

| Package                         | Phase | Role                                             |
|---------------------------------|-------|--------------------------------------------------|
| `internal/poller`               | 1     | Pick up issues labeled with the trigger label.   |
| `internal/gate`                 | 1     | Issue info-sufficiency triage.                   |
| `internal/queue`                | 2     | Task queue with concurrency limits and backoff.  |
| `internal/sandbox`              | 1     | Per-task Docker container with a fresh clone.     |
| `internal/agent`                | 1     | Bounded agent loop over the LLM.                 |
| `internal/agent/llm/claude`     | 1     | Claude API adapter.                              |
| `internal/verifier`             | 1     | Detect and run the repo's tests; feed failures back. |
| `internal/gitops`               | 1     | Commit, push, and open PRs describing the change. |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
