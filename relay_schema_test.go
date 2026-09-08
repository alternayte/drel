package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOutboxSchema_LeaseColumns proves both dialects emit the lease, attempt and
// partition columns the relay needs, and that the partial index skips dead rows.
func TestOutboxSchema_LeaseColumns(t *testing.T) {
	for _, d := range []string{"postgres", "sqlite"} {
		t.Run(d, func(t *testing.T) {
			ddl := drel.OutboxSchema("outbox", d)
			for _, col := range []string{
				`"partition_key"`, `"claimed_by"`, `"claimed_until"`,
				`"attempts"`, `"last_error"`, `"dead_at"`,
			} {
				assert.Contains(t, ddl, col)
			}
			assert.Contains(t, ddl,
				`WHERE "processed_at" IS NULL AND "dead_at" IS NULL;`)

			// The partition lease table holds the cross-replica order.
			assert.Contains(t, ddl, `CREATE TABLE "outbox_partitions" (`)
			assert.Contains(t, ddl, `"partition_key" TEXT PRIMARY KEY`)
			assert.Contains(t, ddl, `"claimed_by" TEXT NOT NULL`)
		})
	}
}

// TestOutboxSchema_LeaseColumnsExecuteAgainstSQLite proves the new DDL is valid.
func TestOutboxSchema_LeaseColumnsExecuteAgainstSQLite(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()
	_, err = engine.Exec(ctx, drel.OutboxSchema("ob", "sqlite"))
	require.NoError(t, err)

	_, err = engine.Exec(ctx,
		`INSERT INTO ob (type, payload, partition_key) VALUES ('t', '{}', 'order-1')`)
	require.NoError(t, err)

	var n int
	require.NoError(t, engine.QueryRow(ctx,
		`SELECT count(*) FROM ob WHERE attempts = 0 AND dead_at IS NULL`).Scan(&n))
	assert.Equal(t, 1, n)
}

// TestOutbox_WritesPartitionKey proves a mapper's partition key reaches the row.
func TestOutbox_WritesPartitionKey(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	_, err = engine.Exec(ctx, `CREATE TABLE ev_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, drel.OutboxSchema("outbox", "sqlite"))
	require.NoError(t, err)

	engine.UseOutbox("outbox", drel.WithOutboxMapper(func(ev any) (drel.OutboxMessage, bool) {
		c := ev.(itemCreated)
		return drel.OutboxMessage{Type: "item.created", Payload: c, PartitionKey: "agg-" + c.Name}, true
	}))

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		drel.NewTxRepository(drel.MustFromContext(ctx), evItemMeta).
			Add(&evItem{Name: "x", events: []any{itemCreated{Name: "x"}}})
		return nil
	}))

	var key string
	require.NoError(t, engine.QueryRow(ctx, `SELECT partition_key FROM outbox`).Scan(&key))
	assert.Equal(t, "agg-x", key)
}

// TestOutbox_NullPartitionKeyWhenUnset proves an unset key stays NULL rather
// than becoming an empty string, so the relay can treat it as "no order".
func TestOutbox_NullPartitionKeyWhenUnset(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	_, err = engine.Exec(ctx, `CREATE TABLE ev_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, drel.OutboxSchema("outbox", "sqlite"))
	require.NoError(t, err)
	engine.UseOutbox("outbox")

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		drel.NewTxRepository(drel.MustFromContext(ctx), evItemMeta).
			Add(&evItem{Name: "x", events: []any{itemCreated{Name: "x"}}})
		return nil
	}))

	var n int
	require.NoError(t, engine.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE partition_key IS NULL`).Scan(&n))
	assert.Equal(t, 1, n)
}
