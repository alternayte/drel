//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver"
	"github.com/alternayte/drel/internal/driver/sqlitedriver"
	"github.com/alternayte/drel/internal/migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSQLiteDriverForTest opens the same in-memory driver the other SQLite
// runner tests use. See sqlite_runner_test.go.
func newSQLiteDriverForTest(t *testing.T) *sqlitedriver.SQLiteDriver {
	t.Helper()
	drv, err := sqlitedriver.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(drv.Close)
	return drv
}

// assertScalarString runs a one-column query and compares the text result.
func assertScalarString(t *testing.T, drv driver.Driver, query, want string) {
	t.Helper()
	var got string
	require.NoError(t, drv.QueryRow(context.Background(), query).Scan(&got), query)
	assert.Equal(t, want, got, query)
}

// assertScalarInt runs a one-column query and compares the integer result.
func assertScalarInt(t *testing.T, drv driver.Driver, query string, want int64) {
	t.Helper()
	var got int64
	require.NoError(t, drv.QueryRow(context.Background(), query).Scan(&got), query)
	assert.Equal(t, want, got, query)
}

// notesTable is the shape under rebuild. It deliberately carries a primary
// key, a plain index, a UNIQUE index and a composite index, because the spec
// requires the rebuild to reproduce every one of them.
func notesTable(bodyNotNull bool) codegen.Table {
	return codegen.Table{
		Name: "notes",
		Columns: []codegen.Column{
			{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			{Name: "body", Type: "TEXT", NotNull: bodyNotNull},
			{Name: "tag", Type: "TEXT", NotNull: true},
			{Name: "slug", Type: "TEXT", NotNull: true},
			{Name: "author", Type: "TEXT", NotNull: true},
			{Name: "year", Type: "INTEGER", NotNull: true},
		},
		Indexes: []codegen.Index{
			{Name: "idx_notes_tag", Columns: []string{"tag"}},
			{Name: "uq_notes_slug", Columns: []string{"slug"}, Unique: true},
			{Name: "idx_notes_author_year", Columns: []string{"author", "year"}},
		},
	}
}

// scalarString returns the single text value a query selects.
func scalarString(t *testing.T, drv driver.Driver, query string) string {
	t.Helper()
	var got string
	require.NoError(t, drv.QueryRow(context.Background(), query).Scan(&got), query)
	return got
}

func TestSQLiteRebuild_AppliesUpAndDownAndKeepsTheData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	oldSchema := codegen.Schema{Tables: []codegen.Table{notesTable(false)}}
	newSchema := codegen.Schema{Tables: []codegen.Table{notesTable(true)}}

	drv := newSQLiteDriverForTest(t)

	_, err := drv.Exec(ctx, `CREATE TABLE notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		body TEXT,
		tag TEXT NOT NULL,
		slug TEXT NOT NULL,
		author TEXT NOT NULL,
		year INTEGER NOT NULL
	)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `CREATE INDEX idx_notes_tag ON notes (tag)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `CREATE UNIQUE INDEX uq_notes_slug ON notes (slug)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `CREATE INDEX idx_notes_author_year ON notes (author, year)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx,
		`INSERT INTO notes (body, tag, slug, author, year) VALUES ('hello', 'a', 's1', 'ann', 2020)`)
	require.NoError(t, err)

	// assertEveryIndexSurvives proves each index is present AND still enforces.
	// Presence in sqlite_master alone would not show that UNIQUE survived.
	assertEveryIndexSurvives := func(stage string) {
		t.Helper()

		assertScalarInt(t, drv,
			`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_notes_tag'`, 1)

		// The UNIQUE index must survive as unique, not as a plain index.
		assertScalarInt(t, drv,
			`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='uq_notes_slug'`, 1)
		assertScalarInt(t, drv,
			`SELECT "unique" FROM pragma_index_list('notes') WHERE name='uq_notes_slug'`, 1)
		assert.Equal(t, "slug", scalarString(t, drv,
			`SELECT group_concat(name) FROM pragma_index_info('uq_notes_slug')`),
			stage+": the UNIQUE index must still cover slug")

		// The composite index must keep both columns, in order. This reads
		// the indexed columns from the pragma rather than the CREATE INDEX
		// text: the index name itself contains both column names, so a
		// substring test on the SQL text would pass even for a one-column
		// index.
		assert.Equal(t, "author,year", scalarString(t, drv,
			`SELECT group_concat(name) FROM pragma_index_info('idx_notes_author_year')`),
			stage+": the composite index must keep both columns, in order")

		// Behaviour, not presence: a duplicate slug must be rejected.
		_, dupErr := drv.Exec(ctx,
			`INSERT INTO notes (body, tag, slug, author, year) VALUES ('dup', 'a', 's1', 'ann', 2020)`)
		assert.Error(t, dupErr, stage+": the UNIQUE index must still reject a duplicate slug")

		// The primary key must survive and still reject a duplicate key.
		_, pkErr := drv.Exec(ctx,
			`INSERT INTO notes (id, body, tag, slug, author, year) VALUES (1, 'dup', 'a', 's2', 'ann', 2020)`)
		assert.Error(t, pkErr, stage+": the primary key must still reject a duplicate id")
	}

	up, down, err := codegen.DiffSchemas(oldSchema, newSchema, "sqlite")
	require.NoError(t, err)
	require.Contains(t, up, "__drel_new")

	_, err = migrate.WriteMigration(dir, "tighten_body", up, down)
	require.NoError(t, err)

	runner := migrate.NewRunner(drv, dir, "sqlite")

	n, err := runner.Up(ctx)
	require.NoError(t, err, "the rebuild must apply cleanly inside the runner transaction")
	assert.Equal(t, 1, n)

	assertScalarString(t, drv, `SELECT body FROM notes WHERE tag = 'a'`, "hello")
	assertScalarInt(t, drv, `SELECT count(*) FROM sqlite_master WHERE name LIKE '%__drel_new'`, 0)
	assertEveryIndexSurvives("after up")

	_, err = drv.Exec(ctx,
		`INSERT INTO notes (body, tag, slug, author, year) VALUES (NULL, 'b', 's3', 'bob', 2021)`)
	assert.Error(t, err, "the rebuild must have applied the NOT NULL constraint")

	require.NoError(t, runner.Down(ctx))
	assertScalarString(t, drv, `SELECT body FROM notes WHERE tag = 'a'`, "hello")
	assertEveryIndexSurvives("after down")

	// After the down migration, body accepts NULL again.
	_, err = drv.Exec(ctx,
		`INSERT INTO notes (body, tag, slug, author, year) VALUES (NULL, 'b', 's4', 'bob', 2021)`)
	assert.NoError(t, err)
}

func TestSQLiteRebuild_ANarrowingChangeThatFailsRollsBackCleanly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	drv := newSQLiteDriverForTest(t)

	_, err := drv.Exec(ctx, `CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `INSERT INTO notes (body) VALUES (NULL)`)
	require.NoError(t, err)

	cols := func(notNull bool) []codegen.Column {
		return []codegen.Column{
			{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			{Name: "body", Type: "TEXT", NotNull: notNull},
		}
	}
	oldSchema := codegen.Schema{Tables: []codegen.Table{{Name: "notes", Columns: cols(false)}}}
	newSchema := codegen.Schema{Tables: []codegen.Table{{Name: "notes", Columns: cols(true)}}}

	up, down, err := codegen.DiffSchemas(oldSchema, newSchema, "sqlite")
	require.NoError(t, err)
	_, err = migrate.WriteMigration(dir, "tighten_body", up, down)
	require.NoError(t, err)

	_, err = migrate.NewRunner(drv, dir, "sqlite").Up(ctx)
	require.Error(t, err, "copying a NULL into a NOT NULL column must fail")

	assertScalarInt(t, drv, `SELECT count(*) FROM notes`, 1)
	assertScalarInt(t, drv, `SELECT count(*) FROM sqlite_master WHERE name LIKE '%__drel_new'`, 0)
}

func TestColumnRename_SQLite_AppliesUpAndDownAndKeepsTheData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	drv := newSQLiteDriverForTest(t)

	_, err := drv.Exec(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `INSERT INTO users (email) VALUES ('a@example.com')`)
	require.NoError(t, err)

	oldSchema := codegen.Schema{Tables: []codegen.Table{{Name: "users", Columns: []codegen.Column{
		{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
		{Name: "email", Type: "TEXT", NotNull: true},
	}}}}
	newSchema := codegen.Schema{Tables: []codegen.Table{{Name: "users", Columns: []codegen.Column{
		{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
		{Name: "email_address", Type: "TEXT", NotNull: true, RenamedFrom: "email"},
	}}}}

	up, down, err := codegen.DiffSchemas(oldSchema, newSchema, "sqlite")
	require.NoError(t, err)
	require.Contains(t, up, "RENAME COLUMN")
	_, err = migrate.WriteMigration(dir, "rename_email", up, down)
	require.NoError(t, err)

	runner := migrate.NewRunner(drv, dir, "sqlite")
	_, err = runner.Up(ctx)
	require.NoError(t, err)

	assertScalarString(t, drv, `SELECT email_address FROM users WHERE id = 1`, "a@example.com")

	require.NoError(t, runner.Down(ctx))
	assertScalarString(t, drv, `SELECT email FROM users WHERE id = 1`, "a@example.com")
}

func TestColumnRename_Postgres_AppliesUpAndDownAndKeepsTheData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	drv := setupMigrateDB(t)

	_, err := drv.Exec(ctx, `CREATE TABLE users (id SERIAL PRIMARY KEY, email TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `INSERT INTO users (email) VALUES ('a@example.com')`)
	require.NoError(t, err)

	oldSchema := codegen.Schema{Tables: []codegen.Table{{Name: "users", Columns: []codegen.Column{
		{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
		{Name: "email", Type: "TEXT", NotNull: true},
	}}}}
	newSchema := codegen.Schema{Tables: []codegen.Table{{Name: "users", Columns: []codegen.Column{
		{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
		{Name: "email_address", Type: "TEXT", NotNull: true, RenamedFrom: "email"},
	}}}}

	up, down, err := codegen.DiffSchemas(oldSchema, newSchema, "postgres")
	require.NoError(t, err)
	require.Contains(t, up, "RENAME COLUMN")
	_, err = migrate.WriteMigration(dir, "rename_email", up, down)
	require.NoError(t, err)

	runner := migrate.NewRunner(drv, dir, "postgres")
	_, err = runner.Up(ctx)
	require.NoError(t, err)

	assertScalarString(t, drv, `SELECT email_address FROM users WHERE id = 1`, "a@example.com")

	require.NoError(t, runner.Down(ctx))
	assertScalarString(t, drv, `SELECT email FROM users WHERE id = 1`, "a@example.com")
}
