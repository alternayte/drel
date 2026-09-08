package drel

import (
	"context"
	"errors"
	"time"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dialect"
)

// ErrNotFound is returned when a query expects a result but finds none.
var ErrNotFound = errors.New("drel: not found")

// ErrConcurrencyConflict is returned when an entity was modified by another transaction.
var ErrConcurrencyConflict = errors.New("drel: concurrency conflict — entity was modified by another transaction")

// QueryBuilder constructs and executes typed queries with an immutable builder pattern.
type QueryBuilder[T any] struct {
	engine   *Engine
	meta     *ModelMeta[T]
	wheres   []ast.WhereClause
	orderBy  []ast.OrderByExpr
	limit    *int
	offset   *int
	after    *string
	before   *string
	filters  []NamedFilter
	primary  bool
	distinct bool
	joins    []ast.JoinNode

	// allowFullTable, when set via AllRows(), opts a BulkUpdate/BulkDelete out of
	// the full-table safety guard for deliberate whole-table writes.
	allowFullTable bool

	// timeout, when non-nil, overrides the engine default query timeout for this
	// query only. nil means "use the engine default".
	timeout *time.Duration

	// When tracker is non-null, results materialized by All are snapshotted and
	// tracked (used by UnitOfWork repositories).
	tracker *changeTracker
	base    *ModelMetaBase
}

func newQueryBuilder[T any](engine *Engine, meta *ModelMeta[T]) *QueryBuilder[T] {
	return &QueryBuilder[T]{
		engine:  engine,
		meta:    meta,
		filters: append([]NamedFilter(nil), meta.Filters...),
	}
}

func (q *QueryBuilder[T]) clone() *QueryBuilder[T] {
	c := &QueryBuilder[T]{
		engine:         q.engine,
		meta:           q.meta,
		wheres:         make([]ast.WhereClause, len(q.wheres)),
		orderBy:        make([]ast.OrderByExpr, len(q.orderBy)),
		limit:          q.limit,
		offset:         q.offset,
		after:          q.after,
		before:         q.before,
		filters:        append([]NamedFilter(nil), q.filters...),
		primary:        q.primary,
		distinct:       q.distinct,
		joins:          append([]ast.JoinNode(nil), q.joins...),
		allowFullTable: q.allowFullTable,
		timeout:        q.timeout,
		tracker:        q.tracker,
		base:           q.base,
	}
	copy(c.wheres, q.wheres)
	copy(c.orderBy, q.orderBy)
	return c
}

// AllRows opts a BulkUpdate or BulkDelete out of the full-table safety guard,
// permitting a deliberate whole-table write with no Where predicate.
func (q *QueryBuilder[T]) AllRows() *QueryBuilder[T] {
	c := q.clone()
	c.allowFullTable = true
	return c
}

// Primary forces this query to read from the primary connection instead of a
// read replica. Has no effect when no replicas are configured.
func (q *QueryBuilder[T]) Primary() *QueryBuilder[T] {
	c := q.clone()
	c.primary = true
	return c
}

// Timeout overrides the engine's default query timeout for this query only. The
// shorter of this value and any caller-supplied context deadline wins. Pass a
// non-positive duration to disable any default for this query.
func (q *QueryBuilder[T]) Timeout(d time.Duration) *QueryBuilder[T] {
	c := q.clone()
	c.timeout = &d
	return c
}

// effectiveTimeout returns the per-builder timeout override, or 0 to mean "use
// the engine default".
func (q *QueryBuilder[T]) effectiveTimeout() time.Duration {
	if q.timeout != nil {
		return *q.timeout
	}
	return 0
}

// Distinct makes the projection emit SELECT DISTINCT, de-duplicating result rows.
// It affects Select and GroupBy projections; it has no effect on All/First/Count/Exists.
func (q *QueryBuilder[T]) Distinct() *QueryBuilder[T] {
	c := q.clone()
	c.distinct = true
	return c
}

// LeftJoin adds a LEFT JOIN to the given table on the supplied ON expression.
// Projection columns from the joined table must be table-qualified via
// QualifiedColRef(...).Ref(). Joins affect Select and GroupBy projections only.
func (q *QueryBuilder[T]) LeftJoin(table string, on JoinOn) *QueryBuilder[T] {
	c := q.clone()
	c.joins = append(c.joins, ast.JoinNode{Table: table, Type: ast.JoinLeft, On: on.sql})
	return c
}

// InnerJoin adds an INNER JOIN to the given table on the supplied ON expression.
// See LeftJoin for column-qualification requirements.
func (q *QueryBuilder[T]) InnerJoin(table string, on JoinOn) *QueryBuilder[T] {
	c := q.clone()
	c.joins = append(c.joins, ast.JoinNode{Table: table, Type: ast.JoinInner, On: on.sql})
	return c
}

// Where adds a filter predicate. Multiple calls are ANDed together.
func (q *QueryBuilder[T]) Where(pred Predicate) *QueryBuilder[T] {
	c := q.clone()
	c.wheres = append(c.wheres, pred.clause)
	return c
}

// OrderBy sets the ordering for the query.
func (q *QueryBuilder[T]) OrderBy(exprs ...OrderExpr) *QueryBuilder[T] {
	c := q.clone()
	for _, e := range exprs {
		c.orderBy = append(c.orderBy, e.ToAST())
	}
	return c
}

// Limit restricts the number of results returned.
func (q *QueryBuilder[T]) Limit(n int) *QueryBuilder[T] {
	c := q.clone()
	c.limit = &n
	return c
}

// Skip sets the offset for the query (number of rows to skip).
func (q *QueryBuilder[T]) Skip(n int) *QueryBuilder[T] {
	c := q.clone()
	c.offset = &n
	return c
}

func (q *QueryBuilder[T]) buildAST(queryType ast.QueryType) ast.SelectNode {
	node := ast.SelectNode{
		Table:    q.meta.Table,
		Columns:  q.meta.Columns,
		OrderBy:  q.orderBy,
		Limit:    q.limit,
		Offset:   q.offset,
		Type:     queryType,
		Distinct: q.distinct,
		Joins:    q.joins,
	}

	allWheres := make([]ast.WhereClause, 0, len(q.filters)+len(q.wheres))
	for _, f := range q.filters {
		allWheres = append(allWheres, f.Clause)
	}
	allWheres = append(allWheres, q.wheres...)

	if len(allWheres) == 1 {
		node.Where = &allWheres[0]
	} else if len(allWheres) > 1 {
		combined := ast.WhereClause{
			LogicalOp: ast.LogicalAnd,
			Children:  allWheres,
		}
		node.Where = &combined
	}

	return node
}

// queryRows runs a built SELECT on the engine, honoring the builder's primary
// routing flag. Implements the projectable interface used by Select/GroupBy.
func (q *QueryBuilder[T]) queryRows(ctx context.Context, sql string, args ...any) (Rows, error) {
	return q.engine.queryRouted(ctx, q.primary, sql, args...)
}

// queryRow runs a built single-row SELECT on the engine, honoring primary
// routing. Implements the projectable interface used by Aggregate.
func (q *QueryBuilder[T]) queryRow(ctx context.Context, sql string, args ...any) Row {
	return q.engine.queryRowRouted(ctx, q.primary, sql, args...)
}

// metaPtr returns the model metadata. Implements the projectable interface.
func (q *QueryBuilder[T]) metaPtr() *ModelMeta[T] { return q.meta }

// projectionDialect returns the dialect for building projection SQL. Implements the projectable interface.
func (q *QueryBuilder[T]) projectionDialect() dialect.Dialect { return q.engine.dialect() }

// Unscoped returns a new builder with all global filters removed.
func (q *QueryBuilder[T]) Unscoped() *QueryBuilder[T] {
	c := q.clone()
	c.filters = nil
	return c
}

// WithoutFilter returns a new builder with the named filter removed.
func (q *QueryBuilder[T]) WithoutFilter(name string) *QueryBuilder[T] {
	c := q.clone()
	var remaining []NamedFilter
	for _, f := range c.filters {
		if f.Name != name {
			remaining = append(remaining, f)
		}
	}
	c.filters = remaining
	return c
}

// All executes the query and returns all matching results.
func (q *QueryBuilder[T]) All(ctx context.Context) ([]*T, error) {
	node := q.buildAST(ast.QuerySelect)
	result := q.engine.dialect().BuildSelect(node)

	rows, err := q.engine.queryRoutedTimeout(ctx, q.primary, q.effectiveTimeout(), result.SQL, result.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*T
	for rows.Next() {
		item, err := q.meta.Scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if q.tracker != nil && q.base != nil && q.meta.Snapshot != nil {
		for i, item := range items {
			canon := q.tracker.Track(item, q.meta.Snapshot(item), q.base)
			items[i] = canon.(*T)
		}
	}
	return items, nil
}

// First returns the first matching result or ErrNotFound if none exist.
func (q *QueryBuilder[T]) First(ctx context.Context) (*T, error) {
	limited := q.Limit(1)
	items, err := limited.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items[0], nil
}

// FirstOrNil returns the first matching result or nil if none exist.
func (q *QueryBuilder[T]) FirstOrNil(ctx context.Context) (*T, error) {
	limited := q.Limit(1)
	items, err := limited.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}

// Count returns the number of matching rows.
func (q *QueryBuilder[T]) Count(ctx context.Context) (int, error) {
	node := q.buildAST(ast.QueryCount)
	result := q.engine.dialect().BuildSelect(node)

	row := q.engine.queryRowRoutedTimeout(ctx, q.primary, q.effectiveTimeout(), result.SQL, result.Args...)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// Exists returns true if at least one matching row exists.
func (q *QueryBuilder[T]) Exists(ctx context.Context) (bool, error) {
	node := q.buildAST(ast.QueryExists)
	result := q.engine.dialect().BuildSelect(node)

	row := q.engine.queryRowRoutedTimeout(ctx, q.primary, q.effectiveTimeout(), result.SQL, result.Args...)
	var exists bool
	if err := row.Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// Take limits the number of results returned. It is an alias for Limit that
// reads naturally alongside Skip/After in pagination expressions.
func (q *QueryBuilder[T]) Take(n int) *QueryBuilder[T] {
	return q.Limit(n)
}

// After positions a cursor-paginated query immediately past the row encoded by
// the cursor (as returned in a prior CursorPage.NextCursor). Combine with
// OrderBy and Take, then call Page.
func (q *QueryBuilder[T]) After(cursor string) *QueryBuilder[T] {
	c := q.clone()
	c.after = &cursor
	return c
}

// Before positions a cursor-paginated query immediately before the row encoded
// by the cursor, walking backward. Mutually exclusive with After. Combine with
// OrderBy and Take, then call Page; results are returned in natural (forward)
// order with PreviousCursor/HasPrev set for further backward navigation.
func (q *QueryBuilder[T]) Before(cursor string) *QueryBuilder[T] {
	c := q.clone()
	c.before = &cursor
	c.after = nil
	return c
}

// PageOffset executes an offset-based page query. Take (or Limit) sets the page
// size and Skip sets the offset. It runs a COUNT to populate Total/TotalPages.
func (q *QueryBuilder[T]) PageOffset(ctx context.Context) (*OffsetPage[T], error) {
	if q.limit == nil {
		return nil, ErrPaginationNeedsLimit
	}
	if *q.limit <= 0 {
		return nil, ErrInvalidPageSize
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, err
	}
	items, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	offset := 0
	if q.offset != nil {
		offset = *q.offset
	}
	return buildOffsetPage(items, total, *q.limit, offset), nil
}

// Page executes a keyset (cursor) page query. OrderBy is required and Take must
// be > 0. Any Skip/offset on the builder is ignored (keyset and OFFSET are
// mutually exclusive). When After is set the page begins past that cursor; when
// Before is set it walks backward and results are returned in natural order.
// Paging over a nullable ordering column requires NullsFirst()/NullsLast() on
// that column, otherwise Page returns ErrCursorColumnNullable.
func (q *QueryBuilder[T]) Page(ctx context.Context) (*CursorPage[T], error) {
	if len(q.orderBy) == 0 {
		return nil, ErrCursorPaginationNeedsOrderBy
	}
	if q.limit == nil {
		return nil, ErrPaginationNeedsLimit
	}
	if *q.limit <= 0 {
		return nil, ErrInvalidPageSize
	}
	pageSize := *q.limit
	order := cursorOrder(q.orderBy, q.meta.PKColumn)

	backward := q.before != nil
	queryOrder := order
	if backward {
		queryOrder = invertOrder(order)
	}

	c := q.clone()
	c.orderBy = queryOrder
	c.after = nil
	c.before = nil
	c.offset = nil // keyset pagination is mutually exclusive with OFFSET; ignore Skip.

	var cursorStr *string
	if q.after != nil {
		cursorStr = q.after
	} else if q.before != nil {
		cursorStr = q.before
	}
	if cursorStr != nil {
		payload, err := decodeCursor(*cursorStr)
		if err != nil {
			return nil, err
		}
		if err := validateCursorColumns(payload, order); err != nil {
			return nil, err
		}
		clause, err := keysetClause(queryOrder, payload.Vals)
		if err != nil {
			return nil, err
		}
		c.wheres = append(c.wheres, clause)
	}
	fetch := pageSize + 1
	c.limit = &fetch
	c.tracker = nil // over-fetch sentinel must never be tracked

	items, err := c.All(ctx)
	if err != nil {
		return nil, err
	}

	hasBoundary := len(items) > pageSize
	if hasBoundary {
		items = items[:pageSize]
	}
	if backward {
		// Backward query ran in inverted order; restore natural order.
		reverseItems(items)
	}

	page := &CursorPage[T]{Items: items}
	if backward {
		page.HasPrev = hasBoundary
		page.HasMore = true // we arrived here from a later page
	} else {
		page.HasMore = hasBoundary
		page.HasPrev = q.after != nil
	}

	if len(items) > 0 {
		if page.HasMore {
			nc, err := cursorForItem(q.meta, order, items[len(items)-1])
			if err != nil {
				return nil, err
			}
			page.NextCursor = nc
		}
		if page.HasPrev {
			pc, err := cursorForItem(q.meta, order, items[0])
			if err != nil {
				return nil, err
			}
			page.PreviousCursor = pc
		}
	}

	if q.tracker != nil && q.base != nil && q.meta.Snapshot != nil {
		for i, item := range page.Items {
			canon := q.tracker.Track(item, q.meta.Snapshot(item), q.base)
			page.Items[i] = canon.(*T)
		}
	}
	return page, nil
}
