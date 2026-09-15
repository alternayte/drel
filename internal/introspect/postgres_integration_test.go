//go:build integration

package introspect_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver/pgxdriver"
	"github.com/alternayte/drel/internal/introspect"
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

func find(items []codegen.DriftItem, kind, name string) *codegen.DriftItem {
	for i := range items {
		if items[i].Kind == kind && items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

// A hand-written index, foreign key and CHECK are invisible to the migration
// differ. Introspection finds them, which is the whole point of the command.
func TestIntrospect_FindsHandWrittenObjects(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	models := []codegen.ModelInfo{{
		Name: "Quest", TableName: "quests", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "UserID", GoType: "string", ColumnName: "user_id", IsExported: true},
			{Name: "State", GoType: "string", ColumnName: "state", IsExported: true},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(models, "postgres"))
	require.NoError(t, err)

	// Objects a person added by hand, exactly as MakerQuest did.
	_, err = drv.Exec(ctx, `CREATE TABLE auth_users (id text PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `ALTER TABLE quests ADD CONSTRAINT quests_user_fk FOREIGN KEY (user_id) REFERENCES auth_users(id)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `CREATE UNIQUE INDEX one_active_quest ON quests (user_id) WHERE state = 'active'`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `ALTER TABLE quests ADD CONSTRAINT quests_state_values CHECK (state IN ('draft', 'active'))`)
	require.NoError(t, err)

	live, err := introspect.Schema(ctx, drv, "postgres")
	require.NoError(t, err)

	drift := codegen.CompareSchemas(live, codegen.BuildSchema(models, "postgres"))
	require.False(t, drift.Empty())

	idx := find(drift.Unmanaged, "index", "one_active_quest")
	require.NotNil(t, idx, "the hand-written index is not reported: %+v", drift)
	assert.Equal(t, "quests", idx.Table)
	assert.Contains(t, idx.Detail, "unique")
	assert.Contains(t, idx.Detail, "state = 'active'")

	fk := find(drift.Unmanaged, "foreign key", "user_id")
	require.NotNil(t, fk, "the hand-written foreign key is not reported: %+v", drift)
	assert.Contains(t, fk.Detail, "auth_users")

	chk := find(drift.Unmanaged, "check constraint", "quests_state_values")
	require.NotNil(t, chk, "the hand-written CHECK is not reported: %+v", drift)

	tbl := find(drift.Unmanaged, "table", "auth_users")
	require.NotNil(t, tbl, "the table drel does not model is not reported: %+v", drift)

	// Nothing the models declare is missing or different: the database is ahead
	// of the models, not behind them.
	assert.Empty(t, drift.Missing, "%+v", drift.Missing)
	assert.Empty(t, drift.Different, "%+v", drift.Different)
}

// A database that matches its models reports no drift. This is the assertion
// that catches a normalisation bug: a type the server renders its own way must
// not read as a difference on every run.
func TestIntrospect_GeneratedSchemaMatchesItsModels(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	models := []codegen.ModelInfo{{
		Name: "Everything", TableName: "everythings", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true},
			{Name: "Count", GoType: "int", ColumnName: "count", IsExported: true},
			{Name: "Big", GoType: "int64", ColumnName: "big", IsExported: true},
			{Name: "Ratio", GoType: "float64", ColumnName: "ratio", IsExported: true},
			{Name: "Active", GoType: "bool", ColumnName: "active", IsExported: true},
			{Name: "Bio", GoType: "*string", ColumnName: "bio", IsExported: true},
			{Name: "At", GoType: "time.Time", ColumnName: "at", IsExported: true},
			{Name: "Day", GoType: "time.Time", ColumnName: "day", IsExported: true, TypeOverride: "date"},
			{Name: "Slug", GoType: "string", ColumnName: "slug", IsExported: true, Unique: true},
			{Name: "Tag", GoType: "string", ColumnName: "tag", IsExported: true, Indexed: true},
		},
	}}

	_, err := drv.Exec(ctx, codegen.GenerateSchema(models, "postgres"))
	require.NoError(t, err)

	live, err := introspect.Schema(ctx, drv, "postgres")
	require.NoError(t, err)

	drift := codegen.CompareSchemas(live, codegen.BuildSchema(models, "postgres"))
	assert.True(t, drift.Empty(),
		"a freshly generated schema must not read as drift\nunmanaged: %+v\nmissing: %+v\ndifferent: %+v",
		drift.Unmanaged, drift.Missing, drift.Different)
}

// TestAdopt_ClosesTheStaleCheckMigration walks the reported failure end to end:
// a hand-written CHECK, a column whose type becomes an enum, and a migration
// that PostgreSQL refuses because it re-checks the stale constraint.
func TestAdopt_ClosesTheStaleCheckMigration(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	v1 := []codegen.ModelInfo{{
		Name: "QuestEvent", TableName: "quest_events", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "ToState", GoType: "string", ColumnName: "to_state", IsExported: true},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(v1, "postgres"))
	require.NoError(t, err)

	// The constraint a hand-written migration added, under its own name.
	_, err = drv.Exec(ctx, `ALTER TABLE quest_events ADD CONSTRAINT ck_quest_events_to_state CHECK (to_state IN ('draft', 'live'))`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `INSERT INTO quest_events (to_state) VALUES ('draft')`)
	require.NoError(t, err)

	// to_state becomes an enum.
	v2 := []codegen.ModelInfo{{
		Name: "QuestEvent", TableName: "quest_events", PKType: "int",
		Fields: []codegen.FieldInfo{{
			Name: "ToState", GoType: "QuestState", LocalGoType: "QuestState",
			ColumnName: "to_state", IsExported: true,
			IsEnum: true, EnumValues: []string{"draft", "live"}, EnumBaseType: "string",
		}},
	}}

	snapshot := codegen.BuildSchema(v1, "postgres")
	desired := codegen.BuildSchema(v2, "postgres")

	// Without adoption the constraint is invisible and the migration fails the
	// way the report describes.
	blindUp, _, err := codegen.DiffSchemas(snapshot, desired, "postgres")
	require.NoError(t, err)
	_, err = drv.Exec(ctx, blindUp)
	require.Error(t, err, "the stale constraint should break this migration:\n%s", blindUp)

	// Adoption records it, and the differ then drops it ahead of the type change.
	live, err := introspect.Schema(ctx, drv, "postgres")
	require.NoError(t, err)
	adoptedSnapshot, adopted := codegen.AdoptLiveObjects(snapshot, live, desired)
	require.NotEmpty(t, adopted)
	require.NotNil(t, find(adopted, "check constraint", "ck_quest_events_to_state"))

	up, down, err := codegen.DiffSchemas(adoptedSnapshot, desired, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `DROP CONSTRAINT IF EXISTS "ck_quest_events_to_state";`)

	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "the migration should apply once the constraint is known:\n%s", up)

	// The row survived, and the column is now an enum.
	var state string
	require.NoError(t, drv.QueryRow(ctx, `SELECT to_state::text FROM quest_events`).Scan(&state))
	assert.Equal(t, "draft", state)

	// The down reverts the type and restores the constraint.
	_, err = drv.Exec(ctx, down)
	require.NoError(t, err, "the down should apply:\n%s", down)

	_, err = drv.Exec(ctx, `INSERT INTO quest_events (to_state) VALUES ('bogus')`)
	assert.Error(t, err, "the restored CHECK should reject a value outside the set")
}

// PostgreSQL rewrites an index predicate when it stores it: IN (...) comes back
// as = ANY (ARRAY[...]) with each element cast to the column type. Comparing
// the text reports a difference on a database that matches its models, so
// verify exits non-zero on a correct schema.
func TestIntrospect_IndexPredicateIsNotComparedAsText(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	models := []codegen.ModelInfo{{
		Name: "UserQuest", TableName: "user_quests", PKType: "int",
		Fields: []codegen.FieldInfo{
			{
				Name: "UserID", GoType: "string", ColumnName: "user_id", IsExported: true,
				IndexNames: []codegen.IndexMembership{{
					Name:   "uq_user_quests_one_active",
					Unique: true,
					Where:  "state IN ('assigned', 'in_progress')",
				}},
			},
			{
				Name: "State", GoType: "QuestState", LocalGoType: "QuestState",
				ColumnName: "state", IsExported: true,
				IsEnum: true, EnumValues: []string{"assigned", "in_progress", "done"}, EnumBaseType: "string",
			},
		},
	}}

	_, err := drv.Exec(ctx, codegen.GenerateSchema(models, "postgres"))
	require.NoError(t, err)

	live, err := introspect.Schema(ctx, drv, "postgres")
	require.NoError(t, err)

	// The server really did rewrite it: this is the condition under test.
	var stored string
	for _, tbl := range live.Tables {
		for _, idx := range tbl.Indexes {
			if idx.Name == "uq_user_quests_one_active" {
				stored = idx.Where
			}
		}
	}
	require.NotEmpty(t, stored)
	require.NotEqual(t, "state IN ('assigned', 'in_progress')", stored,
		"the server stored the predicate verbatim; this test no longer covers the rewrite")

	drift := codegen.CompareSchemas(live, codegen.BuildSchema(models, "postgres"))
	assert.True(t, drift.Empty(),
		"a database that matches its models must report no drift\nunmanaged: %+v\nmissing: %+v\ndifferent: %+v",
		drift.Unmanaged, drift.Missing, drift.Different)
}
