//go:build integration

package codegen_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
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

// TestDiffSchemas_TypeChangeWithData applies a text -> enum and a text ->
// timestamptz change against real Postgres, on a table that already holds rows.
// PostgreSQL refuses both casts without a USING clause, so this is the case the
// generated migration got wrong: it had to be written by hand.
func TestDiffSchemas_TypeChangeWithData(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	v1 := []codegen.ModelInfo{{
		Name: "QuestEvent", TableName: "quest_events", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "ToState", GoType: "string", ColumnName: "to_state", IsExported: true},
			{Name: "StartedAt", GoType: "string", ColumnName: "started_at", IsExported: true},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(v1, "postgres"))
	require.NoError(t, err)

	_, err = drv.Exec(ctx, "INSERT INTO quest_events (to_state, started_at) VALUES ('draft', '2026-01-02T03:04:05Z')")
	require.NoError(t, err)

	v2 := []codegen.ModelInfo{{
		Name: "QuestEvent", TableName: "quest_events", PKType: "int",
		Fields: []codegen.FieldInfo{
			{
				Name: "ToState", GoType: "QuestState", LocalGoType: "QuestState",
				ColumnName: "to_state", IsExported: true,
				IsEnum: true, EnumValues: []string{"draft", "live"}, EnumBaseType: "string",
			},
			{Name: "StartedAt", GoType: "time.Time", LocalGoType: "time.Time", ColumnName: "started_at", IsExported: true},
		},
	}}

	up, down, err := codegen.DiffSchemas(codegen.BuildSchema(v1, "postgres"), codegen.BuildSchema(v2, "postgres"), "postgres")
	require.NoError(t, err)
	require.NotEmpty(t, up)

	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "up migration should apply on Postgres:\n%s", up)

	// The row survived both casts.
	var state string
	var started time.Time
	require.NoError(t, drv.QueryRow(ctx, "SELECT to_state::text, started_at FROM quest_events").Scan(&state, &started))
	assert.Equal(t, "draft", state)
	assert.Equal(t, 2026, started.Year())

	// The enum column rejects a value outside the set.
	_, err = drv.Exec(ctx, "INSERT INTO quest_events (to_state, started_at) VALUES ('bogus', now())")
	assert.Error(t, err, "enum column should reject a value outside the set")

	_, err = drv.Exec(ctx, down)
	require.NoError(t, err, "down migration should apply on Postgres:\n%s", down)
}

// TestDiffSchemas_TypeChangeWithEmptyStrings casts a text column that holds ”
// to a timestamp and to an enum. A plain cast fails on those rows with
// "invalid input syntax", so a whole migration rolls back on real data.
func TestDiffSchemas_TypeChangeWithEmptyStrings(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	v1 := []codegen.ModelInfo{{
		Name: "QuestEvent", TableName: "quest_events", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "ToState", GoType: "*string", ColumnName: "to_state", IsExported: true},
			{Name: "StartedAt", GoType: "*string", ColumnName: "started_at", IsExported: true},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(v1, "postgres"))
	require.NoError(t, err)

	_, err = drv.Exec(ctx, "INSERT INTO quest_events (to_state, started_at) VALUES ('draft', '2026-01-02T03:04:05Z'), ('', '')")
	require.NoError(t, err)

	v2 := []codegen.ModelInfo{{
		Name: "QuestEvent", TableName: "quest_events", PKType: "int",
		Fields: []codegen.FieldInfo{
			{
				Name: "ToState", GoType: "*QuestState", LocalGoType: "QuestState", IsPointer: true,
				ColumnName: "to_state", IsExported: true,
				IsEnum: true, EnumValues: []string{"draft", "live"}, EnumBaseType: "string",
			},
			{Name: "StartedAt", GoType: "*time.Time", LocalGoType: "time.Time", IsPointer: true, ColumnName: "started_at", IsExported: true},
		},
	}}

	up, _, err := codegen.DiffSchemas(codegen.BuildSchema(v1, "postgres"), codegen.BuildSchema(v2, "postgres"), "postgres")
	require.NoError(t, err)

	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "up migration should apply to a table holding empty strings:\n%s", up)

	// The empty strings became NULL; the real values survived.
	var nulls int
	require.NoError(t, drv.QueryRow(ctx, "SELECT count(*) FROM quest_events WHERE to_state IS NULL AND started_at IS NULL").Scan(&nulls))
	assert.Equal(t, 1, nulls)

	var state string
	require.NoError(t, drv.QueryRow(ctx, "SELECT to_state::text FROM quest_events WHERE to_state IS NOT NULL").Scan(&state))
	assert.Equal(t, "draft", state)
}

// TestDeclaredObjects_ApplyToRealPostgres declares a foreign key to a table
// drel does not model, a partial unique index, and a composite index covering a
// trait column, then applies the result. All three were hand-written before.
func TestDeclaredObjects_ApplyToRealPostgres(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	// A table another system owns. drel has no model for it.
	_, err := drv.Exec(ctx, `CREATE TABLE auth_users (id text PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `INSERT INTO auth_users (id) VALUES ('u1')`)
	require.NoError(t, err)

	quests := []codegen.ModelInfo{{
		Name: "Quest", TableName: "quests", PKType: "int",
		Fields: []codegen.FieldInfo{
			{
				Name: "UserID", GoType: "string", ColumnName: "user_id", IsExported: true,
				References: "auth_users", RefColumn: "id", OnDelete: "CASCADE",
				IndexNames: []codegen.IndexMembership{
					{Name: "uq_quest_active", Unique: true, Where: "state = 'active'"},
				},
			},
			{Name: "State", GoType: "string", ColumnName: "state", IsExported: true},
		},
		Indexes: []codegen.IndexDecl{
			{Name: "idx_quest_user_created", Columns: []string{"user_id", "created_at"}},
		},
	}}

	_, err = drv.Exec(ctx, codegen.GenerateSchema(quests, "postgres"))
	require.NoError(t, err, "schema with a declared FK and indexes should apply")

	// The foreign key is enforced.
	_, err = drv.Exec(ctx, `INSERT INTO quests (user_id, state) VALUES ('nobody', 'active')`)
	assert.Error(t, err, "foreign key should reject an unknown user")

	_, err = drv.Exec(ctx, `INSERT INTO quests (user_id, state) VALUES ('u1', 'active')`)
	require.NoError(t, err)

	// The partial unique index allows a second non-active row, and only one
	// active row per user.
	_, err = drv.Exec(ctx, `INSERT INTO quests (user_id, state) VALUES ('u1', 'done')`)
	require.NoError(t, err, "a non-active row is outside the partial index")
	_, err = drv.Exec(ctx, `INSERT INTO quests (user_id, state) VALUES ('u1', 'active')`)
	assert.Error(t, err, "partial unique index should reject a second active quest")

	// ON DELETE CASCADE reaches the rows.
	_, err = drv.Exec(ctx, `DELETE FROM auth_users WHERE id = 'u1'`)
	require.NoError(t, err)
	var left int
	require.NoError(t, drv.QueryRow(ctx, "SELECT count(*) FROM quests").Scan(&left))
	assert.Equal(t, 0, left, "ON DELETE CASCADE should remove the quests")

	// The composite index covering the trait column exists.
	var idxCount int
	require.NoError(t, drv.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'quests' AND indexname = 'idx_quest_user_created'`).Scan(&idxCount))
	assert.Equal(t, 1, idxCount)
}

// TestDeclaredForeignKey_AddedToExistingTable declares a foreign key on a table
// that already exists, which is how a project adopts references= for a key it
// wrote by hand.
func TestDeclaredForeignKey_AddedToExistingTable(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	_, err := drv.Exec(ctx, `CREATE TABLE auth_users (id text PRIMARY KEY)`)
	require.NoError(t, err)

	v1 := []codegen.ModelInfo{{
		Name: "Quest", TableName: "quests", PKType: "int",
		Fields: []codegen.FieldInfo{{Name: "UserID", GoType: "string", ColumnName: "user_id", IsExported: true}},
	}}
	_, err = drv.Exec(ctx, codegen.GenerateSchema(v1, "postgres"))
	require.NoError(t, err)

	v2 := []codegen.ModelInfo{{
		Name: "Quest", TableName: "quests", PKType: "int",
		Fields: []codegen.FieldInfo{{
			Name: "UserID", GoType: "string", ColumnName: "user_id", IsExported: true,
			References: "auth_users", RefColumn: "id", OnDelete: "CASCADE",
		}},
	}}

	up, down, err := codegen.DiffSchemas(codegen.BuildSchema(v1, "postgres"), codegen.BuildSchema(v2, "postgres"), "postgres")
	require.NoError(t, err)
	require.NotEmpty(t, up, "declaring a foreign key must emit SQL")

	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "up migration should apply:\n%s", up)

	_, err = drv.Exec(ctx, `INSERT INTO quests (user_id) VALUES ('nobody')`)
	assert.Error(t, err, "the added foreign key should be enforced")

	// Applying the up a second time is safe: the drop is guarded.
	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "the migration should be repeatable:\n%s", up)

	_, err = drv.Exec(ctx, down)
	require.NoError(t, err, "down migration should apply:\n%s", down)
}

// TestDate_RoundTripsARealDateColumn proves the column type drel.Date
// generates, and that a value written through it reads back. A text column
// holding "2006-01-02" cannot order or range as a date, which is why the type
// exists.
func TestDate_RoundTripsARealDateColumn(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	models := []codegen.ModelInfo{{
		Name: "Entry", TableName: "entries", PKType: "int",
		Fields: []codegen.FieldInfo{{
			Name: "UserDay", GoType: "Date", LocalGoType: "Date",
			TypePkgPath: "github.com/alternayte/drel",
			ColumnName:  "user_day", IsExported: true,
			IsVO: true, IsDate: true, IsComparable: true, HasEqual: true,
		}},
	}}

	ddl := codegen.GenerateSchema(models, "postgres")
	assert.Contains(t, ddl, `"user_day" date`)
	_, err := drv.Exec(ctx, ddl)
	require.NoError(t, err)

	day := drel.NewDate(2026, time.March, 4)
	_, err = drv.Exec(ctx, `INSERT INTO entries (user_day) VALUES ($1)`, day)
	require.NoError(t, err, "a Date must write to a date column")

	var got drel.Date
	require.NoError(t, drv.QueryRow(ctx, `SELECT user_day FROM entries`).Scan(&got))
	assert.Equal(t, day, got)

	// The column is a real date: the server orders and ranges it.
	_, err = drv.Exec(ctx, `INSERT INTO entries (user_day) VALUES ($1)`, drel.NewDate(2026, time.January, 2))
	require.NoError(t, err)
	var first drel.Date
	require.NoError(t, drv.QueryRow(ctx, `SELECT min(user_day) FROM entries`).Scan(&first))
	assert.Equal(t, drel.NewDate(2026, time.January, 2), first)

	var inRange int
	require.NoError(t, drv.QueryRow(ctx,
		`SELECT count(*) FROM entries WHERE user_day >= $1 AND user_day < $2`,
		drel.NewDate(2026, time.February, 1), drel.NewDate(2026, time.April, 1)).Scan(&inRange))
	assert.Equal(t, 1, inRange)

	// The server rejects a day that does not exist, which a text column accepts.
	_, err = drv.Exec(ctx, `INSERT INTO entries (user_day) VALUES ('2026-02-30')`)
	assert.Error(t, err, "a date column should reject 2026-02-30")
}
