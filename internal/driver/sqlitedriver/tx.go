package sqlitedriver

import (
	"context"
	"database/sql"

	"github.com/alternayte/drel/internal/driver"
)

type sqliteTx struct {
	tx *sql.Tx
}

var _ driver.TxUnderlyer = (*sqliteTx)(nil)

func (t *sqliteTx) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

func (t *sqliteTx) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqliteRows{rows: rows}, nil
}

func (t *sqliteTx) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	result, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (t *sqliteTx) Commit(ctx context.Context) error {
	return t.tx.Commit()
}

func (t *sqliteTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback()
}

// Underlying exposes the *sql.Tx the wrapper drives. It implements
// driver.TxUnderlyer, so a caller can join the transaction with a library that
// takes a *sql.Tx.
func (t *sqliteTx) Underlying() any { return t.tx }
