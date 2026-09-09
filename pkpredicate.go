package drel

import (
	"fmt"

	"github.com/alternayte/drel/internal/ast"
)

// keyValuesOf splits a primary key value into one value per key column. A nil
// splitter means a single-column key, whose only value is the key itself.
func keyValuesOf(kv func(any) []any, key any) []any {
	if kv == nil {
		return []any{key}
	}
	return kv(key)
}

// pkPredicate builds the WHERE predicate that selects one row by its primary
// key. A single-column key produces a bare equality, byte-identical to the SQL
// drel emitted before composite keys existed. A composite key produces an AND
// of one equality per column.
func pkPredicate(columns []string, values []any) Predicate {
	if len(columns) != len(values) {
		panic(fmt.Sprintf("drel: primary key has %d columns but %d values", len(columns), len(values)))
	}
	if len(columns) == 1 {
		return newComparison(columns[0], ast.OpEq, values[0])
	}
	preds := make([]Predicate, len(columns))
	for i, c := range columns {
		preds[i] = newComparison(c, ast.OpEq, values[i])
	}
	return And(preds...)
}
