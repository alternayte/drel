package drel

import (
	"testing"

	"github.com/alternayte/drel/internal/ast"
	"github.com/stretchr/testify/assert"
)

func TestIncludeSpec_Unscoped(t *testing.T) {
	rel := &RelationInfo{Name: "books", Type: HasMany}
	spec := NewIncludeSpec(rel)

	assert.False(t, spec.unscoped)

	unscoped := spec.Unscoped()
	assert.True(t, unscoped.unscoped)
	assert.False(t, spec.unscoped) // original unchanged
}

func TestIncludeSpec_WithoutFilter(t *testing.T) {
	rel := &RelationInfo{Name: "posts", Type: HasMany}
	base := NewIncludeSpec(rel)

	wf := base.WithoutFilter("active")
	assert.Equal(t, []string{"active"}, wf.withoutFilter)
	assert.Empty(t, base.withoutFilter) // original unchanged

	wf2 := wf.WithoutFilter("tenant")
	assert.Equal(t, []string{"active", "tenant"}, wf2.withoutFilter)
	assert.Len(t, wf.withoutFilter, 1) // first copy unchanged
}

func TestIncludeSpec_Where(t *testing.T) {
	rel := &RelationInfo{Name: "posts"}
	base := NewIncludeSpec(rel)

	filtered := base.Where(newComparison("status", ast.OpEq, "published"))
	assert.Len(t, filtered.wheres, 1, "expected 1 where clause")
	assert.Len(t, base.wheres, 0, "original was mutated")

	// Chaining adds another clause without mutating previous copy.
	filtered2 := filtered.Where(newComparison("title", ast.OpEq, "hello"))
	assert.Len(t, filtered2.wheres, 2, "expected 2 where clauses after second Where()")
	assert.Len(t, filtered.wheres, 1, "first filtered copy was mutated")
}

func TestIncludeSpec_OrderBy(t *testing.T) {
	rel := &RelationInfo{Name: "posts"}
	base := NewIncludeSpec(rel)

	ordered := base.OrderBy(OrderExpr{column: "created_at", direction: ast.Desc})
	assert.Len(t, ordered.orderBy, 1, "expected 1 order-by expression")
	assert.Equal(t, "created_at", ordered.orderBy[0].Column)
	assert.Equal(t, ast.Desc, ordered.orderBy[0].Direction)
	assert.Len(t, base.orderBy, 0, "original was mutated")

	// Multiple exprs in one call.
	ordered2 := base.OrderBy(
		OrderExpr{column: "title", direction: ast.Asc},
		OrderExpr{column: "id", direction: ast.Desc},
	)
	assert.Len(t, ordered2.orderBy, 2)
}

func TestIncludeSpec_Limit(t *testing.T) {
	rel := &RelationInfo{Name: "posts"}
	base := NewIncludeSpec(rel)

	limited := base.Limit(5)
	assert.NotNil(t, limited.limit, "limit should be set")
	assert.Equal(t, 5, *limited.limit)
	assert.Nil(t, base.limit, "original was mutated")

	// Updating the limit pointer on the copy does not affect the original.
	*limited.limit = 99
	assert.Nil(t, base.limit, "original limit unexpectedly set after pointer mutation")
}

func TestIncludeSpec_Chaining(t *testing.T) {
	rel := &RelationInfo{Name: "posts"}
	spec := NewIncludeSpec(rel).
		Where(newComparison("status", ast.OpEq, "published")).
		OrderBy(OrderExpr{column: "created_at", direction: ast.Desc}).
		Limit(5)

	assert.Len(t, spec.wheres, 1, "missing where clause")
	assert.Len(t, spec.orderBy, 1, "missing order-by expression")
	assert.NotNil(t, spec.limit)
	assert.Equal(t, 5, *spec.limit)
}

func TestIncludeSpec_UnscopedPreservesFilters(t *testing.T) {
	rel := &RelationInfo{Name: "posts"}
	spec := NewIncludeSpec(rel).
		Where(newComparison("status", ast.OpEq, "published")).
		Limit(10).
		Unscoped()

	assert.True(t, spec.unscoped)
	assert.Len(t, spec.wheres, 1, "Where clauses lost through Unscoped()")
	assert.NotNil(t, spec.limit)
}

func TestToMetaBase_CopiesFilters(t *testing.T) {
	meta := &ModelMeta[testModel]{
		Table:         "test",
		Columns:       []string{"id"},
		PKColumns:     []string{"id"},
		Scan:          func(Row) (*testModel, error) { return nil, nil },
		Snapshot:      func(*testModel) any { return nil },
		Diff:          func(*testModel, any) []FieldChange { return nil },
		PKValue:       func(*testModel) any { return nil },
		InsertColumns: func(*testModel) ([]string, []any) { return nil, nil },
		Filters:       []NamedFilter{SoftDeleteFilter},
	}
	base := ToMetaBase(meta)
	assert.Len(t, base.Filters, 1)
	assert.Equal(t, "soft_delete", base.Filters[0].Name)
}

type testModel struct{}
