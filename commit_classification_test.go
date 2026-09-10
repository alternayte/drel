package drel

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel/internal/dberr"
	dialectsqlite "github.com/alternayte/drel/internal/dialect/sqlite"
	"github.com/alternayte/drel/internal/driver"
)

// commitErrTx is a driver.Tx whose Commit returns a fixed error.
type commitErrTx struct{ commitErr error }

func (t *commitErrTx) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	return nil
}
func (t *commitErrTx) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	return nil, nil
}
func (t *commitErrTx) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	return 0, nil
}
func (t *commitErrTx) Commit(ctx context.Context) error   { return t.commitErr }
func (t *commitErrTx) Rollback(ctx context.Context) error { return nil }

// commitErrDriver hands out commitErrTx from Begin/BeginTx.
type commitErrDriver struct{ commitErr error }

func (d *commitErrDriver) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	return nil
}
func (d *commitErrDriver) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	return nil, nil
}
func (d *commitErrDriver) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	return 0, nil
}
func (d *commitErrDriver) Begin(ctx context.Context) (driver.Tx, error) {
	return &commitErrTx{commitErr: d.commitErr}, nil
}
func (d *commitErrDriver) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return &commitErrTx{commitErr: d.commitErr}, nil
}
func (d *commitErrDriver) Ping(ctx context.Context) error { return nil }
func (d *commitErrDriver) Stat() driver.PoolStat          { return driver.PoolStat{} }
func (d *commitErrDriver) Close()                         {}

func TestTransaction_CommitErrorClassified(t *testing.T) {
	// A libSQL-style busy surfaced at COMMIT must classify as serialization.
	drv := &commitErrDriver{commitErr: errors.New("database is locked")}
	e := &Engine{drv: drv}
	err := e.Transaction(context.Background(), func(tx *Tx) error { return nil })
	if !errors.Is(err, dberr.ErrSerializationFailure) {
		t.Fatalf("commit error must classify as ErrSerializationFailure, got %v", err)
	}
}

func TestWithTx_CommitErrorClassified(t *testing.T) {
	drv := &commitErrDriver{commitErr: errors.New("database is locked")}
	e := &Engine{drv: drv}
	err := e.WithTx(context.Background(), func(context.Context) error { return nil })
	if !errors.Is(err, dberr.ErrSerializationFailure) {
		t.Fatalf("WithTx commit error must classify as ErrSerializationFailure, got %v", err)
	}
}

// minimalMeta returns the smallest ModelMeta[commitModel] that BulkInsert and
// BulkUpsert can use: no audit, no versioning, no app-assigned key — just a
// table name and an InsertColumns func. The commitErrTx.Exec returns (0, nil)
// so the commit call is always reached.
type commitModel struct{ Name string }

func minimalMeta() ModelMeta[commitModel] {
	return ModelMeta[commitModel]{
		Table:     "t",
		PKColumns: []string{"id"},
		InsertColumns: func(p *commitModel) ([]string, []any) {
			return []string{"name"}, []any{p.Name}
		},
	}
}

func commitErrEngine(commitErr error) *Engine {
	return &Engine{
		drv: &commitErrDriver{commitErr: commitErr},
		dia: dialectsqlite.New(),
	}
}

func TestBulkInsert_CommitErrorClassified(t *testing.T) {
	// A libSQL-style busy returned at COMMIT must classify as
	// ErrSerializationFailure — identical to the Transaction/SaveChanges paths.
	e := commitErrEngine(errors.New("database is locked"))
	repo := NewRepository(e, minimalMeta())
	_, err := repo.BulkInsert(context.Background(), []*commitModel{{Name: "x"}})
	if !errors.Is(err, dberr.ErrSerializationFailure) {
		t.Fatalf("BulkInsert commit error must classify as ErrSerializationFailure, got %v", err)
	}
}

func TestBulkUpsert_CommitErrorClassified(t *testing.T) {
	// Same coverage for BulkUpsert: commit-time serialization failure must match
	// ErrSerializationFailure via errors.Is.
	e := commitErrEngine(errors.New("database is locked"))
	repo := NewRepository(e, minimalMeta())
	idCol := NewStringCol("id")
	nameCol := NewStringCol("name")
	_, err := repo.BulkUpsert(context.Background(), []*commitModel{{Name: "x"}},
		ConflictColumns(idCol),
		UpdateOnConflict(nameCol),
	)
	if !errors.Is(err, dberr.ErrSerializationFailure) {
		t.Fatalf("BulkUpsert commit error must classify as ErrSerializationFailure, got %v", err)
	}
}
