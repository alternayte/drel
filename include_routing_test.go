package drel

import (
	"context"
	"testing"

	dialectsqlite "github.com/alternayte/drel/internal/dialect/sqlite"
	"github.com/alternayte/drel/internal/driver"
)

// Shared routing-test fixtures. recDriver.Query returns (nil, nil), so the
// `rows.Next()` loop in queryByColumn never runs — we assert only the routing
// call recorded before iteration. The SQLite dialect builds the IN-clause SQL.

type routingParent struct {
	ID       int
	Name     string
	Children []any
}

type routingChild struct {
	ID  int
	PID int
}

func routingParentMeta() *ModelMetaBase {
	return &ModelMetaBase{
		Table:     "parents",
		Columns:   []string{"id"},
		PKColumns: []string{"id"},
		PKValue:   func(e any) any { return e.(*routingParent).ID },
	}
}

func routingChildMeta() *ModelMetaBase {
	return &ModelMetaBase{
		Table:     "children",
		Columns:   []string{"id", "pid"},
		PKColumns: []string{"id"},
		PKValue:   func(e any) any { return e.(*routingChild).ID },
		ColumnValue: func(e any, i int) any {
			c := e.(*routingChild)
			if i == 1 {
				return c.PID
			}
			return c.ID
		},
		ScanRow: func(Row) (any, error) { return &routingChild{}, nil },
	}
}

func routingHasManySpec() IncludeSpec {
	return NewIncludeSpec(&RelationInfo{
		Name:        "Children",
		Type:        HasMany,
		FKColumn:    "pid",
		RelatedMeta: routingChildMeta(),
		FieldSetter: func(p any, related any) { p.(*routingParent).Children = related.([]any) },
	})
}

func TestInclude_RoutesToReplica_WhenNotPrimary(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	r1 := &recDriver{name: "r1", calls: &calls}
	e := &Engine{drv: primary, replicas: []driver.Driver{r1}, dia: dialectsqlite.New()}

	parent := &routingParent{ID: 1}
	exec := &includeExecutor{
		reader:     e,
		parentMeta: routingParentMeta(),
		primary:    false,
	}
	_ = exec.loadRelations(context.Background(), []any{parent}, []IncludeSpec{routingHasManySpec()})

	// The IN query for children must route to the replica, not the primary.
	if len(calls) == 0 || calls[0] != "r1:query" {
		t.Fatalf("include sub-query should hit replica when primary=false, got %v", calls)
	}
}

func TestInclude_RoutesToPrimary_WhenPrimaryForced(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	r1 := &recDriver{name: "r1", calls: &calls}
	e := &Engine{drv: primary, replicas: []driver.Driver{r1}, dia: dialectsqlite.New()}

	parent := &routingParent{ID: 1}
	exec := &includeExecutor{
		reader:     e,
		parentMeta: routingParentMeta(),
		primary:    true,
	}
	_ = exec.loadRelations(context.Background(), []any{parent}, []IncludeSpec{routingHasManySpec()})

	for _, c := range calls {
		if c == "r1:query" {
			t.Fatalf("include sub-query must hit primary when primary=true, got %v", calls)
		}
	}
	if len(calls) == 0 || calls[0] != "primary:query" {
		t.Fatalf("include sub-query should hit primary, got %v", calls)
	}
}

// IncludableQuery.Primary() must force the executor's primary flag so the public
// API can express the read-your-writes intent the finding describes.
func TestIncludableQuery_Primary_ForcesPrimaryRoute(t *testing.T) {
	var calls []string
	primary := &recDriver{name: "primary", calls: &calls}
	r1 := &recDriver{name: "r1", calls: &calls}
	e := &Engine{drv: primary, replicas: []driver.Driver{r1}, dia: dialectsqlite.New()}

	meta := ModelMeta[routingParent]{
		Table:     "parents",
		Columns:   []string{"id"},
		PKColumns: []string{"id"},
		Scan:      func(Row) (*routingParent, error) { return &routingParent{}, nil },
		PKValue:   func(p *routingParent) any { return p.ID },
	}
	repo := NewRepository(e, meta)
	q := repo.Include(routingHasManySpec()).Primary()
	// loadInto must build the executor with primary=true.
	_ = q.loadInto(context.Background(), []*routingParent{{ID: 1}})

	for _, c := range calls {
		if c == "r1:query" {
			t.Fatalf("Include(...).Primary() must route children to primary, got %v", calls)
		}
	}
	if len(calls) == 0 || calls[0] != "primary:query" {
		t.Fatalf("Include(...).Primary() should hit primary, got %v", calls)
	}
}
