package drel_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTx_Underlying_SQLite pins issue #11 for the database/sql drivers: the
// native *sql.Tx is reachable and is the live transaction, so a drel write and
// a write through the handle commit together.
func TestTx_Underlying_SQLite(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		drel.NewTxRepository(tx, sqliteItemMeta).Add(&sqliteItem{Title: "via-drel"})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}

		handle := tx.Underlying()
		sqlTx, ok := handle.(*sql.Tx)
		require.True(t, ok, "SQLite must expose *sql.Tx, got %T", handle)
		_, err := sqlTx.ExecContext(ctx, `INSERT INTO items (title) VALUES (?)`, "via-sql")
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, 2, countItems(t, engine))
}
