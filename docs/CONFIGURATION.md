# Configuration reference

Full reference for every field `wright.yaml` accepts, its default, and the
validation rules `wright validate` enforces. See
[`wright.example.yaml`](../wright.example.yaml) for a runnable starting point.

Config is parsed with unknown-field rejection: any key not listed below fails
`wright validate` / `wright run` immediately instead of being silently
ignored, so typos surface at load time rather than at runtime.

## Top level

| Field   | Type   | Required | Notes                          |
|---------|--------|----------|---------------------------------|
| `version` | int  | yes      | Must be `1`.                    |
| `repos`   | list | yes      | At least one entry; see below. |

## `repos[]`

| Field           | Type   | Default                        | Notes |
|-----------------|--------|---------------------------------|-------|
| `provider`      | string | —                                | Required. `github` \| `gitlab`. |
| `repo`          | string | —                                | Required. `owner/name` (GitHub) or a full project path, nested groups OK (GitLab). No leading/trailing slash, no empty segments, no whitespace. `provider` + `repo` pairs must be unique across the file. |
| `api_base_url`  | string | provider's public SaaS API       | Set for GitHub Enterprise or a self-managed GitLab instance. |
| `token_env`     | string | *(see resolution order)*         | Names the env var to read the provider token from. |
| `trigger_label` | string | `wright`                         | Issue label Wright acts on. Must not be empty. |
| `base_branch`   | string | repo's default branch (via API)  | Branch PRs target. |
| `auto_merge`    | bool   | `false`                          | Explicit opt-in. |
| `budget`        | object | —                                | See [`budget`](#budget). |
| `llm`           | object | —                                | See [`llm`](#llm). |
| `sandbox`       | object | —                                | See [`sandbox`](#sandbox). |
| `verify`        | object | —                                | See [`verify`](#verify). |
| `prompt`        | object | —                                | See [`prompt`](#prompt). |
| `retry`         | object | —                                | See [`retry`](#retry). |

### Repo token resolution order

`token_env`, if set, always wins. Otherwise Wright checks, most specific
first:

| Order | Source                                          |
|-------|--------------------------------------------------|
| 1     | `token_env` from the repo entry (explicit)       |
| 2     | `WRIGHT_GITHUB_TOKEN` / `WRIGHT_GITLAB_TOKEN`    |
| 3     | `GITHUB_TOKEN` / `GITLAB_TOKEN` (conventional)    |

If none of the candidates are set, the command fails and reports which env
vars it looked for.

## `budget`

| Field       | Type | Default | Notes |
|-------------|------|---------|-------|
| `max_turns` | int  | `0`     | Upper bound on agent turns spent per issue. Must be `>= 0`. |

## `llm`

| Field         | Type   | Default            | Notes |
|---------------|--------|---------------------|-------|
| `provider`    | string | `claude`            | `claude` \| `openrouter`. Must not be empty. |
| `auth`        | string | `api_key`           | `api_key` \| `oauth`. **`oauth` is accepted by the schema but rejected by `wright run`/`wright validate` in Phase 1** ("`llm.auth "oauth" (Claude subscription) is not supported in Phase 1; use auth: api_key`" — deferred to Phase 2). |
| `api_key_env` | string | *(see resolution order)* | Env var to read the LLM API key from. |
| `model`       | string | —                    | **Legacy alias** for `agent_model`, kept for compatibility with the Phase 0 schema. If `agent_model` is unset and `model` is set, `model`'s value is used as `agent_model`. Prefer `agent_model` in new configs. |
| `agent_model` | string | `claude-sonnet-5`    | Model used for the agent tool loop. |
| `gate_model`  | string | `claude-haiku-4-5`   | Model used for the pre-agent gate check. |
| `effort`      | string | `high`               | `low` \| `medium` \| `high`. |
| `oauth`       | object | —                    | Only relevant when `auth: oauth`; see [`llm.oauth`](#llmoauth). Not usable in Phase 1 (see above). |

`openrouter` targets OpenRouter's OpenAI-compatible API and only supports
`auth: api_key` — `llm.auth oauth is not supported for openrouter` is a
validation error.

### LLM API key resolution order

`api_key_env`, if set, always wins. Otherwise, by `provider`:

| Provider     | Order |
|--------------|-------|
| `claude` (or unrecognized) | `WRIGHT_ANTHROPIC_API_KEY` → `ANTHROPIC_API_KEY` |
| `openrouter` | `WRIGHT_OPENROUTER_API_KEY` → `OPENROUTER_API_KEY` |

### `llm.oauth`

Deferred to Phase 2 — the fields below are validated but `auth: oauth` itself
is currently rejected at run time.

| Field                     | Type   | Notes |
|---------------------------|--------|-------|
| `access_token_env`        | string | **Required** when `auth: oauth`. |
| `access_token_expiry_env` | string | Optional. Value must be an RFC3339 timestamp. |
| `refresh_token_env`       | string | Optional, but if any of `refresh_token_env` / `client_id_env` / `token_url` is set, all three are required together. |
| `client_id_env`           | string | See above. |
| `token_url`               | string | See above. Must be a valid absolute URL (scheme + host). |

## `sandbox`

| Field     | Type   | Default              | Notes |
|-----------|--------|------------------------|-------|
| `image`   | string | `alpine/git:2.47.2`   | Container image for the per-task sandbox. |
| `workdir` | string | `/workspace`          | Working directory inside the sandbox. |

## `verify`

| Field     | Type   | Default          | Notes |
|-----------|--------|-------------------|-------|
| `command` | string | *(auto-detected)* | Overrides auto-detection when set. |

When `command` is unset, Wright detects it from repo markers, in order:

| Marker                                             | Command        |
|-----------------------------------------------------|----------------|
| `go.mod` present                                     | `go test ./...` |
| `package.json` present with a `scripts.test` entry   | `npm test`      |
| `pytest.ini` present                                  | `pytest`        |
| `pyproject.toml` present and contains `pytest`        | `pytest`        |
| `Makefile` present with a `test:` target              | `make test`     |

If none match, verification fails with "no test command detected".

## `prompt`

| Field             | Type   | Default | Notes |
|-------------------|--------|---------|-------|
| `system_append`   | string | —       | Appended after Wright's default behavior guidance. Mutually exclusive with `system_override`. |
| `system_override` | string | —       | Fully replaces Wright's default behavior guidance. Mutually exclusive with `system_append`. |

Regardless of this setting, Wright's *operational contract* — no
self-commit/push, tool/path rules, verify-retry behavior — is a separate
block that is always enforced and cannot be touched by either field.

## `retry`

Controls backoff for connection attempts to the provider API, the LLM API,
the Docker daemon (image pulls), and git push/clone.

| Field           | Type   | Default       | Notes |
|-----------------|--------|----------------|-------|
| `strategy`      | string | `exponential`  | `exponential` \| `fixed` (simple time-based retries). |
| `max_attempts`  | int    | `4`            | Total tries per connection attempt, including the first. `1` disables retrying. Must be `>= 0`. |
| `base_delay_ms` | int    | `500`          | Delay, in ms, before the first retry (and every retry under `fixed`). Must be `>= 0`. |
| `max_delay_ms`  | int    | `30000`        | Caps any single retry delay, in ms. Must be `>= 0`. |
| `exponent`      | float  | `2`            | Per-attempt delay multiplier under `exponential`. Must be `>= 0`. |

## Validation summary

`wright validate` runs all of the following and reports every failure at
once (not just the first):

- `version` must be `1`.
- At least one entry in `repos`.
- Each repo's `provider` + `repo` pair must be unique across the file.
- `provider` required, must be `github` or `gitlab`.
- `repo` required; must be `owner/name`-shaped (or deeper for GitLab), no
  whitespace, no leading/trailing slash, no empty segments.
- `trigger_label` must not be empty (after defaults are applied).
- `budget.max_turns` must be `>= 0`.
- `llm.provider` must not be empty.
- `llm.auth` must be `api_key` or `oauth`.
- `llm.agent_model` and `llm.gate_model` must not be empty (after defaults).
- `llm.effort` must be `low`, `medium`, or `high`.
- `llm.auth: oauth` is rejected when `llm.provider` is `openrouter`.
- `prompt.system_append` and `prompt.system_override` cannot both be set.
- When `llm.auth: oauth`: `llm.oauth.access_token_env` is required; if any of
  the refresh trio (`refresh_token_env`, `client_id_env`, `token_url`) is
  set, all three are required; `token_url`, if set, must be a valid absolute
  URL.
- `retry.strategy` must be `exponential` or `fixed`.
- `retry.max_attempts`, `retry.base_delay_ms`, `retry.max_delay_ms`, and
  `retry.exponent` must all be `>= 0`.
