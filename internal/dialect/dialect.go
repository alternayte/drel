package dialect

import "github.com/alternayte/drel/internal/ast"

// Result holds a generated SQL statement and its bound arguments.
type Result struct {
	SQL  string
	Args []any
}

// ColumnValue pairs a column name with its new value for mutation queries.
// If Value is a RawExpr, the dialect should embed it as literal SQL instead of a parameter.
type ColumnValue struct {
	Column string
	Value  any
}

// RawExpr represents a raw SQL expression to embed inline (e.g., NOW()).
type RawExpr struct {
	SQL string
}

// AdvisoryLockMode selects blocking vs non-blocking advisory-lock acquisition.
type AdvisoryLockMode int

const (
	// AdvisoryLockBlocking waits until the lock is acquired.
	AdvisoryLockBlocking AdvisoryLockMode = iota
	// AdvisoryLockTry attempts to acquire without blocking and reports success.
	AdvisoryLockTry
)

// Dialect generates SQL for a specific database backend.
type Dialect interface {
	// Name reports the dialect identifier: "postgres" or "sqlite".
	// libSQL/Turso reuse the SQLite dialect, so they also report "sqlite".
	Name() string
	SupportsReturning() bool
	// UsesQuestionPlaceholders reports whether the dialect binds parameters with
	// "?" (SQLite/libSQL) rather than "$N" (Postgres). Raw SQL written with $N is
	// rewritten to ? when this is true.
	UsesQuestionPlaceholders() bool
	// Now returns the SQL expression for the current timestamp.
	// Postgres returns "NOW()", SQLite returns "CURRENT_TIMESTAMP".
	Now() string
	// Explain returns a query that produces a textual execution plan for the
	// given query, and whether the dialect supports plan inspection in the form
	// drel uses for missing-index hints. SQLite returns ("", false).
	Explain(query string) (string, bool)
	BuildSelect(node ast.SelectNode) Result
	BuildInsert(table string, columns []string, values []any, returningCols []string) Result
	// The six statement builders below take the primary key as parallel slices:
	// pkColumns names every key column in key order, and pkValues holds one
	// value per column in the same order. A one-column key produces exactly the
	// SQL drel emitted before composite keys existed.
	BuildUpdate(table string, changes []ColumnValue, pkColumns []string, pkValues []any) Result
	BuildDelete(table string, pkColumns []string, pkValues []any) Result
	BuildSoftDelete(table string, pkColumns []string, pkValues []any) Result
	BuildUpdateVersioned(table string, changes []ColumnValue, pkColumns []string, pkValues []any, versionCol string, currentVersion int) Result
	BuildDeleteVersioned(table string, pkColumns []string, pkValues []any, versionCol string, currentVersion int) Result
	BuildSoftDeleteVersioned(table string, pkColumns []string, pkValues []any, versionCol string, currentVersion int) Result
	BuildBulkInsert(table string, columns []string, rows [][]any) Result
	BuildBulkUpdate(table string, sets []ColumnValue, where *ast.WhereClause) Result
	BuildBulkDelete(table string, where *ast.WhereClause) Result
	BuildBulkSoftDelete(table string, where *ast.WhereClause) Result
	BuildBulkUpsert(table string, columns []string, rows [][]any, conflictCols []string, updateCols []string, doNothing bool) Result
	// AdvisoryLockSQL returns the SQL + args to acquire a transaction-scoped
	// advisory lock for key, and whether the dialect supports advisory locks.
	// When supported is false the runtime treats the call as a documented no-op.
	// For AdvisoryLockTry the returned query yields a single boolean column
	// reporting whether the lock was acquired.
	AdvisoryLockSQL(key int64, mode AdvisoryLockMode) (Result, bool)
}
