package drel

import (
	"context"
)

// ModelMeta describes the database mapping for a model type T.
type ModelMeta[T any] struct {
	Table   string
	Columns []string
	// PKColumns lists every primary key column, in key order. Generated code
	// always sets it.
	PKColumns []string
	// KeyValues splits a primary key value into one value per PKColumns entry,
	// in the same order. When nil the key is a single value.
	KeyValues     func(key any) []any
	Scan          func(Row) (*T, error)
	Snapshot      func(*T) any
	Diff          func(*T, any) []FieldChange
	PKValue       func(*T) any
	InsertColumns func(*T) ([]string, []any)
	ScanReturning func(*T, Row) error
	ScanGenerated func(*T, Row) error // scans created_at, updated_at only (app-assigned)
	KeyStrategy   KeyStrategy
	GenerateKey   func() any
	SetKey        func(*T, any)
	KeyIsZero     func(*T) bool
	ColumnValue   func(*T, int) any
	// NormalizeKey converts a primary-key value scanned as a raw driver type
	// (e.g. [16]byte/string for a UUID, int64 for an integer) into the canonical
	// Go key type returned by PKValue, so pivot-table keys compare equal to it.
	// Optional; when nil the loader falls back to int normalization.
	NormalizeKey   func(any) any
	Filters        []NamedFilter
	HasSoftDelete  bool
	HasVersioned   bool
	HasAudit       bool
	VersionValue   func(*T) int
	SetVersion     func(*T, int)
	AuditSetCreate func(*T, string)
	AuditSetUpdate func(*T, string)
}

// ToMetaBase converts a typed ModelMeta[T] to a type-erased ModelMetaBase.
func ToMetaBase[T any](meta *ModelMeta[T]) *ModelMetaBase {
	base := &ModelMetaBase{
		Table:     meta.Table,
		Columns:   meta.Columns,
		PKColumns: meta.PKColumns,
		KeyValues: meta.KeyValues,
		PKValue: func(entity any) any {
			return meta.PKValue(entity.(*T))
		},
		InsertColumns: func(entity any) ([]string, []any) {
			return meta.InsertColumns(entity.(*T))
		},
		ScanRow: func(row Row) (any, error) {
			return meta.Scan(row)
		},
	}
	// Snapshot/Diff are optional (insert-only models omit them); keep the
	// type-erased wrappers nil when absent so callers can detect their absence.
	if meta.Snapshot != nil {
		base.Snapshot = func(entity any) any {
			return meta.Snapshot(entity.(*T))
		}
	}
	if meta.Diff != nil {
		base.Diff = func(entity any, snapshot any) []FieldChange {
			return meta.Diff(entity.(*T), snapshot)
		}
	}
	if meta.ScanReturning != nil {
		base.ScanReturning = func(entity any, row Row) error {
			return meta.ScanReturning(entity.(*T), row)
		}
	}
	if meta.ColumnValue != nil {
		base.ColumnValue = func(entity any, colIdx int) any {
			return meta.ColumnValue(entity.(*T), colIdx)
		}
	}
	base.NormalizeKey = meta.NormalizeKey
	base.Filters = append([]NamedFilter(nil), meta.Filters...)
	base.HasSoftDelete = meta.HasSoftDelete
	base.HasVersioned = meta.HasVersioned
	base.HasAudit = meta.HasAudit
	if meta.VersionValue != nil {
		base.VersionValue = func(entity any) int {
			return meta.VersionValue(entity.(*T))
		}
		base.SetVersion = func(entity any, v int) {
			meta.SetVersion(entity.(*T), v)
		}
	}
	if meta.AuditSetCreate != nil {
		base.AuditSetCreate = func(entity any, actor string) {
			meta.AuditSetCreate(entity.(*T), actor)
		}
		base.AuditSetUpdate = func(entity any, actor string) {
			meta.AuditSetUpdate(entity.(*T), actor)
		}
	}
	base.KeyStrategy = meta.KeyStrategy
	base.GenerateKey = meta.GenerateKey
	if meta.SetKey != nil {
		base.SetKey = func(entity any, key any) {
			meta.SetKey(entity.(*T), key)
		}
	}
	if meta.KeyIsZero != nil {
		base.KeyIsZero = func(entity any) bool {
			return meta.KeyIsZero(entity.(*T))
		}
	}
	if meta.ScanGenerated != nil {
		base.ScanGenerated = func(entity any, row Row) error {
			return meta.ScanGenerated(entity.(*T), row)
		}
	}
	return base
}

// Repository provides typed query access for a specific model.
type Repository[T any] struct {
	engine *Engine
	meta   ModelMeta[T]
}

// NewRepository creates a new Repository for the given model metadata.
func NewRepository[T any](engine *Engine, meta ModelMeta[T]) *Repository[T] {
	return &Repository[T]{
		engine: engine,
		meta:   meta,
	}
}

func (r *Repository[T]) newBuilder() *QueryBuilder[T] {
	return newQueryBuilder(r.engine, &r.meta)
}

// Find looks up a single record by its primary key. For a composite key, id is
// the model's key struct.
func (r *Repository[T]) Find(ctx context.Context, id any) (*T, error) {
	cols := r.meta.PKColumns
	return r.newBuilder().
		Where(pkPredicate(cols, keyValuesOf(r.meta.KeyValues, id))).
		First(ctx)
}

// Where starts a filtered query.
func (r *Repository[T]) Where(pred Predicate) *QueryBuilder[T] {
	return r.newBuilder().Where(pred)
}

// OrderBy starts an ordered query.
func (r *Repository[T]) OrderBy(exprs ...OrderExpr) *QueryBuilder[T] {
	return r.newBuilder().OrderBy(exprs...)
}

// Limit starts a query with a row limit.
func (r *Repository[T]) Limit(n int) *QueryBuilder[T] {
	return r.newBuilder().Limit(n)
}

// Take starts a query with a row limit (alias for Limit, reads naturally with pagination).
func (r *Repository[T]) Take(n int) *QueryBuilder[T] {
	return r.newBuilder().Limit(n)
}

// Skip starts a query with a row offset.
func (r *Repository[T]) Skip(n int) *QueryBuilder[T] {
	return r.newBuilder().Skip(n)
}

// AllRows opts a BulkUpdate or BulkDelete started from this repository out of
// the full-table safety guard, permitting a deliberate whole-table write with
// no Where predicate.
func (r *Repository[T]) AllRows() *QueryBuilder[T] {
	return r.newBuilder().AllRows()
}

// After starts a cursor-paginated query positioned past the given cursor.
func (r *Repository[T]) After(cursor string) *QueryBuilder[T] {
	return r.newBuilder().After(cursor)
}

// Before starts a cursor-paginated query positioned before the given cursor (backward).
func (r *Repository[T]) Before(cursor string) *QueryBuilder[T] {
	return r.newBuilder().Before(cursor)
}

// All returns all records for this model.
func (r *Repository[T]) All(ctx context.Context) ([]*T, error) {
	return r.newBuilder().All(ctx)
}

// First returns the first record or ErrNotFound.
func (r *Repository[T]) First(ctx context.Context) (*T, error) {
	return r.newBuilder().First(ctx)
}

// FirstOrNil returns the first record or nil.
func (r *Repository[T]) FirstOrNil(ctx context.Context) (*T, error) {
	return r.newBuilder().FirstOrNil(ctx)
}

// Count returns the total number of records for this model.
func (r *Repository[T]) Count(ctx context.Context) (int, error) {
	return r.newBuilder().Count(ctx)
}

// Exists returns true if any records exist for this model.
func (r *Repository[T]) Exists(ctx context.Context) (bool, error) {
	return r.newBuilder().Exists(ctx)
}

// Primary returns a query builder that reads from the primary connection,
// bypassing read replicas (useful for read-your-writes consistency).
func (r *Repository[T]) Primary() *QueryBuilder[T] {
	return r.newBuilder().Primary()
}

// Distinct returns a query builder with SELECT DISTINCT enabled.
func (r *Repository[T]) Distinct() *QueryBuilder[T] {
	return r.newBuilder().Distinct()
}

// LeftJoin returns a query builder with a LEFT JOIN added.
func (r *Repository[T]) LeftJoin(table string, on JoinOn) *QueryBuilder[T] {
	return r.newBuilder().LeftJoin(table, on)
}

// InnerJoin returns a query builder with an INNER JOIN added.
func (r *Repository[T]) InnerJoin(table string, on JoinOn) *QueryBuilder[T] {
	return r.newBuilder().InnerJoin(table, on)
}

// Unscoped returns a query builder with all global filters removed.
func (r *Repository[T]) Unscoped() *QueryBuilder[T] {
	return r.newBuilder().Unscoped()
}

// WithoutFilter returns a query builder with the named filter removed.
func (r *Repository[T]) WithoutFilter(name string) *QueryBuilder[T] {
	return r.newBuilder().WithoutFilter(name)
}
