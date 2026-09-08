package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/internal/dialect/postgres"
	"github.com/alternayte/drel/internal/driver"
	"github.com/alternayte/drel/internal/testmodels"
)

// fakeCopyTx records COPY invocations and never falls back to Exec for inserts.
type fakeCopyTx struct {
	copiedTable string
	copiedCols  []string
	copiedRows  int
	execCalls   int
}

func (t *fakeCopyTx) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	return nil
}
func (t *fakeCopyTx) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	return nil, nil
}
func (t *fakeCopyTx) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	t.execCalls++
	return 0, nil
}
func (t *fakeCopyTx) Commit(ctx context.Context) error   { return nil }
func (t *fakeCopyTx) Rollback(ctx context.Context) error { return nil }
func (t *fakeCopyTx) CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error) {
	t.copiedTable = table
	t.copiedCols = columns
	t.copiedRows += len(rows)
	return int64(len(rows)), nil
}

// fakeCopyDriver hands out a single shared fakeCopyTx so the test can inspect it.
type fakeCopyDriver struct{ tx *fakeCopyTx }

func (d *fakeCopyDriver) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	return nil
}
func (d *fakeCopyDriver) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	return nil, nil
}
func (d *fakeCopyDriver) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	return 0, nil
}
func (d *fakeCopyDriver) Begin(ctx context.Context) (driver.Tx, error) { return d.tx, nil }
func (d *fakeCopyDriver) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return d.tx, nil
}
func (d *fakeCopyDriver) Close()                         {}
func (d *fakeCopyDriver) Ping(ctx context.Context) error { return nil }
func (d *fakeCopyDriver) Stat() driver.PoolStat          { return driver.PoolStat{} }

func TestBulkInsert_UsesCopyWhenTxSupportsIt(t *testing.T) {
	ctx := context.Background()
	ftx := &fakeCopyTx{}
	eng, err := drel.NewEngine("",
		drel.WithDriver(&fakeCopyDriver{tx: ftx}),
		drel.WithDialect(postgres.New()),
	)
	if err != nil {
		t.Fatal(err)
	}

	repo := drel.NewRepository(eng, testmodels.ProductMeta)
	products := []*testmodels.Product{
		{Name: "A", Price: 1, InStock: true},
		{Name: "B", Price: 2, InStock: false},
	}

	n, err := repo.BulkInsert(ctx, products)
	if err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}
	if n != 2 {
		t.Fatalf("affected = %d, want 2", n)
	}
	if ftx.copiedRows != 2 {
		t.Fatalf("copiedRows = %d, want 2 (COPY path not taken)", ftx.copiedRows)
	}
	if ftx.copiedTable != "products" {
		t.Fatalf("copiedTable = %q, want products", ftx.copiedTable)
	}
	if got := ftx.copiedCols; len(got) != 3 || got[0] != "name" || got[1] != "price" || got[2] != "in_stock" {
		t.Fatalf("copiedCols = %v, want [name price in_stock]", got)
	}
	if ftx.execCalls != 0 {
		t.Fatalf("execCalls = %d, want 0 (should not fall back to multi-row INSERT)", ftx.execCalls)
	}
}
