package drel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dberr"
	"github.com/alternayte/drel/internal/driver"
)

// ErrBatchNotExecuted is the initial error on every queued BatchResult. It is
// cleared as Execute runs each item, so reading a result before Execute ran (or
// for an item that never ran because Execute aborted early) returns this rather
// than a silent zero value.
var ErrBatchNotExecuted = errors.New("drel: batch result read before Execute ran this query")

// ErrBatchPartial is returned by Execute when the batch completed but at least
// one queued query failed. Each failing query's own error is available via its
// BatchResult.Result(); ErrBatchPartial wraps the first failure.
var ErrBatchPartial = errors.New("drel: one or more batched queries failed")

// batchPartialError is the concrete error type returned by Execute on partial
// failure. It implements errors.Is/As for both ErrBatchPartial and the first
// per-query failure so callers can check either.
type batchPartialError struct {
	first error
}

func (e *batchPartialError) Error() string {
	return fmt.Sprintf("%s: %v", ErrBatchPartial, e.first)
}

func (e *batchPartialError) Is(target error) bool {
	return target == ErrBatchPartial
}

func (e *batchPartialError) Unwrap() error {
	return e.first
}

// Batch groups multiple read queries so they execute in a single network
// round-trip when the driver supports pipelining (pgx), falling back to
// sequential execution otherwise. Queue queries with BatchAll/BatchFirst/
// BatchCount, call Execute, then read each typed result via Result.
//
//	batch := db.NewBatch()
//	users := drel.BatchAll(batch, db.Users.Where(Users.Active.IsTrue()))
//	count := drel.BatchCount(batch, db.Users)
//	if err := batch.Execute(ctx); err != nil { ... }
//	us, _ := users.Result()
//	n, _ := count.Result()
//
// A batch created with Engine.NewBatch pipelines on pgx (and runs sequentially
// on SQLite/libSQL). A batch created with Tx.NewBatch always runs sequentially
// on the transaction connection so it observes the transaction's uncommitted
// writes; a transaction batches the same way during its flush.
// Batch results are read-only and are NOT snapshotted into a change tracker:
// to mutate-and-save loaded entities, load them via Find/All/Include instead.
type Batch struct {
	engine *Engine
	tx     *Tx // non-nil ⇒ run sequentially on the transaction connection
	items  []batchItem
}

type batchItem struct {
	sql     string
	args    []any
	primary bool
	handler func(Rows) error
	// setErr stores an error (or clears it with nil) on the owning BatchResult.
	setErr func(error)
}

// NewBatch creates an empty query batch bound to this engine.
func (e *Engine) NewBatch() *Batch { return &Batch{engine: e} }

// NewBatch creates a query batch that runs on this transaction's connection.
// Batched queries observe the transaction's own uncommitted writes. Because a
// pgx pipeline cannot span a transaction connection in the general case, a
// transaction batch always runs sequentially (no network pipelining). Batch
// results are read-only: they are not snapshotted into the change tracker.
func (tx *Tx) NewBatch() *Batch { return &Batch{engine: tx.engine, tx: tx} }

func (b *Batch) add(sql string, args []any, primary bool, handler func(Rows) error, setErr func(error)) {
	b.items = append(b.items, batchItem{sql: sql, args: args, primary: primary, handler: handler, setErr: setErr})
}

// Execute runs all queued queries. On a pipelining driver they are sent in one
// round-trip and their results read in order; otherwise they run sequentially.
// Execute drains every queued query even if an earlier one fails: each query's
// own error (and value) is stored on its BatchResult, so Result() faithfully
// reports per-query success or failure. If any query failed, Execute returns
// ErrBatchPartial wrapping the first failure; otherwise it returns nil.
func (b *Batch) Execute(ctx context.Context) error {
	if len(b.items) == 0 {
		return nil
	}
	if b.tx != nil {
		// Transaction batches run sequentially on the tx connection.
		var firstErr error
		for _, it := range b.items {
			it.setErr(nil) // clear ErrBatchNotExecuted
			rows, err := b.tx.queryInternal(ctx, it.sql, it.args...)
			if err != nil {
				it.setErr(err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if hErr := it.handler(rows); hErr != nil {
				rows.Close()
				it.setErr(hErr)
				if firstErr == nil {
					firstErr = hErr
				}
				continue
			}
			rows.Close()
		}
		if firstErr != nil {
			return &batchPartialError{first: firstErr}
		}
		return nil
	}
	var firstErr error
	record := func(it batchItem, err error) {
		if err == nil {
			return
		}
		// Store the error on the item's BatchResult. For failures that bypass
		// the handler (SendBatch / res.Query() errors) the handler never ran so
		// this is the sole setter. For handler errors the handler may already
		// have stored a value; setErr overwrites it with the classified form,
		// which satisfies the contract (non-nil, classified error type).
		it.setErr(err)
		if firstErr == nil {
			firstErr = err
		}
	}

	forcePrimary := false
	for _, it := range b.items {
		if it.primary {
			forcePrimary = true
			break
		}
	}

	// Pick the read target using the same replica-aware logic as the sequential
	// path so both branches are consistent. If the chosen driver supports
	// pipelining, use SendBatch; on failure (and when not forced to primary)
	// attempt a SendBatch failover to the primary before giving up.
	target := b.engine.rowDriver(ctx, forcePrimary)
	if p, ok := target.(driver.Pipeliner); ok {
		items := make([]driver.BatchItem, len(b.items))
		for i, it := range b.items {
			items[i] = driver.BatchItem{SQL: it.sql, Args: it.args}
		}
		res, sendErr := p.SendBatch(ctx, items)
		if sendErr != nil && !forcePrimary && target != b.engine.drv {
			// The replica's pipeline failed; fall back to the primary if it
			// supports pipelining, mirroring the per-query readWithFailover
			// behaviour for sequential reads.
			if pp, ok2 := b.engine.drv.(driver.Pipeliner); ok2 {
				res, sendErr = pp.SendBatch(ctx, items)
			}
		}
		if sendErr != nil {
			// The whole pipeline failed to send: attribute the classified error to
			// every item so none is left at ErrBatchNotExecuted with a zero value.
			classified := dberr.Classify(sendErr)
			for _, it := range b.items {
				record(it, classified)
			}
			return &batchPartialError{first: classified}
		}
		defer res.Close()
		for _, it := range b.items {
			it.setErr(nil) // clear ErrBatchNotExecuted; the handler/record sets the real error
			spanCtx, endSpan := b.engine.startSpan(ctx, "drel.query")
			start := time.Now()
			rows, qErr := res.Query()
			if qErr != nil {
				endSpan(qErr)
				b.engine.notifyQueryHooks(spanCtx, it.sql, it.args, time.Since(start), qErr)
				record(it, dberr.Classify(qErr))
				continue
			}
			hErr := it.handler(rows)
			rows.Close()
			endSpan(hErr)
			b.engine.notifyQueryHooks(spanCtx, it.sql, it.args, time.Since(start), hErr)
			if hErr != nil {
				record(it, dberr.Classify(hErr))
				continue
			}
		}
		if firstErr != nil {
			return &batchPartialError{first: firstErr}
		}
		return nil
	}

	// Sequential fallback (e.g. SQLite, or a replica without pipelining).
	// Route all items to the same driver (primary or a single replica) for
	// consistency. target was already selected above via rowDriver.
	for _, it := range b.items {
		it.setErr(nil) // clear ErrBatchNotExecuted; the handler/record sets the real error
		rows, qErr := b.engine.queryOn(ctx, target, it.sql, it.args...)
		if qErr != nil {
			record(it, qErr) // queryOn already classifies
			continue
		}
		if hErr := it.handler(rows); hErr != nil {
			rows.Close()
			record(it, hErr)
			continue
		}
		rows.Close()
	}
	if firstErr != nil {
		return &batchPartialError{first: firstErr}
	}
	return nil
}

// BatchResult holds the typed outcome of a batched query, available after the
// batch has been executed.
type BatchResult[T any] struct {
	value T
	err   error
}

// Result returns the query's result and any error encountered while scanning it.
func (r *BatchResult[T]) Result() (T, error) { return r.value, r.err }

// BatchAll queues a query returning all matching rows.
func BatchAll[T any](b *Batch, q *QueryBuilder[T]) *BatchResult[[]*T] {
	res := &BatchResult[[]*T]{err: ErrBatchNotExecuted}
	built := q.engine.dialect().BuildSelect(q.buildAST(ast.QuerySelect))
	b.add(built.SQL, built.Args, q.primary, func(rows Rows) error {
		var items []*T
		for rows.Next() {
			it, err := q.meta.Scan(rows)
			if err != nil {
				res.err = err
				return err
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			res.err = err
			return err
		}
		res.value = items
		return nil
	}, func(err error) { res.err = err })
	return res
}

// BatchFirst queues a query returning the first matching row; Result returns
// ErrNotFound if there is none.
func BatchFirst[T any](b *Batch, q *QueryBuilder[T]) *BatchResult[*T] {
	res := &BatchResult[*T]{err: ErrBatchNotExecuted}
	built := q.engine.dialect().BuildSelect(q.Limit(1).buildAST(ast.QuerySelect))
	b.add(built.SQL, built.Args, q.primary, func(rows Rows) error {
		if rows.Next() {
			it, err := q.meta.Scan(rows)
			if err != nil {
				res.err = err
				return err
			}
			res.value = it
			return rows.Err()
		}
		if err := rows.Err(); err != nil {
			res.err = err
			return err
		}
		res.err = ErrNotFound
		return nil
	}, func(err error) { res.err = err })
	return res
}

// BatchCount queues a COUNT query.
func BatchCount[T any](b *Batch, q *QueryBuilder[T]) *BatchResult[int] {
	res := &BatchResult[int]{err: ErrBatchNotExecuted}
	built := q.engine.dialect().BuildSelect(q.buildAST(ast.QueryCount))
	b.add(built.SQL, built.Args, q.primary, func(rows Rows) error {
		if rows.Next() {
			var n int
			if err := rows.Scan(&n); err != nil {
				res.err = err
				return err
			}
			res.value = n
		}
		return rows.Err()
	}, func(err error) { res.err = err })
	return res
}
