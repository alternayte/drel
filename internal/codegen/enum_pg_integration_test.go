//go:build integration

package codegen_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntEnum_RoundTripPostgres proves an integer enum maps to an integer column
// (pgx binds an int cleanly) and the unquoted CHECK enforces the value set.
func TestIntEnum_RoundTripPostgres(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	models := []codegen.ModelInfo{{
		Name: "Ticket", TableName: "tickets", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
				IsExported: true, IsEnum: true, EnumIsInt: true, EnumBaseType: "int",
				EnumValues: []string{"0", "1", "2"}},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(models, "postgres"))
	require.NoError(t, err, "int-enum DDL must be valid Postgres")

	// pgx binds a Go int to the integer column without an enum encode error.
	_, err = drv.Exec(ctx, "INSERT INTO tickets (priority) VALUES ($1)", 2)
	require.NoError(t, err)

	// Out-of-range integer rejected by CHECK.
	_, err = drv.Exec(ctx, "INSERT INTO tickets (priority) VALUES ($1)", 9)
	assert.Error(t, err)
}

// TestStringEnumGrowth_AppliesToPostgres proves a grown string enum yields a
// non-empty migration whose ALTER TYPE ADD VALUE applies and admits the new value.
func TestStringEnumGrowth_AppliesToPostgres(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	v1 := []codegen.ModelInfo{{
		Name: "User", TableName: "users", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Role", GoType: "users.Role", ColumnName: "role", LocalGoType: "Role",
				IsExported: true, IsEnum: true, EnumBaseType: "string",
				EnumValues: []string{"admin", "user"}},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(v1, "postgres"))
	require.NoError(t, err)

	v2 := []codegen.ModelInfo{{
		Name: "User", TableName: "users", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Role", GoType: "users.Role", ColumnName: "role", LocalGoType: "Role",
				IsExported: true, IsEnum: true, EnumBaseType: "string",
				EnumValues: []string{"admin", "user", "moderator"}},
		},
	}}
	up, _, err := codegen.DiffSchemas(codegen.BuildSchema(v1, "postgres"), codegen.BuildSchema(v2, "postgres"), "postgres")
	require.NoError(t, err)
	require.NotEmpty(t, up, "growing an enum must not produce an empty migration")
	require.Contains(t, up, `ADD VALUE 'moderator'`)

	// ALTER TYPE ... ADD VALUE cannot run inside a transaction; execute the
	// statement directly (strip comment lines).
	for _, line := range splitNonComment(up) {
		_, err = drv.Exec(ctx, line)
		require.NoError(t, err, "migration line must apply: %s", line)
	}

	// The new value is now accepted.
	_, err = drv.Exec(ctx, "INSERT INTO users (role) VALUES ('moderator')")
	require.NoError(t, err)
}

// TestEnumDeclarationOrder_Postgres proves ORDER BY on an enum column follows
// declared order (low < high < zzz), not alphabetical order.
func TestEnumDeclarationOrder_Postgres(t *testing.T) {
	ctx := context.Background()
	drv := pgDriver(t)

	models := []codegen.ModelInfo{{
		Name: "Item", TableName: "items", PKType: "int",
		Fields: []codegen.FieldInfo{
			{Name: "Grade", GoType: "items.Grade", ColumnName: "grade", LocalGoType: "Grade",
				IsExported: true, IsEnum: true, EnumBaseType: "string",
				// Declared low, high, zzz — alphabetical would be high, low, zzz.
				EnumValues: []string{"low", "high", "zzz"}},
		},
	}}
	_, err := drv.Exec(ctx, codegen.GenerateSchema(models, "postgres"))
	require.NoError(t, err)

	for _, g := range []string{"zzz", "high", "low"} {
		_, err = drv.Exec(ctx, "INSERT INTO items (grade) VALUES ($1)", g)
		require.NoError(t, err)
	}

	rows, err := drv.Query(ctx, `SELECT grade FROM items ORDER BY grade ASC`)
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var g string
		require.NoError(t, rows.Scan(&g))
		got = append(got, g)
	}
	// Enum sort = declaration order, not alphabetical.
	assert.Equal(t, []string{"low", "high", "zzz"}, got)
}

// splitNonComment returns the non-comment, non-blank lines of a migration body.
func splitNonComment(sql string) []string {
	var out []string
	for _, line := range strings.Split(sql, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		out = append(out, t)
	}
	return out
}
