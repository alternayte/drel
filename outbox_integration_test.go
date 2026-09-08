//go:build integration

package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutboxSchema_PostgresIndexAndRelay(t *testing.T) {
	engine := setupTestDB(t) // reuse the repo's testcontainers helper
	defer engine.Close()
	ctx := context.Background()

	// The Postgres DDL (table + partial index) must apply cleanly.
	_, err := engine.Exec(ctx, drel.OutboxSchema("outbox", "postgres"))
	require.NoError(t, err)

	// The partial index must exist (pg_indexes lists it).
	var idxdef string
	err = engine.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE tablename = 'outbox' AND indexname = 'idx_outbox_unprocessed'`).
		Scan(&idxdef)
	require.NoError(t, err)
	assert.Contains(t, idxdef, "processed_at IS NULL")
	assert.Contains(t, idxdef, "dead_at IS NULL")

	// The relay claim plans against the partial index, not a seq scan. The
	// predicate must match the index, so it carries the dead_at test too.
	rows, err := engine.Query(ctx,
		`EXPLAIN SELECT id, type, payload FROM outbox
		 WHERE processed_at IS NULL AND dead_at IS NULL ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var plan, all string
	for rows.Next() {
		require.NoError(t, rows.Scan(&plan))
		all += plan + "\n"
	}
	require.NoError(t, rows.Err())
	assert.Contains(t, all, "idx_outbox_unprocessed",
		"the relay claim must use the partial index, got plan:\n"+all)
}

func TestBulkInsertWithEvents_PostgresAtomicOutbox(t *testing.T) {
	engine := setupTestDB(t)
	defer engine.Close()
	ctx := context.Background()

	_, err := engine.Exec(ctx, `CREATE TABLE ev_items (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, drel.OutboxSchema("outbox", "postgres"))
	require.NoError(t, err)
	engine.UseOutbox("outbox")

	repo := drel.NewRepository(engine, evItemMeta)
	n, err := repo.BulkInsertWithEvents(ctx, []*evItem{
		{Name: "a", events: []any{itemCreated{Name: "a"}}},
		{Name: "b", events: []any{itemCreated{Name: "b"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	var c int
	require.NoError(t, engine.QueryRow(ctx, `SELECT COUNT(*) FROM outbox`).Scan(&c))
	assert.Equal(t, 2, c)

	// The qualified type name lands in the outbox (cross-package safe).
	var typ string
	require.NoError(t, engine.QueryRow(ctx, `SELECT type FROM outbox LIMIT 1`).Scan(&typ))
	assert.Equal(t, "github.com/alternayte/drel_test.itemCreated", typ)
}
