package drel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTxBulkInsert_RunsInsideTransaction(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		n, err := repo.BulkInsert(ctx, []*sqliteItem{{Title: "a"}, {Title: "b"}, {Title: "c"}})
		if err != nil {
			return err
		}
		assert.Equal(t, 3, n)
		return nil
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, sqliteItemMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// Bulk insert inside a caller transaction must roll back atomically with
// surrounding work when the transaction fails.
func TestTxBulkInsert_RollsBackWithSurroundingWork(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	sentinel := errors.New("boom")
	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		if _, err := repo.BulkInsert(ctx, []*sqliteItem{{Title: "x"}, {Title: "y"}}); err != nil {
			return err
		}
		return sentinel // force rollback
	})
	require.ErrorIs(t, err, sentinel)

	repo := drel.NewRepository(engine, sqliteItemMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "bulk insert must roll back with the surrounding transaction")
}

// txUpsertItem is a local model with a UNIQUE title constraint for upsert tests.
type txUpsertItem struct {
	ID    int
	Title string
}

var txUpsertItemMeta = drel.ModelMeta[txUpsertItem]{
	Table:     "tx_upsert_items",
	Columns:   []string{"id", "title"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*txUpsertItem, error) {
		x := &txUpsertItem{}
		return x, row.Scan(&x.ID, &x.Title)
	},
	PKValue: func(x *txUpsertItem) any { return x.ID },
	InsertColumns: func(x *txUpsertItem) ([]string, []any) {
		return []string{"title"}, []any{x.Title}
	},
}

func setupTxUpsertEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE tx_upsert_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL UNIQUE)`)
	require.NoError(t, err)
	return engine
}

func TestTxBulkUpsert_RunsInsideTransaction(t *testing.T) {
	engine := setupTxUpsertEngine(t)
	ctx := context.Background()

	// Seed a row.
	_, err := engine.Exec(ctx, "INSERT INTO tx_upsert_items (title) VALUES ('orig')")
	require.NoError(t, err)

	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, txUpsertItemMeta)
		// Upsert: 'orig' conflicts on title → update (no-op), 'new' inserts.
		n, err := repo.BulkUpsert(ctx, []*txUpsertItem{{Title: "orig"}, {Title: "new"}},
			drel.ConflictColumns(drel.NewStringCol("title")),
			drel.UpdateOnConflict(drel.NewStringCol("title")),
		)
		if err != nil {
			return err
		}
		assert.Equal(t, 2, n)
		return nil
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, txUpsertItemMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count) // 'orig' (updated) + 'new' (inserted)
}
