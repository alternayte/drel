package drel

import (
	"testing"

	"github.com/alternayte/drel/internal/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectComparisons walks a WhereClause tree and returns a flat list of
// (column, op, isNullCheck) tuples so tests can assert predicate shape without
// depending on a dialect.
type cmpShape struct {
	col      string
	op       ast.Operator
	hasValue bool
}

func collectComparisons(w ast.WhereClause, out *[]cmpShape) {
	if w.Comparison != nil {
		*out = append(*out, cmpShape{col: w.Comparison.Column, op: w.Comparison.Op, hasValue: w.Comparison.Value != nil})
	}
	for _, c := range w.Children {
		collectComparisons(c, out)
	}
}

func TestKeysetClause_NonNullSingleColumnUnchanged(t *testing.T) {
	order := []ast.OrderByExpr{
		{Column: "rank", Direction: ast.Asc},
		{Column: "id", Direction: ast.Asc},
	}
	clause, err := keysetClause(order, []any{5, 10})
	require.NoError(t, err)

	var shapes []cmpShape
	collectComparisons(clause, &shapes)
	// (rank > 5) OR (rank = 5 AND id > 10) — three comparisons, all with values.
	assert.Equal(t, 3, len(shapes))
	assert.Equal(t, ast.OpGT, shapes[0].op)
}

func TestKeysetClause_NullCursorAscNullsLast(t *testing.T) {
	order := []ast.OrderByExpr{
		{Column: "rank", Direction: ast.Asc, Nulls: ast.NullsLast},
		{Column: "id", Direction: ast.Asc},
	}
	// cursor value for rank is nil: we are inside the trailing NULL block.
	clause, err := keysetClause(order, []any{nil, 10})
	require.NoError(t, err)

	var shapes []cmpShape
	collectComparisons(clause, &shapes)
	// First OR term: strict-on-rank is impossible (NULLS LAST, nothing after NULL),
	// so the only advancing term is (rank IS NULL AND id > 10).
	var sawIsNull, sawIdGT bool
	for _, s := range shapes {
		if s.col == "rank" && s.op == ast.OpIsNull {
			sawIsNull = true
		}
		if s.col == "id" && s.op == ast.OpGT {
			sawIdGT = true
		}
	}
	assert.True(t, sawIsNull, "expected rank IS NULL tiebreak term")
	assert.True(t, sawIdGT, "expected id > 10 advancing term")
}

func TestKeysetClause_NullCursorAscNullsFirst(t *testing.T) {
	order := []ast.OrderByExpr{
		{Column: "rank", Direction: ast.Asc, Nulls: ast.NullsFirst},
		{Column: "id", Direction: ast.Asc},
	}
	clause, err := keysetClause(order, []any{nil, 10})
	require.NoError(t, err)

	var shapes []cmpShape
	collectComparisons(clause, &shapes)
	// NULLS FIRST: strictly after a NULL means any non-NULL rank, plus the
	// (rank IS NULL AND id > 10) tiebreak.
	var sawIsNotNull bool
	for _, s := range shapes {
		if s.col == "rank" && s.op == ast.OpIsNotNull {
			sawIsNotNull = true
		}
	}
	assert.True(t, sawIsNotNull, "expected rank IS NOT NULL advancing term")
}

func TestKeysetClause_NullCursorDefaultNullsErrors(t *testing.T) {
	order := []ast.OrderByExpr{
		{Column: "rank", Direction: ast.Asc}, // NullsDefault
		{Column: "id", Direction: ast.Asc},
	}
	_, err := keysetClause(order, []any{nil, 10})
	assert.ErrorIs(t, err, ErrCursorColumnNullable)
}

func TestCursorOrder_AppendsEveryKeyColumnAsATiebreak(t *testing.T) {
	got := cursorOrder(
		[]ast.OrderByExpr{{Column: "created_at", Direction: ast.Desc}},
		[]string{"order_id", "line_no"},
	)
	require.Len(t, got, 3)
	assert.Equal(t, "created_at", got[0].Column)
	assert.Equal(t, "order_id", got[1].Column)
	assert.Equal(t, ast.Asc, got[1].Direction)
	assert.Equal(t, "line_no", got[2].Column)
	assert.Equal(t, ast.Asc, got[2].Direction)
}

func TestCursorOrder_DoesNotDuplicateAKeyColumnAlreadyOrdered(t *testing.T) {
	got := cursorOrder(
		[]ast.OrderByExpr{{Column: "order_id", Direction: ast.Desc}},
		[]string{"order_id", "line_no"},
	)
	require.Len(t, got, 2)
	assert.Equal(t, "order_id", got[0].Column)
	assert.Equal(t, ast.Desc, got[0].Direction)
	assert.Equal(t, "line_no", got[1].Column)
}
