# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres
to [Semantic Versioning](https://semver.org/). While the major version is `0`,
minor versions may contain breaking changes.

## [Unreleased]

### Added

- **Transaction propagation through the context.** `Engine.WithTx(ctx, fn)`
  opens a transaction, puts it in the context it passes to `fn`, and commits
  when `fn` returns nil. `drel.FromContext(ctx)` returns the transaction.
  `drel.MustFromContext(ctx)` returns it and panics when it is absent, so a
  wiring fault fails at once. A nested `WithTx` call on the same engine reuses
  the transaction and opens a savepoint. It does not begin a second
  transaction, and it does not commit. A nested call that carries transaction
  options returns `drel.ErrNestedTxOptions`. The behaviour is the same for
  Postgres, SQLite and LibSQL.
- Generated code gains `db.WithTx(ctx, fn, opts...)`, the `TxRepos` struct, and
  `db.Tx(ctx)`, which returns the tracked repositories bound to the transaction
  in the context.

### Removed

**Breaking.** The connectionless `UnitOfWork` is deleted. `Engine.NewUnitOfWork`,
`drel.UnitOfWork`, `drel.UoWRepository`, `drel.NewUoWRepository`, the generated
`UnitOfWork` struct, the generated `db.NewUnitOfWork` and the generated
`UoW<Model>Repository` types are all gone.

`UnitOfWork` held no connection. `SaveChanges` opened its own short transaction
and committed it, so a caller could not write an application row and a control
row (an outbox entry, an inbox dedupe row, a projection checkpoint) in one
transaction. `Tx` holds the connection, so the context now carries `*Tx`.

Migration:

```go
// before
uow := database.NewUnitOfWork()
uow.Users.Add(u)
err := uow.SaveChanges(ctx)

// after
err := database.WithTx(ctx, func(ctx context.Context) error {
    database.Tx(ctx).Users.Add(u)
    return nil
})
```

The flush is automatic at commit. Call `drel.MustFromContext(ctx).SaveChanges(ctx)`
only when later work in the same transaction needs the generated ids.

## [0.5.0] - 2026-06-15

Production-readiness release. A broad pass over correctness, feature
completeness, hardening, and developer experience that closes the gap between
drel's documented feature set and a production-grade ORM. Since the major
version is `0`, this minor includes correctness fixes that change behavior.

### Added

#### Value objects & column types
- **Multi-column value objects** via `drel.MultiColumnMapper`
  (`DrelColumns`/`DrelValues`/`DrelScanMulti`) — e.g. `Money` → `amount` +
  `currency` — mapped end-to-end through codegen, including change-tracking diffs.
- **Single-column value objects** via the `sql.Scanner` / `driver.Valuer`
  contract, with the SQL column type inferred from the underlying primitive.
- **Range operators** (`GT`/`GTE`/`LT`/`LTE`/`Between`, `Before`/`After`) on
  `time.Time`, `uuid.UUID`, and value-object columns via generated `TimeColumn`
  / `ComparableColumn`.
- **JSON & array columns:** slice, map, and struct fields map to `jsonb`
  (Postgres) / `TEXT` (SQLite) through `drel.JSON[T]`, with structural
  change-detection in diffs; native Postgres arrays via a `type=` tag override.
- `WhereIf` / `True` conditional filters; empty `In`/`NotIn`/`And`/`Or` now emit
  valid SQL instead of an invalid `IN ()`; non-panicking `RawErr`.

#### Bulk, transactions & concurrency
- **Postgres `COPY` fast-path** for `BulkInsert` (`pgx.CopyFrom`) with a
  parameterized fallback; `ON CONFLICT DO NOTHING`.
- **Full-table guards:** an unfiltered `BulkUpdate`/`BulkDelete` errors unless
  you opt in with `AllRows()`. App-assigned keys, audit, and version columns are
  honored in bulk paths.
- **Advisory locks:** `Tx.AdvisoryLock` / `TryAdvisoryLock` (Postgres; SQLite no-op).
- **Automatic retry:** `Engine.TransactionWithRetry` + `WithRetry(RetryConfig)`,
  classifying serialization failures (including at COMMIT), pipeline errors, and
  `SQLITE_BUSY`.
- **Read-only transactions** (`WithReadOnly`) and full `Tx`/`UnitOfWork` parity
  for `Select`/`Aggregate`/`GroupBy`/`Include`/`Bulk*`/`Batch` — all run on the
  transaction's own connection.

#### Operations & observability
- `Engine.Ping`, `HealthCheck`, and `Stats`; a per-query timeout
  (`WithQueryTimeout`); a PgBouncer-compatible simple-exec mode.
- Tracing spans on transaction, bulk, and pipeline paths; a structured batch
  error model (`ErrBatchPartial`, per-item errors, both `errors.Is` targets
  reachable through the chain); concurrency-safe hook registration; bounded
  dev-mode N+1 detection with a timeout-guarded EXPLAIN probe.
- `DISTINCT`, `COUNT(DISTINCT)`, `COUNT(*)`, and JOINs in projections.
- Backward cursor pagination (`Before` / `PreviousCursor` / `HasPrev`).

#### CLI & tooling
- `drel generate --watch` and `//go:generate drel generate` support.
- Atomic code generation (temp + rename, stale-file cleanup) that fails loudly
  on duplicate model names, unresolved relations, or unsupported field types;
  dialect validation and `--config=value` parsing.
- `dreltest` / `pgtest`: error-returning `WithSeed`, `CreateSchema`,
  `WithMigrations`, and dialect guards.

### Changed
- Projections (`Select`/`GroupBy`) now bind result columns to DTO fields by
  `db`-tag **name**, not struct-declaration order; an unknown projected column
  fails loudly with `ErrUnknownProjectionColumn`. (`RawQuery` keeps struct-order
  binding, now documented.)
- The change tracker is finalized only **after** a successful commit, so a
  failed-then-retried `SaveChanges` is safe.
- The transactional outbox is now a post-flush event sink, so events recorded on
  entities created inside before-commit hooks reach both the outbox and
  after-commit handlers.

### Fixed
- **Projection value corruption** — out-of-order `Select`/`GroupBy` columns
  silently swapped values into the wrong DTO fields.
- **Keyset pagination** — `Page` ignored `Skip`; a nullable `ORDER BY` key
  dropped rows; a zero/negative page size panicked. Now correct, with
  `NULLS FIRST/LAST` and null-aware keysets.
- **Delete after a mid-transaction `SaveChanges`** no longer silently skips the
  (soft or hard) delete.
- **Identity map** is keyed by `(table, PK)` — one tracked instance per row, no
  silent lost updates.
- Versioned-on-delete; Attach-on-Audit duplicate column; uint primary-key schema.
- `Include(...).Limit(n)` is applied per-parent (window function); many-to-many
  UUID keys and per-relation `OrderBy` are preserved.
- Migration robustness: drift `verify`, a migration lock, first-`down`
  pivot/enum ordering, FK `ON DELETE`/`ON UPDATE`, SQLite `RETURNING`, and a
  libSQL `ws://` `time.Time` corruption guard.
- Detached-context rollback, savepoint-release safety, and outbox after-commit
  panic recovery.
- A data race in query/commit hook registration
  (`OnQuery`/`OnBeforeCommit`/`OnAfterCommit`).
- `db:` tag parsing: comma-safe `check=`, working `default=`, and fail-loud on
  unknown tag options.

## [0.4.0] - 2026-06-04

Application-assigned primary keys.

### Added
- **Pluggable primary-key strategies.** A model declared with
  `drel.Model[uuid.UUID]` now gets an application-assigned **UUIDv7** key,
  generated and stamped at `Add()` time — the id is valid before any flush, so
  you can record domain events, wire foreign keys, and build object graphs
  without a database round-trip. The INSERT carries the id and reads back only
  the generated timestamps. Integer primary keys keep their database-generated
  auto-increment behavior unchanged. The strategy is inferred from the PK type;
  override it at runtime with `drel.SetKeyStrategy` / `drel.SetKeyGenerator`.
- **`drel.Repo(tx, meta)`** — sugar for `drel.NewTxRepository(tx, meta)`.
- **`Model.SetID`** — assign an application-supplied primary key.
- New example **`examples/uuid-keys`** demonstrating the UUIDv7 flow; the
  `examples/outbox` example now uses app-assigned UUIDs and no longer needs a
  mid-transaction `SaveChanges` to obtain the id.

### Changed
- `github.com/google/uuid` is now a direct dependency (used only for UUIDv7 key
  generation) — a documented exception to the zero-runtime-dependency rule.

### Fixed
- Inserting an app-assigned model whose key was never set now fails loudly with
  a clear error instead of silently persisting a zero key.

## [0.3.2] - 2026-06-03

Production-hardening.

### Added
- **Typed, dialect-neutral errors.** `errors.Is(err, drel.ErrUniqueViolation)`
  and `ErrForeignKeyViolation` / `ErrNotNullViolation` / `ErrCheckViolation` /
  `ErrSerializationFailure`, classified uniformly across Postgres (SQLSTATE),
  SQLite (result codes), and LibSQL (message match). The original driver error
  (e.g. `*pgconn.PgError`) remains reachable via `errors.As`.
- **Connection pool configuration:** `WithMaxConns`, `WithConnMaxLifetime`,
  `WithConnMaxIdleTime` (applied to Postgres, SQLite, and LibSQL pools).

### Fixed
- **Bulk parameter-limit overflow.** `BulkInsert`/`BulkUpsert` sized batches at a
  fixed 1000 rows, which overflowed the per-statement parameter limit for wide
  tables (e.g. >65 columns on Postgres, fewer on SQLite). Batch size is now
  derived from the column count.

## [0.3.1] - 2026-06-03

### Changed
- **LibSQL/Turso works out of the box.** Removed the `libsql` build tag: a
  `libsql://` / `https://` / `wss://` URL now just works with no build flags or
  extra imports, alongside `postgres://`, `file:`, etc. The libSQL client (all
  pure Go, no CGO) is compiled into every build. Removed `ErrLibSQLNotBuilt`.

## [0.3.0] - 2026-06-03

A large release that takes drel from a Postgres-only core to a multi-dialect ORM
with EF Core-style change tracking, robust migrations, observability, and scale
features. It supersedes the unreleased 0.2.0 development line. Everything below
is new since `v0.1.0`.

### Added

#### Dialects & drivers
- **SQLite** dialect and driver via pure-Go `modernc.org/sqlite` (no CGO),
  auto-detected from `file:`, `sqlite://`, `:memory:`, or `*.db` DSNs. WAL,
  busy-timeout, and foreign-keys pragmas; in-memory DBs are pinned to one
  connection.
- **LibSQL/Turso** driver (opt-in via the `libsql` build tag), reusing the
  SQLite-compatible dialect. Detects `libsql://` / `https://` / `http://` /
  `wss://` / `ws://`; `WithAuthToken` injects the Turso token; clear
  `ErrLibSQLNotBuilt` when used without the tag. Verified end-to-end against a
  real `libsql-server` over HTTP.
- DSN-based dialect auto-detection in `NewEngine`; `WithDriver`/`WithDialect`
  overrides. Non-RETURNING mutation path (insert readback) for SQLite/LibSQL.

#### Querying & change tracking
- **UnitOfWork** (`db.NewUnitOfWork()`): EF Core DbContext-style change tracking
  with typed, tracked repositories (`uow.Users.Add/Find/Remove/Attach/Detach/
  AsNoTracking`) and `uow.SaveChanges`.
- Tracked queries, `AsNoTracking`, `Attach`/`Detach`, and nested `Savepoint`s on
  the transaction API.
- Offset pagination (`PageOffset`) and keyset/cursor pagination (`Page`,
  `After`/`Take`) with a deterministic primary-key tiebreaker.
- Projections and aggregations into DTOs: `Select`, `Aggregate`, `GroupBy`
  (+ `GroupBy`/`Having`/aggregate AST nodes), plus a reflection-based DTO scanner
  used only for ad-hoc DTOs.
- Nested includes (`Include(Users.Posts.Then(Posts.Tags))`) and refinable
  includes (`Where`/`OrderBy`/`Limit`/`Unscoped`/`WithoutFilter` per relation);
  `IncludableQuery` composes with root `Where`/`OrderBy`/pagination.
- `RawQuery[T]` / `RawQueryRow[T]` with per-dialect placeholder rewriting.

#### Migrations & codegen
- Structured **schema-snapshot migration diff**: add/drop tables, add/drop
  columns, type/nullability/default changes, indexes, and enums, persisted via
  `.drel_snapshot.json`. SQLite in-place ALTER limitations and column renames
  are surfaced as loud `-- WARNING`/`-- NOTE` comments rather than silent skips.
- `db:` tag options for indexes and constraints: `unique`, `index`,
  `index=<name>` (composite), `check=<expr>`.
- CLI: `drel init` and `drel seed`; dialect-aware `migrate up/down/status/lint`
  (Postgres and SQLite); generated code is now gofmt-clean.

#### Scale & observability
- Read replicas: `WithReadReplica` round-robins reads; writes/transactions use
  the primary; `Primary()` forces read-your-writes.
- Query batching: `NewBatch` + `BatchAll`/`BatchFirst`/`BatchCount` over the pgx
  pipeline, with a sequential fallback.
- Transactional outbox: `Engine.UseOutbox` writes events to an outbox table
  within the SaveChanges transaction; `OutboxSchema` DDL helper.
- Observability: `WithLogger` (slog), `WithQueryLog`, `WithSlowQueryThreshold`,
  `WithTracer` (OpenTelemetry-adaptable), and `WithDevMode` diagnostics (N+1
  heuristic, unbounded-query and unused-tracking warnings, Postgres EXPLAIN-based
  missing-index hints).

#### Testing
- `dreltest` package: `NewSQLite` (in-memory) and `Begin` (savepoint isolation);
  `dreltest/pgtest.NewPostgres` (testcontainers) with `WithMigrations`/`WithSeed`.

### Changed
- Documentation (PRD, README) reconciled with the implemented API; performance
  claims replaced with measured benchmark numbers (no sqlc comparison claimed).
- Migrations are powered by a built-in differ — drel does **not** depend on Atlas.

### Fixed
- `COUNT` no longer emitted trailing `ORDER BY`/`LIMIT`/`OFFSET` (broke offset
  pagination totals).
- Migration version collisions when two migrations were created in the same
  second; down migrations now undo up in reverse order.
- Cursor pagination now encodes named order-key types (enums, `uuid.UUID`, value
  objects); the over-fetch sentinel row is no longer tracked.
- `Attach(StateUnchanged)` no longer panics on insert-only models; nested
  same-name savepoints no longer collide.

## [0.1.0]

Initial release: Postgres (pgx) core, code generation (model scanning, query
builders, scan/snapshot/diff), basic CRUD, snapshot-based change tracking,
implicit transactions, and the type-safe query builder.

[0.5.0]: https://github.com/alternayte/drel-go/releases/tag/v0.5.0
[0.4.0]: https://github.com/alternayte/drel-go/releases/tag/v0.4.0
[0.3.2]: https://github.com/alternayte/drel-go/releases/tag/v0.3.2
[0.3.1]: https://github.com/alternayte/drel-go/releases/tag/v0.3.1
[0.3.0]: https://github.com/alternayte/drel-go/releases/tag/v0.3.0
[0.1.0]: https://github.com/alternayte/drel-go/releases/tag/v0.1.0
