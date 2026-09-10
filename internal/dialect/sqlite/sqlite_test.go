package sqlite

import (
	"testing"
	"time"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dialect"
	"github.com/stretchr/testify/assert"
)

func intPtr(n int) *int { return &n }

// ─── SupportsReturning ────────────────────────────────────────────────────────

func TestSQLite_SupportsReturning(t *testing.T) {
	assert.True(t, New().SupportsReturning())
}

// ─── Now ─────────────────────────────────────────────────────────────────────

func TestSQLite_Now(t *testing.T) {
	assert.Equal(t, "CURRENT_TIMESTAMP", New().Now())
}

// ─── BuildSelect ─────────────────────────────────────────────────────────────

func TestSQLite_BuildSelect(t *testing.T) {
	s := New()

	tests := []struct {
		name     string
		node     ast.SelectNode
		expected dialect.Result
	}{
		{
			name: "simple select no where",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name", "email"},
				Type:    ast.QuerySelect,
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name", "email" FROM "users"`,
				Args: nil,
			},
		},
		{
			name: "where eq with ? placeholder",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "id",
						Op:     ast.OpEq,
						Value:  42,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE "id" = ?`,
				Args: []any{42},
			},
		},
		{
			name: "where neq",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "role",
						Op:     ast.OpNEQ,
						Value:  "admin",
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "role" != ?`,
				Args: []any{"admin"},
			},
		},
		{
			name: "where gt",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "age",
						Op:     ast.OpGT,
						Value:  18,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "age" > ?`,
				Args: []any{18},
			},
		},
		{
			name: "where gte",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "age",
						Op:     ast.OpGTE,
						Value:  18,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "age" >= ?`,
				Args: []any{18},
			},
		},
		{
			name: "where lt",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "age",
						Op:     ast.OpLT,
						Value:  65,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "age" < ?`,
				Args: []any{65},
			},
		},
		{
			name: "where lte",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "age",
						Op:     ast.OpLTE,
						Value:  65,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "age" <= ?`,
				Args: []any{65},
			},
		},
		{
			name: "where like",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "name",
						Op:     ast.OpLike,
						Value:  "J%",
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "name" LIKE ?`,
				Args: []any{"J%"},
			},
		},
		{
			name: "ilike maps to like",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "name",
						Op:     ast.OpILike,
						Value:  "j%",
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "name" LIKE ?`,
				Args: []any{"j%"},
			},
		},
		{
			name: "where in",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "role",
						Op:     ast.OpIn,
						Values: []any{"admin", "mod"},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE "role" IN (?, ?)`,
				Args: []any{"admin", "mod"},
			},
		},
		{
			name: "where not in",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "status",
						Op:     ast.OpNotIn,
						Values: []any{"banned", "pending"},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "status" NOT IN (?, ?)`,
				Args: []any{"banned", "pending"},
			},
		},
		{
			name: "where between",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "age",
						Op:     ast.OpBetween,
						Values: []any{18, 65},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "age" BETWEEN ? AND ?`,
				Args: []any{18, 65},
			},
		},
		{
			name: "where is null",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "deleted_at",
						Op:     ast.OpIsNull,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "deleted_at" IS NULL`,
				Args: nil,
			},
		},
		{
			name: "where is not null",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "deleted_at",
						Op:     ast.OpIsNotNull,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE "deleted_at" IS NOT NULL`,
				Args: nil,
			},
		},
		{
			name: "where AND",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalAnd,
					Children: []ast.WhereClause{
						{
							Comparison: &ast.ComparisonNode{Column: "age", Op: ast.OpGTE, Value: 18},
						},
						{
							Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "admin"},
						},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE ("age" >= ? AND "role" = ?)`,
				Args: []any{18, "admin"},
			},
		},
		{
			name: "where OR",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalOr,
					Children: []ast.WhereClause{
						{
							Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "admin"},
						},
						{
							Comparison: &ast.ComparisonNode{Column: "age", Op: ast.OpGTE, Value: 21},
						},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE ("role" = ? OR "age" >= ?)`,
				Args: []any{"admin", 21},
			},
		},
		{
			name: "where NOT",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalNot,
					Children: []ast.WhereClause{
						{
							Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "banned"},
						},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" WHERE NOT ("role" = ?)`,
				Args: []any{"banned"},
			},
		},
		{
			name: "order by asc",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{{Column: "name", Direction: ast.Asc}},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" ORDER BY "name"`,
				Args: nil,
			},
		},
		{
			name: "order by desc",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{{Column: "created_at", Direction: ast.Desc}},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" ORDER BY "created_at" DESC`,
				Args: nil,
			},
		},
		{
			name: "order by asc nulls last",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{
					{Column: "name", Direction: ast.Asc, Nulls: ast.NullsLast},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" ORDER BY "name" NULLS LAST`,
				Args: nil,
			},
		},
		{
			name: "order by desc nulls first",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{
					{Column: "rank", Direction: ast.Desc, Nulls: ast.NullsFirst},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" ORDER BY "rank" DESC NULLS FIRST`,
				Args: nil,
			},
		},
		{
			name: "limit and offset",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
				Limit:   intPtr(10),
				Offset:  intPtr(20),
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users" LIMIT 10 OFFSET 20`,
				Args: nil,
			},
		},
		{
			name: "count",
			node: ast.SelectNode{
				Table: "users",
				Type:  ast.QueryCount,
			},
			expected: dialect.Result{
				SQL:  `SELECT COUNT(*) FROM "users"`,
				Args: nil,
			},
		},
		{
			name: "count with where",
			node: ast.SelectNode{
				Table: "users",
				Type:  ast.QueryCount,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "admin"},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT COUNT(*) FROM "users" WHERE "role" = ?`,
				Args: []any{"admin"},
			},
		},
		{
			name: "exists",
			node: ast.SelectNode{
				Table: "users",
				Type:  ast.QueryExists,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{Column: "email", Op: ast.OpEq, Value: "test@example.com"},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT EXISTS(SELECT 1 FROM "users" WHERE "email" = ?)`,
				Args: []any{"test@example.com"},
			},
		},
		{
			name: "complex combined query",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name", "email"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalAnd,
					Children: []ast.WhereClause{
						{
							LogicalOp: ast.LogicalOr,
							Children: []ast.WhereClause{
								{Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "admin"}},
								{Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "mod"}},
							},
						},
						{Comparison: &ast.ComparisonNode{Column: "age", Op: ast.OpGTE, Value: 18}},
					},
				},
				OrderBy: []ast.OrderByExpr{
					{Column: "name", Direction: ast.Asc},
					{Column: "created_at", Direction: ast.Desc},
				},
				Limit:  intPtr(25),
				Offset: intPtr(50),
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name", "email" FROM "users" WHERE (("role" = ? OR "role" = ?) AND "age" >= ?) ORDER BY "name", "created_at" DESC LIMIT 25 OFFSET 50`,
				Args: []any{"admin", "mod", 18},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.BuildSelect(tt.node)
			assert.Equal(t, tt.expected.SQL, result.SQL)
			assert.Equal(t, tt.expected.Args, result.Args)
		})
	}
}

// ─── Raw predicates ───────────────────────────────────────────────────────────

func buildRawWhere(sql string, args ...any) dialect.Result {
	s := New()
	node := ast.SelectNode{
		Table:   "test",
		Columns: []string{"id"},
		Where: &ast.WhereClause{
			Raw:     &sql,
			RawArgs: args,
		},
		Type: ast.QuerySelect,
	}
	return s.BuildSelect(node)
}

func TestSQLite_RawPlaceholder_Basic(t *testing.T) {
	r := buildRawWhere("age > ? AND name = ?", 18, "alice")
	assert.Equal(t, `SELECT "id" FROM "test" WHERE age > ? AND name = ?`, r.SQL)
	assert.Equal(t, []any{18, "alice"}, r.Args)
}

func TestSQLite_RawPlaceholder_InsideSingleQuotedString(t *testing.T) {
	r := buildRawWhere("name = '?' AND age > ?", 18)
	assert.Equal(t, `SELECT "id" FROM "test" WHERE name = '?' AND age > ?`, r.SQL)
	assert.Equal(t, []any{18}, r.Args)
}

func TestSQLite_RawPlaceholder_InsideDoubleQuotedIdentifier(t *testing.T) {
	r := buildRawWhere(`"col?" = ?`, 42)
	assert.Equal(t, `SELECT "id" FROM "test" WHERE "col?" = ?`, r.SQL)
	assert.Equal(t, []any{42}, r.Args)
}

func TestSQLite_RawPlaceholder_EscapedSingleQuote(t *testing.T) {
	r := buildRawWhere("name = 'it''s a ?' AND id = ?", 1)
	assert.Equal(t, `SELECT "id" FROM "test" WHERE name = 'it''s a ?' AND id = ?`, r.SQL)
	assert.Equal(t, []any{1}, r.Args)
}

func TestSQLite_RawPlaceholder_MismatchDoesNotInjectError(t *testing.T) {
	// After removing the ERROR: fallback, a mismatch (constructed directly via
	// AST, bypassing Raw's panic) emits partial SQL but does NOT inject "ERROR:".
	s := New()
	sql := "a = ? AND b = ?"
	node := ast.SelectNode{
		Table:   "test",
		Columns: []string{"id"},
		Where: &ast.WhereClause{
			Raw:     &sql,
			RawArgs: []any{1}, // only 1 arg for 2 placeholders
		},
		Type: ast.QuerySelect,
	}
	r := s.BuildSelect(node)
	assert.NotContains(t, r.SQL, "ERROR:")
}

// ─── BuildInsert ─────────────────────────────────────────────────────────────

func TestSQLite_BuildInsert(t *testing.T) {
	s := New()

	t.Run("basic insert no returning", func(t *testing.T) {
		r := s.BuildInsert("users", []string{"name", "email"}, []any{"Alice", "alice@example.com"}, nil)
		assert.Equal(t, `INSERT INTO "users" ("name", "email") VALUES (?, ?)`, r.SQL)
		assert.Equal(t, []any{"Alice", "alice@example.com"}, r.Args)
	})

	t.Run("returningCols emit RETURNING", func(t *testing.T) {
		r := s.BuildInsert("users", []string{"name"}, []any{"Bob"}, []string{"id", "created_at", "updated_at"})
		assert.Equal(t, `INSERT INTO "users" ("name") VALUES (?) RETURNING "id", "created_at", "updated_at"`, r.SQL)
		assert.Equal(t, []any{"Bob"}, r.Args)
	})

	t.Run("no returningCols omits RETURNING", func(t *testing.T) {
		r := s.BuildInsert("users", []string{"name"}, []any{"Bob"}, nil)
		assert.Equal(t, `INSERT INTO "users" ("name") VALUES (?)`, r.SQL)
		assert.NotContains(t, r.SQL, "RETURNING")
	})
}

// ─── BuildUpdate ─────────────────────────────────────────────────────────────

func TestSQLite_BuildUpdate(t *testing.T) {
	s := New()

	t.Run("basic update", func(t *testing.T) {
		r := s.BuildUpdate("users",
			[]dialect.ColumnValue{
				{Column: "name", Value: "Alice"},
				{Column: "email", Value: "alice@example.com"},
			},
			[]string{"id"}, []any{1},
		)
		assert.Equal(t, `UPDATE "users" SET "name" = ?, "email" = ? WHERE "id" = ?`, r.SQL)
		assert.Equal(t, []any{"Alice", "alice@example.com", 1}, r.Args)
	})

	t.Run("update with raw expr", func(t *testing.T) {
		r := s.BuildUpdate("products",
			[]dialect.ColumnValue{
				{Column: "updated_at", Value: dialect.RawExpr{SQL: "CURRENT_TIMESTAMP"}},
				{Column: "stock", Value: 99},
			},
			[]string{"id"}, []any{5},
		)
		assert.Equal(t, `UPDATE "products" SET "updated_at" = CURRENT_TIMESTAMP, "stock" = ? WHERE "id" = ?`, r.SQL)
		assert.Equal(t, []any{99, 5}, r.Args)
	})
}

// ─── BuildDelete ─────────────────────────────────────────────────────────────

func TestSQLite_BuildDelete(t *testing.T) {
	s := New()
	r := s.BuildDelete("users", []string{"id"}, []any{42})
	assert.Equal(t, `DELETE FROM "users" WHERE "id" = ?`, r.SQL)
	assert.Equal(t, []any{42}, r.Args)
}

// ─── BuildSoftDelete ─────────────────────────────────────────────────────────

func TestSQLite_BuildSoftDelete(t *testing.T) {
	s := New()
	r := s.BuildSoftDelete("users", []string{"id"}, []any{7})
	assert.Equal(t, `UPDATE "users" SET "deleted_at" = CURRENT_TIMESTAMP WHERE "id" = ?`, r.SQL)
	assert.Equal(t, []any{7}, r.Args)
	assert.NotContains(t, r.SQL, "NOW()")
}

// ─── BuildUpdateVersioned ────────────────────────────────────────────────────

func TestSQLite_BuildUpdateVersioned(t *testing.T) {
	s := New()
	r := s.BuildUpdateVersioned("items",
		[]dialect.ColumnValue{{Column: "name", Value: "Widget"}},
		[]string{"id"}, []any{3}, "version", 2,
	)
	assert.Equal(t,
		`UPDATE "items" SET "name" = ?, "version" = "version" + 1 WHERE "id" = ? AND "version" = ? RETURNING "version"`,
		r.SQL,
	)
	assert.Equal(t, []any{"Widget", 3, 2}, r.Args)
}

// ─── BuildBulkInsert ─────────────────────────────────────────────────────────

func TestSQLite_BuildBulkInsert(t *testing.T) {
	s := New()
	r := s.BuildBulkInsert("users",
		[]string{"name", "email"},
		[][]any{
			{"Alice", "alice@example.com"},
			{"Bob", "bob@example.com"},
		},
	)
	assert.Equal(t,
		`INSERT INTO "users" ("name", "email") VALUES (?, ?), (?, ?)`,
		r.SQL,
	)
	assert.Equal(t, []any{"Alice", "alice@example.com", "Bob", "bob@example.com"}, r.Args)
}

// ─── BuildBulkUpdate ─────────────────────────────────────────────────────────

func TestSQLite_BuildBulkUpdate(t *testing.T) {
	s := New()

	t.Run("with where", func(t *testing.T) {
		role := "member"
		r := s.BuildBulkUpdate("users",
			[]dialect.ColumnValue{{Column: "active", Value: true}},
			&ast.WhereClause{
				Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: role},
			},
		)
		assert.Equal(t, `UPDATE "users" SET "active" = ? WHERE "role" = ?`, r.SQL)
		assert.Equal(t, []any{true, "member"}, r.Args)
	})

	t.Run("no where", func(t *testing.T) {
		r := s.BuildBulkUpdate("users",
			[]dialect.ColumnValue{{Column: "active", Value: false}},
			nil,
		)
		assert.Equal(t, `UPDATE "users" SET "active" = ?`, r.SQL)
		assert.Equal(t, []any{false}, r.Args)
	})
}

// ─── BuildBulkDelete ─────────────────────────────────────────────────────────

func TestSQLite_BuildBulkDelete(t *testing.T) {
	s := New()

	t.Run("with where", func(t *testing.T) {
		r := s.BuildBulkDelete("sessions",
			&ast.WhereClause{
				Comparison: &ast.ComparisonNode{Column: "expired", Op: ast.OpEq, Value: true},
			},
		)
		assert.Equal(t, `DELETE FROM "sessions" WHERE "expired" = ?`, r.SQL)
		assert.Equal(t, []any{true}, r.Args)
	})

	t.Run("no where", func(t *testing.T) {
		r := s.BuildBulkDelete("sessions", nil)
		assert.Equal(t, `DELETE FROM "sessions"`, r.SQL)
		assert.Nil(t, r.Args)
	})
}

// ─── BuildBulkSoftDelete ─────────────────────────────────────────────────────

func TestSQLite_BuildBulkSoftDelete(t *testing.T) {
	s := New()

	t.Run("with where", func(t *testing.T) {
		r := s.BuildBulkSoftDelete("users",
			&ast.WhereClause{
				Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "guest"},
			},
		)
		assert.Equal(t,
			`UPDATE "users" SET "deleted_at" = CURRENT_TIMESTAMP WHERE "deleted_at" IS NULL AND "role" = ?`,
			r.SQL,
		)
		assert.Equal(t, []any{"guest"}, r.Args)
		assert.NotContains(t, r.SQL, "NOW()")
	})

	t.Run("no where", func(t *testing.T) {
		r := s.BuildBulkSoftDelete("users", nil)
		assert.Equal(t,
			`UPDATE "users" SET "deleted_at" = CURRENT_TIMESTAMP WHERE "deleted_at" IS NULL`,
			r.SQL,
		)
		assert.Nil(t, r.Args)
	})
}

// ─── BuildBulkUpsert ─────────────────────────────────────────────────────────

func TestSQLite_BuildBulkUpsert(t *testing.T) {
	s := New()

	t.Run("do update", func(t *testing.T) {
		r := s.BuildBulkUpsert("users",
			[]string{"id", "name", "email"},
			[][]any{
				{1, "Alice", "alice@example.com"},
				{2, "Bob", "bob@example.com"},
			},
			[]string{"id"},
			[]string{"name", "email"},
			false,
		)
		assert.Equal(t,
			`INSERT INTO "users" ("id", "name", "email") VALUES (?, ?, ?), (?, ?, ?) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name", "email" = EXCLUDED."email"`,
			r.SQL,
		)
		assert.Equal(t, []any{1, "Alice", "alice@example.com", 2, "Bob", "bob@example.com"}, r.Args)
	})

	t.Run("do nothing", func(t *testing.T) {
		r := s.BuildBulkUpsert("users",
			[]string{"id", "name"},
			[][]any{{1, "Alice"}},
			[]string{"id"},
			nil,
			true,
		)
		assert.Equal(t,
			`INSERT INTO "users" ("id", "name") VALUES (?, ?) ON CONFLICT ("id") DO NOTHING`,
			r.SQL,
		)
		assert.Equal(t, []any{1, "Alice"}, r.Args)
	})
}

// ─── GroupBy / Having / Aggregates ───────────────────────────────────────────

func TestSQLite_BuildSelectGroupBy(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:   "orders",
		Columns: []string{"category"},
		GroupBy: []string{"category"},
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggSum, Column: "amount", Alias: "total"},
		},
		Type: ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT "category", SUM("amount") AS "total" FROM "orders" GROUP BY "category"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestSQLite_BuildSelectHaving(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:   "orders",
		Columns: []string{"category"},
		GroupBy: []string{"category"},
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggCount, Column: "id", Alias: "cnt"},
		},
		Having: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "cnt",
				Op:     ast.OpGT,
				Value:  5,
			},
		},
		Type: ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT "category", COUNT("id") AS "cnt" FROM "orders" GROUP BY "category" HAVING "cnt" > ?`, result.SQL)
	assert.Equal(t, []any{5}, result.Args)
}

func TestSQLite_BuildSelectAggregateOnly(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table: "orders",
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggSum, Column: "amount", Alias: "total"},
		},
		Type: ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT SUM("amount") AS "total" FROM "orders"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestSQLite_BuildSelectAggregateNoAlias(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table: "orders",
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggCount, Column: "id"},
		},
		Type: ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT COUNT("id") FROM "orders"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestSQLite_BuildSelectAllAggFuncs(t *testing.T) {
	s := New()
	tests := []struct {
		fn       ast.AggFunc
		expected string
	}{
		{ast.AggSum, "SUM"},
		{ast.AggAvg, "AVG"},
		{ast.AggMin, "MIN"},
		{ast.AggMax, "MAX"},
		{ast.AggCount, "COUNT"},
	}
	for _, tt := range tests {
		node := ast.SelectNode{
			Table:      "t",
			Aggregates: []ast.AggregateExpr{{Func: tt.fn, Column: "v", Alias: "r"}},
			Type:       ast.QuerySelect,
		}
		result := s.BuildSelect(node)
		assert.Contains(t, result.SQL, tt.expected+"(")
	}
}

func TestSQLite_BuildSelectMultipleGroupByColumns(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:   "sales",
		Columns: []string{"region", "category"},
		GroupBy: []string{"region", "category"},
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggSum, Column: "revenue", Alias: "total_revenue"},
		},
		Type: ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t,
		`SELECT "region", "category", SUM("revenue") AS "total_revenue" FROM "sales" GROUP BY "region", "category"`,
		result.SQL,
	)
	assert.Nil(t, result.Args)
}

func TestSQLite_BuildSelectGroupByWithWhere(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:   "orders",
		Columns: []string{"status"},
		GroupBy: []string{"status"},
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggCount, Column: "id", Alias: "cnt"},
		},
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "created_at",
				Op:     ast.OpGTE,
				Value:  "2024-01-01",
			},
		},
		Type: ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t,
		`SELECT "status", COUNT("id") AS "cnt" FROM "orders" WHERE "created_at" >= ? GROUP BY "status"`,
		result.SQL,
	)
	assert.Equal(t, []any{"2024-01-01"}, result.Args)
}

func TestSQLite_BuildUpdate_DeduplicatesColumns(t *testing.T) {
	s := New()
	res := s.BuildUpdate("a_products",
		[]dialect.ColumnValue{
			{Column: "name", Value: "x"},
			{Column: "updated_by", Value: "alice"},
			{Column: "updated_by", Value: "bob"},
		},
		[]string{"id"}, []any{5})
	assert.Equal(t,
		`UPDATE "a_products" SET "name" = ?, "updated_by" = ? WHERE "id" = ?`,
		res.SQL)
	assert.Equal(t, []any{"x", "bob", 5}, res.Args)
}

func TestSQLite_BuildUpdateVersioned_DeduplicatesColumns(t *testing.T) {
	s := New()
	res := s.BuildUpdateVersioned("a_products",
		[]dialect.ColumnValue{
			{Column: "name", Value: "x"},
			{Column: "updated_by", Value: "alice"},
			{Column: "updated_by", Value: "bob"},
		},
		[]string{"id"}, []any{5}, "version", 2)
	assert.Equal(t,
		`UPDATE "a_products" SET "name" = ?, "updated_by" = ?, "version" = "version" + 1 WHERE "id" = ? AND "version" = ? RETURNING "version"`,
		res.SQL)
	assert.Equal(t, []any{"x", "bob", 5, 2}, res.Args)
}

func TestSQLite_BuildDeleteVersioned(t *testing.T) {
	s := New()
	res := s.BuildDeleteVersioned("v_products", []string{"id"}, []any{7}, "version", 3)
	assert.Equal(t,
		`DELETE FROM "v_products" WHERE "id" = ? AND "version" = ? RETURNING "id"`,
		res.SQL)
	assert.Equal(t, []any{7, 3}, res.Args)
}

func TestSQLite_BuildSoftDeleteVersioned(t *testing.T) {
	s := New()
	res := s.BuildSoftDeleteVersioned("v_products", []string{"id"}, []any{7}, "version", 3)
	assert.Equal(t,
		`UPDATE "v_products" SET "deleted_at" = CURRENT_TIMESTAMP, "version" = "version" + 1 WHERE "id" = ? AND "version" = ? RETURNING "id"`,
		res.SQL)
	assert.Equal(t, []any{7, 3}, res.Args)
}

func TestSQLite_BuildSelect_RawNoErrorInjection(t *testing.T) {
	s := New()
	raw := "note = 'huh?' AND n = ?"
	node := ast.SelectNode{
		Table:   "notes",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Raw:     &raw,
			RawArgs: []any{1},
		},
	}
	got := s.BuildSelect(node)
	assert.Equal(t, `SELECT "id" FROM "notes" WHERE note = 'huh?' AND n = ?`, got.SQL)
	assert.NotContains(t, got.SQL, "ERROR:")
	assert.Equal(t, []any{1}, got.Args)
}

func TestSQLite_BuildSelect_EmptyClauses(t *testing.T) {
	s := New()

	zeroNode := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where:   &ast.WhereClause{},
	}
	got := s.BuildSelect(zeroNode)
	assert.Equal(t, `SELECT "id" FROM "users"`, got.SQL)
	assert.Empty(t, got.Args)

	// Note: And() with no args produces Children: []WhereClause{} (non-nil empty slice).
	emptyAnd := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where:   &ast.WhereClause{LogicalOp: ast.LogicalAnd, Children: []ast.WhereClause{}},
	}
	got = s.BuildSelect(emptyAnd)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE 1`, got.SQL)

	// Note: Or() with no args produces Children: []WhereClause{} (non-nil empty slice).
	emptyOr := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where:   &ast.WhereClause{LogicalOp: ast.LogicalOr, Children: []ast.WhereClause{}},
	}
	got = s.BuildSelect(emptyOr)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE 0`, got.SQL)

	mixed := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			LogicalOp: ast.LogicalAnd,
			Children: []ast.WhereClause{
				{Comparison: &ast.ComparisonNode{Column: "name", Op: ast.OpEq, Value: "x"}},
				{},
			},
		},
	}
	got = s.BuildSelect(mixed)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE ("name" = ?)`, got.SQL)
	assert.Equal(t, []any{"x"}, got.Args)
}

func TestSQLite_BuildSelect_EmptyIn(t *testing.T) {
	s := New()

	inNode := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "role",
				Op:     ast.OpIn,
				Values: nil,
			},
		},
	}
	got := s.BuildSelect(inNode)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE 0`, got.SQL)
	assert.Empty(t, got.Args)

	notInNode := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "role",
				Op:     ast.OpNotIn,
				Values: []any{},
			},
		},
	}
	got = s.BuildSelect(notInNode)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE 1`, got.SQL)
	assert.Empty(t, got.Args)
}

// ─── Interface compliance ─────────────────────────────────────────────────────

func TestSQLite_ImplementsDialect(t *testing.T) {
	var _ dialect.Dialect = New()
}

func TestSQLite_BuildSelect_PartitionLimit(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:   "posts",
		Columns: []string{"id", "author_id", "title"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "author_id",
				Op:     ast.OpIn,
				Values: []any{1, 2},
			},
		},
		PartitionLimit: &ast.PartitionLimit{
			PartitionBy: "author_id",
			OrderBy:     []ast.OrderByExpr{{Column: "created_at", Direction: ast.Desc}},
			Limit:       3,
		},
	}
	result := d.BuildSelect(node)
	want := `SELECT "id", "author_id", "title" FROM (SELECT "id", "author_id", "title", ROW_NUMBER() OVER (PARTITION BY "author_id" ORDER BY "created_at" DESC) AS "_drel_rn" FROM "posts" WHERE "author_id" IN (?, ?)) AS "_drel_w" WHERE "_drel_rn" <= 3`
	assert.Equal(t, want, result.SQL)
	assert.Equal(t, []any{1, 2}, result.Args)
}

func TestSQLite_AdvisoryLockSQL_Unsupported(t *testing.T) {
	s := New()
	res, supported := s.AdvisoryLockSQL(42, dialect.AdvisoryLockBlocking)
	assert.False(t, supported)
	assert.Equal(t, dialect.Result{}, res)

	res, supported = s.AdvisoryLockSQL(42, dialect.AdvisoryLockTry)
	assert.False(t, supported)
	assert.Equal(t, dialect.Result{}, res)
}

func TestSQLite_BuildSelectDistinct(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:    "users",
		Columns:  []string{"city"},
		Distinct: true,
		Type:     ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT DISTINCT "city" FROM "users"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestSQLite_BuildSelectLeftJoin(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:   "users",
		Columns: []string{"users.name", "orders.total"},
		Joins: []ast.JoinNode{
			{Table: "orders", Type: ast.JoinLeft, On: `"orders"."user_id" = "users"."id"`},
		},
		Type: ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t,
		`SELECT "users"."name", "orders"."total" FROM "users" LEFT JOIN "orders" ON "orders"."user_id" = "users"."id"`,
		result.SQL,
	)
}

func TestSQLite_BuildSelectSumCoalesced(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:      "orders",
		Aggregates: []ast.AggregateExpr{{Func: ast.AggSum, Column: "amount", Alias: "result", CoalesceZero: true}},
		Type:       ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT COALESCE(SUM("amount"), 0) AS "result" FROM "orders"`, result.SQL)
}

func TestSQLite_BuildSelectCountDistinct(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:      "orders",
		Aggregates: []ast.AggregateExpr{{Func: ast.AggCount, Column: "user_id", Distinct: true, Alias: "buyers"}},
		Type:       ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT COUNT(DISTINCT "user_id") AS "buyers" FROM "orders"`, result.SQL)
}

func TestSQLite_BuildSelectCountStar(t *testing.T) {
	s := New()
	node := ast.SelectNode{
		Table:      "orders",
		Aggregates: []ast.AggregateExpr{{Func: ast.AggCount, Column: "", Alias: "cnt"}},
		Type:       ast.QuerySelect,
	}
	result := s.BuildSelect(node)
	assert.Equal(t, `SELECT COUNT(*) AS "cnt" FROM "orders"`, result.SQL)
}

// ─── normalizeArg (UTC conversion) ──────────────────────────────────────────

func TestNormalizeArg_TimeToUTC(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("time zone data unavailable:", err)
	}
	eastern := time.Date(2025, 6, 1, 12, 0, 0, 0, loc)
	result := normalizeArg(eastern)
	got, ok := result.(time.Time)
	assert.True(t, ok, "result should be time.Time")
	assert.Equal(t, time.UTC, got.Location(), "time.Time should be converted to UTC")
	assert.True(t, eastern.Equal(got), "UTC conversion must preserve the instant")
}

func TestNormalizeArg_UTCTimeUnchanged(t *testing.T) {
	utc := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	result := normalizeArg(utc)
	got, ok := result.(time.Time)
	assert.True(t, ok)
	assert.Equal(t, time.UTC, got.Location())
	assert.Equal(t, utc, got)
}

func TestNormalizeArg_NonTimePassthrough(t *testing.T) {
	assert.Equal(t, 42, normalizeArg(42))
	assert.Equal(t, "hello", normalizeArg("hello"))
	assert.Nil(t, normalizeArg(nil))
}

func TestWriteComparison_TimeArgsNormalized(t *testing.T) {
	// Verify that non-UTC time.Time values in Eq/GT/Between/In are normalised
	// to UTC when appended to args, so SQLite text comparisons are correct.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("time zone data unavailable:", err)
	}
	eastern := time.Date(2025, 6, 1, 12, 0, 0, 0, loc)
	want := eastern.UTC()

	s := New()

	// Eq (default branch)
	r := s.BuildSelect(ast.SelectNode{
		Table:   "products",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{Column: "created_at", Op: ast.OpEq, Value: eastern},
		},
	})
	assert.Equal(t, want, r.Args[0], "Eq: time.Time arg must be UTC")

	// GT
	r = s.BuildSelect(ast.SelectNode{
		Table:   "products",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{Column: "created_at", Op: ast.OpGT, Value: eastern},
		},
	})
	assert.Equal(t, want, r.Args[0], "GT: time.Time arg must be UTC")

	// Between
	r = s.BuildSelect(ast.SelectNode{
		Table:   "products",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "created_at",
				Op:     ast.OpBetween,
				Values: []any{eastern, eastern},
			},
		},
	})
	assert.Equal(t, want, r.Args[0], "Between low: time.Time arg must be UTC")
	assert.Equal(t, want, r.Args[1], "Between high: time.Time arg must be UTC")

	// In
	r = s.BuildSelect(ast.SelectNode{
		Table:   "products",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "created_at",
				Op:     ast.OpIn,
				Values: []any{eastern, eastern},
			},
		},
	})
	assert.Equal(t, want, r.Args[0], "In[0]: time.Time arg must be UTC")
	assert.Equal(t, want, r.Args[1], "In[1]: time.Time arg must be UTC")
}

func TestSQLite_BuildDelete_CompositeKey(t *testing.T) {
	s := New()
	got := s.BuildDelete("order_lines", []string{"order_id", "line_no"}, []any{3, 1})
	assert.Equal(t, `DELETE FROM "order_lines" WHERE "order_id" = ? AND "line_no" = ?`, got.SQL)
	assert.Equal(t, []any{3, 1}, got.Args)
}

func TestSQLite_BuildUpdate_CompositeKey(t *testing.T) {
	s := New()
	got := s.BuildUpdate("order_lines",
		[]dialect.ColumnValue{{Column: "qty", Value: 5}},
		[]string{"order_id", "line_no"}, []any{3, 1})
	assert.Equal(t, `UPDATE "order_lines" SET "qty" = ? WHERE "order_id" = ? AND "line_no" = ?`, got.SQL)
	assert.Equal(t, []any{5, 3, 1}, got.Args)
}

func TestSQLite_BuildSoftDelete_CompositeKey(t *testing.T) {
	s := New()
	got := s.BuildSoftDelete("order_lines", []string{"order_id", "line_no"}, []any{3, 1})
	assert.Equal(t, `UPDATE "order_lines" SET "deleted_at" = CURRENT_TIMESTAMP WHERE "order_id" = ? AND "line_no" = ?`, got.SQL)
	assert.Equal(t, []any{3, 1}, got.Args)
}

func TestSQLite_BuildDeleteVersioned_CompositeKeyReturnsTheFirstKeyColumn(t *testing.T) {
	s := New()
	got := s.BuildDeleteVersioned("order_lines", []string{"order_id", "line_no"}, []any{3, 1}, "version", 4)
	assert.Equal(t,
		`DELETE FROM "order_lines" WHERE "order_id" = ? AND "line_no" = ? AND "version" = ? RETURNING "order_id"`,
		got.SQL)
	assert.Equal(t, []any{3, 1, 4}, got.Args)
}

func TestSQLite_BuildSoftDeleteVersioned_CompositeKey(t *testing.T) {
	s := New()
	got := s.BuildSoftDeleteVersioned("order_lines", []string{"order_id", "line_no"}, []any{3, 1}, "version", 4)
	assert.Equal(t,
		`UPDATE "order_lines" SET "deleted_at" = CURRENT_TIMESTAMP, "version" = "version" + 1 WHERE "order_id" = ? AND "line_no" = ? AND "version" = ? RETURNING "order_id"`,
		got.SQL)
	assert.Equal(t, []any{3, 1, 4}, got.Args)
}

func TestSQLite_BuildUpdateVersioned_CompositeKey(t *testing.T) {
	s := New()
	got := s.BuildUpdateVersioned("order_lines",
		[]dialect.ColumnValue{{Column: "qty", Value: 5}},
		[]string{"order_id", "line_no"}, []any{3, 1}, "version", 4)
	assert.Equal(t,
		`UPDATE "order_lines" SET "qty" = ?, "version" = "version" + 1 WHERE "order_id" = ? AND "line_no" = ? AND "version" = ? RETURNING "version"`,
		got.SQL)
	assert.Equal(t, []any{5, 3, 1, 4}, got.Args)
}
