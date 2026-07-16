# Contributing to Wright

Thanks for considering a contribution. Wright is pre-alpha and interfaces may
still move, but issues and PRs are both welcome.

## Before you start

For anything beyond a small fix, open an issue first to discuss the approach.
It's early enough that a change you'd otherwise spend real time on could
collide with a direction already in progress.

## Development setup

Requires Go 1.26+ and Docker (for the sandbox and its tests).

```bash
make build   # compile ./cmd/wright with version ldflags
make test    # go test ./...   (no live API calls)
make lint    # golangci-lint run
make tidy    # go mod tidy
```

CI runs `go vet`, `go test -race ./...`, and `golangci-lint` (pinned to
`v2.12.2`) on every push and PR — run all three locally before opening one.

### Testing rules

- **No live API calls, ever**, in `go test ./...`. Provider adapters
  (`internal/provider/github`, `internal/provider/gitlab`) are tested against
  `httptest` servers with canned fixtures — follow that pattern for new
  adapter code.
- Manual, end-to-end verification against a real GitHub/GitLab repo is done
  with `wright smoke`, not by adding live calls to the test suite:

  ```bash
  make build
  export WRIGHT_GITHUB_TOKEN=ghp_...      # or WRIGHT_GITLAB_TOKEN=glpat-...
  ./wright smoke --config wright.yaml --repo you/scratch-repo
  ```

  Use a scratch repo you control — `smoke` exercises the real write path
  (branch, commit, PR, comment, label).
- Tests must pass under `-race`.

## Commit messages

Plain, descriptive, imperative mood (`Fix pagination in GitHub issue
comments`, not `Fixed` or `Fixes`). No fixed prefix convention is enforced —
match the style of `git log`.

## Pull requests

- Keep PRs scoped to one change; split unrelated fixes into separate PRs.
- Include tests for new behavior and for bug fixes (a regression test that
  fails without your fix, passes with it).
- Update `docs/CONFIGURATION.md` or the README if your change affects
  configuration or user-facing behavior.
- Describe *why*, not just *what*, in the PR description — the diff already
  shows what changed.

## Reporting bugs

Open a GitHub issue with reproduction steps, what you expected, and what
happened instead. For security vulnerabilities, see [SECURITY.md](SECURITY.md)
instead of filing a public issue.
