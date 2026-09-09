package codegen_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver/sqlitedriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiffSchemas_AppliesToRealSQLite exercises the full generate → diff → apply
// cycle against a real SQLite database, proving the generated CREATE and ALTER
// SQL is valid and the resulting schema behaves as intended.
func TestDiffSchemas_AppliesToRealSQLite(t *testing.T) {
	ctx := context.Background()
	drv, err := sqlitedriver.New(":memory:")
	require.NoError(t, err)
	defer drv.Close()

	usersV1 := codegen.ModelInfo{
		Name: "User", TableName: "users", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true},
			{Name: "Age", GoType: "int", ColumnName: "age", IsExported: true},
		},
	}
	v1 := []codegen.ModelInfo{usersV1}

	// Apply the initial schema.
	_, err = drv.Exec(ctx, codegen.GenerateSchema(v1, "sqlite"))
	require.NoError(t, err)
	_, err = drv.Exec(ctx, "INSERT INTO users (name, age) VALUES ('alice', 30)")
	require.NoError(t, err)

	// Evolve: add a nullable column + an index on age, and a brand-new table.
	usersV2 := usersV1
	usersV2.Fields = []codegen.FieldInfo{
		{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true},
		{Name: "Age", GoType: "int", ColumnName: "age", IsExported: true, Indexed: true},
		{Name: "Bio", GoType: "*string", ColumnName: "bio", IsExported: true},
	}
	posts := codegen.ModelInfo{
		Name: "Post", TableName: "posts", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Title", GoType: "string", ColumnName: "title", IsExported: true},
		},
	}
	v2 := []codegen.ModelInfo{usersV2, posts}

	schemaV1 := codegen.BuildSchema(v1, "sqlite")
	schemaV2 := codegen.BuildSchema(v2, "sqlite")

	up, down, err := codegen.DiffSchemas(schemaV1, schemaV2, "sqlite")
	require.NoError(t, err)
	require.NotEmpty(t, up)
	require.NotEmpty(t, down)

	// Apply the up migration and verify the new shape works.
	_, err = drv.Exec(ctx, up)
	require.NoError(t, err, "up migration should apply cleanly:\n%s", up)

	_, err = drv.Exec(ctx, "UPDATE users SET bio = 'hi' WHERE name = 'alice'")
	require.NoError(t, err, "new bio column should be usable")
	_, err = drv.Exec(ctx, "INSERT INTO posts (title) VALUES ('first')")
	require.NoError(t, err, "new posts table should exist")

	// The age index should now exist.
	row := drv.QueryRow(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_users_age'")
	var n int
	require.NoError(t, row.Scan(&n))
	assert.Equal(t, 1, n, "age index should have been created")

	// Apply the down migration and verify the schema reverted.
	_, err = drv.Exec(ctx, down)
	require.NoError(t, err, "down migration should apply cleanly:\n%s", down)

	_, err = drv.Exec(ctx, "INSERT INTO posts (title) VALUES ('x')")
	assert.Error(t, err, "posts table should be gone after down")
	_, err = drv.Exec(ctx, "UPDATE users SET bio = 'y' WHERE name = 'alice'")
	assert.Error(t, err, "bio column should be gone after down")
}

// TestDiffSchemas_DownEnumOrdering verifies that when a table using an enum is
// dropped, the down migration creates the enum type before recreating the
// table that references it.
func TestDiffSchemas_DownEnumOrdering(t *testing.T) {
	withEnum := []codegen.ModelInfo{{
		Name: "User", TableName: "users", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Role", GoType: "Role", ColumnName: "role", LocalGoType: "Role",
				IsEnum: true, EnumValues: []string{"admin", "user"}, IsExported: true},
		},
	}}
	empty := []codegen.ModelInfo{} // table (and its enum) dropped

	_, down, err := codegen.DiffSchemas(
		codegen.BuildSchema(withEnum, "postgres"),
		codegen.BuildSchema(empty, "postgres"),
		"postgres",
	)
	require.NoError(t, err)
	createType := strings.Index(down, "CREATE TYPE")
	createTable := strings.Index(down, "CREATE TABLE")
	if createType < 0 || createTable < 0 {
		t.Fatalf("down should recreate both type and table:\n%s", down)
	}
	if createType > createTable {
		t.Fatalf("down must CREATE TYPE before CREATE TABLE that uses it:\n%s", down)
	}
}

// TestDiffSchemas_NoChange returns empty when schemas match.
func TestDiffSchemas_NoChange(t *testing.T) {
	models := []codegen.ModelInfo{{
		Name: "User", TableName: "users", PKType: "int",
		Fields: []codegen.FieldInfo{{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true}},
	}}
	s := codegen.BuildSchema(models, "postgres")
	up, down, err := codegen.DiffSchemas(s, s, "postgres")
	require.NoError(t, err)
	assert.Empty(t, up)
	assert.Empty(t, down)
}

// TestEnumDefault_AppliesToRealSQLite proves a parsed default= produces a usable
// column DEFAULT: a NULL-omitting INSERT picks it up.
func TestEnumDefault_AppliesToRealSQLite(t *testing.T) {
	ctx := context.Background()
	drv, err := sqlitedriver.New(":memory:")
	require.NoError(t, err)
	defer drv.Close()

	models := []codegen.ModelInfo{{
		Name: "Account", TableName: "accounts", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Name", GoType: "string", ColumnName: "name", IsExported: true},
			{Name: "Role", GoType: "accounts.Role", ColumnName: "role", LocalGoType: "Role",
				IsExported: true, IsEnum: true, EnumBaseType: "string",
				EnumValues: []string{"admin", "user"}, Default: "user"},
		},
	}}

	_, err = drv.Exec(ctx, codegen.GenerateSchema(models, "sqlite"))
	require.NoError(t, err)

	// Insert without specifying role; the DEFAULT 'user' should apply.
	_, err = drv.Exec(ctx, "INSERT INTO accounts (name) VALUES ('alice')")
	require.NoError(t, err)

	rows, err := drv.Query(ctx, "SELECT role FROM accounts WHERE name = 'alice'")
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var role string
	require.NoError(t, rows.Scan(&role))
	assert.Equal(t, "user", role)
}

// TestIntEnum_AppliesToRealSQLite proves the generated integer-enum DDL is valid
// SQLite and that the unquoted numeric CHECK enforces the value set.
func TestIntEnum_AppliesToRealSQLite(t *testing.T) {
	ctx := context.Background()
	drv, err := sqlitedriver.New(":memory:")
	require.NoError(t, err)
	defer drv.Close()

	models := []codegen.ModelInfo{{
		Name: "Ticket", TableName: "tickets", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
				IsExported: true, IsEnum: true, EnumIsInt: true, EnumBaseType: "int",
				EnumValues: []string{"0", "1", "2"}},
		},
	}}

	_, err = drv.Exec(ctx, codegen.GenerateSchema(models, "sqlite"))
	require.NoError(t, err, "int-enum CREATE TABLE must be valid SQLite")

	// A valid value inserts.
	_, err = drv.Exec(ctx, "INSERT INTO tickets (priority) VALUES (1)")
	require.NoError(t, err)

	// An out-of-range integer is rejected by the CHECK.
	_, err = drv.Exec(ctx, "INSERT INTO tickets (priority) VALUES (5)")
	assert.Error(t, err, "CHECK should reject an int outside the enum value set")
}
