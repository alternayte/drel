# Drel

Code-generation-based Go ORM targeting Postgres and SQLite/LibSQL (Turso). Delivers EF Core-level DX with sqlc/pgx-level performance via compile-time type safety, snapshot-based change tracking, and zero runtime reflection.

## Design Principles

- **Performance by default** — generated code, no reflection, direct pgx for Postgres. Static queries are pre-generated SQL strings.
- **DX over ceremony** — defining and querying models should feel natural.
- **Domain models, not data bags** — models encapsulate behavior via value objects and domain methods.
- **Feature-slice native** — models live in feature packages, not a central `models/` directory. Codegen aggregates across packages.
- **Escape hatches everywhere** — raw SQL, custom queries, manual transactions always available.

## Architecture

```
CLI (cmd/drel)           Runtime library (drel package)
  ├── model scanner        ├── Engine / Tx (context)
  ├── codegen emitter      ├── Repository[T]
  └── migration gen        ├── Query builder → AST → Dialect emitter
      (Atlas)              │     ├── Postgres (pgx)
                           │     └── SQLite (go-sqlite3 / libsql)
                           └── Change tracker (snapshot/diff)
```

**Query path:** Query Builder API → AST nodes → Dialect Emitter → SQL string + args

**Change tracking:** `Find` snapshots entity state via generated `snapshotT()`. `SaveChanges` diffs via generated `diffT()`. Only changed fields go into UPDATE. No reflection at any point.

## Project Layout

```
features/<name>/
  model.go          — struct definition + domain logic + value objects
  <name>_drel.go    — GENERATED: query builder, scan, snapshot, diff
  handlers.go       — HTTP handlers
  queries.go        — custom query functions
  events.go         — domain event types
db/
  drel_gen.go       — GENERATED: aggregated DB struct
  migrations/       — SQL migration files (Atlas)
docs/
  prd.md            — product requirements document
```

## Key Conventions

- **Unexported model fields** with accessor methods for encapsulation. Generated code in the same package accesses them directly.
- **Value objects** implement `drel.ColumnMapper` (single column) or `drel.MultiColumnMapper` (multi-column). Validation lives in constructors.
- **Enums** are Go `string` or `int` types with `const` values. Codegen discovers them and generates DB constraints.
- **ModelMeta[T]** — each model registers a metadata struct with scan/snapshot/diff functions. `Repository[T]` uses these; no reflection needed.
- **Context transaction** — `db.WithTx(ctx, fn)` opens a transaction and puts it in the context. `db.Tx(ctx).Users.Find()` = tracked; `db.Users.All()` = untracked read-only. A nested `WithTx` call uses a savepoint.

## Commands

```bash
# Build
go build ./...

# Test
go test ./...

# Vet / lint
go vet ./...

# Code generation (once CLI exists)
go run ./cmd/drel generate

# Migrations (once CLI exists)
go run ./cmd/drel migrate new "<name>"
go run ./cmd/drel migrate up
go run ./cmd/drel migrate down
go run ./cmd/drel migrate status
```

## Dependencies

| Dependency | Purpose | Scope |
|---|---|---|
| `jackc/pgx/v5` | Postgres driver | Runtime (Postgres) |
| `mattn/go-sqlite3` or `modernc.org/sqlite` | SQLite driver | Runtime (SQLite) |
| `tursodatabase/libsql-client-go` | Turso driver | Runtime (LibSQL) |
| `github.com/google/uuid` | UUIDv7 generation for app-assigned keys | Runtime (only when using uuid PKs) |
| `ariga.io/atlas` | Migration diffing/generation | CLI only |
| `golang.org/x/tools/go/packages` | Go source analysis for codegen | CLI only |

Zero runtime dependencies beyond the database driver and `google/uuid` (used only for UUIDv7 key generation).

## Current Milestone

**M1 — Core Engine:** Model definition, codegen CLI (scan + emit), Postgres dialect with pgx, basic CRUD, change tracking with snapshot diffing, implicit transactions, type-safe query builder.
