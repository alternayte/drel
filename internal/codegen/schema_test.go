package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GoTypeToSQL — Postgres (existing behavior preserved)
// ---------------------------------------------------------------------------

func TestGoTypeToSQL(t *testing.T) {
	tests := []struct {
		goType  string
		sqlType string
	}{
		{"int", "integer"},
		{"int8", "smallint"},
		{"int16", "smallint"},
		{"int32", "integer"},
		{"int64", "bigint"},
		{"string", "text"},
		{"bool", "boolean"},
		{"float32", "real"},
		{"float64", "double precision"},
		{"time.Time", "timestamptz"},
		{"github.com/google/uuid.UUID", "uuid"},
		{"uuid.UUID", "uuid"},
	}
	for _, tt := range tests {
		t.Run(tt.goType, func(t *testing.T) {
			assert.Equal(t, tt.sqlType, GoTypeToSQL(tt.goType, "postgres"))
		})
	}
}

func TestGoTypeToSQL_Pointer(t *testing.T) {
	assert.Equal(t, "text", GoTypeToSQL("*string", "postgres"))
	assert.Equal(t, "integer", GoTypeToSQL("*int", "postgres"))
}

func TestGoTypeToSQL_Unknown(t *testing.T) {
	assert.Equal(t, "text", GoTypeToSQL("custom.Type", "postgres"))
}

// ---------------------------------------------------------------------------
// GoTypeToSQL — SQLite
// ---------------------------------------------------------------------------

func TestGoTypeToSQL_SQLite(t *testing.T) {
	tests := []struct {
		goType  string
		sqlType string
	}{
		{"int", "INTEGER"},
		{"int8", "INTEGER"},
		{"int16", "INTEGER"},
		{"int32", "INTEGER"},
		{"int64", "INTEGER"},
		{"uint", "INTEGER"},
		{"uint8", "INTEGER"},
		{"uint16", "INTEGER"},
		{"uint32", "INTEGER"},
		{"uint64", "INTEGER"},
		{"string", "TEXT"},
		{"bool", "INTEGER"},
		{"float32", "REAL"},
		{"float64", "REAL"},
		{"time.Time", "DATETIME"},
		{"github.com/google/uuid.UUID", "TEXT"},
		{"uuid.UUID", "TEXT"},
	}
	for _, tt := range tests {
		t.Run(tt.goType, func(t *testing.T) {
			assert.Equal(t, tt.sqlType, GoTypeToSQL(tt.goType, "sqlite"))
		})
	}
}

func TestGoTypeToSQL_SQLite_Pointer(t *testing.T) {
	assert.Equal(t, "TEXT", GoTypeToSQL("*string", "sqlite"))
	assert.Equal(t, "INTEGER", GoTypeToSQL("*int", "sqlite"))
}

func TestGoTypeToSQL_SQLite_Unknown(t *testing.T) {
	assert.Equal(t, "TEXT", GoTypeToSQL("custom.Type", "sqlite"))
}

// ---------------------------------------------------------------------------
// GenerateCreateTable — Postgres
// ---------------------------------------------------------------------------

func TestGenerateCreateTable_BasicModel(t *testing.T) {
	m := ModelInfo{
		Name: "User", PkgName: "users", PKType: "int", TableName: "users",
		Fields: []FieldInfo{
			{Name: "name", GoType: "string", ColumnName: "name"},
			{Name: "email", GoType: "string", ColumnName: "email"},
		},
	}
	sql := GenerateCreateTable(m, nil, "postgres")

	assert.Contains(t, sql, `CREATE TABLE "users" (`)
	assert.Contains(t, sql, `"id" SERIAL PRIMARY KEY`)
	assert.Contains(t, sql, `"name" text NOT NULL`)
	assert.Contains(t, sql, `"email" text NOT NULL`)
	assert.Contains(t, sql, `"created_at" timestamptz NOT NULL DEFAULT NOW()`)
	assert.Contains(t, sql, `"updated_at" timestamptz NOT NULL DEFAULT NOW()`)
}

func TestGenerateCreateTable_BigintPK(t *testing.T) {
	m := ModelInfo{Name: "Post", PKType: "int64", TableName: "posts",
		Fields: []FieldInfo{{Name: "title", GoType: "string", ColumnName: "title"}}}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `"id" BIGSERIAL PRIMARY KEY`)
}

func TestGenerateCreateTable_Traits(t *testing.T) {
	m := ModelInfo{Name: "Post", PKType: "int", TableName: "posts",
		Fields:        []FieldInfo{{Name: "title", GoType: "string", ColumnName: "title"}},
		HasSoftDelete: true, HasVersioned: true, HasAudit: true}
	sql := GenerateCreateTable(m, nil, "postgres")

	assert.Contains(t, sql, `"deleted_at" timestamptz`)
	assert.NotContains(t, sql, `"deleted_at" timestamptz NOT NULL`)
	assert.Contains(t, sql, `"version" integer NOT NULL DEFAULT 1`)
	assert.Contains(t, sql, `"created_by" text`)
	assert.Contains(t, sql, `"updated_by" text`)
}

func TestGenerateCreateTable_Nullable(t *testing.T) {
	m := ModelInfo{Name: "Profile", PKType: "int", TableName: "profiles",
		Fields: []FieldInfo{{Name: "bio", GoType: "*string", ColumnName: "bio"}}}
	sql := GenerateCreateTable(m, nil, "postgres")

	assert.Contains(t, sql, `"bio" text`)
	assert.NotContains(t, sql, `"bio" text NOT NULL`)
}

// ---------------------------------------------------------------------------
// GenerateCreateTable — SQLite
// ---------------------------------------------------------------------------

func TestGenerateCreateTable_SQLite_BasicModel(t *testing.T) {
	m := ModelInfo{
		Name: "User", PkgName: "users", PKType: "int", TableName: "users",
		Fields: []FieldInfo{
			{Name: "name", GoType: "string", ColumnName: "name"},
			{Name: "email", GoType: "string", ColumnName: "email"},
		},
	}
	sql := GenerateCreateTable(m, nil, "sqlite")

	assert.Contains(t, sql, `CREATE TABLE "users" (`)
	assert.Contains(t, sql, `"id" INTEGER PRIMARY KEY AUTOINCREMENT`)
	assert.Contains(t, sql, `"name" TEXT NOT NULL`)
	assert.Contains(t, sql, `"email" TEXT NOT NULL`)
	assert.Contains(t, sql, `"created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`)
	assert.Contains(t, sql, `"updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`)
	assert.NotContains(t, sql, "NOW()")
	assert.NotContains(t, sql, "timestamptz")
}

func TestGenerateCreateTable_SQLite_BigintPK(t *testing.T) {
	m := ModelInfo{Name: "Post", PKType: "int64", TableName: "posts",
		Fields: []FieldInfo{{Name: "title", GoType: "string", ColumnName: "title"}}}
	sql := GenerateCreateTable(m, nil, "sqlite")
	// SQLite uses INTEGER PRIMARY KEY AUTOINCREMENT for all integer types.
	assert.Contains(t, sql, `"id" INTEGER PRIMARY KEY AUTOINCREMENT`)
	assert.NotContains(t, sql, "BIGSERIAL")
}

func TestGenerateCreateTable_SQLite_Traits(t *testing.T) {
	m := ModelInfo{Name: "Post", PKType: "int", TableName: "posts",
		Fields:        []FieldInfo{{Name: "title", GoType: "string", ColumnName: "title"}},
		HasSoftDelete: true, HasVersioned: true, HasAudit: true}
	sql := GenerateCreateTable(m, nil, "sqlite")

	assert.Contains(t, sql, `"deleted_at" DATETIME`)
	assert.NotContains(t, sql, `"deleted_at" DATETIME NOT NULL`)
	assert.Contains(t, sql, `"version" INTEGER NOT NULL DEFAULT 1`)
	assert.Contains(t, sql, `"created_by" TEXT`)
	assert.Contains(t, sql, `"updated_by" TEXT`)
}

func TestGenerateCreateTable_SQLite_UUIDPK(t *testing.T) {
	m := ModelInfo{Name: "Widget", PKType: "uuid.UUID", TableName: "widgets",
		Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}}}
	sql := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, sql, `"id" TEXT PRIMARY KEY`)
	assert.NotContains(t, sql, "AUTOINCREMENT")
}

func TestGenerateCreateTable_SQLite_Enum(t *testing.T) {
	m := ModelInfo{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
		{Name: "role", GoType: "users.Role", ColumnName: "role", LocalGoType: "Role",
			IsEnum: true, EnumValues: []string{"admin", "user"}},
	}}
	sql := GenerateCreateTable(m, nil, "sqlite")

	assert.Contains(t, sql, `"role" TEXT NOT NULL CHECK("role" IN ('admin', 'user'))`)
	assert.NotContains(t, sql, "CREATE TYPE")
}

// ---------------------------------------------------------------------------
// GenerateSchema — Postgres
// ---------------------------------------------------------------------------

func TestGenerateSchema_MultipleModels(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PKType: "int", TableName: "users",
			Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}}},
		{Name: "Post", PKType: "int", TableName: "posts",
			Fields: []FieldInfo{{Name: "title", GoType: "string", ColumnName: "title"}}},
	}
	sql := GenerateSchema(models, "postgres")
	assert.Contains(t, sql, `CREATE TABLE "users"`)
	assert.Contains(t, sql, `CREATE TABLE "posts"`)
}

func TestGenerateSchema_WithEnum(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
			{Name: "name", GoType: "string", ColumnName: "name"},
			{Name: "role", GoType: "users.Role", ColumnName: "role", LocalGoType: "Role",
				IsEnum: true, EnumValues: []string{"admin", "user"}},
		}},
	}
	sql := GenerateSchema(models, "postgres")

	assert.Contains(t, sql, `CREATE TYPE "role" AS ENUM ('admin', 'user');`)
	assert.Contains(t, sql, `"role" "role" NOT NULL`)
}

func TestGenerateSchema_BelongsToFK(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
			{Name: "name", GoType: "string", ColumnName: "name"},
		}},
		{Name: "Post", PKType: "int", TableName: "posts", Fields: []FieldInfo{
			{Name: "title", GoType: "string", ColumnName: "title"},
			{Name: "authorID", GoType: "int", ColumnName: "author_id"},
			{Name: "author", GoType: "*users.User", RelTag: "belongs_to,fk=author_id",
				Relation: &RelationFieldInfo{Type: "belongs_to", FK: "author_id", TargetModel: "User"}},
		}},
	}
	sql := GenerateSchema(models, "postgres")

	assert.Contains(t, sql, `"author_id" integer NOT NULL REFERENCES "users"("id")`)
}

func TestGenerateCreateTable_NoFK_WhenNilMap(t *testing.T) {
	m := ModelInfo{Name: "Post", PKType: "int", TableName: "posts", Fields: []FieldInfo{
		{Name: "authorID", GoType: "int", ColumnName: "author_id"},
	}}
	sql := GenerateCreateTable(m, nil, "postgres")

	assert.Contains(t, sql, `"author_id" integer NOT NULL`)
	assert.NotContains(t, sql, "REFERENCES")
}

func TestGenerateDropSchema_ReverseOrder(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", TableName: "users"},
		{Name: "Post", TableName: "posts"},
	}
	sql := GenerateDropSchema(models)

	postsIdx := strings.Index(sql, `DROP TABLE IF EXISTS "posts"`)
	usersIdx := strings.Index(sql, `DROP TABLE IF EXISTS "users"`)
	assert.Greater(t, postsIdx, -1)
	assert.Greater(t, usersIdx, -1)
	assert.Less(t, postsIdx, usersIdx)
}

func TestGenerateSchema_EnumEscaping(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
			{Name: "status", GoType: "string", ColumnName: "status", LocalGoType: "Status",
				IsEnum: true, EnumValues: []string{"it's", "normal"}},
		}},
	}
	sql := GenerateSchema(models, "postgres")
	assert.Contains(t, sql, "'it''s'")
}

func TestGenerateSchema_ManyToManyPivotTable(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "Author", PkgName: "models", PKType: "int", TableName: "authors",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name", LocalGoType: "string"},
				{Name: "tags", GoType: "[]*Tag", Relation: &RelationFieldInfo{
					Type: "many_to_many", FK: "author_id", JoinTable: "author_tags",
					RefColumn: "tag_id", TargetModel: "Tag",
				}},
			},
		},
		{
			Name: "Tag", PkgName: "models", PKType: "int", TableName: "tags",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name", LocalGoType: "string"},
			},
		},
	}

	schema := GenerateSchema(models, "postgres")

	assert.Contains(t, schema, `CREATE TABLE "author_tags"`)
	assert.Contains(t, schema, `"author_id" integer NOT NULL REFERENCES "authors"("id")`)
	assert.Contains(t, schema, `"tag_id" integer NOT NULL REFERENCES "tags"("id")`)
	assert.Contains(t, schema, `PRIMARY KEY ("author_id", "tag_id")`)
}

func TestGenerateSchema_ManyToManyDeduplication(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "Author", PkgName: "models", PKType: "int", TableName: "authors",
			Fields: []FieldInfo{
				{Name: "tags", GoType: "[]*Tag", Relation: &RelationFieldInfo{
					Type: "many_to_many", FK: "author_id", JoinTable: "author_tags",
					RefColumn: "tag_id", TargetModel: "Tag",
				}},
			},
		},
		{
			Name: "Tag", PkgName: "models", PKType: "int", TableName: "tags",
			Fields: []FieldInfo{
				{Name: "authors", GoType: "[]*Author", Relation: &RelationFieldInfo{
					Type: "many_to_many", FK: "tag_id", JoinTable: "author_tags",
					RefColumn: "author_id", TargetModel: "Author",
				}},
			},
		},
	}

	schema := GenerateSchema(models, "postgres")

	count := strings.Count(schema, `CREATE TABLE "author_tags"`)
	assert.Equal(t, 1, count)
}

// ---------------------------------------------------------------------------
// GenerateSchema — SQLite
// ---------------------------------------------------------------------------

func TestGenerateSchema_SQLite_NoCreateType(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
			{Name: "name", GoType: "string", ColumnName: "name"},
			{Name: "role", GoType: "users.Role", ColumnName: "role", LocalGoType: "Role",
				IsEnum: true, EnumValues: []string{"admin", "user"}},
		}},
	}
	sql := GenerateSchema(models, "sqlite")

	// No CREATE TYPE for SQLite.
	assert.NotContains(t, sql, "CREATE TYPE")
	// Enum handled via CHECK constraint.
	assert.Contains(t, sql, `CHECK("role" IN ('admin', 'user'))`)
}

func TestGenerateSchema_SQLite_FullSchema(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
			{Name: "name", GoType: "string", ColumnName: "name"},
			{Name: "email", GoType: "string", ColumnName: "email"},
		}},
		{Name: "Post", PKType: "int64", TableName: "posts", Fields: []FieldInfo{
			{Name: "title", GoType: "string", ColumnName: "title"},
			{Name: "views", GoType: "int", ColumnName: "views"},
		}},
	}
	sql := GenerateSchema(models, "sqlite")

	assert.Contains(t, sql, `"id" INTEGER PRIMARY KEY AUTOINCREMENT`)
	assert.Contains(t, sql, `"name" TEXT NOT NULL`)
	assert.Contains(t, sql, `"views" INTEGER NOT NULL`)
	assert.Contains(t, sql, `CURRENT_TIMESTAMP`)
	assert.NotContains(t, sql, "NOW()")
	assert.NotContains(t, sql, "SERIAL")
	assert.NotContains(t, sql, "timestamptz")
}

func TestGenerateSchema_SQLite_ManyToMany(t *testing.T) {
	models := []ModelInfo{
		{Name: "Author", PkgName: "models", PKType: "int", TableName: "authors",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name"},
				{Name: "tags", GoType: "[]*Tag", Relation: &RelationFieldInfo{
					Type: "many_to_many", FK: "author_id", JoinTable: "author_tags",
					RefColumn: "tag_id", TargetModel: "Tag",
				}},
			}},
		{Name: "Tag", PkgName: "models", PKType: "int", TableName: "tags",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name"},
			}},
	}
	sql := GenerateSchema(models, "sqlite")

	assert.Contains(t, sql, `CREATE TABLE "author_tags"`)
	assert.Contains(t, sql, `"author_id" INTEGER NOT NULL REFERENCES "authors"("id")`)
	assert.Contains(t, sql, `"tag_id" INTEGER NOT NULL REFERENCES "tags"("id")`)
}

func TestParseDBTag_TypeOverride(t *testing.T) {
	col, opts, err := parseDBTag(`db:"data,type=jsonb"`)
	require.NoError(t, err)
	assert.Equal(t, "data", col)
	assert.Equal(t, "jsonb", opts.typ)
}

func TestGenerateCreateTable_TypeOverride(t *testing.T) {
	m := ModelInfo{
		Name: "Account", PKType: "int", TableName: "accounts",
		Fields: []FieldInfo{
			// VOBaseType empty (struct-backed VO); explicit override wins.
			{Name: "money", GoType: "models.Money", ColumnName: "money", LocalGoType: "Money", IsVO: true, TypeOverride: "numeric(12,2)"},
		},
	}
	pg := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, pg, `"money" numeric(12,2) NOT NULL`)
	assert.NotContains(t, pg, `"money" text`)
}

func TestGenerateCreateTable_VOBaseType(t *testing.T) {
	m := ModelInfo{
		Name: "Account", PKType: "int", TableName: "accounts",
		Fields: []FieldInfo{
			{Name: "email", GoType: "models.Email", ColumnName: "email", LocalGoType: "Email", IsVO: true, VOBaseType: "string"},
			{Name: "balance", GoType: "models.Cents", ColumnName: "balance", LocalGoType: "Cents", IsVO: true, VOBaseType: "int64"},
		},
	}

	pg := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, pg, `"email" text NOT NULL`)
	assert.Contains(t, pg, `"balance" bigint NOT NULL`)
	assert.NotContains(t, pg, `"balance" text`)

	lite := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, lite, `"balance" INTEGER NOT NULL`)
	assert.NotContains(t, lite, `"balance" TEXT`)
}

// TestGenerateCreateTable_NullableVO verifies that a single-column VO with
// HasIsZero=true produces a nullable column (no NOT NULL constraint) in the
// generated DDL. The VO's Value() returns nil for the zero value, so the
// column must accept SQL NULL.
func TestGenerateCreateTable_NullableVO(t *testing.T) {
	m := ModelInfo{
		Name: "Account", PKType: "int", TableName: "accounts",
		Fields: []FieldInfo{
			// email: HasIsZero=true -> nullable (zero email -> NULL)
			{Name: "email", GoType: "models.Email", ColumnName: "email", LocalGoType: "Email",
				IsVO: true, VOBaseType: "string", HasIsZero: true, IsComparable: true},
			// balance: no HasIsZero -> NOT NULL
			{Name: "balance", GoType: "models.Cents", ColumnName: "balance", LocalGoType: "Cents",
				IsVO: true, VOBaseType: "int64", IsComparable: true},
		},
	}

	pg := GenerateCreateTable(m, nil, "postgres")
	// email is nullable: must NOT have NOT NULL.
	assert.Contains(t, pg, `"email" text`)
	assert.NotContains(t, pg, `"email" text NOT NULL`)
	// balance is NOT NULL.
	assert.Contains(t, pg, `"balance" bigint NOT NULL`)

	lite := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, lite, `"email" TEXT`)
	assert.NotContains(t, lite, `"email" TEXT NOT NULL`)
	assert.Contains(t, lite, `"balance" INTEGER NOT NULL`)
}

func TestCreateTable_UUIDPK_NoAutoGen(t *testing.T) {
	m := ModelInfo{
		Name: "Account", PKType: "uuid.UUID", PKTypePkg: "github.com/google/uuid",
		TableName: "accounts",
		Fields:    []FieldInfo{{Name: "Name", GoType: "string", ColumnName: "name"}},
	}

	pg := GenerateCreateTable(m, nil, "postgres")
	if !strings.Contains(pg, `"id" uuid PRIMARY KEY`) {
		t.Fatalf("postgres: expected uuid PK, got:\n%s", pg)
	}
	// Extract the "id" PK line and assert it has no SERIAL or DEFAULT on the PK
	// itself. (DEFAULT is valid on trait columns like created_at; we only
	// care the PK column is free of auto-generation clauses.)
	for _, line := range strings.Split(pg, "\n") {
		if strings.Contains(line, `"id"`) {
			if strings.Contains(line, "SERIAL") || strings.Contains(line, "DEFAULT") {
				t.Fatalf("postgres: app-assigned uuid PK must have no SERIAL/DEFAULT on id line:\n%s", line)
			}
			break
		}
	}

	lite := GenerateCreateTable(m, nil, "sqlite")
	if !strings.Contains(lite, `"id" TEXT PRIMARY KEY`) {
		t.Fatalf("sqlite: expected TEXT PK, got:\n%s", lite)
	}
	if strings.Contains(lite, "AUTOINCREMENT") {
		t.Fatalf("sqlite: app-assigned uuid PK must have no AUTOINCREMENT:\n%s", lite)
	}
}

func TestGenerateSchema_IntEnum_NoCreateType(t *testing.T) {
	models := []ModelInfo{
		{Name: "Ticket", PKType: "int", TableName: "tickets", Fields: []FieldInfo{
			{Name: "priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
				IsEnum: true, EnumIsInt: true, EnumBaseType: "int", EnumValues: []string{"0", "1", "2"}},
		}},
	}
	sql := GenerateSchema(models, "postgres")
	// No CREATE TYPE for an int enum.
	assert.NotContains(t, sql, `CREATE TYPE "priority"`)
}

func TestBuildEnums_OnlyStringEnums(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
			{Name: "role", GoType: "users.Role", ColumnName: "role", LocalGoType: "Role",
				IsEnum: true, EnumBaseType: "string", EnumValues: []string{"admin", "user"}},
			{Name: "priority", GoType: "users.Priority", ColumnName: "priority", LocalGoType: "Priority",
				IsEnum: true, EnumIsInt: true, EnumBaseType: "int", EnumValues: []string{"0", "1"}},
		}},
	}
	enums := buildEnums(models)
	require.Len(t, enums, 1)
	assert.Equal(t, "role", enums[0].Name)
	assert.False(t, enums[0].IsInt)
}

func TestGenerateCreateTable_Postgres_IntEnum(t *testing.T) {
	m := ModelInfo{Name: "Ticket", PKType: "int", TableName: "tickets", Fields: []FieldInfo{
		{Name: "priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
			IsEnum: true, EnumIsInt: true, EnumBaseType: "int", EnumValues: []string{"0", "1", "2"}},
	}}
	sql := GenerateCreateTable(m, nil, "postgres")
	// Integer column type, NOT a quoted enum type name.
	// The CHECK is now a named table-level constraint (not inline) so it can be altered in place.
	assert.Contains(t, sql, `"priority" integer NOT NULL`)
	assert.Contains(t, sql, `CONSTRAINT "chk_tickets_priority" CHECK ("priority" IN (0, 1, 2))`)
	assert.NotContains(t, sql, `"priority" "priority"`)
	// The inline form must NOT appear for Postgres.
	assert.NotContains(t, sql, `"priority" integer NOT NULL CHECK`)
}

func TestGenerateCreateTable_SQLite_IntEnum(t *testing.T) {
	m := ModelInfo{Name: "Ticket", PKType: "int", TableName: "tickets", Fields: []FieldInfo{
		{Name: "priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
			IsEnum: true, EnumIsInt: true, EnumBaseType: "int", EnumValues: []string{"0", "1", "2"}},
	}}
	sql := GenerateCreateTable(m, nil, "sqlite")
	// INTEGER column, unquoted numeric CHECK.
	assert.Contains(t, sql, `"priority" INTEGER NOT NULL CHECK("priority" IN (0, 1, 2))`)
	assert.NotContains(t, sql, "TEXT")
}

func TestBuildTable_StringEnumDefault_Quoted_Postgres(t *testing.T) {
	m := ModelInfo{Name: "Account", PKType: "int", TableName: "accounts", Fields: []FieldInfo{
		{Name: "role", GoType: "accounts.Role", ColumnName: "role", LocalGoType: "Role",
			IsEnum: true, EnumBaseType: "string", EnumValues: []string{"admin", "user"}, Default: "user"},
	}}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `DEFAULT 'user'`)
}

func TestBuildTable_IntEnumDefault_Unquoted(t *testing.T) {
	m := ModelInfo{Name: "Ticket", PKType: "int", TableName: "tickets", Fields: []FieldInfo{
		{Name: "priority", GoType: "tickets.Priority", ColumnName: "priority", LocalGoType: "Priority",
			IsEnum: true, EnumIsInt: true, EnumBaseType: "int", EnumValues: []string{"0", "1", "2"}, Default: "0"},
	}}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `DEFAULT 0`)
	assert.NotContains(t, sql, `DEFAULT '0'`)
}

func TestBuildTable_StringColumnDefault_Quoted(t *testing.T) {
	m := ModelInfo{Name: "User", PKType: "int", TableName: "users", Fields: []FieldInfo{
		{Name: "tier", GoType: "string", ColumnName: "tier", Default: "free"},
	}}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `DEFAULT 'free'`)
}

func TestCreateTable_NamedCheckConstraint_Postgres(t *testing.T) {
	tbl := Table{
		Name: "products",
		Columns: []Column{
			{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
			{Name: "price", Type: "integer", NotNull: true, Check: "price > 0"},
		},
	}
	sql := createTableSQL(tbl, "postgres")
	assert.Contains(t, sql, `CONSTRAINT "chk_products_price" CHECK (price > 0)`)
	// The inline "CHECK(...)" form must NOT appear for Postgres.
	assert.NotContains(t, sql, `"price" integer NOT NULL CHECK(price > 0)`)
}

func TestCheckConstraintName(t *testing.T) {
	assert.Equal(t, "chk_products_price", checkConstraintName("products", "price"))
}

func TestCreateTable_InlineCheck_SQLite(t *testing.T) {
	tbl := Table{
		Name: "products",
		Columns: []Column{
			{Name: "id", Type: "INTEGER PRIMARY KEY", NotNull: true, PK: true},
			{Name: "price", Type: "INTEGER", NotNull: true, Check: "price > 0"},
		},
	}
	sql := createTableSQL(tbl, "sqlite")
	// SQLite keeps the inline CHECK (no ALTER support; table rebuild required).
	assert.Contains(t, sql, "CHECK(price > 0)")
}

// TestBuildTable_JSONAndArrayColumns asserts that buildTable assigns the correct
// inferred SQL column type for JSON/array fields on both dialects:
//   - []string (IsJSON+IsArray, no override) -> jsonb (Postgres) / TEXT (SQLite)
//   - map[string]string (IsJSON, no override) -> jsonb (Postgres) / TEXT (SQLite)
//   - []string with type=text[] override -> text[] verbatim (both dialects)
//   - explicit type=jsonb override -> jsonb verbatim
func TestBuildTable_JSONAndArrayColumns(t *testing.T) {
	m := ModelInfo{
		Name:      "Doc",
		TableName: "docs",
		PKType:    "int",
		Fields: []FieldInfo{
			{Name: "Tags", GoType: "[]string", ColumnName: "tags", LocalGoType: "[]string", IsJSON: true, IsArray: true},
			{Name: "Meta", GoType: "map[string]string", ColumnName: "meta", LocalGoType: "map[string]string", IsJSON: true},
			{Name: "Arr", GoType: "[]string", ColumnName: "arr", LocalGoType: "[]string", IsJSON: true, IsArray: true, TypeOverride: "text[]"},
			{Name: "Raw", GoType: "map[string]any", ColumnName: "raw", LocalGoType: "map[string]any", IsJSON: true, TypeOverride: "jsonb"},
		},
	}

	pgCols := columnsByName(buildTable(m, nil, "postgres").Columns)
	assert.Equal(t, "jsonb", pgCols["tags"].Type, "default slice -> jsonb on postgres")
	assert.Equal(t, "jsonb", pgCols["meta"].Type, "default map -> jsonb on postgres")
	assert.Equal(t, "text[]", pgCols["arr"].Type, "type=text[] override wins on postgres")
	assert.Equal(t, "jsonb", pgCols["raw"].Type, "type=jsonb override")

	sqCols := columnsByName(buildTable(m, nil, "sqlite").Columns)
	assert.Equal(t, "TEXT", sqCols["tags"].Type, "slice -> TEXT (JSON) on sqlite")
	assert.Equal(t, "TEXT", sqCols["meta"].Type, "map -> TEXT (JSON) on sqlite")
	// type= override is dialect-agnostic verbatim; text[] on sqlite is the author's responsibility.
	assert.Equal(t, "text[]", sqCols["arr"].Type)
}

// columnsByName indexes a column slice by column name for assertions.
func columnsByName(cols []Column) map[string]Column {
	m := make(map[string]Column, len(cols))
	for _, c := range cols {
		m[c.Name] = c
	}
	return m
}

func TestGenerateCreateTable_CompositeKeyPostgres(t *testing.T) {
	m := ModelInfo{
		Name:      "OrderLine",
		TableName: "order_lines",
		PKType:    "OrderLineKey",
		Key: []KeyColumn{
			{FieldName: "OrderID", ColumnName: "order_id", GoType: "int"},
			{FieldName: "LineNo", ColumnName: "line_no", GoType: "int"},
		},
		KeyIsStruct: true,
		Fields:      []FieldInfo{{Name: "Qty", GoType: "int", ColumnName: "qty", IsExported: true}},
	}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `"order_id" integer NOT NULL`)
	assert.Contains(t, sql, `"line_no" integer NOT NULL`)
	assert.Contains(t, sql, `PRIMARY KEY ("order_id", "line_no")`)
	assert.NotContains(t, sql, "SERIAL")
}

func TestGenerateCreateTable_CompositeKeySQLite(t *testing.T) {
	m := ModelInfo{
		Name:      "OrderLine",
		TableName: "order_lines",
		PKType:    "OrderLineKey",
		Key: []KeyColumn{
			{FieldName: "OrderID", ColumnName: "order_id", GoType: "int"},
			{FieldName: "LineNo", ColumnName: "line_no", GoType: "int"},
		},
		KeyIsStruct: true,
	}
	sql := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, sql, `PRIMARY KEY ("order_id", "line_no")`)
	assert.NotContains(t, sql, "AUTOINCREMENT")
}

func TestGenerateCreateTable_NamedScalarKeyUsesTheTaggedColumnName(t *testing.T) {
	m := ModelInfo{
		Name:      "Country",
		TableName: "countries",
		PKType:    "string",
		Key:       []KeyColumn{{ColumnName: "code", GoType: "string"}},
	}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `"code" text PRIMARY KEY`)
}

func TestGenerateCreateTable_CompositeKeyUUIDColumnPostgres(t *testing.T) {
	m := ModelInfo{
		Name:      "TenantResource",
		TableName: "tenant_resources",
		PKType:    "TenantResourceKey",
		Key: []KeyColumn{
			{FieldName: "TenantID", ColumnName: "tenant_id", GoType: "UUID", UnderlyingGoType: "uuid.UUID"},
			{FieldName: "ResourceNo", ColumnName: "resource_no", GoType: "int", UnderlyingGoType: "int"},
		},
		KeyIsStruct: true,
	}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `"tenant_id" uuid NOT NULL`)
	assert.NotContains(t, sql, `"tenant_id" text`)
}

func TestGenerateCreateTable_CompositeKeyUUIDColumnSQLite(t *testing.T) {
	m := ModelInfo{
		Name:      "TenantResource",
		TableName: "tenant_resources",
		PKType:    "TenantResourceKey",
		Key: []KeyColumn{
			{FieldName: "TenantID", ColumnName: "tenant_id", GoType: "UUID", UnderlyingGoType: "uuid.UUID"},
			{FieldName: "ResourceNo", ColumnName: "resource_no", GoType: "int", UnderlyingGoType: "int"},
		},
		KeyIsStruct: true,
	}
	sql := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, sql, `"tenant_id" TEXT NOT NULL`)
}

func TestGenerateCreateTable_CompositeKeyNamedIntColumnPostgres(t *testing.T) {
	m := ModelInfo{
		Name:      "OrderLine",
		TableName: "order_lines",
		PKType:    "OrderLineKey",
		Key: []KeyColumn{
			{FieldName: "OrderID", ColumnName: "order_id", GoType: "OrderID", UnderlyingGoType: "int"},
			{FieldName: "LineNo", ColumnName: "line_no", GoType: "int", UnderlyingGoType: "int"},
		},
		KeyIsStruct: true,
	}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `"order_id" integer NOT NULL`)
	assert.NotContains(t, sql, `"order_id" text`)
}

func TestGenerateCreateTable_CompositeKeyNamedIntColumnSQLite(t *testing.T) {
	m := ModelInfo{
		Name:      "OrderLine",
		TableName: "order_lines",
		PKType:    "OrderLineKey",
		Key: []KeyColumn{
			{FieldName: "OrderID", ColumnName: "order_id", GoType: "OrderID", UnderlyingGoType: "int"},
			{FieldName: "LineNo", ColumnName: "line_no", GoType: "int", UnderlyingGoType: "int"},
		},
		KeyIsStruct: true,
	}
	sql := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, sql, `"order_id" INTEGER NOT NULL`)
	assert.NotContains(t, sql, `"order_id" TEXT`)
}

func TestGenerateCreateTable_SingleFieldStructKeyIntPostgres(t *testing.T) {
	m := ModelInfo{
		Name:      "Account",
		TableName: "accounts",
		PKType:    "AccountKey",
		Key: []KeyColumn{
			{FieldName: "AccountID", ColumnName: "account_id", GoType: "int", UnderlyingGoType: "int"},
		},
		KeyIsStruct: true,
	}
	sql := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, sql, `"account_id" integer PRIMARY KEY`)
	assert.NotContains(t, sql, "SERIAL")
}

func TestGenerateCreateTable_SingleFieldStructKeyIntSQLite(t *testing.T) {
	m := ModelInfo{
		Name:      "Account",
		TableName: "accounts",
		PKType:    "AccountKey",
		Key: []KeyColumn{
			{FieldName: "AccountID", ColumnName: "account_id", GoType: "int", UnderlyingGoType: "int"},
		},
		KeyIsStruct: true,
	}
	sql := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, sql, `"account_id" INTEGER PRIMARY KEY`)
	assert.NotContains(t, sql, "AUTOINCREMENT")
}

// A named scalar key type (type AccountID int) must take its SQL type from the
// key column's underlying kind, not from the local type name, which
// GoTypeToSQL does not recognize and would map to text. It is
// application-assigned (isAppAssignedPK is true for any non-builtin name), so
// it must never auto-increment.
func TestGenerateCreateTable_NamedScalarIntPK(t *testing.T) {
	m := ModelInfo{Name: "Account", PKType: "AccountID", TableName: "accounts",
		Key:    []KeyColumn{{ColumnName: "id", GoType: "AccountID", UnderlyingGoType: "int"}},
		Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}}}

	pg := GenerateCreateTable(m, nil, "postgres")
	assert.Contains(t, pg, `"id" integer PRIMARY KEY`)
	assert.NotContains(t, pg, "SERIAL")
	assert.NotContains(t, pg, `"id" text`)

	lite := GenerateCreateTable(m, nil, "sqlite")
	assert.Contains(t, lite, `"id" INTEGER PRIMARY KEY`)
	assert.NotContains(t, lite, "AUTOINCREMENT")
	assert.NotContains(t, lite, `"id" TEXT`)
}

// A named scalar key type over string keeps a text column.
func TestGenerateCreateTable_NamedScalarStringPK(t *testing.T) {
	m := ModelInfo{Name: "Country", PKType: "CountryCode", TableName: "countries",
		Key:    []KeyColumn{{ColumnName: "code", GoType: "CountryCode", UnderlyingGoType: "string"}},
		Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}}}
	assert.Contains(t, GenerateCreateTable(m, nil, "postgres"), `"code" text PRIMARY KEY`)
}
