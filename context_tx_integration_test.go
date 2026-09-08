//go:build integration

package drel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/internal/testmodels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctrlTable creates the control table that models the host outbox pattern.
func ctrlTable(t *testing.T, engine *drel.Engine) {
	t.Helper()
	_, err := engine.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS ctrl_rows (id SERIAL PRIMARY KEY, note TEXT NOT NULL)`)
	require.NoError(t, err)
}

func ctrlCount(t *testing.T, engine *drel.Engine) int {
	t.Helper()
	var n int
	require.NoError(t, engine.QueryRow(context.Background(), `SELECT count(*) FROM ctrl_rows`).Scan(&n))
	return n
}

// TestIntegration_WithTx_CommitsAggregateAndControlRow proves the property the
// host depends on. A tracked model write and a hand-written control row commit
// together in one transaction.
func TestIntegration_WithTx_CommitsAggregateAndControlRow(t *testing.T) {
	engine := setupTestDB(t)
	ctrlTable(t, engine)
	ctx := context.Background()

	var productID int
	err := engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)

		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		p := &testmodels.Product{Name: "Widget", Price: 100, InStock: true}
		repo.Add(p)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		productID = p.ID

		_, err := tx.Exec(ctx, `INSERT INTO ctrl_rows (note) VALUES ($1)`, "product created")
		return err
	})
	require.NoError(t, err)

	check := drel.NewRepository(engine, testmodels.ProductMeta)
	got, err := check.Find(ctx, productID)
	require.NoError(t, err)
	assert.Equal(t, "Widget", got.Name)
	assert.Equal(t, 1, ctrlCount(t, engine))
}

// TestIntegration_WithTx_RollbackDropsBoth proves the other half. A failure
// after both writes leaves neither row behind.
func TestIntegration_WithTx_RollbackDropsBoth(t *testing.T) {
	engine := setupTestDB(t)
	ctrlTable(t, engine)
	ctx := context.Background()
	sentinel := errors.New("publish failed")

	var productID int
	err := engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)

		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		p := &testmodels.Product{Name: "Ghost", Price: 1, InStock: true}
		repo.Add(p)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		productID = p.ID

		if _, err := tx.Exec(ctx, `INSERT INTO ctrl_rows (note) VALUES ($1)`, "ghost"); err != nil {
			return err
		}
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)

	check := drel.NewRepository(engine, testmodels.ProductMeta)
	_, findErr := check.Find(ctx, productID)
	assert.ErrorIs(t, findErr, drel.ErrNotFound)
	assert.Equal(t, 0, ctrlCount(t, engine))
}

// TestIntegration_WithTx_NestedErrorKeepsOuterWork proves that a nested call
// rolls back to its savepoint only.
func TestIntegration_WithTx_NestedErrorKeepsOuterWork(t *testing.T) {
	engine := setupTestDB(t)
	ctrlTable(t, engine)
	ctx := context.Background()
	sentinel := errors.New("handler failed")

	err := engine.WithTx(ctx, func(outer context.Context) error {
		if _, err := drel.MustFromContext(outer).Exec(outer,
			`INSERT INTO ctrl_rows (note) VALUES ($1)`, "outer"); err != nil {
			return err
		}

		innerErr := engine.WithTx(outer, func(inner context.Context) error {
			if _, err := drel.MustFromContext(inner).Exec(inner,
				`INSERT INTO ctrl_rows (note) VALUES ($1)`, "inner"); err != nil {
				return err
			}
			return sentinel
		})
		assert.ErrorIs(t, innerErr, sentinel)
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, 1, ctrlCount(t, engine))
	var note string
	require.NoError(t, engine.QueryRow(ctx, `SELECT note FROM ctrl_rows`).Scan(&note))
	assert.Equal(t, "outer", note)
}

// TestIntegration_WithTx_RollbackOnPanic proves the panic path rolls back and
// does not leak the connection.
func TestIntegration_WithTx_RollbackOnPanic(t *testing.T) {
	engine := setupTestDB(t)
	ctrlTable(t, engine)
	ctx := context.Background()

	assert.Panics(t, func() {
		_ = engine.WithTx(ctx, func(ctx context.Context) error {
			_, err := drel.MustFromContext(ctx).Exec(ctx,
				`INSERT INTO ctrl_rows (note) VALUES ($1)`, "doomed")
			require.NoError(t, err)
			panic("boom")
		})
	})

	assert.Equal(t, 0, ctrlCount(t, engine))
}
