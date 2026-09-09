package es_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/es"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCheckpoints returns an engine with a checkpoint table and a read model.
func newCheckpoints(t *testing.T) (*drel.Engine, *es.Checkpoints) {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })

	ctx := context.Background()
	_, err = engine.Exec(ctx, es.CheckpointSchema("drel_checkpoints", "sqlite"))
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `CREATE TABLE read_model (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
	require.NoError(t, err)
	return engine, es.NewCheckpoints(engine, "drel_checkpoints")
}

func TestCheckpointSchema_EmitsBothDialects(t *testing.T) {
	for _, d := range []string{"postgres", "sqlite"} {
		ddl := es.CheckpointSchema("drel_checkpoints", d)
		assert.Contains(t, ddl, `CREATE TABLE "drel_checkpoints"`)
		assert.Contains(t, ddl, `"projection" TEXT NOT NULL PRIMARY KEY`)
		assert.Contains(t, ddl, `"xact_id"`)
		assert.Contains(t, ddl, `"global_pos"`)
		assert.Contains(t, ddl, `"updated_at"`)
	}
}

func TestCheckpoints_LoadUnknownIsZero(t *testing.T) {
	_, cp := newCheckpoints(t)

	got, err := cp.Load(context.Background(), "orders")
	require.NoError(t, err)
	assert.Equal(t, es.Position{}, got, "an unknown projection starts at the beginning")
}

func TestCheckpoints_SaveThenLoad(t *testing.T) {
	engine, cp := newCheckpoints(t)
	ctx := context.Background()
	want := es.Position{XactID: 735, GlobalPos: 42}

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return cp.Save(ctx, "orders", want)
	}))

	got, err := cp.Load(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestCheckpoints_SaveIsAnUpsert(t *testing.T) {
	engine, cp := newCheckpoints(t)
	ctx := context.Background()

	for _, pos := range []es.Position{{XactID: 1, GlobalPos: 1}, {XactID: 2, GlobalPos: 9}} {
		require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
			return cp.Save(ctx, "orders", pos)
		}))
	}

	got, err := cp.Load(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, es.Position{XactID: 2, GlobalPos: 9}, got)

	var n int
	require.NoError(t, engine.QueryRow(ctx, `SELECT count(*) FROM drel_checkpoints`).Scan(&n))
	assert.Equal(t, 1, n, "a projection keeps one row")
}

func TestCheckpoints_SavePanicsWithoutTransaction(t *testing.T) {
	_, cp := newCheckpoints(t)

	assert.Panics(t, func() {
		_ = cp.Save(context.Background(), "orders", es.Position{GlobalPos: 1})
	}, "a checkpoint outside the read model transaction is a wiring fault")
}

func TestCheckpoints_SaveRollsBackWithTheTransaction(t *testing.T) {
	engine, cp := newCheckpoints(t)
	ctx := context.Background()
	boom := errors.New("the projection failed")

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO read_model (name) VALUES (?)`, "Alice"); err != nil {
			return err
		}
		if err := cp.Save(ctx, "orders", es.Position{XactID: 1, GlobalPos: 1}); err != nil {
			return err
		}
		return boom
	})
	assert.ErrorIs(t, err, boom)

	got, err := cp.Load(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, es.Position{}, got, "the checkpoint must roll back with the read model")

	var n int
	require.NoError(t, engine.QueryRow(ctx, `SELECT count(*) FROM read_model`).Scan(&n))
	assert.Equal(t, 0, n)
}

func TestCheckpoints_ResetReturnsToZero(t *testing.T) {
	engine, cp := newCheckpoints(t)
	ctx := context.Background()

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return cp.Save(ctx, "orders", es.Position{XactID: 9, GlobalPos: 99})
	}))

	require.NoError(t, cp.Reset(ctx, "orders"))

	got, err := cp.Load(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, es.Position{}, got)
}

func TestCheckpoints_ResetClearsTheReadModelInOneTransaction(t *testing.T) {
	engine, cp := newCheckpoints(t)
	ctx := context.Background()

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO read_model (name) VALUES (?)`, "Alice"); err != nil {
			return err
		}
		return cp.Save(ctx, "orders", es.Position{XactID: 9, GlobalPos: 99})
	}))

	// A replay clears both together.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx, `DELETE FROM read_model`); err != nil {
			return err
		}
		return cp.Reset(ctx, "orders")
	}))

	got, err := cp.Load(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, es.Position{}, got)

	var n int
	require.NoError(t, engine.QueryRow(ctx, `SELECT count(*) FROM read_model`).Scan(&n))
	assert.Equal(t, 0, n)
}

func TestCheckpoints_ResetIsIndependentOfSave(t *testing.T) {
	engine, cp := newCheckpoints(t)
	ctx := context.Background()

	// Reset on an unknown projection writes the zero row instead of failing.
	require.NoError(t, cp.Reset(ctx, "never-ran"))
	got, err := cp.Load(ctx, "never-ran")
	require.NoError(t, err)
	assert.Equal(t, es.Position{}, got)

	// A reset inside a transaction that rolls back leaves the checkpoint alone.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return cp.Save(ctx, "orders", es.Position{XactID: 5, GlobalPos: 5})
	}))
	_ = engine.WithTx(ctx, func(ctx context.Context) error {
		if err := cp.Reset(ctx, "orders"); err != nil {
			return err
		}
		return errors.New("abandon the replay")
	})

	got, err = cp.Load(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, es.Position{XactID: 5, GlobalPos: 5}, got)
}

func TestCheckpoints_TwoProjectionsAreIndependent(t *testing.T) {
	engine, cp := newCheckpoints(t)
	ctx := context.Background()

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		if err := cp.Save(ctx, "orders", es.Position{XactID: 1, GlobalPos: 10}); err != nil {
			return err
		}
		return cp.Save(ctx, "billing", es.Position{XactID: 2, GlobalPos: 20})
	}))

	orders, err := cp.Load(ctx, "orders")
	require.NoError(t, err)
	billing, err := cp.Load(ctx, "billing")
	require.NoError(t, err)

	assert.Equal(t, es.Position{XactID: 1, GlobalPos: 10}, orders)
	assert.Equal(t, es.Position{XactID: 2, GlobalPos: 20}, billing)
}

func TestCheckpoints_LoadReadsItsOwnWrite(t *testing.T) {
	engine, cp := newCheckpoints(t)

	require.NoError(t, engine.WithTx(context.Background(), func(ctx context.Context) error {
		if err := cp.Save(ctx, "orders", es.Position{XactID: 3, GlobalPos: 30}); err != nil {
			return err
		}
		got, err := cp.Load(ctx, "orders")
		require.NoError(t, err)
		assert.Equal(t, es.Position{XactID: 3, GlobalPos: 30}, got,
			"a load in the transaction must see the save")
		return nil
	}))
}
