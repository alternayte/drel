package drel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctxTxEngine returns an in-memory SQLite engine with one table.
func ctxTxEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	_, err = engine.Exec(context.Background(),
		`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL)`)
	require.NoError(t, err)
	return engine
}

func ctxTxCount(t *testing.T, e *drel.Engine) int {
	t.Helper()
	var n int
	require.NoError(t, e.QueryRow(context.Background(), `SELECT count(*) FROM notes`).Scan(&n))
	return n
}

func ctxTxInsert(t *testing.T, ctx context.Context, body string) {
	t.Helper()
	_, err := drel.MustFromContext(ctx).Exec(ctx, `INSERT INTO notes (body) VALUES (?)`, body)
	require.NoError(t, err)
}

func TestWithTx_PutsTxInContext(t *testing.T) {
	engine := ctxTxEngine(t)

	err := engine.WithTx(context.Background(), func(ctx context.Context) error {
		tx, ok := drel.FromContext(ctx)
		assert.True(t, ok, "the transaction is not in the context")
		assert.NotNil(t, tx)
		ctxTxInsert(t, ctx, "one")
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, ctxTxCount(t, engine))
}

func TestWithTx_RollbackOnError(t *testing.T) {
	engine := ctxTxEngine(t)
	sentinel := errors.New("boom")

	err := engine.WithTx(context.Background(), func(ctx context.Context) error {
		ctxTxInsert(t, ctx, "one")
		return sentinel
	})

	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, 0, ctxTxCount(t, engine))
}

func TestWithTx_RollbackOnPanic(t *testing.T) {
	engine := ctxTxEngine(t)

	assert.Panics(t, func() {
		_ = engine.WithTx(context.Background(), func(ctx context.Context) error {
			ctxTxInsert(t, ctx, "one")
			panic("boom")
		})
	})

	assert.Equal(t, 0, ctxTxCount(t, engine))
}

func TestWithTx_NestedReusesTheSameTransaction(t *testing.T) {
	engine := ctxTxEngine(t)

	err := engine.WithTx(context.Background(), func(outer context.Context) error {
		outerTx := drel.MustFromContext(outer)
		return engine.WithTx(outer, func(inner context.Context) error {
			assert.Same(t, outerTx, drel.MustFromContext(inner),
				"the nested call did not reuse the outer transaction")
			ctxTxInsert(t, inner, "one")
			return nil
		})
	})

	require.NoError(t, err)
	assert.Equal(t, 1, ctxTxCount(t, engine))
}

func TestWithTx_NestedErrorKeepsOuterWork(t *testing.T) {
	engine := ctxTxEngine(t)
	sentinel := errors.New("inner failed")

	err := engine.WithTx(context.Background(), func(outer context.Context) error {
		ctxTxInsert(t, outer, "outer")

		innerErr := engine.WithTx(outer, func(inner context.Context) error {
			ctxTxInsert(t, inner, "inner")
			return sentinel
		})
		assert.ErrorIs(t, innerErr, sentinel)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, ctxTxCount(t, engine), "the savepoint did not roll back the nested insert")

	var body string
	require.NoError(t, engine.QueryRow(context.Background(), `SELECT body FROM notes`).Scan(&body))
	assert.Equal(t, "outer", body)
}

func TestWithTx_NestedRejectsOptions(t *testing.T) {
	engine := ctxTxEngine(t)

	err := engine.WithTx(context.Background(), func(outer context.Context) error {
		return engine.WithTx(outer, func(context.Context) error {
			t.Fatal("the nested call ran even though it carried an option")
			return nil
		}, drel.WithReadOnly())
	})

	assert.ErrorIs(t, err, drel.ErrNestedTxOptions)
}

func TestWithTx_ForeignEngineTxIsNotReused(t *testing.T) {
	engineA := ctxTxEngine(t)
	engineB := ctxTxEngine(t)

	err := engineA.WithTx(context.Background(), func(outer context.Context) error {
		txA := drel.MustFromContext(outer)
		return engineB.WithTx(outer, func(inner context.Context) error {
			assert.NotSame(t, txA, drel.MustFromContext(inner),
				"engine B reused the transaction of engine A")
			ctxTxInsert(t, inner, "b")
			return nil
		})
	})

	require.NoError(t, err)
	assert.Equal(t, 0, ctxTxCount(t, engineA))
	assert.Equal(t, 1, ctxTxCount(t, engineB))
}

func TestWithTx_CancelledContext(t *testing.T) {
	engine := ctxTxEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := engine.WithTx(ctx, func(context.Context) error {
		t.Fatal("fn ran with a cancelled context")
		return nil
	})

	assert.ErrorIs(t, err, context.Canceled)
}
