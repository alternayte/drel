package drel

import (
	"context"
	"fmt"
	"time"

	"github.com/alternayte/drel/internal/dberr"
	"github.com/alternayte/drel/internal/driver"
)

// SetClause pairs a column name with a value for use in bulk updates and upserts.
type SetClause struct {
	Column string
	Value  any
}

// Set creates a SetClause from a typed column and value.
func Set[C interface{ Name() string }, V any](col C, value V) SetClause {
	return SetClause{Column: col.Name(), Value: value}
}

// UpsertOption configures BulkUpsert behavior.
type UpsertOption func(*upsertConfig)

type upsertConfig struct {
	conflictCols []string
	updateCols   []string
	doNothing    bool
}

// ConflictColumns specifies which columns form the conflict target for an upsert.
func ConflictColumns[C interface{ Name() string }](cols ...C) UpsertOption {
	return func(cfg *upsertConfig) {
		for _, c := range cols {
			cfg.conflictCols = append(cfg.conflictCols, c.Name())
		}
	}
}

// UpdateOnConflict specifies which columns to update when a conflict is detected.
func UpdateOnConflict[C interface{ Name() string }](cols ...C) UpsertOption {
	return func(cfg *upsertConfig) {
		for _, c := range cols {
			cfg.updateCols = append(cfg.updateCols, c.Name())
		}
	}
}

// DoNothing configures the upsert to emit ON CONFLICT (...) DO NOTHING: rows
// that conflict on the conflict target are skipped instead of updated. When
// set, UpdateOnConflict becomes optional and is ignored.
func DoNothing() UpsertOption {
	return func(cfg *upsertConfig) {
		cfg.doNothing = true
	}
}

const bulkBatchSize = 1000

// maxBulkParams is a conservative cap on bound parameters per statement, safe
// across dialects: SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 32766 and
// Postgres allows 65535. Staying under the smaller keeps wide-table bulk
// operations from overflowing the limit.
const maxBulkParams = 32766

// safeBatchSize returns the largest row count whose parameter total stays within
// maxBulkParams, capped at bulkBatchSize. numCols is the bound parameters per row.
func safeBatchSize(numCols int) int {
	if numCols <= 0 {
		return bulkBatchSize
	}
	n := maxBulkParams / numCols
	if n < 1 {
		n = 1
	}
	if n > bulkBatchSize {
		n = bulkBatchSize
	}
	return n
}

// appendUniformRow validates that an entity's insert columns match the batch's
// established column list (same names, same order, same length) and appends its
// values. The first entity establishes `columns`. A mismatch is a loud error
// rather than a silent positional bind against the wrong columns.
func appendUniformRow(table string, columns *[]string, rows *[][]any, cols []string, vals []any) error {
	if len(cols) != len(vals) {
		return fmt.Errorf("drel: bulk %s: InsertColumns returned %d columns but %d values", table, len(cols), len(vals))
	}
	if *columns == nil {
		*columns = cols
	} else {
		if len(cols) != len(*columns) {
			return fmt.Errorf("drel: bulk %s: non-uniform column shape (%d vs %d columns); InsertColumns must return a fixed column set", table, len(cols), len(*columns))
		}
		for i := range cols {
			if cols[i] != (*columns)[i] {
				return fmt.Errorf("drel: bulk %s: non-uniform column shape (column %d %q vs %q); InsertColumns must return a fixed column set", table, i, cols[i], (*columns)[i])
			}
		}
	}
	*rows = append(*rows, vals)
	return nil
}

// bulkInsertColumns runs the same per-entity marker logic as the single-row
// insert path (mutation.go) for a bulk batch: audit-stamp create fields,
// initialize the version, stamp+prepend an app-assigned primary key with a
// loud zero-key guard. It returns the (possibly PK-prefixed) column and value
// slices to be appended to the batch. DB-generated id/timestamp back-fill
// (RETURNING hydration) is intentionally not performed for bulk inserts; only
// app-assigned keys (produced by the app/generator) are set on the entities.
func bulkInsertColumns(ctx context.Context, base *ModelMetaBase, entity any) ([]string, []any, error) {
	if base.HasAudit && base.AuditSetCreate != nil {
		base.AuditSetCreate(entity, ActorFromContext(ctx))
	}
	if base.HasVersioned && base.SetVersion != nil {
		base.SetVersion(entity, 1)
	}

	cols, vals := base.InsertColumns(entity)

	if base.KeyStrategy == KeyAppAssigned {
		// Stamp a key via the generator (honoring the runtime registry) when one
		// is not already set, then require it be non-zero — mirroring
		// mutation.go:47-59. A forgotten key is a loud error, never a zero PK.
		stampKey(entity, base)
		if base.KeyIsZero != nil && base.KeyIsZero(entity) {
			return nil, nil, fmt.Errorf("drel: bulk insert %s: app-assigned primary key is zero (no key generator registered and no key was set)", base.Table)
		}
		pkCols := base.PKColumns
		pkVals := keyValuesOf(base.KeyValues, base.PKValue(entity))
		cols = append(append([]string(nil), pkCols...), cols...)
		vals = append(append([]any(nil), pkVals...), vals...)
	}

	return cols, vals, nil
}

// BulkInsert inserts multiple entities in batches, bypassing change tracking.
// Returns the total number of rows affected.
//
// IMPORTANT: BulkInsert is a fast path that opens its own transaction and does
// NOT go through the change tracker. Domain events recorded on the entities
// (RecordEvent) are NOT collected, NOT written to the outbox, and NOT
// dispatched to before/after-commit hooks. Use SaveChanges (Engine.Transaction
// or a UnitOfWork) when you need event dispatch, or BulkInsertWithEvents to
// persist events through the outbox path inside the bulk transaction.
func (r *Repository[T]) BulkInsert(ctx context.Context, entities []*T) (int, error) {
	return r.bulkInsert(ctx, entities, false)
}

// BulkInsertWithEvents inserts multiple entities in batches like BulkInsert, but
// additionally collects domain events recorded on the entities (RecordEvent),
// runs the engine's registered OnBeforeCommit hooks (e.g. the outbox writer from
// UseOutbox) inside the same transaction so event persistence commits
// atomically with the inserted rows, and dispatches the events to after-commit
// hooks once the transaction commits. Change tracking is still bypassed; this is
// a targeted opt-in for event delivery on the bulk path.
func (r *Repository[T]) BulkInsertWithEvents(ctx context.Context, entities []*T) (int, error) {
	return r.bulkInsert(ctx, entities, true)
}

func (r *Repository[T]) bulkInsert(ctx context.Context, entities []*T, withEvents bool) (int, error) {
	if len(entities) == 0 {
		return 0, nil
	}

	drv := r.engine.driver()
	d := r.engine.dialect()
	base := ToMetaBase(&r.meta)

	dbTx, err := drv.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("drel: bulk insert begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = dbTx.Rollback(ctx)
		}
	}()

	firstCols, _, err := bulkInsertColumns(ctx, base, entities[0])
	if err != nil {
		return 0, err
	}
	batchSize := safeBatchSize(len(firstCols))

	total := 0
	for i := 0; i < len(entities); i += batchSize {
		end := i + batchSize
		if end > len(entities) {
			end = len(entities)
		}
		batch := entities[i:end]

		var columns []string
		var rows [][]any
		for _, entity := range batch {
			cols, vals, prepErr := bulkInsertColumns(ctx, base, entity)
			if prepErr != nil {
				return 0, prepErr
			}
			if err := appendUniformRow(r.meta.Table, &columns, &rows, cols, vals); err != nil {
				return 0, err
			}
		}

		if copier, ok := dbTx.(driver.TxBulkCopier); ok {
			spanCtx, endSpan := r.engine.startSpan(ctx, "drel.exec")
			start := time.Now()
			affected, copyErr := copier.CopyFrom(spanCtx, r.meta.Table, columns, rows)
			endSpan(copyErr)
			r.engine.notifyQueryHooks(spanCtx, "COPY "+r.meta.Table, nil, time.Since(start), copyErr)
			if copyErr != nil {
				return 0, fmt.Errorf("drel: bulk insert %s: %w", r.meta.Table, dberr.Classify(copyErr))
			}
			total += int(affected)
			continue
		}

		result := d.BuildBulkInsert(r.meta.Table, columns, rows)
		spanCtx, endSpan := r.engine.startSpan(ctx, "drel.exec")
		start := time.Now()
		affected, execErr := dbTx.Exec(spanCtx, result.SQL, result.Args...)
		endSpan(execErr)
		r.engine.notifyQueryHooks(spanCtx, result.SQL, result.Args, time.Since(start), execErr)
		if execErr != nil {
			return 0, fmt.Errorf("drel: bulk insert %s: %w", r.meta.Table, dberr.Classify(execErr))
		}
		total += int(affected)
	}

	var events []any
	if withEvents {
		events = collectBulkEvents(entities)
		if len(events) > 0 {
			tx := &Tx{engine: r.engine, dbTx: dbTx, tracker: newChangeTracker()}
			for _, hook := range r.engine.snapshotBeforeCommitHooks() {
				if err := hook(ctx, tx, events); err != nil {
					return total, fmt.Errorf("drel: bulk insert before-commit hook: %w", err)
				}
			}
			for _, sink := range r.engine.eventSinks {
				if err := sink(ctx, tx, events); err != nil {
					return total, fmt.Errorf("drel: bulk insert event sink: %w", err)
				}
			}
		}
	}

	if err := dbTx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("drel: bulk insert commit: %w", dberr.Classify(err))
	}
	committed = true

	if withEvents && len(events) > 0 {
		r.engine.dispatchAfterCommit(ctx, events)
	}

	return total, nil
}

// collectBulkEvents drains PendingEvents from any entities that implement
// EventRecorder, clearing them as it goes (matching SaveChanges semantics).
func collectBulkEvents[T any](entities []*T) []any {
	var events []any
	for _, e := range entities {
		if er, ok := any(e).(EventRecorder); ok {
			events = append(events, er.PendingEvents()...)
			er.ClearEvents()
		}
	}
	return events
}

// BulkUpsert inserts or updates multiple entities based on conflict resolution.
// It bypasses change tracking and executes directly against the database.
// Returns the total number of rows affected.
//
// IMPORTANT: like BulkInsert, BulkUpsert bypasses the change tracker. Domain
// events recorded on the entities (RecordEvent) are NOT collected, written to
// the outbox, or dispatched. Use SaveChanges when you need events.

// ErrBulkDeleteRequiresFilter is returned when BulkDelete is called without any WHERE predicates or filters.
var ErrBulkDeleteRequiresFilter = fmt.Errorf("drel: BulkDelete requires at least one Where predicate to prevent accidental full-table deletes")

func (r *Repository[T]) BulkUpsert(ctx context.Context, entities []*T, opts ...UpsertOption) (int, error) {
	if len(entities) == 0 {
		return 0, nil
	}

	cfg := &upsertConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if len(cfg.conflictCols) == 0 {
		return 0, fmt.Errorf("drel: bulk upsert %s: ConflictColumns is required", r.meta.Table)
	}
	if len(cfg.updateCols) == 0 && !cfg.doNothing {
		return 0, fmt.Errorf("drel: bulk upsert %s: UpdateOnConflict is required (or use DoNothing)", r.meta.Table)
	}

	drv := r.engine.driver()
	d := r.engine.dialect()
	base := ToMetaBase(&r.meta)

	tx, err := drv.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("drel: bulk upsert begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	firstCols, _, err := bulkInsertColumns(ctx, base, entities[0])
	if err != nil {
		return 0, err
	}
	batchSize := safeBatchSize(len(firstCols))

	total := 0
	for i := 0; i < len(entities); i += batchSize {
		end := i + batchSize
		if end > len(entities) {
			end = len(entities)
		}
		batch := entities[i:end]

		var columns []string
		var rows [][]any
		for _, entity := range batch {
			cols, vals, prepErr := bulkInsertColumns(ctx, base, entity)
			if prepErr != nil {
				return 0, prepErr
			}
			if err := appendUniformRow(r.meta.Table, &columns, &rows, cols, vals); err != nil {
				return 0, err
			}
		}

		result := d.BuildBulkUpsert(r.meta.Table, columns, rows, cfg.conflictCols, cfg.updateCols, cfg.doNothing)
		spanCtx, endSpan := r.engine.startSpan(ctx, "drel.exec")
		start := time.Now()
		affected, execErr := tx.Exec(spanCtx, result.SQL, result.Args...)
		endSpan(execErr)
		r.engine.notifyQueryHooks(spanCtx, result.SQL, result.Args, time.Since(start), execErr)
		if execErr != nil {
			return 0, fmt.Errorf("drel: bulk upsert %s: %w", r.meta.Table, dberr.Classify(execErr))
		}
		total += int(affected)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("drel: bulk upsert commit: %w", dberr.Classify(err))
	}

	return total, nil
}
