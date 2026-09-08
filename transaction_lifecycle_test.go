package drel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When the caller's context is already cancelled and fn returns an error, the
// deferred rollback must still run and clean up so the row is not persisted.
// With the old code (Rollback(ctx) on a dead ctx) cleanup is unreliable; with
// a detached rollback ctx the row must never appear.
func TestTransaction_RollbackRunsWithCancelledContext(t *testing.T) {
	engine := setupSQLiteEngine(t)

	ctx, cancel := context.WithCancel(context.Background())

	sentinel := errors.New("boom")
	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, sqliteItemMeta).Add(&sqliteItem{Title: "ghost"})
		if e := tx.SaveChanges(ctx); e != nil {
			return e
		}
		// Cancel the caller context *before* returning the error, so the
		// deferred rollback executes against an already-cancelled ctx.
		cancel()
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	// The insert must have been rolled back; cancellation must not skip cleanup.
	n := countItems(t, engine)
	assert.Equal(t, 0, n, "rollback must run even when caller ctx is cancelled")
}

func TestTransaction_FailsFastOnCancelledContext(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, called, "fn must not run when ctx is already cancelled")
}

func TestTransaction_WithReadOnly_AllowsReads(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	insertItem(t, engine, "seed")

	var got int
	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		n, e := drel.NewTxRepository(tx, sqliteItemMeta).AsNoTracking().Count(ctx)
		got = n
		return e
	}, drel.WithReadOnly())
	require.NoError(t, err)
	assert.Equal(t, 1, got)
}

// WithReadOnly composes with WithIsolation: both must reach BeginTx.
func TestTransaction_WithReadOnlyAndIsolation_Compose(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	insertItem(t, engine, "seed")

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		_, e := drel.NewTxRepository(tx, sqliteItemMeta).AsNoTracking().Count(ctx)
		return e
	}, drel.WithReadOnly(), drel.WithIsolation(drel.Serializable))
	require.NoError(t, err)
}
