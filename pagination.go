package drel

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"errors"
	"fmt"
	"time"

	"github.com/alternayte/drel/internal/ast"
)

// ErrCursorPaginationNeedsOrderBy is returned when Page is called without an OrderBy.
// Keyset pagination requires a deterministic ordering.
var ErrCursorPaginationNeedsOrderBy = errors.New("drel: cursor pagination requires OrderBy")

// ErrPaginationNeedsLimit is returned when a pagination method is called without Take/Limit.
var ErrPaginationNeedsLimit = errors.New("drel: pagination requires Take/Limit to set the page size")

// ErrInvalidCursor is returned when a cursor string cannot be decoded.
var ErrInvalidCursor = errors.New("drel: invalid cursor")

// ErrInvalidPageSize is returned when a pagination page size (Take/Limit) is <= 0.
var ErrInvalidPageSize = errors.New("drel: page size (Take/Limit) must be > 0")

// ErrCursorColumnNullable is returned when a keyset cursor must page across a
// NULL-valued ordering key whose NULL placement is not pinned. Pin it with
// NullsFirst()/NullsLast() on that OrderBy column so paging is deterministic.
var ErrCursorColumnNullable = errors.New(
	"drel: cursor pagination over a NULL-valued ordering key requires NullsFirst()/NullsLast() on that column")

// OffsetPage holds a page of results from offset-based pagination.
type OffsetPage[T any] struct {
	Items      []*T
	Total      int
	Page       int // 1-based page number
	PageSize   int
	TotalPages int
	HasMore    bool
}

// CursorPage holds a page of results from keyset (cursor) pagination.
type CursorPage[T any] struct {
	Items          []*T
	NextCursor     string // empty when there are no more results
	PreviousCursor string // empty when there is no previous page
	HasMore        bool
	HasPrev        bool
}

func init() {
	// Register the concrete types that can appear as ordering-key values so they
	// survive gob round-tripping inside an opaque cursor.
	gob.Register(int(0))
	gob.Register(int8(0))
	gob.Register(int16(0))
	gob.Register(int32(0))
	gob.Register(int64(0))
	gob.Register(uint(0))
	gob.Register(uint8(0))
	gob.Register(uint16(0))
	gob.Register(uint32(0))
	gob.Register(uint64(0))
	gob.Register(float32(0))
	gob.Register(float64(0))
	gob.Register("")
	gob.Register(false)
	gob.Register([]byte(nil))
	gob.Register(time.Time{})
}

// cursorPayload is the gob-encoded contents of a cursor string. Columns is stored
// alongside Vals so a cursor cannot be silently applied to a mismatched ordering.
type cursorPayload struct {
	Columns []string
	Vals    []any
}

func encodeCursor(columns []string, vals []any) (string, error) {
	// Register each value's concrete type so gob can encode named types
	// (enums, uuid.UUID, value objects) behind the []any. Registration is
	// process-global and idempotent for a given type; subsequent decodes in the
	// same process therefore succeed. The static init() registrations cover the
	// built-in kinds for decode-before-encode robustness across restarts.
	for _, v := range vals {
		registerCursorType(v)
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(cursorPayload{Columns: columns, Vals: vals}); err != nil {
		return "", fmt.Errorf("drel: encode cursor (order-key type may be unsupported): %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

// registerCursorType registers v's concrete type with gob, swallowing the
// benign panic gob raises if a different type was already registered under the
// same name (extremely unlikely for ordering-key types).
func registerCursorType(v any) {
	if v == nil {
		return
	}
	defer func() { _ = recover() }()
	gob.Register(v)
}

func decodeCursor(s string) (cursorPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursorPayload{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	var p cursorPayload
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&p); err != nil {
		return cursorPayload{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	return p, nil
}

// keysetClause builds the WHERE predicate for keyset pagination given an ordering
// and the cursor values of the last row of the previous page. For ORDER BY
// (c1 d1, c2 d2, ...) the predicate is the lexicographic row comparison:
//
//	(S1)
//	OR (E1 AND S2)
//	OR (E1 AND E2 AND S3) ...
//
// where Ek is the null-aware equality term for column k (col = vk, or col IS NULL
// when vk is nil) and Sk is the strict "this column advances past the cursor"
// term. Sk and Ek are NULL-aware so nullable ordering columns never drop rows.
func keysetClause(order []ast.OrderByExpr, vals []any) (ast.WhereClause, error) {
	var orTerms []ast.WhereClause
	for k := range order {
		var andTerms []ast.WhereClause
		for j := 0; j < k; j++ {
			eq, err := keysetEqTerm(order[j], vals[j])
			if err != nil {
				return ast.WhereClause{}, err
			}
			andTerms = append(andTerms, eq)
		}
		strict, ok, err := keysetStrictTerm(order[k], vals[k])
		if err != nil {
			return ast.WhereClause{}, err
		}
		if !ok {
			// No row can strictly advance past the cursor on this column
			// (e.g. NULLS LAST sitting on a NULL): contributes no OR term.
			continue
		}
		andTerms = append(andTerms, strict)
		if len(andTerms) == 1 {
			orTerms = append(orTerms, andTerms[0])
		} else {
			orTerms = append(orTerms, ast.WhereClause{LogicalOp: ast.LogicalAnd, Children: andTerms})
		}
	}
	if len(orTerms) == 0 {
		// Cursor sits at the very end under the given ordering: match nothing.
		// A guaranteed-false predicate keeps the page empty rather than full.
		return ast.WhereClause{Comparison: &ast.ComparisonNode{Column: order[0].Column, Op: ast.OpIsNull}}, nil
	}
	if len(orTerms) == 1 {
		return orTerms[0], nil
	}
	return ast.WhereClause{LogicalOp: ast.LogicalOr, Children: orTerms}, nil
}

// keysetEqTerm builds the null-aware equality tiebreak term for a prefix column:
// "col = v" when v is non-nil, "col IS NULL" when v is nil.
func keysetEqTerm(o ast.OrderByExpr, v any) (ast.WhereClause, error) {
	if v == nil {
		if o.Nulls == ast.NullsDefault {
			return ast.WhereClause{}, ErrCursorColumnNullable
		}
		return ast.WhereClause{Comparison: &ast.ComparisonNode{Column: o.Column, Op: ast.OpIsNull}}, nil
	}
	return ast.WhereClause{Comparison: &ast.ComparisonNode{Column: o.Column, Op: ast.OpEq, Value: v}}, nil
}

// keysetStrictTerm builds the "this column strictly advances past the cursor"
// term. ok is false when no row can be strictly after the cursor on this column
// (NULLS LAST sitting on a NULL), so the caller omits that OR branch.
func keysetStrictTerm(o ast.OrderByExpr, v any) (term ast.WhereClause, ok bool, err error) {
	op := ast.OpGT
	nullsLast := o.Nulls == ast.NullsLast
	if o.Direction == ast.Desc {
		op = ast.OpLT
		// For DESC, Postgres default is NULLS FIRST (NULLs sort before values).
		// We only need the IS NULL extension for NULLS LAST with DESC.
	}
	if v != nil {
		// Non-NULL cursor value: for columns where NULLs sort AFTER the current
		// value (NULLS LAST), the strict advancing term must include IS NULL so
		// NULL rows are not silently dropped by the WHERE clause (SQL NULL
		// comparisons evaluate to UNKNOWN, never to TRUE).
		// For NULLS FIRST the NULLs already appeared before this value, so
		// plain col op v is sufficient.
		if nullsLast {
			// (col op v OR col IS NULL)
			return ast.WhereClause{
				LogicalOp: ast.LogicalOr,
				Children: []ast.WhereClause{
					{Comparison: &ast.ComparisonNode{Column: o.Column, Op: op, Value: v}},
					{Comparison: &ast.ComparisonNode{Column: o.Column, Op: ast.OpIsNull}},
				},
			}, true, nil
		}
		return ast.WhereClause{Comparison: &ast.ComparisonNode{Column: o.Column, Op: op, Value: v}}, true, nil
	}
	// NULL cursor value: depends on NULL placement.
	switch o.Nulls {
	case ast.NullsLast:
		// NULLs are last; nothing is strictly after a NULL on this column.
		return ast.WhereClause{}, false, nil
	case ast.NullsFirst:
		// NULLs are first; strictly after a NULL means any non-NULL value.
		return ast.WhereClause{Comparison: &ast.ComparisonNode{Column: o.Column, Op: ast.OpIsNotNull}}, true, nil
	default:
		return ast.WhereClause{}, false, ErrCursorColumnNullable
	}
}

// cursorOrder returns the ordering used for keyset pagination: the caller's
// ORDER BY, followed by every primary key column that is not already ordered.
// The key columns make the ordering total, so a cursor never skips or repeats
// a row when the caller's ordering has ties.
func cursorOrder(orderBy []ast.OrderByExpr, pkColumns []string) []ast.OrderByExpr {
	out := append([]ast.OrderByExpr(nil), orderBy...)
	for _, pk := range pkColumns {
		seen := false
		for _, o := range out {
			if o.Column == pk {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, ast.OrderByExpr{Column: pk, Direction: ast.Asc})
		}
	}
	return out
}

// buildOffsetPage assembles an OffsetPage from a fetched slice and the total count.
func buildOffsetPage[T any](items []*T, total, pageSize, offset int) *OffsetPage[T] {
	page := 1
	totalPages := 0
	if pageSize > 0 {
		page = offset/pageSize + 1
		totalPages = (total + pageSize - 1) / pageSize
	}
	return &OffsetPage[T]{
		Items:      items,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
		HasMore:    offset+len(items) < total,
	}
}

// validateCursorColumns ensures a decoded cursor matches the current ordering,
// preventing a cursor minted under one OrderBy from being misapplied to another.
func validateCursorColumns(p cursorPayload, order []ast.OrderByExpr) error {
	if len(p.Columns) != len(order) || len(p.Vals) != len(order) {
		return fmt.Errorf("%w: cursor does not match the current ordering", ErrInvalidCursor)
	}
	for i := range order {
		if p.Columns[i] != order[i].Column {
			return fmt.Errorf("%w: cursor was created for a different ordering", ErrInvalidCursor)
		}
	}
	return nil
}

// cursorForItem encodes the ordering-key cursor for a single item under the
// given order. Used to mint PreviousCursor for backward navigation.
func cursorForItem[T any](meta *ModelMeta[T], order []ast.OrderByExpr, item *T) (string, error) {
	vals, err := extractCursorVals(meta, order, item)
	if err != nil {
		return "", err
	}
	cols := make([]string, len(order))
	for i, o := range order {
		cols[i] = o.Column
	}
	return encodeCursor(cols, vals)
}

// invertOrder returns a copy of order with every direction flipped, used to run
// a backward keyset query.
func invertOrder(order []ast.OrderByExpr) []ast.OrderByExpr {
	out := make([]ast.OrderByExpr, len(order))
	for i, o := range order {
		o.Direction = ast.Asc
		if order[i].Direction == ast.Asc {
			o.Direction = ast.Desc
		}
		o.Column = order[i].Column
		// Flip NULLS placement too so the inverted scan is a true mirror.
		switch order[i].Nulls {
		case ast.NullsFirst:
			o.Nulls = ast.NullsLast
		case ast.NullsLast:
			o.Nulls = ast.NullsFirst
		default:
			o.Nulls = ast.NullsDefault
		}
		out[i] = o
	}
	return out
}

// reverseItems reverses a slice in place.
func reverseItems[T any](items []*T) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

// extractCursorVals reads the ordering-key column values from an entity, in the
// order columns appear in `order`.
func extractCursorVals[T any](meta *ModelMeta[T], order []ast.OrderByExpr, entity *T) ([]any, error) {
	if meta.ColumnValue == nil {
		return nil, fmt.Errorf("drel: cursor pagination on %s requires generated ColumnValue accessor", meta.Table)
	}
	vals := make([]any, len(order))
	for i, o := range order {
		idx := findColumnIndex(meta.Columns, o.Column)
		if idx < 0 {
			return nil, fmt.Errorf("drel: cursor pagination: order column %q not found on %s", o.Column, meta.Table)
		}
		v := meta.ColumnValue(entity, idx)
		if tv, ok := v.(time.Time); ok {
			// Drop the monotonic clock reading and normalize the location so the
			// gob-encoded cursor compares cleanly against the DB-stored value.
			v = tv.Round(0).UTC()
		}
		vals[i] = v
	}
	return vals, nil
}
