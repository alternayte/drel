package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper column/table builders ----------------------------------------------

func pgTable(name string, cols ...Column) Table {
	return Table{Name: name, Columns: cols}
}

func TestDiffSchemas_NoChange(t *testing.T) {
	s := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "name", Type: "text", NotNull: true},
		),
	}}
	up, down, err := DiffSchemas(s, s, "postgres")
	require.NoError(t, err)
	assert.Equal(t, "", up)
	assert.Equal(t, "", down)
}

func TestDiffSchemas_AddTable(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
		Table{
			Name: "posts",
			Columns: []Column{
				{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				{Name: "title", Type: "text", NotNull: true},
			},
			Indexes: []Index{{Name: "idx_posts_title", Columns: []string{"title"}}},
		},
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `CREATE TABLE "posts"`)
	assert.Contains(t, up, `"title" text NOT NULL`)
	assert.Contains(t, up, `CREATE INDEX "idx_posts_title" ON "posts" ("title");`)
	assert.NotContains(t, up, `CREATE TABLE "users"`)
	assert.Contains(t, down, `DROP TABLE IF EXISTS "posts";`)
}

func TestDiffSchemas_DropTable(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
		pgTable("posts", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "title", Type: "text", NotNull: true}),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `DROP TABLE IF EXISTS "posts";`)
	assert.Contains(t, down, `CREATE TABLE "posts"`)
	assert.Contains(t, down, `"title" text NOT NULL`)
}

func TestDiffSchemas_AddColumn_Nullable(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "bio", Type: "text"},
		),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "users" ADD COLUMN "bio" text;`)
	assert.NotContains(t, up, `"bio" text NOT NULL`)
	assert.Contains(t, down, `ALTER TABLE "users" DROP COLUMN "bio";`)
}

func TestDiffSchemas_AddColumn_NotNull(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "email", Type: "text", NotNull: true},
		),
	}}
	up, _, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "users" ADD COLUMN "email" text NOT NULL;`)
}

func TestDiffSchemas_DropColumn(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "legacy", Type: "text"},
		),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "users" DROP COLUMN "legacy";`)
	assert.Contains(t, down, `ALTER TABLE "users" ADD COLUMN "legacy" text;`)
}

func TestDiffSchemas_TypeChange_Postgres(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "age", Type: "integer", NotNull: true},
		),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "age", Type: "bigint", NotNull: true},
		),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "users" ALTER COLUMN "age" TYPE bigint;`)
	assert.Contains(t, down, `ALTER TABLE "users" ALTER COLUMN "age" TYPE integer;`)
}

func TestDiffSchemas_TypeChange_SQLiteWarning(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			Column{Name: "age", Type: "INTEGER", NotNull: true},
		),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			Column{Name: "age", Type: "TEXT", NotNull: true},
		),
	}}
	up, _, err := DiffSchemas(old, newS, "sqlite")
	require.NoError(t, err)
	assert.Contains(t, up, `-- WARNING: SQLite cannot ALTER COLUMN TYPE for "users"."age" (INTEGER -> TEXT)`)
}

func TestDiffSchemas_NotNullChange_Postgres(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "bio", Type: "text"},
		),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "bio", Type: "text", NotNull: true},
		),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "users" ALTER COLUMN "bio" SET NOT NULL;`)
	assert.Contains(t, down, `ALTER TABLE "users" ALTER COLUMN "bio" DROP NOT NULL;`)
}

func TestDiffSchemas_NotNullChange_SQLiteWarning(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			Column{Name: "bio", Type: "TEXT"},
		),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			Column{Name: "bio", Type: "TEXT", NotNull: true},
		),
	}}
	up, _, err := DiffSchemas(old, newS, "sqlite")
	require.NoError(t, err)
	assert.Contains(t, up, `-- WARNING: SQLite cannot ALTER COLUMN NOT NULL for "users"."bio"`)
}

func TestDiffSchemas_AddIndex(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "email", Type: "text", NotNull: true},
		),
	}}
	newS := Schema{Tables: []Table{
		{
			Name: "users",
			Columns: []Column{
				{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				{Name: "email", Type: "text", NotNull: true},
			},
			Indexes: []Index{{Name: "idx_users_email", Columns: []string{"email"}}},
		},
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `CREATE INDEX "idx_users_email" ON "users" ("email");`)
	assert.Contains(t, down, `DROP INDEX "idx_users_email";`)
}

func TestDiffSchemas_DropIndex(t *testing.T) {
	old := Schema{Tables: []Table{
		{
			Name: "users",
			Columns: []Column{
				{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				{Name: "email", Type: "text", NotNull: true},
			},
			Indexes: []Index{{Name: "idx_users_email", Columns: []string{"email"}}},
		},
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "email", Type: "text", NotNull: true},
		),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `DROP INDEX "idx_users_email";`)
	assert.Contains(t, down, `CREATE INDEX "idx_users_email" ON "users" ("email");`)
}

func TestDiffSchemas_UniqueIndex(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "email", Type: "text", NotNull: true},
		),
	}}
	newS := Schema{Tables: []Table{
		{
			Name: "users",
			Columns: []Column{
				{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				{Name: "email", Type: "text", NotNull: true},
			},
			Indexes: []Index{{Name: "uq_users_email", Columns: []string{"email"}, Unique: true}},
		},
	}}
	up, _, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `CREATE UNIQUE INDEX "uq_users_email" ON "users" ("email");`)
}

func TestDiffSchemas_CompositeIndex(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "first", Type: "text", NotNull: true},
			Column{Name: "last", Type: "text", NotNull: true},
		),
	}}
	newS := Schema{Tables: []Table{
		{
			Name: "users",
			Columns: []Column{
				{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				{Name: "first", Type: "text", NotNull: true},
				{Name: "last", Type: "text", NotNull: true},
			},
			Indexes: []Index{{Name: "idx_name", Columns: []string{"first", "last"}}},
		},
	}}
	up, _, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `CREATE INDEX "idx_name" ON "users" ("first", "last");`)
}

func TestDiffSchemas_AddEnum_Postgres(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
	}}
	newS := Schema{
		Enums: []EnumDef{{Name: "role", Values: []string{"admin", "user"}}},
		Tables: []Table{
			pgTable("users",
				Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				Column{Name: "role", Type: `"role"`, NotNull: true},
			),
		},
	}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `CREATE TYPE "role" AS ENUM ('admin', 'user');`)
	assert.Contains(t, up, `ALTER TABLE "users" ADD COLUMN "role" "role" NOT NULL;`)
	assert.Contains(t, down, `DROP TYPE "role";`)
}

func TestDiffSchemas_AddEnum_SQLiteNoCreateType(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("users", Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true}),
	}}
	newS := Schema{Tables: []Table{
		pgTable("users",
			Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			Column{Name: "role", Type: "TEXT", NotNull: true, Check: `"role" IN ('admin', 'user')`},
		),
	}}
	up, _, err := DiffSchemas(old, newS, "sqlite")
	require.NoError(t, err)
	assert.NotContains(t, up, "CREATE TYPE")
	assert.Contains(t, up, `ALTER TABLE "users" ADD COLUMN "role" TEXT NOT NULL CHECK("role" IN ('admin', 'user'));`)
}

// BuildSchema-driven integration: full round of model -> schema -> diff.
func TestBuildSchema_IndexesFromTags(t *testing.T) {
	m := ModelInfo{
		Name: "User", PKType: "int", TableName: "users",
		Fields: []FieldInfo{
			{Name: "email", GoType: "string", ColumnName: "email", Unique: true},
			{Name: "age", GoType: "int", ColumnName: "age", Indexed: true},
			{Name: "first", GoType: "string", ColumnName: "first", IndexName: "idx_name"},
			{Name: "last", GoType: "string", ColumnName: "last", IndexName: "idx_name"},
			{Name: "score", GoType: "int", ColumnName: "score", CheckExpr: "score >= 0"},
		},
	}
	s := BuildSchema([]ModelInfo{m}, "postgres")
	tbl := s.Tables[0]

	// Indexes sorted by name: idx_name, idx_users_age, uq_users_email
	assert.Equal(t, []Index{
		{Name: "idx_name", Columns: []string{"first", "last"}},
		{Name: "idx_users_age", Columns: []string{"age"}},
		{Name: "uq_users_email", Columns: []string{"email"}, Unique: true},
	}, tbl.Indexes)

	// Check constraint surfaces on the score column.
	var score Column
	for _, c := range tbl.Columns {
		if c.Name == "score" {
			score = c
		}
	}
	assert.Equal(t, "score >= 0", score.Check)
}

func TestBuildSchema_DefaultAndTypeOverride(t *testing.T) {
	m := ModelInfo{
		Name: "User", PKType: "int", TableName: "users",
		Fields: []FieldInfo{
			{Name: "role", GoType: "string", ColumnName: "role", Default: "user"},
			{Name: "age", GoType: "int", ColumnName: "age", Default: "0"},
			{Name: "meta", GoType: "string", ColumnName: "meta", TypeOverride: "jsonb"},
			{Name: "ts", GoType: "time.Time", ColumnName: "ts", Default: "now()"},
			// String-typed columns with SQL-expression defaults must NOT be quoted.
			{Name: "uid", GoType: "string", ColumnName: "uid", Default: "gen_random_uuid()"},
			{Name: "arr", GoType: "string", ColumnName: "arr", Default: "ARRAY['a','b']"},
			// Already-quoted literal: pass through as-is.
			{Name: "pre", GoType: "string", ColumnName: "pre", Default: "'explicit'"},
		},
	}
	cols := func(s Schema) map[string]Column {
		out := map[string]Column{}
		for _, c := range s.Tables[0].Columns {
			out[c.Name] = c
		}
		return out
	}

	pg := cols(BuildSchema([]ModelInfo{m}, "postgres"))
	// String default is single-quoted; numeric default is bare; SQL-call default is bare.
	assert.Equal(t, "'user'", pg["role"].Default)
	assert.Equal(t, "0", pg["age"].Default)
	assert.Equal(t, "now()", pg["ts"].Default)
	// type= override replaces the inferred type.
	assert.Equal(t, "jsonb", pg["meta"].Type)
	// String-typed column with SQL-expression default must NOT be single-quoted.
	assert.Equal(t, "gen_random_uuid()", pg["uid"].Default)
	assert.Equal(t, "ARRAY['a','b']", pg["arr"].Default)
	// Already-quoted literal must pass through as-is (not double-quoted).
	assert.Equal(t, "'explicit'", pg["pre"].Default)

	sl := cols(BuildSchema([]ModelInfo{m}, "sqlite"))
	assert.Equal(t, "'user'", sl["role"].Default)
	assert.Equal(t, "0", sl["age"].Default)
	assert.Equal(t, "jsonb", sl["meta"].Type)
	assert.Equal(t, "gen_random_uuid()", sl["uid"].Default)
	assert.Equal(t, "ARRAY['a','b']", sl["arr"].Default)
	assert.Equal(t, "'explicit'", sl["pre"].Default)
}

func TestDiffSchemas_DefaultAddDrop(t *testing.T) {
	noDefault := ModelInfo{
		Name: "User", PKType: "int", TableName: "users",
		Fields: []FieldInfo{{Name: "role", GoType: "string", ColumnName: "role"}},
	}
	withDefault := ModelInfo{
		Name: "User", PKType: "int", TableName: "users",
		Fields: []FieldInfo{{Name: "role", GoType: "string", ColumnName: "role", Default: "user"}},
	}

	// Postgres: adding a default emits SET DEFAULT; the down emits DROP DEFAULT.
	up, down, err := DiffSchemas(
		BuildSchema([]ModelInfo{noDefault}, "postgres"),
		BuildSchema([]ModelInfo{withDefault}, "postgres"),
		"postgres",
	)
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "users" ALTER COLUMN "role" SET DEFAULT 'user';`)
	assert.Contains(t, down, `ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT;`)

	// SQLite cannot ALTER DEFAULT; it surfaces a WARNING rather than silently skipping.
	upLite, _, err := DiffSchemas(
		BuildSchema([]ModelInfo{noDefault}, "sqlite"),
		BuildSchema([]ModelInfo{withDefault}, "sqlite"),
		"sqlite",
	)
	require.NoError(t, err)
	assert.Contains(t, upLite, "WARNING: SQLite cannot ALTER COLUMN DEFAULT")
}

func TestDiffSchemas_GrowStringEnum_Postgres(t *testing.T) {
	old := Schema{
		Enums: []EnumDef{{Name: "role", Values: []string{"admin", "user"}, BaseType: "string"}},
		Tables: []Table{
			pgTable("users",
				Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				Column{Name: "role", Type: `"role"`, NotNull: true},
			),
		},
	}
	newS := Schema{
		Enums: []EnumDef{{Name: "role", Values: []string{"admin", "user", "moderator"}, BaseType: "string"}},
		Tables: []Table{
			pgTable("users",
				Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
				Column{Name: "role", Type: `"role"`, NotNull: true},
			),
		},
	}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TYPE "role" ADD VALUE 'moderator';`)
	// down cannot trivially drop an enum value — must warn, not silently no-op.
	assert.Contains(t, down, "WARNING")
	assert.NotEqual(t, "", up)
}

func TestDiffSchemas_ShrinkStringEnum_Postgres_Warns(t *testing.T) {
	old := Schema{
		Enums:  []EnumDef{{Name: "role", Values: []string{"admin", "user", "guest"}, BaseType: "string"}},
		Tables: []Table{pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true})},
	}
	newS := Schema{
		Enums:  []EnumDef{{Name: "role", Values: []string{"admin", "user"}, BaseType: "string"}},
		Tables: []Table{pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true})},
	}
	up, _, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, "WARNING")
	assert.Contains(t, up, "guest")
}

func TestDiffSchemas_GrowStringEnum_SQLiteNoAlterType(t *testing.T) {
	// SQLite has no CREATE TYPE / ALTER TYPE; string enums never appear in
	// s.Enums for SQLite, so growing one yields no ALTER TYPE here.
	old := Schema{Tables: []Table{pgTable("users", Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true})}}
	newS := Schema{Tables: []Table{pgTable("users", Column{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true})}}
	up, _, err := DiffSchemas(old, newS, "sqlite")
	require.NoError(t, err)
	assert.NotContains(t, up, "ALTER TYPE")
}

func TestDiffSchemas_GrowIntEnum_ProducesMigration(t *testing.T) {
	v1 := []ModelInfo{{
		Name: "Ticket", PKType: "int", TableName: "tickets", Fields: []FieldInfo{
			{Name: "priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
				IsEnum: true, EnumIsInt: true, EnumBaseType: "int", EnumValues: []string{"0", "1"}},
		},
	}}
	v2 := []ModelInfo{{
		Name: "Ticket", PKType: "int", TableName: "tickets", Fields: []FieldInfo{
			{Name: "priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
				IsEnum: true, EnumIsInt: true, EnumBaseType: "int", EnumValues: []string{"0", "1", "2"}},
		},
	}}

	for _, dialect := range []string{"postgres", "sqlite"} {
		up, _, err := DiffSchemas(BuildSchema(v1, dialect), BuildSchema(v2, dialect), dialect)
		require.NoError(t, err)
		assert.NotEqual(t, "", up, "growing an int enum must not produce an empty migration on %s", dialect)
		// The migration references the changed CHECK value set.
		assert.Contains(t, up, "2", "migration should reference the newly added enum value on %s", dialect)
	}
}

func TestDiffSchemas_CheckChange_Postgres(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("products",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "price", Type: "integer", NotNull: true, Check: "price > 0"},
		),
	}}
	newS := Schema{Tables: []Table{
		pgTable("products",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "price", Type: "integer", NotNull: true, Check: "price >= 1"},
		),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)

	assert.Contains(t, up, `ALTER TABLE "products" DROP CONSTRAINT IF EXISTS "chk_products_price";`)
	assert.Contains(t, up, `ALTER TABLE "products" ADD CONSTRAINT "chk_products_price" CHECK (price >= 1);`)
	// Down reverses to the old expression.
	assert.Contains(t, down, `ALTER TABLE "products" ADD CONSTRAINT "chk_products_price" CHECK (price > 0);`)
	assert.Contains(t, down, `ALTER TABLE "products" DROP CONSTRAINT IF EXISTS "chk_products_price";`)
	// No leftover WARNING comment on the Postgres path.
	assert.NotContains(t, up, "WARNING: CHECK constraint")
}

func TestDiffSchemas_AddCheckToExistingColumn_Postgres(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("products",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "price", Type: "integer", NotNull: true},
		),
	}}
	newS := Schema{Tables: []Table{
		pgTable("products",
			Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			Column{Name: "price", Type: "integer", NotNull: true, Check: "price > 0"},
		),
	}}
	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "products" ADD CONSTRAINT "chk_products_price" CHECK (price > 0);`)
	assert.Contains(t, down, `ALTER TABLE "products" DROP CONSTRAINT IF EXISTS "chk_products_price";`)
}

func TestDiffSchemas_CheckChange_SQLite_WarnsOnly(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "products", Columns: []Column{
			{Name: "id", Type: "INTEGER PRIMARY KEY", NotNull: true, PK: true},
			{Name: "price", Type: "INTEGER", NotNull: true, Check: "price > 0"},
		}},
	}}
	newS := Schema{Tables: []Table{
		{Name: "products", Columns: []Column{
			{Name: "id", Type: "INTEGER PRIMARY KEY", NotNull: true, PK: true},
			{Name: "price", Type: "INTEGER", NotNull: true, Check: "price >= 1"},
		}},
	}}
	up, _, err := DiffSchemas(old, newS, "sqlite")
	require.NoError(t, err)
	assert.Contains(t, up, "WARNING: SQLite cannot ALTER CHECK")
	assert.NotContains(t, up, "ADD CONSTRAINT")
}

func TestDiffSchemas_DropToEmpty_CoversPivotsAndEnums(t *testing.T) {
	desired := Schema{
		Enums: []EnumDef{{Name: "status", Values: []string{"active", "archived"}}},
		Tables: []Table{
			pgTable("users", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
			pgTable("roles", Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true}),
			{
				Name: "users_roles",
				Columns: []Column{
					{Name: "user_id", Type: "bigint", NotNull: true, Ref: "users"},
					{Name: "role_id", Type: "bigint", NotNull: true, Ref: "roles"},
				},
				PrimaryKey: []string{"user_id", "role_id"},
			},
		},
	}

	// up = drop everything; down = recreate everything.
	up, down, err := DiffSchemas(desired, Schema{}, "postgres")
	require.NoError(t, err)

	// The first-migration DOWN is this "up" (drop) direction.
	assert.Contains(t, up, `DROP TABLE IF EXISTS "users_roles";`)
	assert.Contains(t, up, `DROP TABLE IF EXISTS "users";`)
	assert.Contains(t, up, `DROP TABLE IF EXISTS "roles";`)
	assert.Contains(t, up, `DROP TYPE "status";`)

	// All three tables must appear in the drop output.
	pivotIdx := strings.Index(up, `DROP TABLE IF EXISTS "users_roles";`)
	usersIdx := strings.Index(up, `DROP TABLE IF EXISTS "users";`)
	rolesIdx := strings.Index(up, `DROP TABLE IF EXISTS "roles";`)
	require.Greater(t, pivotIdx, -1, "pivot drop must be present")
	require.Greater(t, usersIdx, -1, "users drop must be present")
	require.Greater(t, rolesIdx, -1, "roles drop must be present")

	// Pivot must drop BEFORE the parent tables it FK-references.
	// Violating this order fails on Postgres: "cannot drop table because other objects depend on it".
	assert.Less(t, pivotIdx, usersIdx, "pivot must drop before users (FK dependency)")
	assert.Less(t, pivotIdx, rolesIdx, "pivot must drop before roles (FK dependency)")

	// Enum DROP TYPE comes after the tables that use it (at the end of the output).
	enumIdx := strings.Index(up, `DROP TYPE "status";`)
	assert.Less(t, usersIdx, enumIdx, "DROP TYPE must come after table drops")

	_ = down // down (recreate) is exercised by other tests
}
