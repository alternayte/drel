# Drel

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Code-generation-based Go ORM for Postgres and SQLite/LibSQL (Turso).
Type-safe queries, snapshot-based change tracking, zero runtime reflection,
EF Core-level developer experience.

## Installation

```bash
go install github.com/alternayte/drel/cmd/drel@latest
```

## Quick Start

### 1. Define a model

```go
package models

import "github.com/alternayte/drel"

type Task struct {
    drel.Model[int]
    Title    string `db:"title"`
    Done     bool   `db:"done"`
    Priority int    `db:"priority"`
}

func NewTask(title string, priority int) *Task {
    return &Task{Title: title, Priority: priority}
}

func (t *Task) MarkDone() { t.Done = true }
```

### 2. Generate code

```bash
drel generate
```

This produces type-safe query builders, scan functions, snapshot/diff
helpers, and a `DB` struct that aggregates all discovered models.

### 3. Use it

```go
// Open the generated DB
database, err := db.Open(dsn)

// Insert inside a transaction that travels through the context
err = database.WithTx(ctx, func(ctx context.Context) error {
    database.Tx(ctx).Tasks.Add(models.NewTask("Build ORM", 1))
    return nil
})

// Query with generated type-safe columns (read-only, untracked)
tasks, err := database.Tasks.
    Where(models.Tasks.Done.IsFalse()).
    OrderBy(models.Tasks.Priority.Asc()).
    All(ctx)

// Update with change tracking (only modified columns are UPDATEd)
err = database.WithTx(ctx, func(ctx context.Context) error {
    task, err := database.Tx(ctx).Tasks.Find(ctx, 1) // tracked
    if err != nil {
        return err
    }
    task.MarkDone()
    return nil
})

// A nested WithTx call joins the same transaction through a savepoint.
// Reach the transaction anywhere with drel.MustFromContext(ctx).

// Or an explicit multi-statement transaction:
err = database.Transaction(ctx, func(tx *drel.Tx) error {
    repo := drel.NewTxRepository(tx, models.TaskMeta)
    repo.Add(models.NewTask("ship it", 2))
    return tx.SaveChanges(ctx)
})
```

## Features

- **No reflection** -- all scanning, diffing, and query building use
  generated code.
- **Snapshot-based change tracking** -- only modified columns appear in
  UPDATE statements.
- **Type-safe query builder** -- compile-time checked column predicates
  and ordering (`Eq`, `In`, `Between`, `ILike`, `Raw`), range operators
  (`GT`/`GTE`/`LT`/`LTE`, plus `Before`/`After`) on `time.Time`, `uuid.UUID`,
  and value-object columns, conditional `WhereIf`, and valid SQL for empty
  `In`/`And`/`Or` (no `IN ()` corruption).
- **Value objects** -- single-column VOs via the `sql.Scanner` /
  `driver.Valuer` contract (the SQL column type is inferred from the
  underlying primitive), and multi-column VOs via `drel.MultiColumnMapper`
  (e.g. `Money` → `amount` + `currency`), both mapped end-to-end through
  codegen including change-tracking diffs.
- **JSON & array columns** -- slice, map, and struct fields map to `jsonb`
  (Postgres) / `TEXT` (SQLite) through `drel.JSON[T]`, with structural
  change-detection in diffs; native Postgres arrays via a `type=` tag override.
- **Transactions** -- explicit transaction API with configurable isolation,
  read-only transactions (`WithReadOnly`), advisory locks
  (`Tx.AdvisoryLock` / `TryAdvisoryLock`; a SQLite no-op), automatic retry on
  serialization failures (`TransactionWithRetry` / `WithRetry`), and automatic
  flush on commit. Projections, includes, bulk operations, and batching all run
  on the transaction's own connection inside an explicit `Tx`.
- **Soft delete, versioning, audit** -- embed `drel.SoftDelete`,
  `drel.Versioned`, or `drel.Audit` for automatic column management.
- **Primary keys** -- integer auto-increment by default, or application-assigned
  **UUIDv7** via `drel.Model[uuid.UUID]` (generated and stamped at `Add()`, so the
  id is valid before any flush; time-ordered for index locality). Pluggable
  per-model via `SetKeyStrategy` / `SetKeyGenerator`.
- **Relationships** -- generated `RelationInfo` and `IncludeSpec` for
  has-many, has-one, belongs-to, and many-to-many eager loading with
  cross-package support. Filter-aware includes respect soft-delete on
  related models, with `Unscoped()` opt-out.
- **Bulk operations** -- `BulkInsert`, `BulkUpdate`, `BulkDelete`,
  `BulkUpsert` with batching, a Postgres `COPY` fast-path, `ON CONFLICT DO
  NOTHING`, and full-table guards (an unfiltered `BulkUpdate`/`BulkDelete`
  errors unless you opt in with `AllRows()`). App-assigned keys, audit, and
  version columns are honored in bulk paths.
- **Domain events & outbox** -- record events on entities, dispatch them
  after commit, and optionally persist them to a transactional outbox table
  via `Engine.UseOutbox`. `drel.NewRelay` publishes the table: it claims a
  batch under a lease, so more than one replica can poll one table, and it
  keeps the messages of one `PartitionKey` in order. A failed message retries,
  and it moves to the dead-letter state after the attempt limit.
- **Pagination** -- offset (`PageOffset`) and keyset/cursor (`Page`) paging
  with a deterministic primary-key tiebreaker.
- **Projections & aggregations** -- `Select`, `Aggregate`, `GroupBy` into
  arbitrary DTOs.
- **Nested & filtered includes** -- `Include(Users.Posts.Then(Posts.Tags))`,
  with `Where`/`OrderBy`/`Limit` per relationship; split-query loading avoids
  cartesian products.
- **Change-tracking depth** -- tracked queries by default, `AsNoTracking`,
  `Attach`/`Detach`, and nested `Savepoint`s.
- **Migrations** -- dialect-aware schema generation and a structured snapshot
  diff (`drel migrate new`) that emits add/drop/alter for tables, columns,
  types, nullability, and indexes; `up`/`down`/`status`/`lint` for both
  dialects. Declare indexes/checks with `db:` tag options.
- **Read replicas** -- `WithReadReplica` round-robins reads; writes and
  transactions use the primary; `Primary()` forces read-your-writes.
- **Query batching** -- `NewBatch` + `BatchAll`/`BatchFirst`/`BatchCount`
  pipeline queries over pgx (sequential fallback elsewhere).
- **Observability** -- structured `slog` query logging, slow-query and
  dev-mode diagnostics (N+1, unbounded queries, missing-index hints), and an
  OpenTelemetry-adaptable `Tracer`.
- **CLI** -- `drel init`, `generate` (with `--watch` and `//go:generate`
  support), `migrate`, `seed`; dialect validation and atomic code generation
  that fails loudly on duplicate model names, unresolved relations, or
  unsupported field types.
- **Health & timeouts** -- `Engine.Ping`, `HealthCheck`, and `Stats`, plus a
  configurable per-query timeout (`WithQueryTimeout`) and a PgBouncer-compatible
  simple-exec mode.
- **Typed errors** -- `errors.Is(err, drel.ErrUniqueViolation)` (and FK /
  not-null / check / serialization-failure / not-found / concurrency-conflict),
  classified uniformly across Postgres, SQLite, and LibSQL; the original driver
  error stays reachable via `errors.As`.
- **Connection pool control** -- `WithMaxConns`, `WithConnMaxLifetime`,
  `WithConnMaxIdleTime`.
- **Raw SQL escape hatches** -- `Engine.Exec`, `Engine.Query`,
  `Engine.QueryRow`, `RawQuery[T]`, and `Tx.Exec`, `Tx.QueryRow` for anything
  the ORM does not cover.

## Examples

See [examples/](examples/) for working samples:

- [getting-started](examples/getting-started/) -- minimal CRUD
- [sqlite-todo](examples/sqlite-todo/) -- SQLite dialect, tag indexes, cursor pagination
- [model-features](examples/model-features/) -- soft delete, versioning, audit, and JSON/array columns
- [value-objects](examples/value-objects/) -- single-column (`Email`) and multi-column (`Money`) value objects
- [enums](examples/enums/) -- string and int enums with generated DB constraints
- [relationships](examples/relationships/) -- associations and includes
- [bulk-ops](examples/bulk-ops/) -- batch operations
- [api](examples/api/) -- dynamic query composition from HTTP parameters (IQueryable-style conditional `Where` chaining)
- [multi-model](examples/multi-model/) -- domain events, transaction hooks
- [outbox](examples/outbox/) -- transactional outbox: events persisted atomically with data, plus the lease-based relay
- [observability](examples/observability/) -- structured query logging, tracing spans, and dev-mode diagnostics
- [uuid-keys](examples/uuid-keys/) -- application-assigned UUIDv7 primary keys
- [internals](examples/internals/) -- what codegen produces, hand-written, to see the machinery

Primary keys: integer auto-increment by default; use `drel.Model[uuid.UUID]`
for app-assigned UUIDv7 (stamped at `Add()`).

## Dialects

- **Postgres** — direct `pgx`, auto-detected from `postgres://` DSNs.
- **SQLite** — pure-Go `modernc.org/sqlite`, auto-detected from `file:`,
  `sqlite://`, `:memory:`, or `*.db` DSNs.
- **LibSQL/Turso** — `libsql://`/`https://`/`wss://` DSNs, no build flags or
  imports required. Verified end-to-end against a real libSQL server over HTTP.
  Prefer `libsql://`/`https://` over `ws://` for models with `time.Time` columns.

## Limitations

- Migration diffing does not auto-detect column **renames** — a rename appears
  as drop + add; edit the generated SQL if you intend a rename. SQLite cannot
  `ALTER COLUMN TYPE`/nullability in place, so those changes are emitted as
  loud `-- WARNING` comments to be applied by hand.
- Bulk `Set` accepts `any` values — type safety is enforced on column
  predicates and `Find` but not on bulk mutation values.
- True JOIN-based eager loading is intentionally not offered; relationships
  load via batched split queries (correct for every shape, no cartesian
  products).
- Primary keys must be a single surrogate column (`int` auto-increment or
  `uuid.UUID`); composite and natural keys are not yet supported.

## License

MIT -- see [LICENSE](LICENSE).
