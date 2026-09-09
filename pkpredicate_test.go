package drel

import (
	"testing"

	"github.com/alternayte/drel/internal/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPKPredicate_SingleColumnIsABareComparison(t *testing.T) {
	p := pkPredicate([]string{"id"}, []any{7})
	require.NotNil(t, p.clause.Comparison, "a one-column key must not be wrapped in an AND node")
	assert.Equal(t, "id", p.clause.Comparison.Column)
	assert.Equal(t, ast.OpEq, p.clause.Comparison.Op)
	assert.Equal(t, 7, p.clause.Comparison.Value)
	assert.Empty(t, p.clause.Children)
}

func TestPKPredicate_TwoColumnsAreAndedEqualities(t *testing.T) {
	p := pkPredicate([]string{"order_id", "line_no"}, []any{3, 1})
	assert.Nil(t, p.clause.Comparison)
	assert.Equal(t, ast.LogicalAnd, p.clause.LogicalOp)
	require.Len(t, p.clause.Children, 2)
	assert.Equal(t, "order_id", p.clause.Children[0].Comparison.Column)
	assert.Equal(t, 3, p.clause.Children[0].Comparison.Value)
	assert.Equal(t, "line_no", p.clause.Children[1].Comparison.Column)
	assert.Equal(t, 1, p.clause.Children[1].Comparison.Value)
}

func TestPKPredicate_PanicsOnLengthMismatch(t *testing.T) {
	assert.Panics(t, func() { pkPredicate([]string{"a", "b"}, []any{1}) })
}

func TestPKColumnsOf_FallsBackToTheLegacySingleColumn(t *testing.T) {
	assert.Equal(t, []string{"id"}, pkColumnsOf("id", nil))
	assert.Equal(t, []string{"a", "b"}, pkColumnsOf("id", []string{"a", "b"}))
}

func TestKeyValuesOf_FallsBackToTheWholeKey(t *testing.T) {
	assert.Equal(t, []any{7}, keyValuesOf(nil, 7))
	split := func(k any) []any { return []any{k, 2} }
	assert.Equal(t, []any{7, 2}, keyValuesOf(split, 7))
}

func TestFind_CompositeKeyBuildsAnAndedPredicate(t *testing.T) {
	cols := pkColumnsOf("", []string{"order_id", "line_no"})
	split := func(k any) []any {
		key := k.(struct {
			OrderID int
			LineNo  int
		})
		return []any{key.OrderID, key.LineNo}
	}
	p := pkPredicate(cols, keyValuesOf(split, struct {
		OrderID int
		LineNo  int
	}{3, 1}))
	require.Len(t, p.clause.Children, 2)
	assert.Equal(t, 3, p.clause.Children[0].Comparison.Value)
	assert.Equal(t, 1, p.clause.Children[1].Comparison.Value)
}
