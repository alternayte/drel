//go:build integration

package codegen_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/stretchr/testify/require"
)

// spaceModule is the module from issue 15: one model references another with
// references=, and the referencing model is declared first.
func spaceModule() []codegen.ModelInfo {
	return []codegen.ModelInfo{
		{Name: "Member", PKType: "uuid.UUID", TableName: "space_members", Fields: []codegen.FieldInfo{
			{Name: "SpaceID", GoType: "uuid.UUID", ColumnName: "space_id", IsExported: true,
				References: "spaces", RefColumn: "id", OnDelete: "CASCADE"},
		}},
		{Name: "Space", PKType: "uuid.UUID", TableName: "spaces", Fields: []codegen.FieldInfo{
			{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true},
		}},
	}
}

// Postgres resolves a REFERENCES target when it runs CREATE TABLE. The full
// schema must therefore order the tables itself; the model order is the order
// the packages were scanned in, and it means nothing to the database.
func TestGenerateSchema_CreatesReferencedTableFirstOnPostgres(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	_, err := drv.Exec(ctx, codegen.GenerateSchema(spaceModule(), "postgres"))
	require.NoError(t, err, "a referenced table must be created before the table that references it")
}

// The same ordering applies to an incremental migration that adds several new
// tables at once: a new table that references another new table needs it first.
func TestDiffSchemas_NewTablesInOneMigrationApplyToPostgres(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	oldModels := []codegen.ModelInfo{
		{Name: "Account", PKType: "int", TableName: "accounts", Fields: []codegen.FieldInfo{
			{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true},
		}},
	}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(oldModels, "postgres"))
	require.NoError(t, err)

	newModels := append(oldModels, spaceModule()...)
	up, down, err := codegen.DiffSchemas(
		codegen.BuildSchema(oldModels, "postgres"),
		codegen.BuildSchema(newModels, "postgres"),
		"postgres")
	require.NoError(t, err)
	require.NotEmpty(t, up)

	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "up migration:\n%s", up)

	_, err = drv.Exec(ctx, down)
	require.NoError(t, err, "down migration:\n%s", down)

	var remaining int
	row := drv.QueryRow(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name IN ('spaces', 'space_members')")
	require.NoError(t, row.Scan(&remaining))
	require.Zero(t, remaining, "down must drop both tables")
}
