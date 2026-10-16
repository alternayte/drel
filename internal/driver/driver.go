package driver

import (
	"context"
	"time"
)

// PoolConfig configures a driver's connection pool. Zero values mean "use the
// driver default".
type PoolConfig struct {
	MaxConns        int           // maximum open connections
	ConnMaxLifetime time.Duration // recycle connections older than this
	ConnMaxIdleTime time.Duration // close connections idle longer than this
	// SimpleProtocol, when true, makes the Postgres driver use pgx's simple
	// query protocol (no server-side prepared statements) for compatibility with
	// PgBouncer transaction/statement pooling. Ignored by SQLite/libSQL.
	SimpleProtocol bool
}

// PoolStat is a dialect-neutral snapshot of connection-pool utilisation, used
// for health/metrics endpoints. Counts come from pgxpool.Stat (Postgres) or
// sql.DB.Stats (SQLite/libSQL).
type PoolStat struct {
	MaxConns      int32 // configured maximum open connections (0 = driver default / unlimited)
	AcquiredConns int32 // connections currently checked out / in use
	IdleConns     int32 // connections currently idle in the pool
	TotalConns    int32 // total connections currently held (acquired + idle + constructing)
}

// IsolationLevel represents a transaction isolation level.
type IsolationLevel int

const (
	IsoDefault IsolationLevel = iota
	IsoReadCommitted
	IsoRepeatableRead
	IsoSerializable
)

// TxOptions configures transaction behaviour.
type TxOptions struct {
	Isolation IsolationLevel
	ReadOnly  bool
}

// Row represents a single database row that can scan values into destinations.
type Row interface {
	Scan(dest ...any) error
}

// Rows represents a set of database rows from a query result.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}

// Tx represents a database transaction.
type Tx interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// BatchItem is a single queued query in a pipelined batch.
type BatchItem struct {
	SQL  string
	Args []any
}

// BatchResults yields the result rows for each queued query, in order.
type BatchResults interface {
	// Query returns the rows for the next queued query.
	Query() (Rows, error)
	// Close releases the batch results.
	Close() error
}

// Pipeliner is an optional Driver capability: sending multiple queries in a
// single network round-trip (e.g. the pgx pipeline). Drivers that do not
// implement it fall back to sequential execution.
type Pipeliner interface {
	SendBatch(ctx context.Context, items []BatchItem) (BatchResults, error)
}

// BulkCopier is an optional Driver capability: high-throughput bulk load via
// the native COPY protocol (e.g. pgx CopyFrom). Drivers that do not implement
// it fall back to multi-row INSERT. CopyFrom returns the number of rows copied.
type BulkCopier interface {
	CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error)
}

// TxBulkCopier is the transaction-scoped counterpart of BulkCopier: it lets the
// runtime run a COPY inside an already-open transaction so the load commits or
// rolls back atomically with the surrounding work. Txs that do not implement it
// fall back to multi-row INSERT.
type TxBulkCopier interface {
	CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error)
}

// TxUnderlyer is an optional Tx capability: it exposes the driver-native
// transaction handle. The pgx driver returns pgx.Tx; database/sql-backed
// drivers (SQLite, libSQL, WithSQLDB) return *sql.Tx. A caller that must join
// the transaction with another library (a job queue, for example) uses it.
type TxUnderlyer interface {
	Underlying() any
}

// Driver abstracts database access for the drel engine.
type Driver interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	Begin(ctx context.Context) (Tx, error)
	BeginTx(ctx context.Context, opts TxOptions) (Tx, error)
	// Ping verifies a working connection to the database, used by health probes.
	Ping(ctx context.Context) error
	// Stat returns a snapshot of connection-pool utilisation.
	Stat() PoolStat
	Close()
}
