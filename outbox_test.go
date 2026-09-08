package drel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evItem is a model that records domain events.
type evItem struct {
	ID        int
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	events    []any
}

func (e *evItem) PendingEvents() []any { return e.events }
func (e *evItem) ClearEvents()         { e.events = nil }

type itemCreated struct {
	Name string
}

var evItemMeta = drel.ModelMeta[evItem]{
	Table:    "ev_items",
	Columns:  []string{"id", "name", "created_at", "updated_at"},
	PKColumn: "id",
	Scan: func(r drel.Row) (*evItem, error) {
		it := &evItem{}
		return it, r.Scan(&it.ID, &it.Name, &it.CreatedAt, &it.UpdatedAt)
	},
	PKValue:       func(it *evItem) any { return it.ID },
	InsertColumns: func(it *evItem) ([]string, []any) { return []string{"name"}, []any{it.Name} },
	ScanReturning: func(it *evItem, row drel.Row) error {
		return row.Scan(&it.ID, &it.CreatedAt, &it.UpdatedAt)
	},
}

func TestOutbox_WritesWithinTransaction(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	_, err = engine.Exec(ctx, `CREATE TABLE ev_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, drel.OutboxSchema("outbox", "sqlite"))
	require.NoError(t, err)

	engine.UseOutbox("outbox")

	// Create an entity that records an event.
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, evItemMeta)
		it := &evItem{Name: "widget", events: []any{itemCreated{Name: "widget"}}}
		repo.Add(it)
		return tx.SaveChanges(ctx)
	})
	require.NoError(t, err)

	// The outbox row must have been written in the same transaction.
	row := engine.QueryRow(ctx, `SELECT type, payload FROM outbox`)
	var typ, payload string
	require.NoError(t, row.Scan(&typ, &payload))
	assert.Equal(t, "github.com/alternayte/drel_test.itemCreated", typ)
	assert.JSONEq(t, `{"Name":"widget"}`, payload)
}

func TestOutboxSchema_EmitsPartialIndexSQLite(t *testing.T) {
	ddl := drel.OutboxSchema("outbox", "sqlite")
	// Table is still created.
	assert.Contains(t, ddl, `CREATE TABLE "outbox"`)
	// A partial index on unprocessed rows must be emitted so the relay's
	// `WHERE processed_at IS NULL` poll does not full-scan a growing table.
	assert.Contains(t, ddl,
		`CREATE INDEX "idx_outbox_unprocessed" ON "outbox" ("id") WHERE "processed_at" IS NULL AND "dead_at" IS NULL;`)
}

func TestOutboxSchema_EmitsPartialIndexPostgres(t *testing.T) {
	ddl := drel.OutboxSchema("outbox", "postgres")
	assert.Contains(t, ddl, `CREATE TABLE "outbox"`)
	assert.Contains(t, ddl,
		`CREATE INDEX "idx_outbox_unprocessed" ON "outbox" ("id") WHERE "processed_at" IS NULL AND "dead_at" IS NULL;`)
}

// TestOutboxSchema_IndexExecutesAgainstSQLite proves the emitted DDL (table +
// index) is valid SQLite that a relay can rely on.
func TestOutboxSchema_IndexExecutesAgainstSQLite(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	_, err = engine.Exec(ctx, drel.OutboxSchema("ob", "sqlite"))
	require.NoError(t, err)

	// The partial index must exist on the table.
	var name string
	err = engine.QueryRow(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='ob' AND name='idx_ob_unprocessed'`).
		Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "idx_ob_unprocessed", name)
}

func TestOutbox_RollbackDiscardsMessages(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	_, err = engine.Exec(ctx, `CREATE TABLE ev_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, drel.OutboxSchema("outbox", "sqlite"))
	require.NoError(t, err)
	engine.UseOutbox("outbox")

	// Force a rollback after SaveChanges by returning an error from the tx fn.
	_ = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, evItemMeta)
		repo.Add(&evItem{Name: "x", events: []any{itemCreated{Name: "x"}}})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return assert.AnError // triggers rollback
	})

	var n int
	require.NoError(t, engine.QueryRow(ctx, `SELECT COUNT(*) FROM outbox`).Scan(&n))
	assert.Equal(t, 0, n, "outbox writes must roll back with the transaction")
}

func TestEventTypeName_QualifiesPackagePath(t *testing.T) {
	// Two event types named the same in different packages must not collide:
	// the qualified name includes the package path.
	name, err := drel.EventTypeNameForTest(itemCreated{Name: "x"})
	require.NoError(t, err)
	assert.Equal(t, "github.com/alternayte/drel_test.itemCreated", name)

	// Pointer receiver unwraps to the same qualified name.
	name, err = drel.EventTypeNameForTest(&itemCreated{Name: "x"})
	require.NoError(t, err)
	assert.Equal(t, "github.com/alternayte/drel_test.itemCreated", name)
}

func TestEventTypeName_RejectsAnonymousAndNil(t *testing.T) {
	// nil maps to no type and must error rather than producing "".
	_, err := drel.EventTypeNameForTest(nil)
	require.Error(t, err)

	// An anonymous struct has an empty Name() and must error.
	anon := struct{ X int }{X: 1}
	_, err = drel.EventTypeNameForTest(anon)
	require.Error(t, err)
}

// TestOutbox_DefaultMapperRejectsAnonymousEvent proves the error surfaces
// through SaveChanges instead of writing type="".
func TestOutbox_DefaultMapperRejectsAnonymousEvent(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	_, err = engine.Exec(ctx, `CREATE TABLE ev_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, drel.OutboxSchema("outbox", "sqlite"))
	require.NoError(t, err)
	engine.UseOutbox("outbox")

	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, evItemMeta)
		// Anonymous-struct event has no type name.
		it := &evItem{Name: "widget", events: []any{struct{ X int }{X: 1}}}
		repo.Add(it)
		return tx.SaveChanges(ctx)
	})
	require.Error(t, err)

	// Nothing was written because the before-commit hook errored and rolled back.
	var n int
	require.NoError(t, engine.QueryRow(ctx, `SELECT COUNT(*) FROM outbox`).Scan(&n))
	assert.Equal(t, 0, n)
}
