//go:build integration

package drel_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type lsItem struct {
	ID        int
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

var lsItemMeta = drel.ModelMeta[lsItem]{
	Table: "ls_items", Columns: []string{"id", "title", "created_at", "updated_at"}, PKColumns: []string{"id"},
	Scan: func(r drel.Row) (*lsItem, error) {
		it := &lsItem{}
		return it, r.Scan(&it.ID, &it.Title, &it.CreatedAt, &it.UpdatedAt)
	},
	PKValue:       func(it *lsItem) any { return it.ID },
	InsertColumns: func(it *lsItem) ([]string, []any) { return []string{"title"}, []any{it.Title} },
	ScanReturning: func(it *lsItem, row drel.Row) error {
		return row.Scan(&it.ID, &it.CreatedAt, &it.UpdatedAt)
	},
}

// setupLibSQLEngine starts a sqld container and returns a connected Engine plus its
// DSN. The container and engine are cleaned up when the test ends.
func setupLibSQLEngine(t *testing.T) *drel.Engine {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/tursodatabase/libsql-server:latest",
			ExposedPorts: []string{"8080/tcp"},
			Env:          map[string]string{"SQLD_NODE": "primary"},
			WaitingFor:   wait.ForListeningPort("8080/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "8080")
	require.NoError(t, err)
	dsn := fmt.Sprintf("http://%s:%s", host, port.Port())

	engine, err := drel.NewEngine(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })

	_, err = engine.Exec(ctx, `CREATE TABLE ls_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	return engine
}

// TestLibSQLIntegration_Insert_UsesReturning asserts that inserts against a real
// libSQL server use INSERT … RETURNING (not the old last_insert_rowid readback).
func TestLibSQLIntegration_Insert_UsesReturning(t *testing.T) {
	var captured []string
	engine := setupLibSQLEngine(t)
	engine.OnQuery(func(_ context.Context, ev drel.QueryEvent) {
		captured = append(captured, ev.SQL)
	})

	ctx := context.Background()
	item := &lsItem{Title: "ret-check"}
	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, lsItemMeta).Add(item)
		return tx.SaveChanges(ctx)
	})
	require.NoError(t, err)
	assert.NotZero(t, item.ID)
	assert.False(t, item.CreatedAt.IsZero())

	var sawReturning, sawReadback bool
	for _, sql := range captured {
		if strings.Contains(sql, "INSERT INTO") && strings.Contains(sql, "RETURNING") {
			sawReturning = true
		}
		if strings.Contains(sql, "last_insert_rowid()") {
			sawReadback = true
		}
	}
	assert.True(t, sawReturning, "libsql insert must use RETURNING; captured=%v", captured)
	assert.False(t, sawReadback, "libsql insert must not use last_insert_rowid(); captured=%v", captured)
}

// TestIntegration_LibSQL_RoundTrip verifies drel against a real libSQL/Turso
// server (sqld) over HTTP — the transport Turso's libsql:// DSNs use. Requires
// the `libsql` build tag (the client is compiled in only then).
func TestIntegration_LibSQL_RoundTrip(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/tursodatabase/libsql-server:latest",
			ExposedPorts: []string{"8080/tcp"},
			Env:          map[string]string{"SQLD_NODE": "primary"},
			WaitingFor:   wait.ForListeningPort("8080/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "8080")
	require.NoError(t, err)
	dsn := fmt.Sprintf("http://%s:%s", host, port.Port())

	engine, err := drel.NewEngine(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })

	_, err = engine.Exec(ctx, `CREATE TABLE ls_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	// Insert in a transaction (Begin/Commit + non-RETURNING readback over libSQL).
	require.NoError(t, engine.Transaction(ctx, func(tx *drel.Tx) error {
		drel.NewTxRepository(tx, lsItemMeta).Add(&lsItem{Title: "hello-turso"})
		return tx.SaveChanges(ctx)
	}))

	repo := drel.NewRepository(engine, lsItemMeta)
	got, err := repo.Where(drel.NewStringCol("title").Eq("hello-turso")).First(ctx)
	require.NoError(t, err)
	assert.NotZero(t, got.ID)

	n, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}
