package drel

import (
	"context"
	"strings"
	"testing"

	dialectsqlite "github.com/alternayte/drel/internal/dialect/sqlite"
	"github.com/alternayte/drel/internal/driver"
)

// sqlCaptureDriver records the SQL text passed to Query so a test can inspect
// the ORDER BY clause without a real database.
type sqlCaptureDriver struct {
	lastSQL string
}

func (d *sqlCaptureDriver) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	d.lastSQL = sql
	return nil
}
func (d *sqlCaptureDriver) Query(ctx context.Context, sql string, args ...any) (driver.Rows, error) {
	d.lastSQL = sql
	return nil, nil
}
func (d *sqlCaptureDriver) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	d.lastSQL = sql
	return 0, nil
}
func (d *sqlCaptureDriver) Begin(ctx context.Context) (driver.Tx, error) { return nil, nil }
func (d *sqlCaptureDriver) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return nil, nil
}
func (d *sqlCaptureDriver) Close()                         {}
func (d *sqlCaptureDriver) Ping(ctx context.Context) error { return nil }
func (d *sqlCaptureDriver) Stat() driver.PoolStat          { return driver.PoolStat{} }

type poParent struct {
	ID       int
	Children []any
}

type poChild struct {
	OrderID  int
	LineNo   int
	ParentID int
}

func poParentMeta() *ModelMetaBase {
	return &ModelMetaBase{
		Table:     "po_parents",
		Columns:   []string{"id"},
		PKColumns: []string{"id"},
		PKValue:   func(e any) any { return e.(*poParent).ID },
	}
}

// poChildMeta deliberately declares a two-column PKColumns. queryByColumn's
// caller (loadHasManyOrOne) always passes the relationship *target*'s meta
// here, and the plan's Limit #1 forbids a composite-key model from being a
// target — Task 8 enforces that at codegen time. This test bypasses that by
// constructing the meta directly, purely to exercise the partitionOrder
// fallback's tiebreak-building logic in isolation.
func poChildMeta() *ModelMetaBase {
	return &ModelMetaBase{
		Table:     "po_children",
		Columns:   []string{"order_id", "line_no", "parent_id"},
		PKColumns: []string{"order_id", "line_no"},
		PKValue:   func(e any) any { c := e.(*poChild); return [2]int{c.OrderID, c.LineNo} },
		ColumnValue: func(e any, i int) any {
			c := e.(*poChild)
			switch i {
			case 0:
				return c.OrderID
			case 1:
				return c.LineNo
			default:
				return c.ParentID
			}
		},
		ScanRow: func(Row) (any, error) { return &poChild{}, nil },
	}
}

func poHasManySpec(limit int) IncludeSpec {
	return NewIncludeSpec(&RelationInfo{
		Name:        "Children",
		Type:        HasMany,
		FKColumn:    "parent_id",
		RelatedMeta: poChildMeta(),
		FieldSetter: func(p any, related any) { p.(*poParent).Children = related.([]any) },
	}).Limit(limit)
}

// TestQueryByColumn_PartitionOrderDefaultsToEveryKeyColumn proves the
// per-parent LIMIT tiebreak orders by every primary key column, in key
// order, not just the first — a composite key needs all of them to be a
// deterministic tiebreak.
func TestQueryByColumn_PartitionOrderDefaultsToEveryKeyColumn(t *testing.T) {
	drv := &sqlCaptureDriver{}
	e := &Engine{drv: drv, dia: dialectsqlite.New()}

	parents := []any{&poParent{ID: 1}, &poParent{ID: 2}}
	exec := &includeExecutor{
		reader:     e,
		parentMeta: poParentMeta(),
		primary:    true,
	}
	// batch of 2 parents + a limit forces the per-parent PARTITION BY path.
	_ = exec.loadRelations(context.Background(), parents, []IncludeSpec{poHasManySpec(3)})

	if drv.lastSQL == "" {
		t.Fatal("expected a query to have been issued")
	}
	orderIdx := strings.Index(drv.lastSQL, "ORDER BY")
	if orderIdx < 0 {
		t.Fatalf("expected an ORDER BY clause in the partitioned query, got: %s", drv.lastSQL)
	}
	orderClause := drv.lastSQL[orderIdx:]
	rnIdx := strings.Index(orderClause, ")")
	if rnIdx >= 0 {
		orderClause = orderClause[:rnIdx]
	}
	if !strings.Contains(orderClause, `"order_id"`) {
		t.Fatalf("partition ORDER BY must include order_id, got: %s", orderClause)
	}
	if !strings.Contains(orderClause, `"line_no"`) {
		t.Fatalf("partition ORDER BY must include line_no, got: %s", orderClause)
	}
	orderIdIdx := strings.Index(orderClause, `"order_id"`)
	lineNoIdx := strings.Index(orderClause, `"line_no"`)
	if orderIdIdx < 0 || lineNoIdx < 0 || orderIdIdx > lineNoIdx {
		t.Fatalf("expected order_id before line_no, in key order, got: %s", orderClause)
	}
}
