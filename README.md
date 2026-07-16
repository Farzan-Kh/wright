# Wright

Wright is a self-hosted daemon that turns well-scoped GitHub and GitLab
issues into verified, ready-to-review pull requests — unattended, around the
clock — while treating **cost per resolved issue** as the design constraint,
not an afterthought.

## The problem

Every backlog has a long tail of issues that are clearly scoped but not worth
a senior engineer's time: small bug fixes, minor features, chores, mechanical
refactors. General-purpose AI coding assistants — chat-driven, IDE-embedded,
cloud "AI engineer" products — can technically do this work, but they're
built for a human sitting in the loop, paying for a broad, flexible tool on
every turn. That cost structure doesn't fit the job of "take a scoped issue,
produce a merged PR, unattended, at scale, cheaply."

## The idea

Wright is narrow on purpose. It does one job — issue in, verified PR out —
and spends an LLM call only where an LLM call is actually required:
understanding the issue and writing or adjusting code. Everything around
that — polling, gating, sandboxing, git operations, running the repo's own
test suite, retrying against real failures, opening the PR — is deterministic
Go, not more model calls. That's the whole efficiency story: not a cheaper
model, a smaller loop.

It's built first for individual developers and small teams who want their
backlog worked down without a large AI spend or a third party holding push
access to their code — which is also why it's self-hosted: a single static
binary, small enough to read end to end, so you know exactly what it does
with your credentials.

## What Wright is not

- **Not a general-purpose coding assistant.** No chat, no IDE plugin, no "help
  me design this system." It takes a scoped issue and produces a patch —
  nothing broader.
- **Not interactive.** There's no pairing session to drive. It runs
  unattended; you interact with it through issue comments, PR review, and
  config, not a conversation.
- **Not a product manager.** It doesn't decide what should be built. Scoping
  the work — writing and labeling the issue — is still yours; Wright
  implements.
- **Not a hosted service.** Self-hosted first, by design — your token, your
  container, your infrastructure.
- **Not (yet) proven at scale.** It's pre-alpha, see below — the pipeline
  runs end to end, but the reliability track record is still being built.

> **Status: pre-alpha (Phase 1).** The end-to-end single-repo pipeline is now
> implemented: poll labeled issues, run a gate, execute an agent in a Docker
> sandbox, verify with the repo's own tests, and open a PR. Interfaces/config
> may still evolve while the project hardens toward Phase 2.

> **⚠️ Security: the trigger label is a trust boundary.** Wright feeds issue
> text — from anyone who can file an issue — to an LLM agent that writes code
> and opens PRs against your repo. Only apply `trigger_label` to issues from
> people you trust. See [SECURITY.md](SECURITY.md) for the full threat model,
> token scope guidance, and hardening recommendations.

## What works today

- A `Provider` interface with **GitHub** and **GitLab** adapters covering the
  write path (including issue-label add/remove and PR operations).
- A Phase 1 pipeline: poll → gate → sandboxed agent tool loop → verifier retry
  loop → git push → PR creation.
- Per-issue token and turn accounting.
- A YAML config format (`wright.yaml`) describing one or more repos.
- A CLI:
  - `wright validate` — load and validate config offline, and confirm required
    token env vars are set.
  - `wright once` — prove provider access and list labeled issues.
  - `wright run` — run one full Phase 1 pipeline pass for one repo.
  - `wright smoke` — manual provider write-path smoke test.
  - `wright version` — print the build version.

## Configuration

Wright reads a `wright.yaml`. See [`wright.example.yaml`](wright.example.yaml)
for a fully commented example, and
[`docs/CONFIGURATION.md`](docs/CONFIGURATION.md) for the full reference of
every field, default, and validation rule.

```yaml
version: 1
repos:
  - provider: github            # "github" | "gitlab"
    repo: your-org/your-repo    # GitLab: full project path, nested groups OK
    # api_base_url: https://gitlab.example.com   # GHE / self-hosted GitLab only
    # token_env: MY_TOKEN_VAR                     # env var NAME; see table below
    trigger_label: wright       # default "wright"
    # base_branch: main         # default: repo's default branch via API
    auto_merge: false           # default false — explicit opt-in
    budget:
      max_turns: 30
    llm:
      provider: claude          # "claude" (default) | "openrouter"
      auth: api_key             # api_key (Phase 1); oauth is deferred to Phase 2
      agent_model: claude-sonnet-5
      gate_model: claude-haiku-4-5
      effort: high
    # prompt:
    #   system_append: "Always update CHANGELOG.md when you change public behavior."
    #   # system_override: ...   # advanced — see wright.example.yaml
```

> **`prompt` customization.** `system_append` adds repo-specific instructions
> after Wright's default behavior guidance; `system_override` fully replaces
> it (mutually exclusive with `system_append`; `wright validate` rejects
> setting both). Either way, Wright's *operational contract* — don't
> self-commit/push, tool path rules, verify-retry behavior — is a separate,
> always-enforced block that neither field can touch. Only use
> `system_override` if you know exactly what default guidance you're
> discarding.

> **LLM providers.** `claude` is the default and the path Phase 1 is built
> around and tested against. `openrouter` is an optional extension beyond
> that: it targets OpenRouter's OpenAI-compatible API, supports
> `auth: api_key` only, and reads its key from `WRIGHT_OPENROUTER_API_KEY` /
> `OPENROUTER_API_KEY`.

Credentials are **never** stored in the config file. Tokens are read from
environment variables, resolved in this order:

| Order | Source                        | Notes                                  |
|-------|-------------------------------|----------------------------------------|
| 1     | `token_env` from the repo entry | Explicit override; you name the var. |
| 2     | `WRIGHT_GITHUB_TOKEN` / `WRIGHT_GITLAB_TOKEN` | Wright-specific, by provider. |
| 3     | `GITHUB_TOKEN` / `GITLAB_TOKEN` | Conventional fallback, by provider.  |

## Quick start

```bash
make build
export WRIGHT_GITHUB_TOKEN=ghp_...      # or WRIGHT_GITLAB_TOKEN=glpat-...
cp wright.example.yaml wright.yaml      # then edit for your repo
./wright validate --config wright.yaml
./wright once --config wright.yaml
./wright run --config wright.yaml
```

## Development

```bash
make build   # compile ./cmd/wright with version ldflags
make test    # go test ./...   (no live API calls)
make lint    # golangci-lint run
make tidy    # go mod tidy
```

The test suite makes **zero live API calls** — adapters are tested against
`httptest` servers with canned fixtures. Live verification is done manually via
`wright smoke` against your own scratch repos.

### A note on `PushCommits`

`wright run` uses real git inside the sandbox clone (checkout/commit/push).
`PushCommits` remains available primarily for deterministic provider-level smoke
and adapter testing.

## Package map

Current implementation centers on `internal/{poller,gate,sandbox,agent,verifier,gitops,pipeline}`
plus `internal/{config,provider,cli}`.

Phase 2 work (queueing/concurrency/reliability hardening) is planned but not yet implemented.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
