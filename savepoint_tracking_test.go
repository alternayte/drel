package drel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertItem(t *testing.T, engine *drel.Engine, title string) int {
	t.Helper()
	ctx := context.Background()
	item := &sqliteItem{Title: title}
	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, sqliteItemMeta).Add(item)
		return tx.SaveChanges(ctx)
	})
	require.NoError(t, err)
	return item.ID
}

func countItems(t *testing.T, engine *drel.Engine) int {
	t.Helper()
	n, err := drel.NewRepository(engine, sqliteItemMeta).Count(context.Background())
	require.NoError(t, err)
	return n
}

// ─── Savepoints ──────────────────────────────────────────────────────────────

func TestSavepoint_ReleaseCommits(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		repo.Add(&sqliteItem{Title: "outer"})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return tx.Savepoint(ctx, "sp1", func(sp *drel.Tx) error {
			drel.NewTxRepository(sp, sqliteItemMeta).Add(&sqliteItem{Title: "inner"})
			return sp.SaveChanges(ctx)
		})
	})
	require.NoError(t, err)
	assert.Equal(t, 2, countItems(t, engine))
}

func TestSavepoint_RollbackUndoesInnerButKeepsOuter(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		repo.Add(&sqliteItem{Title: "outer"})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		// The savepoint's work is rolled back; the error is intentionally swallowed
		// so the outer transaction still commits.
		spErr := tx.Savepoint(ctx, "risky", func(sp *drel.Tx) error {
			drel.NewTxRepository(sp, sqliteItemMeta).Add(&sqliteItem{Title: "inner"})
			if err := sp.SaveChanges(ctx); err != nil {
				return err
			}
			return fmt.Errorf("boom")
		})
		assert.Error(t, spErr)
		return nil
	})
	require.NoError(t, err)
	// Only "outer" survives — the inner insert was rolled back AND the tracker was
	// reverted so it was not re-flushed on the outer commit.
	assert.Equal(t, 1, countItems(t, engine))
}

func TestSavepoint_Nested(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, sqliteItemMeta).Add(&sqliteItem{Title: "a"})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return tx.Savepoint(ctx, "outer_sp", func(sp1 *drel.Tx) error {
			drel.NewTxRepository(sp1, sqliteItemMeta).Add(&sqliteItem{Title: "b"})
			if err := sp1.SaveChanges(ctx); err != nil {
				return err
			}
			_ = sp1.Savepoint(ctx, "inner_sp", func(sp2 *drel.Tx) error {
				drel.NewTxRepository(sp2, sqliteItemMeta).Add(&sqliteItem{Title: "c"})
				if err := sp2.SaveChanges(ctx); err != nil {
					return err
				}
				return fmt.Errorf("drop c")
			})
			return nil
		})
	})
	require.NoError(t, err)
	assert.Equal(t, 2, countItems(t, engine)) // a and b, not c
}

func TestSavepoint_NestedSameName(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	// Reusing the same savepoint name at different nesting levels must not
	// collide: the inner rollback drops only "c", the outer keeps "a"+"b".
	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, sqliteItemMeta).Add(&sqliteItem{Title: "a"})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return tx.Savepoint(ctx, "sp", func(sp1 *drel.Tx) error {
			drel.NewTxRepository(sp1, sqliteItemMeta).Add(&sqliteItem{Title: "b"})
			if err := sp1.SaveChanges(ctx); err != nil {
				return err
			}
			_ = sp1.Savepoint(ctx, "sp", func(sp2 *drel.Tx) error { // same name
				drel.NewTxRepository(sp2, sqliteItemMeta).Add(&sqliteItem{Title: "c"})
				if err := sp2.SaveChanges(ctx); err != nil {
					return err
				}
				return fmt.Errorf("drop c")
			})
			return nil
		})
	})
	require.NoError(t, err)
	assert.Equal(t, 2, countItems(t, engine))
}

// On the SUCCESS path, the savepoint's tracked changes flush before RELEASE
// (issue #12). If RELEASE then fails (provoked here by pre-releasing the
// framework's savepoint, name "sp_rel_1"), the error must surface, the tracker
// must be reverted so the outer commit does not re-flush, and the flushed
// insert stays in the transaction because only a full rollback can rewind it.
func TestSavepoint_ReleaseFailureAfterFlushReturnsError(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, sqliteItemMeta).Add(&sqliteItem{Title: "outer"})
		if e := tx.SaveChanges(ctx); e != nil {
			return e
		}

		spErr := tx.Savepoint(ctx, "rel", func(sp *drel.Tx) error {
			// Stage an add; Savepoint itself flushes it before RELEASE.
			drel.NewTxRepository(sp, sqliteItemMeta).Add(&sqliteItem{Title: "inner"})
			// Pre-release the framework's savepoint so the framework's own
			// RELEASE on the success path fails ("no such savepoint").
			_, _ = sp.Exec(ctx, "RELEASE SAVEPOINT sp_rel_1")
			return nil
		})
		require.Error(t, spErr)
		require.Contains(t, spErr.Error(), "release savepoint")
		// Swallow it so the outer transaction still commits; the flushed inner
		// insert is already in the transaction and commits with it.
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, 2, countItems(t, engine))
}

// TestSavepoint_FlushFailureOnSuccessPathRollsBack pins the other half of issue
// #12: the success-path flush runs inside the savepoint, so a flush error
// rewinds the savepoint and restores the tracker instead of surfacing at the
// outer commit.
func TestSavepoint_FlushFailureOnSuccessPathRollsBack(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	insertItem(t, engine, "existing")

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, sqliteItemMeta).Add(&sqliteItem{Title: "keep"})
		spErr := tx.Savepoint(ctx, "flush-fails", func(sp *drel.Tx) error {
			// StateModified force-updates every column; a missing PK makes the
			// update affect zero rows, which the flush reports as an error.
			ghost := &sqliteItem{ID: 4242, Title: "ghost"}
			drel.NewTxRepository(sp, sqliteItemMeta).Attach(ghost, drel.StateModified)
			return nil // success path -> flush before RELEASE
		})
		require.Error(t, spErr)
		require.Contains(t, spErr.Error(), "no rows affected")
		// Swallow it so the outer transaction commits; "keep" must still land.
		return nil
	})
	require.NoError(t, err)

	// "existing" and "keep"; the ghost row was never written and the failed
	// savepoint's tracked work was dropped.
	assert.Equal(t, 2, countItems(t, engine))
}

// TestWithTx_NestedFlushesOnRelease pins the reported shape of issue #12. A
// nested WithTx call inside a dreltest-style sandbox behaves like a top-level
// one: the raw SQL after the call, in the same transaction, observes the
// tracked change because Savepoint flushes before RELEASE.
func TestWithTx_NestedFlushesOnRelease(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	id := insertItem(t, engine, "new")

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		// A write function written as if it owned the transaction; here it is
		// nested, so it joins through a savepoint.
		err := engine.WithTx(ctx, func(ctx context.Context) error {
			item, err := drel.NewTxRepository(drel.MustFromContext(ctx), sqliteItemMeta).Find(ctx, id)
			if err != nil {
				return err
			}
			item.Title = "done"
			return nil
		})
		if err != nil {
			return err
		}

		// Raw SQL in the outer transaction must observe the nested change.
		var title string
		if err := drel.MustFromContext(ctx).QueryRow(ctx, `SELECT title FROM items WHERE id = $1`, id).Scan(&title); err != nil {
			return err
		}
		if title != "done" {
			return fmt.Errorf("nested WithTx did not flush: title = %q, want %q", title, "done")
		}
		return nil
	})
	require.NoError(t, err)

	found, err := drel.NewRepository(engine, sqliteItemMeta).Find(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "done", found.Title)
}

func TestAttach_UnchangedInsertOnlyMetaDoesNotPanic(t *testing.T) {
	// evItemMeta (defined in outbox_test.go) has no Snapshot/Diff. Attaching as
	// StateUnchanged must not panic even though such a model can't be diffed.
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE ev_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_ = engine.Transaction(ctx, func(tx *drel.Tx) error {
			repo := drel.NewTxRepository(tx, evItemMeta)
			repo.Attach(&evItem{ID: 1, Name: "x"}, drel.StateUnchanged)
			return tx.SaveChanges(ctx)
		})
	})
}

// ─── Tracking by default vs AsNoTracking ─────────────────────────────────────

func TestTxQuery_TracksByDefault(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	insertItem(t, engine, "Original")

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		items, err := repo.Where(drel.NewStringCol("title").Eq("Original")).All(ctx)
		if err != nil {
			return err
		}
		require.Len(t, items, 1)
		items[0].Title = "Tracked" // mutation must be detected on commit
		return nil
	})
	require.NoError(t, err)

	found, err := drel.NewRepository(engine, sqliteItemMeta).Where(drel.NewStringCol("title").Eq("Tracked")).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Tracked", found.Title)
}

func TestTxQuery_AsNoTrackingDoesNotPersist(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	insertItem(t, engine, "Original")

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		items, err := repo.AsNoTracking().Where(drel.NewStringCol("title").Eq("Original")).All(ctx)
		if err != nil {
			return err
		}
		require.Len(t, items, 1)
		items[0].Title = "ShouldNotPersist"
		return nil
	})
	require.NoError(t, err)

	n, err := drel.NewRepository(engine, sqliteItemMeta).Where(drel.NewStringCol("title").Eq("Original")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "untracked mutation must not be persisted")
}

// ─── Attach / Detach ─────────────────────────────────────────────────────────

func TestAttach_ModifiedPersistsFullUpdate(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	id := insertItem(t, engine, "Original")

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		// Entity built outside the tracker (e.g. from a request body).
		ext := &sqliteItem{ID: id, Title: "Attached"}
		repo.Attach(ext, drel.StateModified)
		return tx.SaveChanges(ctx)
	})
	require.NoError(t, err)

	found, err := drel.NewRepository(engine, sqliteItemMeta).Find(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Attached", found.Title)
}

func TestDetach_StopsTracking(t *testing.T) {
	engine := setupSQLiteEngine(t)
	ctx := context.Background()
	id := insertItem(t, engine, "Original")

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, sqliteItemMeta)
		found, err := repo.Find(ctx, id)
		if err != nil {
			return err
		}
		repo.Detach(found)
		found.Title = "ShouldNotPersist"
		return tx.SaveChanges(ctx)
	})
	require.NoError(t, err)

	found, err := drel.NewRepository(engine, sqliteItemMeta).Find(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Original", found.Title)
}
