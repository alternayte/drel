package pgxdriver

import (
	"context"

	"github.com/alternayte/drel/internal/driver"
	"github.com/jackc/pgx/v5"
)

type pgxTx struct {
	tx pgx.Tx
}

func (t *pgxTx) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	return t.tx.QueryRow(ctx, sql, args...)
}

func (t *pgxTx) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	rows, err := t.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows: rows}, nil
}

func (t *pgxTx) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := t.tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (t *pgxTx) Commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

func (t *pgxTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}

// CopyFrom performs a high-throughput bulk load via the pgx COPY protocol inside
// this transaction. It implements driver.TxBulkCopier.
func (t *pgxTx) CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error) {
	return t.tx.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows))
}

// Underlying exposes the pgx.Tx the wrapper drives. It implements
// driver.TxUnderlyer, so a caller can join the transaction with a library that
// takes a pgx.Tx (a job queue, for example).
func (t *pgxTx) Underlying() any { return t.tx }
