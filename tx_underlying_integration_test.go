//go:build integration

package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/internal/testmodels"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_TxUnderlying_PgxTx pins issue #11 for Postgres: the live
// pgx.Tx is reachable, so a foreign library that takes a pgx.Tx (a job queue,
// for example) joins the same transaction as the drel write.
func TestIntegration_TxUnderlying_PgxTx(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, testmodels.ProductMeta).
			Add(&testmodels.Product{Name: "via-drel", Price: 1, InStock: true})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}

		handle := tx.Underlying()
		pgxTx, ok := handle.(pgx.Tx)
		require.True(t, ok, "Postgres must expose pgx.Tx, got %T", handle)
		_, err := pgxTx.Exec(ctx,
			"INSERT INTO products (name, price, in_stock) VALUES ($1, $2, $3)",
			"via-pgx", 2, true)
		return err
	})
	require.NoError(t, err)

	n, err := drel.NewRepository(engine, testmodels.ProductMeta).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}
