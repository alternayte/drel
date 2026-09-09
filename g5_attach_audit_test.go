package drel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Audit test model exercising Attach(StateModified) on SQLite ───────────────

type g5AuditItem struct {
	ID        int
	Title     string
	CreatedBy string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type g5AuditSnapshot struct{ Title string }

var g5AuditItemMeta = drel.ModelMeta[g5AuditItem]{
	Table:     "g5_audit_items",
	Columns:   []string{"id", "title", "created_by", "updated_by", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	HasAudit:  true,
	AuditSetCreate: func(p *g5AuditItem, actor string) {
		p.CreatedBy = actor
		p.UpdatedBy = actor
	},
	AuditSetUpdate: func(p *g5AuditItem, actor string) {
		p.UpdatedBy = actor
	},
	Scan: func(row drel.Row) (*g5AuditItem, error) {
		p := &g5AuditItem{}
		err := row.Scan(&p.ID, &p.Title, &p.CreatedBy, &p.UpdatedBy, &p.CreatedAt, &p.UpdatedAt)
		return p, err
	},
	Snapshot: func(p *g5AuditItem) any { return g5AuditSnapshot{Title: p.Title} },
	Diff: func(p *g5AuditItem, snap any) []drel.FieldChange {
		s := snap.(g5AuditSnapshot)
		var changes []drel.FieldChange
		if p.Title != s.Title {
			changes = append(changes, drel.FieldChange{Column: "title", Value: p.Title})
		}
		return changes
	},
	PKValue: func(p *g5AuditItem) any { return p.ID },
	InsertColumns: func(p *g5AuditItem) ([]string, []any) {
		return []string{"title", "created_by", "updated_by"},
			[]any{p.Title, p.CreatedBy, p.UpdatedBy}
	},
	ScanReturning: func(p *g5AuditItem, row drel.Row) error {
		return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	},
}

func setupG5AuditSQLite(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	_, err = engine.Exec(context.Background(), `
		CREATE TABLE g5_audit_items (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			updated_by TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)
	return engine
}

func TestSQLite_AttachAudit_Modified_DoesNotErrorOrClobberCreatedBy(t *testing.T) {
	engine := setupG5AuditSQLite(t)
	ctx := context.Background()

	// Insert a row created by "creator". The actor is carried on the context
	// (drel.WithActor returns a derived context, NOT a TxOption).
	creatorCtx := drel.WithActor(ctx, "creator")
	require.NoError(t, engine.Transaction(creatorCtx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, g5AuditItemMeta)
		repo.Add(&g5AuditItem{Title: "Original"})
		return nil
	}))

	var id int
	row := engine.QueryRow(ctx, "SELECT id FROM g5_audit_items WHERE title = ?", "Original")
	require.NoError(t, row.Scan(&id))

	// Attach an externally-built entity as Modified, with a different actor.
	// Today this emits a duplicate updated_by (hard SQL error) and would clobber
	// created_by. After the fix it must succeed, leave created_by untouched, and
	// set updated_by to the new actor exactly once.
	editorCtx := drel.WithActor(ctx, "editor")
	err := engine.Transaction(editorCtx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, g5AuditItemMeta)
		ext := &g5AuditItem{ID: id, Title: "Attached"}
		repo.Attach(ext, drel.StateModified)
		return tx.SaveChanges(editorCtx)
	})
	require.NoError(t, err)

	var title, createdBy, updatedBy string
	row = engine.QueryRow(ctx,
		"SELECT title, created_by, updated_by FROM g5_audit_items WHERE id = ?", id)
	require.NoError(t, row.Scan(&title, &createdBy, &updatedBy))
	assert.Equal(t, "Attached", title)
	assert.Equal(t, "creator", createdBy, "created_by must NOT be clobbered by a modified-attach")
	assert.Equal(t, "editor", updatedBy, "updated_by must be the new actor, set exactly once")
}
