# Contributing to drel

Thanks for your interest in improving drel! This document covers how to build,
test, and submit changes.

drel is pre-1.0 — the API may still change between minor versions. Bug reports,
real-world usage feedback, and focused PRs are all very welcome.

## Prerequisites

- Go (see the version in [`go.mod`](go.mod)).
- Docker — required only for the integration test suites (they use
  [testcontainers](https://golang.testcontainers.org/) to spin up Postgres and
  libSQL).

## Building

```bash
go build ./...                 # core + CLI (Postgres + SQLite + LibSQL/Turso)
go vet ./...
gofmt -l .                     # should print nothing
```

The CLI lives in `./cmd/drel`.

## Testing

```bash
# Fast unit + SQLite tests (no Docker needed)
go test ./...
go test -race ./...

# Integration suite — Postgres + a real libsql-server round-trip (needs Docker)
go test -tags integration ./...

# Benchmarks (needs Docker)
go test -tags integration -bench . -benchmem .
```

Integration tests are gated behind the `integration` build tag, so the default
`go test ./...` stays fast and Docker-free. **Please add tests for new query
paths** — several real bugs (e.g. int64/int mismatches) only surface against a
real database.

The root integration suite shares one Postgres container. `setupTestDB` creates
a fresh, empty database on that container for each test, so every test still
starts from an empty schema and from sequence 1. The container starts on the
first test that needs it, and `TestMain` stops it at the end. Do not start a
container in a new test — call `setupTestDB` or `newTestEngine`.

## Code generation

drel generates per-model code and an aggregated `DB` struct. After changing the
emitter (`internal/codegen`), regenerate every example so they don't drift:

```bash
for d in examples/*/; do
  [ -f "$d/drel.yaml" ] && (cd "$d" && go run ../../cmd/drel generate)
done
```

Generated files (`*_drel.go`, `db/drel_gen.go`) are gofmt-formatted by the
emitter and committed to the repo.

## Commits & pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`,
  `fix:`, `docs:`, `chore:`, `test:`, `refactor:`. The release changelog is
  generated from commit subjects.
- Keep PRs focused; include tests and update docs (README / `docs/prd.md`) when
  behavior or public API changes.
- Before opening a PR, make sure `go build ./...`, `go vet ./...`,
  `gofmt -l .` (empty), and `go test ./...` all pass. CI runs these plus the
  integration suites.

## Reporting bugs

Open an issue with a minimal reproduction (model definition + the query/SaveChanges
that misbehaves), the dialect, and the drel version. See
[SECURITY.md](SECURITY.md) for security-sensitive reports.
