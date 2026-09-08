package dreltest_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/dreltest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const notesDDL = `CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL)`

func rollbackEngine(t *testing.T) *drel.Engine {
	t.Helper()
	return dreltest.NewSQLite(t, dreltest.WithSchema(notesDDL))
}

func countNotes(t *testing.T, e *drel.Engine) int {
	t.Helper()
	var n int
	require.NoError(t, e.QueryRow(context.Background(), `SELECT count(*) FROM notes`).Scan(&n))
	return n
}

func TestWithRollback_ContextCarriesTheTransaction(t *testing.T) {
	engine := rollbackEngine(t)

	dreltest.WithRollback(t, engine, func(ctx context.Context) {
		tx, ok := drel.FromContext(ctx)
		assert.True(t, ok, "the transaction must travel in the context")
		assert.NotNil(t, tx)
	})
}

func TestWithRollback_WritesAreRolledBack(t *testing.T) {
	engine := rollbackEngine(t)

	t.Run("writes", func(t *testing.T) {
		dreltest.WithRollback(t, engine, func(ctx context.Context) {
			_, err := drel.MustFromContext(ctx).Exec(ctx, `INSERT INTO notes (body) VALUES (?)`, "one")
			require.NoError(t, err)

			var n int
			require.NoError(t, drel.MustFromContext(ctx).
				QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&n))
			assert.Equal(t, 1, n, "the test must read its own write")
		})
	})

	assert.Equal(t, 0, countNotes(t, engine), "the write must roll back when the test ends")
}

func TestWithRollback_NestedWithTxJoinsTheSameTransaction(t *testing.T) {
	engine := rollbackEngine(t)

	dreltest.WithRollback(t, engine, func(ctx context.Context) {
		outer := drel.MustFromContext(ctx)

		require.NoError(t, engine.WithTx(ctx, func(inner context.Context) error {
			assert.Same(t, outer, drel.MustFromContext(inner),
				"code under test must join the harness transaction")
			_, err := drel.MustFromContext(inner).Exec(inner,
				`INSERT INTO notes (body) VALUES (?)`, "from the service")
			return err
		}))

		var n int
		require.NoError(t, outer.QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&n))
		assert.Equal(t, 1, n)
	})
}

func TestWithRollback_NestedCommitIsStillRolledBack(t *testing.T) {
	engine := rollbackEngine(t)

	t.Run("service commits", func(t *testing.T) {
		dreltest.WithRollback(t, engine, func(ctx context.Context) {
			require.NoError(t, engine.WithTx(ctx, func(inner context.Context) error {
				_, err := drel.MustFromContext(inner).Exec(inner,
					`INSERT INTO notes (body) VALUES (?)`, "committed by the service")
				return err
			}))
		})
	})

	assert.Equal(t, 0, countNotes(t, engine),
		"a nested commit is a savepoint release; the harness still rolls back")
}

func TestWithRollback_RollsBackAfterFatal(t *testing.T) {
	engine := rollbackEngine(t)

	// A failing test must not leave its writes behind. fakeT's FailNow panics,
	// which unwinds the same way t.Fatal does.
	t.Run("failing test", func(t *testing.T) {
		ft := &fakeT{T: t}
		func() {
			defer func() { _ = recover() }()
			dreltest.WithRollback(ft, engine, func(ctx context.Context) {
				_, err := drel.MustFromContext(ctx).Exec(ctx,
					`INSERT INTO notes (body) VALUES (?)`, "doomed")
				require.NoError(t, err)
				ft.Fatal("the test failed here")
			})
		}()
		assert.True(t, ft.failed)
	})

	assert.Equal(t, 0, countNotes(t, engine), "a failed test must not leave a row behind")
}

// TestWithRollback_SQLiteIsSerial documents the SQLite limit. A SQLite engine
// drives one connection, so two parked transactions cannot exist at one time.
// Run the tests one after another on SQLite, and run them in parallel on
// Postgres, where the pool gives each test its own connection.
func TestWithRollback_SQLiteIsSerial(t *testing.T) {
	engine := rollbackEngine(t)

	for _, body := range []string{"first", "second"} {
		t.Run(body, func(t *testing.T) {
			dreltest.WithRollback(t, engine, func(ctx context.Context) {
				tx := drel.MustFromContext(ctx)
				_, err := tx.Exec(ctx, `INSERT INTO notes (body) VALUES (?)`, body)
				require.NoError(t, err)

				var n int
				require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&n))
				assert.Equal(t, 1, n, "each test must see only its own row")
			})
		})
	}

	assert.Equal(t, 0, countNotes(t, engine))
}
