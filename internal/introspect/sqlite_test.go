package introspect_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver/sqlitedriver"
	"github.com/alternayte/drel/internal/introspect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sqliteDriver(t *testing.T) *sqlitedriver.SQLiteDriver {
	t.Helper()
	drv, err := sqlitedriver.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(drv.Close)
	return drv
}

func findItem(items []codegen.DriftItem, kind, name string) *codegen.DriftItem {
	for i := range items {
		if items[i].Kind == kind && items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func sqliteModels() []codegen.ModelInfo {
	return []codegen.ModelInfo{{
		Name: "Quest", TableName: "quests", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "UserID", GoType: "string", ColumnName: "user_id", IsExported: true},
			{Name: "State", GoType: "string", ColumnName: "state", IsExported: true},
			{Name: "Slug", GoType: "string", ColumnName: "slug", IsExported: true, Unique: true},
		},
	}}
}

func TestSQLiteIntrospect_FindsHandWrittenObjects(t *testing.T) {
	ctx := context.Background()
	drv := sqliteDriver(t)
	models := sqliteModels()

	_, err := drv.Exec(ctx, codegen.GenerateSchema(models, "sqlite"))
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `CREATE TABLE auth_users (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = drv.Exec(ctx, `CREATE UNIQUE INDEX one_active_quest ON quests (user_id) WHERE state = 'active'`)
	require.NoError(t, err)

	live, err := introspect.Schema(ctx, drv, "sqlite")
	require.NoError(t, err)

	drift := codegen.CompareSchemas(live, codegen.BuildSchema(models, "sqlite"))

	idx := findItem(drift.Unmanaged, "index", "one_active_quest")
	require.NotNil(t, idx, "the hand-written index is not reported: %+v", drift.Unmanaged)
	assert.Contains(t, idx.Detail, "unique")
	assert.Contains(t, idx.Detail, "state = 'active'")

	require.NotNil(t, findItem(drift.Unmanaged, "table", "auth_users"),
		"the table drel does not model is not reported: %+v", drift.Unmanaged)
}

// A freshly generated SQLite schema must not read as drift, or every run
// reports a difference that is not there.
func TestSQLiteIntrospect_GeneratedSchemaMatchesItsModels(t *testing.T) {
	ctx := context.Background()
	drv := sqliteDriver(t)
	models := sqliteModels()

	_, err := drv.Exec(ctx, codegen.GenerateSchema(models, "sqlite"))
	require.NoError(t, err)

	live, err := introspect.Schema(ctx, drv, "sqlite")
	require.NoError(t, err)

	drift := codegen.CompareSchemas(live, codegen.BuildSchema(models, "sqlite"))
	assert.True(t, drift.Empty(),
		"unmanaged: %+v\nmissing: %+v\ndifferent: %+v", drift.Unmanaged, drift.Missing, drift.Different)
}
