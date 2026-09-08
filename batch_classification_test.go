package drel

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel/internal/dberr"
	"github.com/alternayte/drel/internal/driver"
	"github.com/jackc/pgx/v5/pgconn"
)

// pipelineErrResults yields a single Query() error mimicking a pgx pipeline
// where a queued query fails with a unique violation.
type pipelineErrResults struct{ queryErr error }

func (r *pipelineErrResults) Query() (driver.Rows, error) { return nil, r.queryErr }
func (r *pipelineErrResults) Close() error                { return nil }

// pipelineDriver is a driver.Driver + driver.Pipeliner whose batch fails.
type pipelineDriver struct {
	sendErr  error
	queryErr error
}

func (d *pipelineDriver) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	return nil
}
func (d *pipelineDriver) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	return nil, nil
}
func (d *pipelineDriver) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	return 0, nil
}
func (d *pipelineDriver) Begin(ctx context.Context) (driver.Tx, error) { return nil, nil }
func (d *pipelineDriver) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return nil, nil
}
func (d *pipelineDriver) Ping(ctx context.Context) error { return nil }
func (d *pipelineDriver) Stat() driver.PoolStat          { return driver.PoolStat{} }
func (d *pipelineDriver) Close()                         {}
func (d *pipelineDriver) SendBatch(ctx context.Context, items []driver.BatchItem) (driver.BatchResults, error) {
	if d.sendErr != nil {
		return nil, d.sendErr
	}
	return &pipelineErrResults{queryErr: d.queryErr}, nil
}

func uniqueViolation() *pgconn.PgError {
	return &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
}

func TestBatch_PipelineQueryErrorClassified(t *testing.T) {
	e := &Engine{drv: &pipelineDriver{queryErr: uniqueViolation()}}
	b := e.NewBatch()
	// Queue one item; its SQL/args are irrelevant — the fake fails at Query().
	b.add("SELECT 1", nil, false, func(rows Rows) error { return nil }, func(error) {})
	err := b.Execute(context.Background())
	if !errors.Is(err, dberr.ErrUniqueViolation) {
		t.Fatalf("pipeline Query error must classify as ErrUniqueViolation, got %v", err)
	}
	// Original *pgconn.PgError must remain reachable.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("original *pgconn.PgError should be reachable, got %v", err)
	}
}

func TestBatch_PipelineSendBatchErrorClassified(t *testing.T) {
	e := &Engine{drv: &pipelineDriver{sendErr: uniqueViolation()}}
	b := e.NewBatch()
	b.add("SELECT 1", nil, false, func(rows Rows) error { return nil }, func(error) {})
	err := b.Execute(context.Background())
	if !errors.Is(err, dberr.ErrUniqueViolation) {
		t.Fatalf("SendBatch error must classify as ErrUniqueViolation, got %v", err)
	}
}
