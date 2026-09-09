//go:build integration

package migrate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver/pgxdriver"
	"github.com/alternayte/drel/internal/migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupMigrateDB(t *testing.T) *pgxdriver.PgxDriver {
	t.Helper()
	drv, _ := setupMigrateDBWithDSN(t)
	return drv
}

// setupMigrateDBWithDSN also returns the connection string, so a test can open
// a second pool and model two processes rather than two goroutines.
func setupMigrateDBWithDSN(t *testing.T) (*pgxdriver.PgxDriver, string) {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("dreltest"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	drv, err := pgxdriver.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(func() { drv.Close() })

	return drv, connStr
}

func writeFiles(t *testing.T, dir, version, name, up, down string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, version+"_"+name+".up.sql"), []byte(up), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, version+"_"+name+".down.sql"), []byte(down), 0644))
}

func TestIntegration_Migrate_Up(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);",
		"DROP TABLE users;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	count, err := runner.Up(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	row := drv.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'users'")
	var c int
	require.NoError(t, row.Scan(&c))
	assert.Equal(t, 1, c)
}

func TestIntegration_Migrate_Up_Idempotent(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY);", "DROP TABLE users;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	c1, _ := runner.Up(ctx)
	c2, _ := runner.Up(ctx)
	assert.Equal(t, 1, c1)
	assert.Equal(t, 0, c2)
}

func TestIntegration_Migrate_Up_Multiple(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);", "DROP TABLE users;")
	writeFiles(t, dir, "20260510130000", "add_email",
		"ALTER TABLE users ADD COLUMN email TEXT;", "ALTER TABLE users DROP COLUMN email;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	count, err := runner.Up(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestIntegration_Migrate_Down(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY);", "DROP TABLE users;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	_, _ = runner.Up(ctx)

	err := runner.Down(ctx)
	require.NoError(t, err)

	row := drv.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'users'")
	var c int
	require.NoError(t, row.Scan(&c))
	assert.Equal(t, 0, c)
}

func TestIntegration_Migrate_Down_OnlyLast(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY);", "DROP TABLE users;")
	writeFiles(t, dir, "20260510130000", "create_posts",
		"CREATE TABLE posts (id SERIAL PRIMARY KEY);", "DROP TABLE posts;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	_, _ = runner.Up(ctx)
	_ = runner.Down(ctx)

	row := drv.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'users'")
	var usersCount int
	require.NoError(t, row.Scan(&usersCount))
	assert.Equal(t, 1, usersCount)

	row = drv.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'posts'")
	var postsCount int
	require.NoError(t, row.Scan(&postsCount))
	assert.Equal(t, 0, postsCount)
}

func TestIntegration_Migrate_Status(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY);", "DROP TABLE users;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	_, _ = runner.Up(ctx)

	writeFiles(t, dir, "20260510130000", "create_posts",
		"CREATE TABLE posts (id SERIAL PRIMARY KEY);", "DROP TABLE posts;")

	statuses, err := runner.Status(ctx)
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.True(t, statuses[0].Applied)
	assert.False(t, statuses[1].Applied)
}

func TestIntegration_Migrate_Lint_DetectsTampering(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY);", "DROP TABLE users;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	_, _ = runner.Up(ctx)

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "20260510120000_create_users.up.sql"),
		[]byte("CREATE TABLE users (id SERIAL PRIMARY KEY, hacked BOOLEAN);"), 0644))

	issues, err := runner.Lint(ctx)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, "20260510120000", issues[0].Version)
}

func TestIntegration_Migrate_Lint_Clean(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY);", "DROP TABLE users;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	_, _ = runner.Up(ctx)

	issues, err := runner.Lint(ctx)
	require.NoError(t, err)
	assert.Empty(t, issues)
}

func TestIntegration_Migrate_ConcurrentUp_Serialized(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()

	connStr := os.Getenv("DREL_TEST_DSN")
	_ = connStr // documented below; we reuse drv plus a second driver

	dir := t.TempDir()
	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);",
		"DROP TABLE users;")

	// Two runners sharing the same primary driver pool simulate two booting
	// replicas. The advisory lock must serialize them so exactly one applies.
	r1 := migrate.NewRunner(drv, dir, "postgres")
	r2 := migrate.NewRunner(drv, dir, "postgres")

	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 2)
	run := func(r *migrate.Runner) {
		n, err := r.Up(ctx)
		ch <- res{n, err}
	}
	go run(r1)
	go run(r2)

	a, b := <-ch, <-ch
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	// Exactly one runner applied the migration; the other saw it already applied.
	assert.Equal(t, 1, a.n+b.n, "migration must be applied exactly once")

	// The table exists exactly once.
	row := drv.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'users'")
	var c int
	require.NoError(t, row.Scan(&c))
	assert.Equal(t, 1, c)
}

func TestIntegration_FirstMigration_RoundTrip_PivotAndEnum(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	// Describe the desired schema using the same Schema struct that the CLI
	// would produce, then generate the UP/DOWN via DiffSchemas — the same
	// code path the CLI uses. This ensures the test exercises the real
	// generated SQL, not a hand-crafted stand-in that could diverge from the
	// actual code path and silently mask ordering bugs.
	desired := codegen.Schema{
		Enums: []codegen.EnumDef{{Name: "status", Values: []string{"active", "archived"}}},
		Tables: []codegen.Table{
			{
				Name: "users",
				Columns: []codegen.Column{
					{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
					{Name: "state", Type: `"status"`, NotNull: true},
				},
			},
			{
				Name: "roles",
				Columns: []codegen.Column{
					{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				},
			},
			{
				Name: "users_roles",
				Columns: []codegen.Column{
					{Name: "user_id", Type: "bigint", NotNull: true, Ref: "users"},
					{Name: "role_id", Type: "bigint", NotNull: true, Ref: "roles"},
				},
				PrimaryKey: []string{"user_id", "role_id"},
			},
		},
	}

	// The CLI generates a first migration via DiffSchemas(empty, desired):
	//   up   = CREATE TYPE / CREATE TABLE ...  (apply the schema)
	//   down = DROP TABLE / DROP TYPE ...      (undo — written to .down.sql)
	// We use the same call here so the test exercises the real generated SQL.
	up, down, err := codegen.DiffSchemas(codegen.Schema{}, desired, "postgres")
	require.NoError(t, err)
	require.NotEmpty(t, up, "DiffSchemas must produce UP SQL for non-empty desired schema")
	require.NotEmpty(t, down, "DiffSchemas must produce DOWN SQL for non-empty desired schema")

	writeFiles(t, dir, "20260614120000", "init", up, down)

	runner := migrate.NewRunner(drv, dir, "postgres")

	// up → down → up must succeed. The generated down SQL must drop the pivot
	// before its parent tables (FK ordering); Postgres rejects the DROP otherwise.
	_, err = runner.Up(ctx)
	require.NoError(t, err)
	require.NoError(t, runner.Down(ctx))

	// The pivot table and enum type are gone after down.
	row := drv.QueryRow(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'users_roles'")
	var tcount int
	require.NoError(t, row.Scan(&tcount))
	assert.Equal(t, 0, tcount, "pivot table must be dropped on rollback")

	row = drv.QueryRow(ctx, "SELECT COUNT(*) FROM pg_type WHERE typname = 'status'")
	var ecount int
	require.NoError(t, row.Scan(&ecount))
	assert.Equal(t, 0, ecount, "enum type must be dropped on rollback")

	// Re-apply must not fail with "type already exists".
	_, err = runner.Up(ctx)
	require.NoError(t, err)
}

func TestIntegration_Migrate_Roundtrip(t *testing.T) {
	drv := setupMigrateDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	writeFiles(t, dir, "20260510120000", "create_users",
		"CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);", "DROP TABLE users;")

	runner := migrate.NewRunner(drv, dir, "postgres")
	_, _ = runner.Up(ctx)

	_, _ = drv.Exec(ctx, "INSERT INTO users (name) VALUES ('Alice')")

	_ = runner.Down(ctx)
	_, _ = runner.Up(ctx)

	row := drv.QueryRow(ctx, "SELECT COUNT(*) FROM users")
	var c int
	require.NoError(t, row.Scan(&c))
	assert.Equal(t, 0, c)
}

// TestIntegration_Migrate_ConcurrentUp_ManyReplicas models a deployment where
// several replicas boot at one time and every one of them runs Up. Each replica
// holds its own connection pool, as a separate process does.
//
// CREATE TABLE IF NOT EXISTS is not race-safe on Postgres: two sessions can both
// pass the existence check, and one then fails on the catalog index with
// "duplicate key value violates unique constraint pg_type_typname_nsp_index".
// The tracking table must therefore be created under the migration lock.
//
// Exactly one replica may apply the migration, and no replica may fail.
func TestIntegration_Migrate_ConcurrentUp_ManyReplicas(t *testing.T) {
	drv, dsn := setupMigrateDBWithDSN(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFiles(t, dir, "20260510120000", "create_widgets",
		"CREATE TABLE widgets (id SERIAL PRIMARY KEY, name TEXT NOT NULL);",
		"DROP TABLE widgets;")

	const replicas = 8
	runners := make([]*migrate.Runner, replicas)
	for i := range runners {
		d, err := pgxdriver.New(ctx, dsn)
		require.NoError(t, err)
		t.Cleanup(func() { d.Close() })
		runners[i] = migrate.NewRunner(d, dir, "postgres")
	}

	type res struct {
		n   int
		err error
	}
	ch := make(chan res, replicas)
	start := make(chan struct{})
	for _, r := range runners {
		go func(r *migrate.Runner) {
			<-start
			n, err := r.Up(ctx)
			ch <- res{n, err}
		}(r)
	}
	close(start)

	applied := 0
	for i := 0; i < replicas; i++ {
		got := <-ch
		require.NoError(t, got.err, "a booting replica must not fail")
		applied += got.n
	}
	assert.Equal(t, 1, applied, "exactly one replica may apply the migration")

	var count int
	require.NoError(t, drv.QueryRow(ctx, "SELECT count(*) FROM drel_migrations").Scan(&count))
	assert.Equal(t, 1, count)
}
