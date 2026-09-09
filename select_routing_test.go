package drel

import (
	"context"
	"testing"

	dialectsqlite "github.com/alternayte/drel/internal/dialect/sqlite"
	"github.com/alternayte/drel/internal/driver"
)

type routingDTO struct {
	Name string `db:"name"`
}

type routingGroupDTO struct {
	Name string `db:"name"`
	C    int    `db:"c"`
}

func routingModelMeta() ModelMeta[routingParent] {
	return ModelMeta[routingParent]{
		Table:     "parents",
		Columns:   []string{"id", "name"},
		PKColumns: []string{"id"},
		Scan:      func(Row) (*routingParent, error) { return &routingParent{}, nil },
		PKValue:   func(p *routingParent) any { return p.ID },
	}
}

func newRoutingEngine(calls *[]string) (*Engine, *recDriver, *recDriver) {
	primary := &recDriver{name: "primary", calls: calls}
	r1 := &recDriver{name: "r1", calls: calls}
	e := &Engine{drv: primary, replicas: []driver.Driver{r1}, dia: dialectsqlite.New()}
	return e, primary, r1
}

// recDriver.Query / QueryRow return nil Rows / Row, so Select/GroupBy/Aggregate
// panic when they iterate or scan. The routing call (queryRouted/queryRowRouted)
// is recorded BEFORE that panic, so each call is wrapped in a recover and we
// assert only on the recorded routing target.
func safe(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

func TestSelect_RoutesToReplica_WhenNotPrimary(t *testing.T) {
	var calls []string
	e, _, _ := newRoutingEngine(&calls)
	repo := NewRepository(e, routingModelMeta())
	safe(func() { _, _ = Select[routingDTO](context.Background(), repo.newBuilder(), ColRef("name")) })
	if len(calls) == 0 || calls[0] != "r1:query" {
		t.Fatalf("Select should hit replica by default, got %v", calls)
	}
}

func TestSelect_RoutesToPrimary_WhenPrimaryForced(t *testing.T) {
	var calls []string
	e, _, _ := newRoutingEngine(&calls)
	repo := NewRepository(e, routingModelMeta())
	safe(func() { _, _ = Select[routingDTO](context.Background(), repo.newBuilder().Primary(), ColRef("name")) })
	if len(calls) == 0 || calls[0] != "primary:query" {
		t.Fatalf("Select.Primary() must hit primary, got %v", calls)
	}
}

func TestAggregate_RoutesToPrimary_WhenPrimaryForced(t *testing.T) {
	var calls []string
	e, _, _ := newRoutingEngine(&calls)
	repo := NewRepository(e, routingModelMeta())
	safe(func() {
		_, _ = Aggregate[int](context.Background(), repo.newBuilder().Primary(), CountCol(ColRef("id")))
	})
	if len(calls) == 0 || calls[0] != "primary:queryrow" {
		t.Fatalf("Aggregate.Primary() must hit primary, got %v", calls)
	}
}

func TestAggregate_RoutesToReplica_WhenNotPrimary(t *testing.T) {
	var calls []string
	e, _, _ := newRoutingEngine(&calls)
	repo := NewRepository(e, routingModelMeta())
	safe(func() {
		_, _ = Aggregate[int](context.Background(), repo.newBuilder(), CountCol(ColRef("id")))
	})
	if len(calls) == 0 || calls[0] != "r1:queryrow" {
		t.Fatalf("Aggregate should hit replica by default, got %v", calls)
	}
}

func TestGroupBy_RoutesToReplica_WhenNotPrimary(t *testing.T) {
	var calls []string
	e, _, _ := newRoutingEngine(&calls)
	repo := NewRepository(e, routingModelMeta())
	safe(func() {
		_, _ = GroupBy[routingGroupDTO](context.Background(), repo.newBuilder(),
			[]GroupSpec{Group(ColRef("name"))},
			[]AliasedAgg{As("c", CountCol(ColRef("id")))})
	})
	if len(calls) == 0 || calls[0] != "r1:query" {
		t.Fatalf("GroupBy should hit replica by default, got %v", calls)
	}
}

func TestGroupBy_RoutesToPrimary_WhenPrimaryForced(t *testing.T) {
	var calls []string
	e, _, _ := newRoutingEngine(&calls)
	repo := NewRepository(e, routingModelMeta())
	safe(func() {
		_, _ = GroupBy[routingGroupDTO](context.Background(), repo.newBuilder().Primary(),
			[]GroupSpec{Group(ColRef("name"))},
			[]AliasedAgg{As("c", CountCol(ColRef("id")))})
	})
	for _, c := range calls {
		if c == "r1:query" {
			t.Fatalf("GroupBy.Primary() must hit primary, got %v", calls)
		}
	}
	if len(calls) == 0 || calls[0] != "primary:query" {
		t.Fatalf("GroupBy.Primary() should hit primary, got %v", calls)
	}
}
