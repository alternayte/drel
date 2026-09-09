package drel_test

import (
	"testing"

	"github.com/alternayte/drel/internal/dialect"
	"github.com/alternayte/drel/internal/dialect/postgres"
	dialectsqlite "github.com/alternayte/drel/internal/dialect/sqlite"
	"github.com/stretchr/testify/assert"
)

func TestPostgres_BuildInsert(t *testing.T) {
	pg := postgres.New()
	result := pg.BuildInsert("users", []string{"name", "email", "age"}, []any{"Alice", "alice@test.com", 30}, []string{"id", "created_at", "updated_at"})
	assert.Equal(t, `INSERT INTO "users" ("name", "email", "age") VALUES ($1, $2, $3) RETURNING "id", "created_at", "updated_at"`, result.SQL)
	assert.Equal(t, []any{"Alice", "alice@test.com", 30}, result.Args)
}

func TestPostgres_BuildInsert_NoReturning(t *testing.T) {
	pg := postgres.New()
	result := pg.BuildInsert("logs", []string{"message"}, []any{"hello"}, nil)
	assert.Equal(t, `INSERT INTO "logs" ("message") VALUES ($1)`, result.SQL)
	assert.Equal(t, []any{"hello"}, result.Args)
}

func TestPostgres_BuildUpdate_SingleField(t *testing.T) {
	pg := postgres.New()
	result := pg.BuildUpdate("users", []dialect.ColumnValue{{Column: "name", Value: "Bob"}}, []string{"id"}, []any{1})
	assert.Equal(t, `UPDATE "users" SET "name" = $1 WHERE "id" = $2`, result.SQL)
	assert.Equal(t, []any{"Bob", 1}, result.Args)
}

func TestPostgres_BuildUpdate_MultipleFields(t *testing.T) {
	pg := postgres.New()
	result := pg.BuildUpdate("users", []dialect.ColumnValue{{Column: "name", Value: "Bob"}, {Column: "age", Value: 31}}, []string{"id"}, []any{42})
	assert.Equal(t, `UPDATE "users" SET "name" = $1, "age" = $2 WHERE "id" = $3`, result.SQL)
	assert.Equal(t, []any{"Bob", 31, 42}, result.Args)
}

func TestPostgres_BuildDelete(t *testing.T) {
	pg := postgres.New()
	result := pg.BuildDelete("users", []string{"id"}, []any{99})
	assert.Equal(t, `DELETE FROM "users" WHERE "id" = $1`, result.SQL)
	assert.Equal(t, []any{99}, result.Args)
}

// ─── Dialect Now() integration with mutation updated_at ──────────────────────

func TestDialect_Now_Postgres(t *testing.T) {
	pg := postgres.New()
	assert.Equal(t, "NOW()", pg.Now())
}

func TestDialect_Now_SQLite(t *testing.T) {
	sq := dialectsqlite.New()
	assert.Equal(t, "CURRENT_TIMESTAMP", sq.Now())
}

// ─── SQLite dialect mutation SQL shapes ─────────────────────────────────────

func TestSQLite_BuildInsert_EmitsReturning(t *testing.T) {
	sq := dialectsqlite.New()
	result := sq.BuildInsert("users", []string{"name", "email"}, []any{"Alice", "alice@test.com"}, []string{"id", "created_at", "updated_at"})
	assert.Contains(t, result.SQL, "RETURNING")
	assert.Equal(t, `INSERT INTO "users" ("name", "email") VALUES (?, ?) RETURNING "id", "created_at", "updated_at"`, result.SQL)
}

func TestSQLite_BuildUpdateVersioned_EmitsReturning(t *testing.T) {
	sq := dialectsqlite.New()
	result := sq.BuildUpdateVersioned("items",
		[]dialect.ColumnValue{{Column: "name", Value: "Widget"}},
		[]string{"id"}, []any{3}, "version", 2,
	)
	assert.Contains(t, result.SQL, "RETURNING")
	assert.Equal(t,
		`UPDATE "items" SET "name" = ?, "version" = "version" + 1 WHERE "id" = ? AND "version" = ? RETURNING "version"`,
		result.SQL,
	)
	assert.Equal(t, []any{"Widget", 3, 2}, result.Args)
}

func TestSQLite_BuildUpdate_WithRawExpr_UsesCurrentTimestamp(t *testing.T) {
	sq := dialectsqlite.New()
	result := sq.BuildUpdate("users",
		[]dialect.ColumnValue{
			{Column: "name", Value: "Bob"},
			{Column: "updated_at", Value: dialect.RawExpr{SQL: sq.Now()}},
		},
		[]string{"id"}, []any{1},
	)
	assert.Contains(t, result.SQL, "CURRENT_TIMESTAMP")
	assert.NotContains(t, result.SQL, "NOW()")
}

func TestPostgres_BuildUpdate_WithRawExpr_UsesNow(t *testing.T) {
	pg := postgres.New()
	result := pg.BuildUpdate("users",
		[]dialect.ColumnValue{
			{Column: "name", Value: "Bob"},
			{Column: "updated_at", Value: dialect.RawExpr{SQL: pg.Now()}},
		},
		[]string{"id"}, []any{1},
	)
	assert.Contains(t, result.SQL, "NOW()")
	assert.NotContains(t, result.SQL, "CURRENT_TIMESTAMP")
}
