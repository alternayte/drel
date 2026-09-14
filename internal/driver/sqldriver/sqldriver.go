// Package sqldriver adapts any database/sql connection to driver.Driver. It
// backs the libSQL/Turso driver and the public WithSQLDB option, which lets a
// caller supply a driver drel does not depend on.
package sqldriver

import (
	"context"
	"database/sql"

	"github.com/alternayte/drel/internal/driver"
)

// Driver implements driver.Driver over a database/sql handle.
type Driver struct {
	db *sql.DB
}

// New wraps an open *sql.DB. The caller owns the handle's configuration; Close
// closes it.
func New(db *sql.DB) *Driver { return &Driver{db: db} }

// DB returns the wrapped handle.
func (d *Driver) DB() *sql.DB { return d.db }

// ApplyPoolConfig applies the non-zero pool settings to the handle.
func ApplyPoolConfig(db *sql.DB, pc ...driver.PoolConfig) {
	if len(pc) == 0 {
		return
	}
	if pc[0].MaxConns > 0 {
		db.SetMaxOpenConns(pc[0].MaxConns)
	}
	if pc[0].ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(pc[0].ConnMaxLifetime)
	}
	if pc[0].ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(pc[0].ConnMaxIdleTime)
	}
}

func (d *Driver) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

func (d *Driver) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlRows{rows: rows}, nil
}

func (d *Driver) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	result, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *Driver) Begin(ctx context.Context) (driver.Tx, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &sqlTx{tx: tx}, nil
}

// BeginTx starts a transaction. An SQLite-compatible database supports only
// SERIALIZABLE isolation, so the requested level is ignored; the ReadOnly flag
// is forwarded.
func (d *Driver) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: opts.ReadOnly})
	if err != nil {
		return nil, err
	}
	return &sqlTx{tx: tx}, nil
}

func (d *Driver) Close() { d.db.Close() }

// Ping verifies a working connection to the database.
func (d *Driver) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// Stat returns a snapshot of the database/sql connection pool.
func (d *Driver) Stat() driver.PoolStat {
	s := d.db.Stats()
	return driver.PoolStat{
		MaxConns:      int32(s.MaxOpenConnections),
		AcquiredConns: int32(s.InUse),
		IdleConns:     int32(s.Idle),
		TotalConns:    int32(s.OpenConnections),
	}
}

type sqlRows struct{ rows *sql.Rows }

func (r *sqlRows) Next() bool             { return r.rows.Next() }
func (r *sqlRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *sqlRows) Close()                 { r.rows.Close() }
func (r *sqlRows) Err() error             { return r.rows.Err() }

type sqlTx struct{ tx *sql.Tx }

func (t *sqlTx) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

func (t *sqlTx) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlRows{rows: rows}, nil
}

func (t *sqlTx) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	result, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (t *sqlTx) Commit(ctx context.Context) error   { return t.tx.Commit() }
func (t *sqlTx) Rollback(ctx context.Context) error { return t.tx.Rollback() }
