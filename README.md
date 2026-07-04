# Patchr

Patchr is a self-hosted Go daemon that resolves labeled, well-scoped GitHub and
GitLab issues with an LLM agent and opens pull requests — built from the ground
up to minimize token cost per resolved issue.

> **Status: pre-alpha (Phase 1).** The end-to-end single-repo pipeline is now
> implemented: poll labeled issues, run a gate, execute an agent in a Docker
> sandbox, verify with the repo's own tests, and open a PR. Interfaces/config
> may still evolve while the project hardens toward Phase 2.

## What works today

- A `Provider` interface with **GitHub** and **GitLab** adapters covering the
  write path (including issue-label add/remove and PR operations).
- A Phase 1 pipeline: poll → gate → sandboxed agent tool loop → verifier retry
  loop → git push → PR creation.
- Per-issue token/cost accounting (USD in API-key mode, tokens/turns in OAuth
  mode).
- A YAML config format (`patchr.yaml`) describing one or more repos.
- A CLI:
  - `patchr validate` — load and validate config offline, and confirm required
    token env vars are set.
  - `patchr once` — prove provider access and list labeled issues.
  - `patchr run` — run one full Phase 1 pipeline pass for one repo.
  - `patchr smoke` — manual provider write-path smoke test.
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
    budget:
      max_usd: 2.00
      max_turns: 30
    llm:
      provider: claude          # "claude" (default) | "openrouter"
      auth: api_key             # api_key (Phase 1); oauth is deferred to Phase 2
      agent_model: claude-sonnet-5
      gate_model: claude-haiku-4-5
      effort: high
```

> **LLM providers.** `claude` is the default and the path the cost-per-issue
> metric is designed around (Phase 1 is Claude-only per `docs/PHASE_1_PLAN.md`).
> `openrouter` is an optional extension beyond that plan: it targets OpenRouter's
> OpenAI-compatible API, supports `auth: api_key` only, and reads its key from
> `PATCHR_OPENROUTER_API_KEY` / `OPENROUTER_API_KEY`.

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
./patchr run --config patchr.yaml
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

`patchr run` uses real git inside the sandbox clone (checkout/commit/push).
`PushCommits` remains available primarily for deterministic provider-level smoke
and adapter testing.

## Package map

Current implementation centers on `internal/{poller,gate,sandbox,agent,verifier,gitops,pipeline}`
plus `internal/{config,provider,cli}`.

Phase 2 work (queueing/concurrency/reliability hardening) is tracked in `docs/ROADMAP.md`.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
