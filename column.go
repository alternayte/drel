package drel

import (
	"cmp"
	"time"

	"github.com/alternayte/drel/internal/ast"
)

type Column[T any] struct {
	name string
}

func NewCol[T any](name string) Column[T] {
	return Column[T]{name: name}
}

func (c Column[T]) Name() string { return c.name }

func (c Column[T]) Eq(v T) Predicate {
	return newComparison(c.name, ast.OpEq, v)
}

func (c Column[T]) NEQ(v T) Predicate {
	return newComparison(c.name, ast.OpNEQ, v)
}

func (c Column[T]) IsNull() Predicate {
	return newNullCheck(c.name, true)
}

func (c Column[T]) IsNotNull() Predicate {
	return newNullCheck(c.name, false)
}

func (c Column[T]) In(vs ...T) Predicate {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return newInComparison(c.name, vals)
}

func (c Column[T]) NotIn(vs ...T) Predicate {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return newNotInComparison(c.name, vals)
}

func (c Column[T]) Asc() OrderExpr {
	return OrderExpr{column: c.name, direction: ast.Asc}
}

func (c Column[T]) Desc() OrderExpr {
	return OrderExpr{column: c.name, direction: ast.Desc}
}

type OrderedColumn[T cmp.Ordered] struct {
	col Column[T]
}

func NewOrderedCol[T cmp.Ordered](name string) OrderedColumn[T] {
	return OrderedColumn[T]{col: NewCol[T](name)}
}

func (c OrderedColumn[T]) Name() string            { return c.col.name }
func (c OrderedColumn[T]) Eq(v T) Predicate        { return c.col.Eq(v) }
func (c OrderedColumn[T]) NEQ(v T) Predicate       { return c.col.NEQ(v) }
func (c OrderedColumn[T]) IsNull() Predicate       { return c.col.IsNull() }
func (c OrderedColumn[T]) IsNotNull() Predicate    { return c.col.IsNotNull() }
func (c OrderedColumn[T]) In(vs ...T) Predicate    { return c.col.In(vs...) }
func (c OrderedColumn[T]) NotIn(vs ...T) Predicate { return c.col.NotIn(vs...) }
func (c OrderedColumn[T]) Asc() OrderExpr          { return c.col.Asc() }
func (c OrderedColumn[T]) Desc() OrderExpr         { return c.col.Desc() }

func (c OrderedColumn[T]) Between(low, high T) Predicate {
	return newBetweenComparison(c.col.name, low, high)
}

func (c OrderedColumn[T]) GT(v T) Predicate {
	return newComparison(c.col.name, ast.OpGT, v)
}

func (c OrderedColumn[T]) GTE(v T) Predicate {
	return newComparison(c.col.name, ast.OpGTE, v)
}

func (c OrderedColumn[T]) LT(v T) Predicate {
	return newComparison(c.col.name, ast.OpLT, v)
}

func (c OrderedColumn[T]) LTE(v T) Predicate {
	return newComparison(c.col.name, ast.OpLTE, v)
}

type StringColumn struct {
	col Column[string]
}

func NewStringCol(name string) StringColumn {
	return StringColumn{col: NewCol[string](name)}
}

func (c StringColumn) Name() string                 { return c.col.name }
func (c StringColumn) Eq(v string) Predicate        { return c.col.Eq(v) }
func (c StringColumn) NEQ(v string) Predicate       { return c.col.NEQ(v) }
func (c StringColumn) IsNull() Predicate            { return c.col.IsNull() }
func (c StringColumn) IsNotNull() Predicate         { return c.col.IsNotNull() }
func (c StringColumn) In(vs ...string) Predicate    { return c.col.In(vs...) }
func (c StringColumn) NotIn(vs ...string) Predicate { return c.col.NotIn(vs...) }
func (c StringColumn) Asc() OrderExpr               { return c.col.Asc() }
func (c StringColumn) Desc() OrderExpr              { return c.col.Desc() }

func (c StringColumn) Like(pattern string) Predicate {
	return newComparison(c.col.name, ast.OpLike, pattern)
}

func (c StringColumn) ILike(pattern string) Predicate {
	return newComparison(c.col.name, ast.OpILike, pattern)
}

func (c StringColumn) Contains(substr string) Predicate {
	return newComparison(c.col.name, ast.OpLike, "%"+substr+"%")
}

func (c StringColumn) HasPrefix(prefix string) Predicate {
	return newComparison(c.col.name, ast.OpLike, prefix+"%")
}

func (c StringColumn) HasSuffix(suffix string) Predicate {
	return newComparison(c.col.name, ast.OpLike, "%"+suffix)
}

func (c StringColumn) GT(v string) Predicate  { return newComparison(c.col.name, ast.OpGT, v) }
func (c StringColumn) GTE(v string) Predicate { return newComparison(c.col.name, ast.OpGTE, v) }
func (c StringColumn) LT(v string) Predicate  { return newComparison(c.col.name, ast.OpLT, v) }
func (c StringColumn) LTE(v string) Predicate { return newComparison(c.col.name, ast.OpLTE, v) }

type BoolColumn struct {
	col Column[bool]
}

func NewBoolCol(name string) BoolColumn {
	return BoolColumn{col: NewCol[bool](name)}
}

func (c BoolColumn) Name() string         { return c.col.name }
func (c BoolColumn) Eq(v bool) Predicate  { return c.col.Eq(v) }
func (c BoolColumn) NEQ(v bool) Predicate { return c.col.NEQ(v) }
func (c BoolColumn) IsNull() Predicate    { return c.col.IsNull() }
func (c BoolColumn) IsNotNull() Predicate { return c.col.IsNotNull() }
func (c BoolColumn) Asc() OrderExpr       { return c.col.Asc() }
func (c BoolColumn) Desc() OrderExpr      { return c.col.Desc() }

func (c BoolColumn) IsTrue() Predicate  { return c.col.Eq(true) }
func (c BoolColumn) IsFalse() Predicate { return c.col.Eq(false) }

// TimeColumn is a dedicated column type for time.Time values. It provides
// range operators (GT/GTE/LT/LTE/Between/Before/After) in addition to the
// standard equality/null/in operators available on Column[T].
type TimeColumn struct {
	col Column[time.Time]
}

func NewTimeCol(name string) TimeColumn {
	return TimeColumn{col: NewCol[time.Time](name)}
}

func (c TimeColumn) Name() string                    { return c.col.name }
func (c TimeColumn) Eq(v time.Time) Predicate        { return c.col.Eq(v) }
func (c TimeColumn) NEQ(v time.Time) Predicate       { return c.col.NEQ(v) }
func (c TimeColumn) IsNull() Predicate               { return c.col.IsNull() }
func (c TimeColumn) IsNotNull() Predicate            { return c.col.IsNotNull() }
func (c TimeColumn) In(vs ...time.Time) Predicate    { return c.col.In(vs...) }
func (c TimeColumn) NotIn(vs ...time.Time) Predicate { return c.col.NotIn(vs...) }
func (c TimeColumn) Asc() OrderExpr                  { return c.col.Asc() }
func (c TimeColumn) Desc() OrderExpr                 { return c.col.Desc() }
func (c TimeColumn) ColRef() ColumnRef               { return ColumnRef{name: c.col.name} }

func (c TimeColumn) GT(v time.Time) Predicate  { return newComparison(c.col.name, ast.OpGT, v) }
func (c TimeColumn) GTE(v time.Time) Predicate { return newComparison(c.col.name, ast.OpGTE, v) }
func (c TimeColumn) LT(v time.Time) Predicate  { return newComparison(c.col.name, ast.OpLT, v) }
func (c TimeColumn) LTE(v time.Time) Predicate { return newComparison(c.col.name, ast.OpLTE, v) }

// Between returns a predicate matching rows where the column value is between
// low and high inclusive (SQL BETWEEN low AND high).
func (c TimeColumn) Between(low, high time.Time) Predicate {
	return newBetweenComparison(c.col.name, low, high)
}

// Before is a convenience alias for LT.
func (c TimeColumn) Before(t time.Time) Predicate { return c.LT(t) }

// After is a convenience alias for GT.
func (c TimeColumn) After(t time.Time) Predicate { return c.GT(t) }

// ComparableColumn is a column type for values that support ordering but are
// not covered by the built-in numeric/string/time-specific column types — for
// example uuid.UUID, *time.Time, or custom value objects that implement a
// total order. It provides the full range-operator set (GT/GTE/LT/LTE/Between).
type ComparableColumn[T any] struct {
	col Column[T]
}

func NewComparableCol[T any](name string) ComparableColumn[T] {
	return ComparableColumn[T]{col: NewCol[T](name)}
}

func (c ComparableColumn[T]) Name() string            { return c.col.name }
func (c ComparableColumn[T]) Eq(v T) Predicate        { return c.col.Eq(v) }
func (c ComparableColumn[T]) NEQ(v T) Predicate       { return c.col.NEQ(v) }
func (c ComparableColumn[T]) IsNull() Predicate       { return c.col.IsNull() }
func (c ComparableColumn[T]) IsNotNull() Predicate    { return c.col.IsNotNull() }
func (c ComparableColumn[T]) In(vs ...T) Predicate    { return c.col.In(vs...) }
func (c ComparableColumn[T]) NotIn(vs ...T) Predicate { return c.col.NotIn(vs...) }
func (c ComparableColumn[T]) Asc() OrderExpr          { return c.col.Asc() }
func (c ComparableColumn[T]) Desc() OrderExpr         { return c.col.Desc() }
func (c ComparableColumn[T]) ColRef() ColumnRef       { return ColumnRef{name: c.col.name} }

func (c ComparableColumn[T]) GT(v T) Predicate  { return newComparison(c.col.name, ast.OpGT, v) }
func (c ComparableColumn[T]) GTE(v T) Predicate { return newComparison(c.col.name, ast.OpGTE, v) }
func (c ComparableColumn[T]) LT(v T) Predicate  { return newComparison(c.col.name, ast.OpLT, v) }
func (c ComparableColumn[T]) LTE(v T) Predicate { return newComparison(c.col.name, ast.OpLTE, v) }

// Between returns a predicate matching rows where the column value is between
// low and high inclusive (SQL BETWEEN low AND high).
func (c ComparableColumn[T]) Between(low, high T) Predicate {
	return newBetweenComparison(c.col.name, low, high)
}

// ColRef returns a ColumnRef for use with Select projections and aggregates.
func (c Column[T]) ColRef() ColumnRef { return ColumnRef{name: c.name} }

// ColRef returns a ColumnRef for use with Select projections and aggregates.
func (c OrderedColumn[T]) ColRef() ColumnRef { return ColumnRef{name: c.col.name} }

// ColRef returns a ColumnRef for use with Select projections and aggregates.
func (c StringColumn) ColRef() ColumnRef { return ColumnRef{name: c.col.name} }

// ColRef returns a ColumnRef for use with Select projections and aggregates.
func (c BoolColumn) ColRef() ColumnRef { return ColumnRef{name: c.col.name} }
