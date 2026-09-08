//go:build integration

package accounts_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alternayte/drel/examples/value-objects/accounts"
)

func newSQLiteEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })

	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner TEXT NOT NULL,
		balance_amount TEXT NOT NULL,
		balance_currency TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	require.NoError(t, err)
	return engine
}

func TestMultiColVO_RoundTrip_SQLite(t *testing.T) {
	engine := newSQLiteEngine(t)
	ctx := context.Background()

	// Insert in a context transaction (exercises expanded InsertColumns).
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		drel.NewTxRepository(drel.MustFromContext(ctx), accounts.AccountMeta).
			Add(accounts.NewAccount("alice", accounts.NewMoney(100, "USD")))
		return nil
	}))

	// Read back (exercises generated scan + DrelScanMulti).
	read := drel.NewRepository(engine, accounts.AccountMeta)
	loaded, err := read.Where(accounts.Accounts.BalanceCurrency.Eq("USD")).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "alice", loaded.Owner())
	assert.Equal(t, 100, loaded.Balance().Amount())
	assert.Equal(t, "USD", loaded.Balance().Currency())

	// Mutate one sub-column, commit (exercises per-sub-column diff).
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		acct, err := drel.NewTxRepository(drel.MustFromContext(ctx), accounts.AccountMeta).
			Find(ctx, loaded.ID())
		if err != nil {
			return err
		}
		acct.SetBalance(accounts.NewMoney(250, "USD"))
		return nil
	}))

	final, err := read.Find(ctx, loaded.ID())
	require.NoError(t, err)
	assert.Equal(t, 250, final.Balance().Amount())
	assert.Equal(t, "USD", final.Balance().Currency())
}
