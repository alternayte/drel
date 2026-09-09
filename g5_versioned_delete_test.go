package drel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Versioned (+ optional soft-delete) test models for the SQLite delete path ──

type g5VerItem struct {
	ID        int
	Title     string
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type g5VerSnapshot struct{ Title string }

var g5VerItemMeta = drel.ModelMeta[g5VerItem]{
	Table:        "g5_ver_items",
	Columns:      []string{"id", "title", "version", "created_at", "updated_at"},
	PKColumns:    []string{"id"},
	HasVersioned: true,
	VersionValue: func(p *g5VerItem) int { return p.Version },
	SetVersion:   func(p *g5VerItem, v int) { p.Version = v },
	Scan: func(row drel.Row) (*g5VerItem, error) {
		p := &g5VerItem{}
		err := row.Scan(&p.ID, &p.Title, &p.Version, &p.CreatedAt, &p.UpdatedAt)
		return p, err
	},
	Snapshot: func(p *g5VerItem) any { return g5VerSnapshot{Title: p.Title} },
	Diff: func(p *g5VerItem, snap any) []drel.FieldChange {
		s := snap.(g5VerSnapshot)
		var changes []drel.FieldChange
		if p.Title != s.Title {
			changes = append(changes, drel.FieldChange{Column: "title", Value: p.Title})
		}
		return changes
	},
	PKValue:       func(p *g5VerItem) any { return p.ID },
	InsertColumns: func(p *g5VerItem) ([]string, []any) { return []string{"title"}, []any{p.Title} },
	ScanReturning: func(p *g5VerItem, row drel.Row) error {
		return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	},
}

type g5SoftVerItem struct {
	ID        int
	Title     string
	DeletedAt *time.Time
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type g5SoftVerSnapshot struct{ Title string }

var g5SoftVerItemMeta = drel.ModelMeta[g5SoftVerItem]{
	Table:         "g5_softver_items",
	Columns:       []string{"id", "title", "deleted_at", "version", "created_at", "updated_at"},
	PKColumns:     []string{"id"},
	HasSoftDelete: true,
	HasVersioned:  true,
	VersionValue:  func(p *g5SoftVerItem) int { return p.Version },
	SetVersion:    func(p *g5SoftVerItem, v int) { p.Version = v },
	Scan: func(row drel.Row) (*g5SoftVerItem, error) {
		p := &g5SoftVerItem{}
		err := row.Scan(&p.ID, &p.Title, &p.DeletedAt, &p.Version, &p.CreatedAt, &p.UpdatedAt)
		return p, err
	},
	Snapshot: func(p *g5SoftVerItem) any { return g5SoftVerSnapshot{Title: p.Title} },
	Diff: func(p *g5SoftVerItem, snap any) []drel.FieldChange {
		s := snap.(g5SoftVerSnapshot)
		var changes []drel.FieldChange
		if p.Title != s.Title {
			changes = append(changes, drel.FieldChange{Column: "title", Value: p.Title})
		}
		return changes
	},
	PKValue:       func(p *g5SoftVerItem) any { return p.ID },
	InsertColumns: func(p *g5SoftVerItem) ([]string, []any) { return []string{"title"}, []any{p.Title} },
	ScanReturning: func(p *g5SoftVerItem, row drel.Row) error {
		return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	},
}

func setupG5VersionedSQLite(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()
	_, err = engine.Exec(ctx, `
		CREATE TABLE g5_ver_items (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT NOT NULL,
			version    INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE g5_softver_items (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT NOT NULL,
			deleted_at DATETIME,
			version    INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	require.NoError(t, err)
	return engine
}

func TestSQLite_VersionedHardDelete_StaleVersionConflicts(t *testing.T) {
	engine := setupG5VersionedSQLite(t)
	ctx := context.Background()

	// Insert one versioned row (version becomes 1).
	item := &g5VerItem{Title: "Original"}
	require.NoError(t, engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, g5VerItemMeta).Add(item)
		return nil
	}))
	require.Equal(t, 1, item.Version)

	// Externally bump the DB version to 2 BEFORE starting the delete transaction.
	// (Inside a transaction we'd deadlock SQLite's single connection.)
	_, err := engine.Exec(ctx, "UPDATE g5_ver_items SET version = 2 WHERE id = ?", item.ID)
	require.NoError(t, err)

	// Attempt to delete item that still thinks it's at version 1.
	// Attach it as Unchanged so the tracker knows about it at version=1,
	// then Remove will issue DELETE WHERE id=? AND version=1 (stale → 0 rows).
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, g5VerItemMeta)
		repo.Attach(item, drel.StateUnchanged)
		return repo.Remove(item)
	})
	require.ErrorIs(t, err, drel.ErrConcurrencyConflict)

	// Row must still exist (the stale delete was rejected).
	row := engine.QueryRow(ctx, "SELECT COUNT(*) FROM g5_ver_items WHERE id = ?", item.ID)
	var count int
	require.NoError(t, row.Scan(&count))
	assert.Equal(t, 1, count, "stale-version delete must not remove the row")
}

func TestSQLite_VersionedHardDelete_CurrentVersionSucceeds(t *testing.T) {
	engine := setupG5VersionedSQLite(t)
	ctx := context.Background()

	item := &g5VerItem{Title: "Original"}
	require.NoError(t, engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, g5VerItemMeta).Add(item)
		return nil
	}))

	require.NoError(t, engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, g5VerItemMeta)
		loaded, err := repo.Find(ctx, item.ID)
		if err != nil {
			return err
		}
		return repo.Remove(loaded)
	}))

	row := engine.QueryRow(ctx, "SELECT COUNT(*) FROM g5_ver_items WHERE id = ?", item.ID)
	var count int
	require.NoError(t, row.Scan(&count))
	assert.Equal(t, 0, count, "current-version delete must remove the row")
}

func TestSQLite_VersionedSoftDelete_StaleVersionConflicts(t *testing.T) {
	engine := setupG5VersionedSQLite(t)
	ctx := context.Background()

	item := &g5SoftVerItem{Title: "Original"}
	require.NoError(t, engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, g5SoftVerItemMeta).Add(item)
		return nil
	}))
	require.Equal(t, 1, item.Version)

	// Externally bump the DB version to 2 before the delete transaction.
	_, err := engine.Exec(ctx, "UPDATE g5_softver_items SET version = 2 WHERE id = ?", item.ID)
	require.NoError(t, err)

	// Attempt to soft-delete item that still thinks it's at version 1.
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, g5SoftVerItemMeta)
		repo.Attach(item, drel.StateUnchanged)
		return repo.Remove(item)
	})
	require.ErrorIs(t, err, drel.ErrConcurrencyConflict)

	// Soft-delete column must remain NULL (not soft-deleted) because of the stale version.
	row := engine.QueryRow(ctx, "SELECT deleted_at FROM g5_softver_items WHERE id = ?", item.ID)
	var deletedAt *time.Time
	require.NoError(t, row.Scan(&deletedAt))
	assert.Nil(t, deletedAt, "stale-version soft-delete must not set deleted_at")
}

func TestSQLite_VersionedSoftDelete_CurrentVersionSucceeds(t *testing.T) {
	engine := setupG5VersionedSQLite(t)
	ctx := context.Background()

	item := &g5SoftVerItem{Title: "Original"}
	require.NoError(t, engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, g5SoftVerItemMeta).Add(item)
		return nil
	}))

	require.NoError(t, engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, g5SoftVerItemMeta)
		loaded, err := repo.Find(ctx, item.ID)
		if err != nil {
			return err
		}
		return repo.Remove(loaded)
	}))

	row := engine.QueryRow(ctx, "SELECT deleted_at, version FROM g5_softver_items WHERE id = ?", item.ID)
	var deletedAt *time.Time
	var version int
	require.NoError(t, row.Scan(&deletedAt, &version))
	assert.NotNil(t, deletedAt, "current-version soft-delete must set deleted_at")
	assert.Equal(t, 2, version, "soft-delete must bump the version")
}
