# Security Policy

## Supported versions

Wright is pre-alpha. Only the latest commit on `main` is supported — there are
no maintained release branches yet.

## Reporting a vulnerability

Please report security issues privately, not in a public issue:

- Preferred: [GitHub Private Vulnerability Reporting](../../security/advisories/new)
  (Security tab → "Report a vulnerability").
- Alternative: email farzankhalili01@gmail.com.

Please include reproduction steps and the impact you'd expect. You should get
an acknowledgement within a few days; pre-alpha means fixes ship as soon as
they're ready rather than on a disclosure timeline.

## Threat model

Wright polls issues carrying a trigger label, feeds the issue text to an LLM
agent, and gives that agent tools to read/write files in a sandboxed clone of
your repository, then opens a pull request with the result. **The issue text
is untrusted input** — it can come from anyone who can file an issue on a
repo Wright watches, which for public repos is anyone with a GitHub/GitLab
account. This is a prompt-injection surface: a crafted issue could try to
steer the agent into destructive or exfiltrating behavior.

**What limits the blast radius today:**

- **The trigger label is the actual security boundary.** Wright only acts on
  issues carrying `trigger_label` (default `wright`). Anyone who can apply
  that label to an issue can direct the agent — see the hardening guidance
  below.
- **Docker sandbox isolation** (`internal/sandbox`). The agent's tool loop
  runs inside a container, not on the host. It only ever operates on a clone
  of the target repo inside that container.
- **A fixed operational contract** the agent cannot be talked out of,
  regardless of what the issue text says or what `system_override` in config
  replaces: Wright does not let the agent self-commit or self-push outside
  the sandboxed flow, and tool access is restricted to the paths and
  operations Wright grants — `system_override` replaces default *behavior*
  guidance, not this contract.
- **A gate model** (`internal/gate`) triages issues before the agent ever
  runs, as a first filter against off-topic or malformed input.
- **`auto_merge: false` by default.** Wright opens a PR; a human merges it.
  Nothing lands on your default branch without a second, human, decision
  unless you explicitly opt in to auto-merge.
- **Outbound text and reference sanitization** at the provider boundary
  (`internal/provider/sanitize.go`) before anything the agent produces is
  posted back to GitHub/GitLab.

**What Wright does *not* defend against:**

- A trusted labeler filing a genuinely malicious issue — the label is the
  trust boundary, not the issue content.
- Vulnerabilities in the LLM provider itself, or in the target repo's own
  build/test tooling that the sandboxed agent invokes (e.g. a malicious
  `Makefile` target or test that does something dangerous when run — the
  sandbox limits *where* that runs, not what a compromised build script could
  do inside the container).
- Supply-chain compromise of Wright's own dependencies.
- Anything outside the token scopes described below — Wright cannot do more
  damage than the token it's given permits, which is why token scoping
  matters.

## Token scope guidance

Wright needs a repo-write token to open PRs, add/remove labels, and comment on
issues. Based on the actual API calls each adapter makes
(`internal/provider/github`, `internal/provider/gitlab`):

**GitHub.** The adapter reads/writes repository contents (blobs, trees,
commits, branches), issues (list, get, comment, label), and pull requests
(list, create, get, edit, merge). Recommended:

- **Fine-grained personal access token** (preferred over classic) scoped to
  just the repo(s) Wright watches, with repository permissions:
  - Contents: Read and write
  - Issues: Read and write
  - Pull requests: Read and write
  - Metadata: Read (required automatically)
  - Nothing else — no Actions, no Administration, no organization permissions.
- If you must use a classic PAT, `repo` is the minimum scope that covers
  this (GitHub doesn't subdivide classic scopes further for private repos).

**GitLab.** The adapter uses the Commits/Branches/Issues/Notes/MergeRequests
REST APIs, which require the `api` scope — `read_repository` /
`write_repository` alone are not sufficient since Wright doesn't operate over
git-over-HTTP. Recommended:

- A **project access token** (not a personal one) scoped to the specific
  project, with the `api` scope and **Developer** role. Use **Maintainer**
  only if the target branch is protected in a way that requires it to merge.

**Either provider:** prefer a token scoped to the specific repo(s), not your
whole account/org. Store it in the environment variable Wright reads
(`WRIGHT_GITHUB_TOKEN` / `WRIGHT_GITLAB_TOKEN`, or a `token_env` override per
repo) — never in `wright.yaml` itself.

## Hardening guidance

- **Trusted labelers only.** Since the trigger label is the security
  boundary, restrict who can apply it — e.g. via branch/label permissions,
  or by only running Wright against repos where you control who has triage
  access.
- **Least-privilege tokens.** Scope the token to exactly the repo(s) in
  `wright.yaml`, per the guidance above. Rotate it if you suspect exposure.
- **Leave `auto_merge: false`** unless you have CI gating the merge and trust
  every labeler on the repo. This is the single biggest lever you have.
- **Review PRs Wright opens like any other external contribution** — the
  sandbox and operational contract reduce risk, they don't eliminate the
  need for human review before merge.
