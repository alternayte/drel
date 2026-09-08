package drel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Identity-map test model (in-memory SQLite) ──────────────────────────────

type idmapRow struct {
	ID        int
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type idmapSnapshot struct {
	Title string
}

var idmapMeta = drel.ModelMeta[idmapRow]{
	Table:    "idmap_rows",
	Columns:  []string{"id", "title", "created_at", "updated_at"},
	PKColumn: "id",
	Scan: func(row drel.Row) (*idmapRow, error) {
		p := &idmapRow{}
		err := row.Scan(&p.ID, &p.Title, &p.CreatedAt, &p.UpdatedAt)
		return p, err
	},
	Snapshot: func(p *idmapRow) any { return idmapSnapshot{Title: p.Title} },
	Diff: func(p *idmapRow, snap any) []drel.FieldChange {
		s := snap.(idmapSnapshot)
		var changes []drel.FieldChange
		if p.Title != s.Title {
			changes = append(changes, drel.FieldChange{Column: "title", Value: p.Title})
		}
		return changes
	},
	PKValue:       func(p *idmapRow) any { return p.ID },
	InsertColumns: func(p *idmapRow) ([]string, []any) { return []string{"title"}, []any{p.Title} },
	ScanReturning: func(p *idmapRow, row drel.Row) error {
		return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	},
}

func setupIdmapEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	_, err = engine.Exec(context.Background(), `
		CREATE TABLE idmap_rows (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)
	return engine
}

func seedIdmapRow(t *testing.T, engine *drel.Engine, title string) int {
	t.Helper()
	row := &idmapRow{Title: title}
	err := engine.Transaction(context.Background(), func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, idmapMeta)
		repo.Add(row)
		return tx.SaveChanges(context.Background())
	})
	require.NoError(t, err)
	require.NotZero(t, row.ID)
	return row.ID
}

func TestIdentityMap_FindTwiceReturnsSamePointer(t *testing.T) {
	engine := setupIdmapEngine(t)
	ctx := context.Background()
	id := seedIdmapRow(t, engine, "Original")

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.NewTxRepository(drel.MustFromContext(ctx), idmapMeta)

		a, err := repo.Find(ctx, id)
		require.NoError(t, err)
		b, err := repo.Find(ctx, id) // second load through the same context transaction
		require.NoError(t, err)

		assert.Same(t, a, b, "two Find(id) calls in one transaction must return the same tracked instance")
		return nil
	}))
}

func TestIdentityMap_TxFindTwiceReturnsSamePointer(t *testing.T) {
	engine := setupIdmapEngine(t)
	ctx := context.Background()
	id := seedIdmapRow(t, engine, "Original")

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, idmapMeta)

		a, err := repo.Find(ctx, id)
		if err != nil {
			return err
		}
		b, err := repo.Find(ctx, id) // second load within the same Tx
		if err != nil {
			return err
		}
		assert.Same(t, a, b, "two Find(id) calls within one Tx must return the same tracked instance")

		a.Title = "Updated"
		all, err := repo.Where(drel.Raw("id = ?", id)).All(ctx)
		if err != nil {
			return err
		}
		require.Len(t, all, 1)
		assert.Same(t, a, all[0])
		assert.Equal(t, "Updated", all[0].Title)
		return tx.SaveChanges(ctx)
	})
	require.NoError(t, err)

	// Verify a single coherent persisted value.
	got, err := drel.NewRepository(engine, idmapMeta).Find(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Updated", got.Title)
}

func TestIdentityMap_NoLostUpdateAcrossPaths(t *testing.T) {
	engine := setupIdmapEngine(t)
	ctx := context.Background()
	id := seedIdmapRow(t, engine, "Original")

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.NewTxRepository(drel.MustFromContext(ctx), idmapMeta)

		// Path 1: load and mutate.
		a, err := repo.Find(ctx, id)
		require.NoError(t, err)
		a.Title = "Updated"

		// Path 2: load again via Where (different query path, same row). Before the
		// identity map this produced a second, stale tracked instance whose flush
		// clobbered path 1's change (last-writer-wins). Now it is the same pointer.
		others, err := repo.Where(drel.Raw("id = ?", id)).All(ctx)
		require.NoError(t, err)
		require.Len(t, others, 1)
		assert.Same(t, a, others[0], "Where load of the same row must reuse the canonical instance")
		assert.Equal(t, "Updated", others[0].Title, "the canonical instance carries path 1's mutation")
		return nil
	}))

	// Reload outside the transaction: exactly one coherent value, no clobber.
	got, err := drel.NewRepository(engine, idmapMeta).Find(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Updated", got.Title)
}
