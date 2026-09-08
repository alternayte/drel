//go:build integration

package dreltest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/dreltest"
	"github.com/alternayte/drel/dreltest/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pgNotesDDL = `CREATE TABLE notes (id BIGSERIAL PRIMARY KEY, body TEXT NOT NULL)`

func pgRollbackEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine := pgtest.NewPostgres(t)
	_, err := engine.Exec(context.Background(), pgNotesDDL)
	require.NoError(t, err)
	return engine
}

func pgCountNotes(t *testing.T, e *drel.Engine) int {
	t.Helper()
	var n int
	require.NoError(t, e.QueryRow(context.Background(), `SELECT count(*) FROM notes`).Scan(&n))
	return n
}

// TestIntegration_WithRollback_Postgres proves the harness isolates a test on
// the database the host uses.
func TestIntegration_WithRollback_Postgres(t *testing.T) {
	engine := pgRollbackEngine(t)

	t.Run("writes", func(t *testing.T) {
		dreltest.WithRollback(t, engine, func(ctx context.Context) {
			// The code under test opens its own transaction. It must join the
			// harness transaction through a savepoint.
			require.NoError(t, engine.WithTx(ctx, func(inner context.Context) error {
				_, err := drel.MustFromContext(inner).Exec(inner,
					`INSERT INTO notes (body) VALUES ($1)`, "from the service")
				return err
			}))

			var n int
			require.NoError(t, drel.MustFromContext(ctx).
				QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&n))
			assert.Equal(t, 1, n)
		})
	})

	assert.Equal(t, 0, pgCountNotes(t, engine), "the test must leave the database clean")
}

// TestIntegration_WithRollback_ParallelPostgres proves the point of the harness:
// many tests run at one time against one database and stay isolated.
func TestIntegration_WithRollback_ParallelPostgres(t *testing.T) {
	engine := pgRollbackEngine(t)

	t.Run("group", func(t *testing.T) {
		for i := 0; i < 6; i++ {
			t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
				t.Parallel()
				dreltest.WithRollback(t, engine, func(ctx context.Context) {
					tx := drel.MustFromContext(ctx)
					_, err := tx.Exec(ctx, `INSERT INTO notes (body) VALUES ($1)`, "row")
					require.NoError(t, err)

					var n int
					require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&n))
					assert.Equal(t, 1, n, "each parallel test must see only its own row")
				})
			})
		}
	})

	assert.Equal(t, 0, pgCountNotes(t, engine))
}
