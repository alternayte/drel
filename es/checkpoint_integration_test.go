//go:build integration

package es_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/dreltest/pgtest"
	"github.com/alternayte/drel/es"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPGProjection returns an engine with an event store, a checkpoint table and
// a read model.
func newPGProjection(t *testing.T) (*drel.Engine, *es.Store, *es.Checkpoints) {
	t.Helper()
	engine := pgtest.NewPostgres(t)
	ctx := context.Background()

	_, err := engine.Exec(ctx, es.Schema("drel_events", "postgres"))
	require.NoError(t, err)
	_, err = engine.Exec(ctx, es.CheckpointSchema("drel_checkpoints", "postgres"))
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `CREATE TABLE customers (name TEXT PRIMARY KEY, orders INT NOT NULL)`)
	require.NoError(t, err)

	return engine, es.NewStore(engine, "drel_events"), es.NewCheckpoints(engine, "drel_checkpoints")
}

func customerCount(t *testing.T, e *drel.Engine) int {
	t.Helper()
	var n int
	require.NoError(t, e.QueryRow(context.Background(), `SELECT count(*) FROM customers`).Scan(&n))
	return n
}

// TestIntegration_Checkpoints_ReadModelAndCheckpointCommitTogether proves the
// property item 5 exists for.
func TestIntegration_Checkpoints_ReadModelAndCheckpointCommitTogether(t *testing.T) {
	engine, _, cp := newPGProjection(t)
	ctx := context.Background()
	boom := errors.New("the projection failed")

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO customers (name, orders) VALUES ($1, $2)`, "Alice", 1); err != nil {
			return err
		}
		if err := cp.Save(ctx, "customers", es.Position{XactID: 100, GlobalPos: 7}); err != nil {
			return err
		}
		return boom
	})
	assert.ErrorIs(t, err, boom)

	got, err := cp.Load(ctx, "customers")
	require.NoError(t, err)
	assert.Equal(t, es.Position{}, got)
	assert.Equal(t, 0, customerCount(t, engine))
}

// runProjection reads one batch of the log, writes the read model and saves the
// checkpoint, all in one transaction for each batch. It returns how many events
// it handled. failOn makes the handler fail on one customer, to prove that a
// failure does not advance the checkpoint.
func runProjection(t *testing.T, engine *drel.Engine, store *es.Store, cp *es.Checkpoints, failOn string) (int, error) {
	t.Helper()
	ctx := context.Background()
	handled := 0

	for {
		var batch []es.Record
		from, err := cp.Load(ctx, "customers")
		if err != nil {
			return handled, err
		}

		it, err := store.ReadAll(ctx, from, 2)
		if err != nil {
			return handled, err
		}
		for it.Next() {
			batch = append(batch, it.Record())
		}
		if err := it.Err(); err != nil {
			it.Close()
			return handled, err
		}
		it.Close()

		if len(batch) == 0 {
			return handled, nil
		}

		err = engine.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			for _, rec := range batch {
				var ev OrderPlaced
				if err := json.Unmarshal(rec.Payload, &ev); err != nil {
					return err
				}
				if ev.Customer == failOn {
					return errors.New("the handler rejected " + ev.Customer)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO customers (name, orders) VALUES ($1, 1)
					 ON CONFLICT (name) DO UPDATE SET orders = customers.orders + 1`, ev.Customer); err != nil {
					return err
				}
				if err := cp.Save(ctx, "customers", rec.Position); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return handled, err
		}
		handled += len(batch)
	}
}

// TestIntegration_Projection_EndToEnd walks the log with a checkpoint, stops,
// and resumes exactly where it stopped.
func TestIntegration_Projection_EndToEnd(t *testing.T) {
	engine, store, cp := newPGProjection(t)
	ctx := context.Background()

	names := []string{"Alice", "Bob", "Carol", "Dave", "Erin"}
	for i, name := range names {
		require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
			return store.Append(ctx, "order-"+name, 0, []any{OrderPlaced{Customer: name, Total: i}})
		}))
	}

	n, err := runProjection(t, engine, store, cp, "")
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, 5, customerCount(t, engine))

	// A second pass finds nothing, because the checkpoint holds.
	n, err = runProjection(t, engine, store, cp, "")
	require.NoError(t, err)
	assert.Equal(t, 0, n, "the projection must not read an event two times")
	assert.Equal(t, 5, customerCount(t, engine))

	// New events resume from the checkpoint.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-Frank", 0, []any{OrderPlaced{Customer: "Frank"}})
	}))
	n, err = runProjection(t, engine, store, cp, "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 6, customerCount(t, engine))

	// Every customer appears one time.
	var maxOrders int
	require.NoError(t, engine.QueryRow(ctx, `SELECT max(orders) FROM customers`).Scan(&maxOrders))
	assert.Equal(t, 1, maxOrders, "no event may be handled two times")
}

// TestIntegration_Projection_FailureDoesNotAdvance proves that a failed handler
// leaves the checkpoint where it was, and that the retry writes the row one
// time.
func TestIntegration_Projection_FailureDoesNotAdvance(t *testing.T) {
	engine, store, cp := newPGProjection(t)
	ctx := context.Background()

	for _, name := range []string{"Alice", "Bob", "Carol", "Dave"} {
		require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
			return store.Append(ctx, "order-"+name, 0, []any{OrderPlaced{Customer: name}})
		}))
	}

	// The handler rejects Carol, who sits in the second batch.
	n, err := runProjection(t, engine, store, cp, "Carol")
	require.Error(t, err)
	assert.Equal(t, 2, n, "the first batch commits, and the second rolls back")
	assert.Equal(t, 2, customerCount(t, engine))

	before, err := cp.Load(ctx, "customers")
	require.NoError(t, err)

	// The failure repeats, and nothing moves.
	_, err = runProjection(t, engine, store, cp, "Carol")
	require.Error(t, err)
	after, err := cp.Load(ctx, "customers")
	require.NoError(t, err)
	assert.Equal(t, before, after, "a failed batch must not advance the checkpoint")
	assert.Equal(t, 2, customerCount(t, engine))

	// The handler is fixed. The replay writes each remaining customer one time.
	n, err = runProjection(t, engine, store, cp, "")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, 4, customerCount(t, engine))

	var maxOrders int
	require.NoError(t, engine.QueryRow(ctx, `SELECT max(orders) FROM customers`).Scan(&maxOrders))
	assert.Equal(t, 1, maxOrders, "the retry must not double count")
}

// TestIntegration_Projection_ResetReplays proves Reset rebuilds a projection.
func TestIntegration_Projection_ResetReplays(t *testing.T) {
	engine, store, cp := newPGProjection(t)
	ctx := context.Background()

	for _, name := range []string{"Alice", "Bob"} {
		require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
			return store.Append(ctx, "order-"+name, 0, []any{OrderPlaced{Customer: name}})
		}))
	}
	_, err := runProjection(t, engine, store, cp, "")
	require.NoError(t, err)
	assert.Equal(t, 2, customerCount(t, engine))

	// Clear the read model and the checkpoint together, then replay.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx, `DELETE FROM customers`); err != nil {
			return err
		}
		return cp.Reset(ctx, "customers")
	}))
	assert.Equal(t, 0, customerCount(t, engine))

	n, err := runProjection(t, engine, store, cp, "")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, 2, customerCount(t, engine))
}
