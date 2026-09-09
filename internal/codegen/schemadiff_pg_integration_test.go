//go:build integration

package codegen_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver/pgxdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func pgDriver(t *testing.T) *pgxdriver.PgxDriver {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("dreltest"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(30*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	conn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	drv, err := pgxdriver.New(ctx, conn)
	require.NoError(t, err)
	t.Cleanup(drv.Close)
	return drv
}

// TestDiffSchemas_AppliesToRealPostgres validates that generated CREATE and the
// Postgres-specific ALTER COLUMN TYPE / SET NOT NULL / CREATE TYPE / CREATE INDEX
// statements apply cleanly against a real Postgres instance.
func TestDiffSchemas_AppliesToRealPostgres(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	v1 := []codegen.ModelInfo{{
		Name: "User", TableName: "users", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true},
			{Name: "Age", GoType: "int", ColumnName: "age", IsExported: true},
			{Name: "Bio", GoType: "*string", ColumnName: "bio", IsExported: true},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(v1, "postgres"))
	require.NoError(t, err)

	// Evolve: widen age int->int64 (ALTER TYPE), make bio NOT NULL (SET NOT NULL),
	// add a unique index on name, and add a new table.
	v2 := []codegen.ModelInfo{
		{
			Name: "User", TableName: "users", PKType: "int",
			Fields: []codegen.FieldInfo{
				{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true, Unique: true},
				{Name: "Age", GoType: "int64", ColumnName: "age", IsExported: true},
				{Name: "Bio", GoType: "string", ColumnName: "bio", IsExported: true},
			},
		},
		{
			Name: "Post", TableName: "posts", PKType: "int",
			Fields: []codegen.FieldInfo{{Name: "Title", GoType: "string", ColumnName: "title", IsExported: true}},
		},
	}

	up, down, err := codegen.DiffSchemas(codegen.BuildSchema(v1, "postgres"), codegen.BuildSchema(v2, "postgres"), "postgres")
	require.NoError(t, err)
	require.NotEmpty(t, up)

	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "up migration should apply on Postgres:\n%s", up)

	// bio is now NOT NULL; name is unique.
	_, err = drv.Exec(ctx, "INSERT INTO users (name, age, bio) VALUES ('a', 1, 'x')")
	require.NoError(t, err)
	_, err = drv.Exec(ctx, "INSERT INTO users (name, age, bio) VALUES ('a', 2, 'y')")
	assert.Error(t, err, "unique index on name should reject duplicate")
	_, err = drv.Exec(ctx, "INSERT INTO posts (title) VALUES ('t')")
	require.NoError(t, err)

	_, err = drv.Exec(ctx, down)
	require.NoError(t, err, "down migration should apply on Postgres:\n%s", down)
}
