package postgres

import (
	"testing"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dialect"
	"github.com/stretchr/testify/assert"
)

func intPtr(n int) *int { return &n }

func TestPostgres_BuildSelect(t *testing.T) {
	pg := New()

	tests := []struct {
		name     string
		node     ast.SelectNode
		expected dialect.Result
	}{
		{
			name: "simple select all columns",
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
			name: "where eq",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "id",
						Op:     ast.OpEq,
						Value:  1,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE "id" = $1`,
				Args: []any{1},
			},
		},
		{
			name: "where neq",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "role" != $1`,
				Args: []any{"admin"},
			},
		},
		{
			name: "where gt",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "age" > $1`,
				Args: []any{18},
			},
		},
		{
			name: "where gte",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "age" >= $1`,
				Args: []any{18},
			},
		},
		{
			name: "where lt",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "age" < $1`,
				Args: []any{65},
			},
		},
		{
			name: "where lte",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "age" <= $1`,
				Args: []any{65},
			},
		},
		{
			name: "where like",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "name" LIKE $1`,
				Args: []any{"J%"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "role" IN ($1, $2)`,
				Args: []any{"admin", "mod"},
			},
		},
		{
			name: "where is null",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "deleted_at",
						Op:     ast.OpIsNull,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE "deleted_at" IS NULL`,
				Args: nil,
			},
		},
		{
			name: "where is not null",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "deleted_at",
						Op:     ast.OpIsNotNull,
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE "deleted_at" IS NOT NULL`,
				Args: nil,
			},
		},
		{
			name: "where AND two conditions",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalAnd,
					Children: []ast.WhereClause{
						{
							Comparison: &ast.ComparisonNode{
								Column: "age",
								Op:     ast.OpGTE,
								Value:  18,
							},
						},
						{
							Comparison: &ast.ComparisonNode{
								Column: "role",
								Op:     ast.OpEq,
								Value:  "admin",
							},
						},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE ("age" >= $1 AND "role" = $2)`,
				Args: []any{18, "admin"},
			},
		},
		{
			name: "where OR two conditions",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalOr,
					Children: []ast.WhereClause{
						{
							Comparison: &ast.ComparisonNode{
								Column: "role",
								Op:     ast.OpEq,
								Value:  "admin",
							},
						},
						{
							Comparison: &ast.ComparisonNode{
								Column: "age",
								Op:     ast.OpGTE,
								Value:  21,
							},
						},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE ("role" = $1 OR "age" >= $2)`,
				Args: []any{"admin", 21},
			},
		},
		{
			name: "where NOT",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalNot,
					Children: []ast.WhereClause{
						{
							Comparison: &ast.ComparisonNode{
								Column: "role",
								Op:     ast.OpEq,
								Value:  "banned",
							},
						},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE NOT ("role" = $1)`,
				Args: []any{"banned"},
			},
		},
		{
			name: "order by single asc",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{
					{Column: "name", Direction: ast.Asc},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" ORDER BY "name"`,
				Args: nil,
			},
		},
		{
			name: "order by single desc",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{
					{Column: "created_at", Direction: ast.Desc},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" ORDER BY "created_at" DESC`,
				Args: nil,
			},
		},
		{
			name: "order by multiple",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{
					{Column: "name", Direction: ast.Asc},
					{Column: "age", Direction: ast.Desc},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" ORDER BY "name", "age" DESC`,
				Args: nil,
			},
		},
		{
			name: "order by asc nulls last",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{
					{Column: "name", Direction: ast.Asc, Nulls: ast.NullsLast},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" ORDER BY "name" NULLS LAST`,
				Args: nil,
			},
		},
		{
			name: "order by desc nulls first",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				OrderBy: []ast.OrderByExpr{
					{Column: "rank", Direction: ast.Desc, Nulls: ast.NullsFirst},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" ORDER BY "rank" DESC NULLS FIRST`,
				Args: nil,
			},
		},
		{
			name: "limit only",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Limit:   intPtr(10),
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" LIMIT 10`,
				Args: nil,
			},
		},
		{
			name: "offset only",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Offset:  intPtr(20),
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" OFFSET 20`,
				Args: nil,
			},
		},
		{
			name: "limit and offset",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Limit:   intPtr(10),
				Offset:  intPtr(20),
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" LIMIT 10 OFFSET 20`,
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
					Comparison: &ast.ComparisonNode{
						Column: "role",
						Op:     ast.OpEq,
						Value:  "admin",
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT COUNT(*) FROM "users" WHERE "role" = $1`,
				Args: []any{"admin"},
			},
		},
		{
			name: "exists",
			node: ast.SelectNode{
				Table: "users",
				Type:  ast.QueryExists,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "email",
						Op:     ast.OpEq,
						Value:  "test@test.com",
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT EXISTS(SELECT 1 FROM "users" WHERE "email" = $1)`,
				Args: []any{"test@test.com"},
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
								{
									Comparison: &ast.ComparisonNode{
										Column: "role",
										Op:     ast.OpEq,
										Value:  "admin",
									},
								},
								{
									Comparison: &ast.ComparisonNode{
										Column: "role",
										Op:     ast.OpEq,
										Value:  "mod",
									},
								},
							},
						},
						{
							Comparison: &ast.ComparisonNode{
								Column: "age",
								Op:     ast.OpGTE,
								Value:  18,
							},
						},
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
				SQL:  `SELECT "id", "name", "email" FROM "users" WHERE (("role" = $1 OR "role" = $2) AND "age" >= $3) ORDER BY "name", "created_at" DESC LIMIT 25 OFFSET 50`,
				Args: []any{"admin", "mod", 18},
			},
		},
		{
			name: "empty where (no conditions)",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id"},
				Type:    ast.QuerySelect,
			},
			expected: dialect.Result{
				SQL:  `SELECT "id" FROM "users"`,
				Args: nil,
			},
		},
		{
			name: "where between (two sequential params)",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "age" BETWEEN $1 AND $2`,
				Args: []any{18, 65},
			},
		},
		{
			name: "where between after another param (index offset)",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					LogicalOp: ast.LogicalAnd,
					Children: []ast.WhereClause{
						{Comparison: &ast.ComparisonNode{Column: "role", Op: ast.OpEq, Value: "admin"}},
						{Comparison: &ast.ComparisonNode{Column: "age", Op: ast.OpBetween, Values: []any{18, 65}}},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE ("role" = $1 AND "age" BETWEEN $2 AND $3)`,
				Args: []any{"admin", 18, 65},
			},
		},
		{
			name: "where not in (multi placeholder)",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
				Type:    ast.QuerySelect,
				Where: &ast.WhereClause{
					Comparison: &ast.ComparisonNode{
						Column: "role",
						Op:     ast.OpNotIn,
						Values: []any{"banned", "spam", "bot"},
					},
				},
			},
			expected: dialect.Result{
				SQL:  `SELECT "id", "name" FROM "users" WHERE "role" NOT IN ($1, $2, $3)`,
				Args: []any{"banned", "spam", "bot"},
			},
		},
		{
			name: "where ilike",
			node: ast.SelectNode{
				Table:   "users",
				Columns: []string{"id", "name"},
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
				SQL:  `SELECT "id", "name" FROM "users" WHERE "name" ILIKE $1`,
				Args: []any{"j%"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pg.BuildSelect(tt.node)
			assert.Equal(t, tt.expected.SQL, result.SQL)
			assert.Equal(t, tt.expected.Args, result.Args)
		})
	}
}

func buildRawWhere(sql string, args ...any) dialect.Result {
	p := New()
	node := ast.SelectNode{
		Table:   "test",
		Columns: []string{"id"},
		Where: &ast.WhereClause{
			Raw:     &sql,
			RawArgs: args,
		},
		Type: ast.QuerySelect,
	}
	return p.BuildSelect(node)
}

func TestPostgres_Now(t *testing.T) {
	assert.Equal(t, "NOW()", New().Now())
}

func TestPostgres_SupportsReturning(t *testing.T) {
	assert.True(t, New().SupportsReturning())
}

func TestRawPlaceholder_Basic(t *testing.T) {
	r := buildRawWhere("age > ? AND name = ?", 18, "alice")
	assert.Equal(t, `SELECT "id" FROM "test" WHERE age > $1 AND name = $2`, r.SQL)
	assert.Equal(t, []any{18, "alice"}, r.Args)
}

func TestRawPlaceholder_InsideSingleQuotedString(t *testing.T) {
	r := buildRawWhere("name = '?' AND age > ?", 18)
	assert.Equal(t, `SELECT "id" FROM "test" WHERE name = '?' AND age > $1`, r.SQL)
	assert.Equal(t, []any{18}, r.Args)
}

func TestRawPlaceholder_InsideDoubleQuotedIdentifier(t *testing.T) {
	r := buildRawWhere(`"col?" = ?`, 42)
	assert.Equal(t, `SELECT "id" FROM "test" WHERE "col?" = $1`, r.SQL)
	assert.Equal(t, []any{42}, r.Args)
}

func TestRawPlaceholder_InsideDollarQuotedString(t *testing.T) {
	r := buildRawWhere("body = $$hello ? world$$ AND id = ?", 1)
	assert.Equal(t, `SELECT "id" FROM "test" WHERE body = $$hello ? world$$ AND id = $1`, r.SQL)
	assert.Equal(t, []any{1}, r.Args)
}

func TestRawPlaceholder_EscapedSingleQuote(t *testing.T) {
	r := buildRawWhere("name = 'it''s a ?' AND id = ?", 1)
	assert.Equal(t, `SELECT "id" FROM "test" WHERE name = 'it''s a ?' AND id = $1`, r.SQL)
	assert.Equal(t, []any{1}, r.Args)
}

func TestBuildSelectGroupBy(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:   "orders",
		Columns: []string{"category"},
		GroupBy: []string{"category"},
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggSum, Column: "amount", Alias: "total"},
		},
		Type: ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT "category", SUM("amount") AS "total" FROM "orders" GROUP BY "category"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestBuildSelectHaving(t *testing.T) {
	d := New()
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
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT "category", COUNT("id") AS "cnt" FROM "orders" GROUP BY "category" HAVING "cnt" > $1`, result.SQL)
	assert.Equal(t, []any{5}, result.Args)
}

func TestBuildSelectAggregateOnly(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table: "orders",
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggSum, Column: "amount", Alias: "total"},
		},
		Type: ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT SUM("amount") AS "total" FROM "orders"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestBuildSelectAggregateNoAlias(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table: "orders",
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggCount, Column: "id"},
		},
		Type: ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT COUNT("id") FROM "orders"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestBuildSelectAllAggFuncs(t *testing.T) {
	d := New()
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
		result := d.BuildSelect(node)
		assert.Contains(t, result.SQL, tt.expected+"(")
	}
}

func TestBuildSelectMultipleGroupByColumns(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:   "sales",
		Columns: []string{"region", "category"},
		GroupBy: []string{"region", "category"},
		Aggregates: []ast.AggregateExpr{
			{Func: ast.AggSum, Column: "revenue", Alias: "total_revenue"},
		},
		Type: ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t,
		`SELECT "region", "category", SUM("revenue") AS "total_revenue" FROM "sales" GROUP BY "region", "category"`,
		result.SQL,
	)
	assert.Nil(t, result.Args)
}

func TestBuildSelectGroupByWithWhere(t *testing.T) {
	d := New()
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
	result := d.BuildSelect(node)
	assert.Equal(t,
		`SELECT "status", COUNT("id") AS "cnt" FROM "orders" WHERE "created_at" >= $1 GROUP BY "status"`,
		result.SQL,
	)
	assert.Equal(t, []any{"2024-01-01"}, result.Args)
}

func TestPostgres_BuildUpdate_DeduplicatesColumns(t *testing.T) {
	pg := New()
	res := pg.BuildUpdate("a_products",
		[]dialect.ColumnValue{
			{Column: "name", Value: "x"},
			{Column: "updated_by", Value: "alice"},
			{Column: "updated_by", Value: "bob"}, // duplicate: last wins
		},
		[]string{"id"}, []any{5})
	// Single updated_by assignment, keeping the last value (bob -> $3 ... pkVal -> $4).
	assert.Equal(t,
		`UPDATE "a_products" SET "name" = $1, "updated_by" = $2 WHERE "id" = $3`,
		res.SQL)
	assert.Equal(t, []any{"x", "bob", 5}, res.Args)
}

func TestPostgres_BuildUpdateVersioned_DeduplicatesColumns(t *testing.T) {
	pg := New()
	res := pg.BuildUpdateVersioned("a_products",
		[]dialect.ColumnValue{
			{Column: "name", Value: "x"},
			{Column: "updated_by", Value: "alice"},
			{Column: "updated_by", Value: "bob"},
		},
		[]string{"id"}, []any{5}, "version", 2)
	assert.Equal(t,
		`UPDATE "a_products" SET "name" = $1, "updated_by" = $2, "version" = "version" + 1 WHERE "id" = $3 AND "version" = $4 RETURNING "version"`,
		res.SQL)
	assert.Equal(t, []any{"x", "bob", 5, 2}, res.Args)
}

func TestPostgres_BuildDeleteVersioned(t *testing.T) {
	pg := New()
	res := pg.BuildDeleteVersioned("v_products", []string{"id"}, []any{7}, "version", 3)
	assert.Equal(t,
		`DELETE FROM "v_products" WHERE "id" = $1 AND "version" = $2 RETURNING "id"`,
		res.SQL)
	assert.Equal(t, []any{7, 3}, res.Args)
}

func TestPostgres_BuildSoftDeleteVersioned(t *testing.T) {
	pg := New()
	res := pg.BuildSoftDeleteVersioned("v_products", []string{"id"}, []any{7}, "version", 3)
	assert.Equal(t,
		`UPDATE "v_products" SET "deleted_at" = NOW(), "version" = "version" + 1 WHERE "id" = $1 AND "version" = $2 RETURNING "id"`,
		res.SQL)
	assert.Equal(t, []any{7, 3}, res.Args)
}

func TestPostgres_BuildSelect_RawNoErrorInjection(t *testing.T) {
	pg := New()
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
	got := pg.BuildSelect(node)
	assert.Equal(t, `SELECT "id" FROM "notes" WHERE note = 'huh?' AND n = $1`, got.SQL)
	assert.NotContains(t, got.SQL, "ERROR:")
	assert.Equal(t, []any{1}, got.Args)
}

func TestPostgres_BuildSelect_EmptyClauses(t *testing.T) {
	pg := New()

	// Zero-value WhereClause (from Predicate{}) -> no WHERE at all.
	zeroNode := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where:   &ast.WhereClause{},
	}
	got := pg.BuildSelect(zeroNode)
	assert.Equal(t, `SELECT "id" FROM "users"`, got.SQL)
	assert.Empty(t, got.Args)

	// Empty And() -> all-empty children -> TRUE.
	// Note: And() with no args produces Children: []WhereClause{} (non-nil empty slice).
	emptyAnd := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where:   &ast.WhereClause{LogicalOp: ast.LogicalAnd, Children: []ast.WhereClause{}},
	}
	got = pg.BuildSelect(emptyAnd)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE TRUE`, got.SQL)

	// Empty Or() -> all-empty children -> FALSE.
	// Note: Or() with no args produces Children: []WhereClause{} (non-nil empty slice).
	emptyOr := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where:   &ast.WhereClause{LogicalOp: ast.LogicalOr, Children: []ast.WhereClause{}},
	}
	got = pg.BuildSelect(emptyOr)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE FALSE`, got.SQL)

	// Real predicate ANDed with a zero Predicate{} -> just the real predicate.
	mixed := ast.SelectNode{
		Table:   "users",
		Columns: []string{"id"},
		Type:    ast.QuerySelect,
		Where: &ast.WhereClause{
			LogicalOp: ast.LogicalAnd,
			Children: []ast.WhereClause{
				{Comparison: &ast.ComparisonNode{Column: "name", Op: ast.OpEq, Value: "x"}},
				{}, // zero Predicate{}
			},
		},
	}
	got = pg.BuildSelect(mixed)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE ("name" = $1)`, got.SQL)
	assert.Equal(t, []any{"x"}, got.Args)
}

func TestPostgres_BuildSelect_EmptyIn(t *testing.T) {
	pg := New()

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
	got := pg.BuildSelect(inNode)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE FALSE`, got.SQL)
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
	got = pg.BuildSelect(notInNode)
	assert.Equal(t, `SELECT "id" FROM "users" WHERE TRUE`, got.SQL)
	assert.Empty(t, got.Args)
}

func TestRawPlaceholder_MismatchDoesNotInjectError(t *testing.T) {
	// After removing the ERROR: fallback, a mismatch (constructed directly via
	// AST, bypassing Raw's panic) emits partial SQL but does NOT inject "ERROR:".
	// In normal usage Raw() panics before reaching the emitter, so this path is
	// only reachable via direct AST construction.
	p := New()
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
	r := p.BuildSelect(node)
	assert.NotContains(t, r.SQL, "ERROR:")
}

func TestPostgres_BuildSelect_PartitionLimit(t *testing.T) {
	pg := New()
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
	result := pg.BuildSelect(node)
	want := `SELECT "id", "author_id", "title" FROM (SELECT "id", "author_id", "title", ROW_NUMBER() OVER (PARTITION BY "author_id" ORDER BY "created_at" DESC) AS "_drel_rn" FROM "posts" WHERE "author_id" IN ($1, $2)) AS "_drel_w" WHERE "_drel_rn" <= 3`
	assert.Equal(t, want, result.SQL)
	assert.Equal(t, []any{1, 2}, result.Args)
}

func TestPostgres_BuildSelect_PartitionLimit_DefaultOrderByPK(t *testing.T) {
	pg := New()
	node := ast.SelectNode{
		Table:   "posts",
		Columns: []string{"id", "author_id"},
		Type:    ast.QuerySelect,
		PartitionLimit: &ast.PartitionLimit{
			PartitionBy: "author_id",
			OrderBy:     []ast.OrderByExpr{{Column: "id", Direction: ast.Asc}},
			Limit:       5,
		},
	}
	result := pg.BuildSelect(node)
	want := `SELECT "id", "author_id" FROM (SELECT "id", "author_id", ROW_NUMBER() OVER (PARTITION BY "author_id" ORDER BY "id") AS "_drel_rn" FROM "posts") AS "_drel_w" WHERE "_drel_rn" <= 5`
	assert.Equal(t, want, result.SQL)
}

func TestPostgres_BuildBulkUpsert(t *testing.T) {
	pg := New()

	t.Run("do update", func(t *testing.T) {
		r := pg.BuildBulkUpsert("users",
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
			`INSERT INTO "users" ("id", "name", "email") VALUES ($1, $2, $3), ($4, $5, $6) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name", "email" = EXCLUDED."email"`,
			r.SQL,
		)
		assert.Equal(t, []any{1, "Alice", "alice@example.com", 2, "Bob", "bob@example.com"}, r.Args)
	})

	t.Run("do nothing", func(t *testing.T) {
		r := pg.BuildBulkUpsert("users",
			[]string{"id", "name"},
			[][]any{{1, "Alice"}},
			[]string{"id"},
			nil,
			true,
		)
		assert.Equal(t,
			`INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO NOTHING`,
			r.SQL,
		)
		assert.Equal(t, []any{1, "Alice"}, r.Args)
	})
}

func TestPostgres_AdvisoryLockSQL_Blocking(t *testing.T) {
	pg := New()
	res, supported := pg.AdvisoryLockSQL(42, dialect.AdvisoryLockBlocking)
	assert.True(t, supported)
	assert.Equal(t, "SELECT pg_advisory_xact_lock($1)", res.SQL)
	assert.Equal(t, []any{int64(42)}, res.Args)
}

func TestPostgres_AdvisoryLockSQL_Try(t *testing.T) {
	pg := New()
	res, supported := pg.AdvisoryLockSQL(7, dialect.AdvisoryLockTry)
	assert.True(t, supported)
	assert.Equal(t, "SELECT pg_try_advisory_xact_lock($1)", res.SQL)
	assert.Equal(t, []any{int64(7)}, res.Args)
}

func TestBuildSelectDistinct(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:    "users",
		Columns:  []string{"city"},
		Distinct: true,
		Type:     ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT DISTINCT "city" FROM "users"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestBuildSelectQualifiedWhere(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:   "orders",
		Columns: []string{"orders.amount"},
		Where: &ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: "orders.amount",
				Op:     ast.OpGTE,
				Value:  20.0,
			},
		},
		Type: ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT "orders"."amount" FROM "orders" WHERE "orders"."amount" >= $1`, result.SQL)
	assert.Equal(t, []any{20.0}, result.Args)
}

func TestBuildSelectLeftJoin(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:   "users",
		Columns: []string{"users.name", "orders.total"},
		Joins: []ast.JoinNode{
			{Table: "orders", Type: ast.JoinLeft, On: `"orders"."user_id" = "users"."id"`},
		},
		Type: ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t,
		`SELECT "users"."name", "orders"."total" FROM "users" LEFT JOIN "orders" ON "orders"."user_id" = "users"."id"`,
		result.SQL,
	)
}

func TestBuildSelectInnerJoin(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:   "users",
		Columns: []string{"users.name"},
		Joins: []ast.JoinNode{
			{Table: "orders", Type: ast.JoinInner, On: `"orders"."user_id" = "users"."id"`},
		},
		Type: ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t,
		`SELECT "users"."name" FROM "users" INNER JOIN "orders" ON "orders"."user_id" = "users"."id"`,
		result.SQL,
	)
}

func TestBuildSelectSumCoalesced(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:      "orders",
		Aggregates: []ast.AggregateExpr{{Func: ast.AggSum, Column: "amount", Alias: "result", CoalesceZero: true}},
		Type:       ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT COALESCE(SUM("amount"), 0) AS "result" FROM "orders"`, result.SQL)
}

func TestBuildSelectCountDistinct(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:      "orders",
		Aggregates: []ast.AggregateExpr{{Func: ast.AggCount, Column: "user_id", Distinct: true, Alias: "buyers"}},
		Type:       ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT COUNT(DISTINCT "user_id") AS "buyers" FROM "orders"`, result.SQL)
}

func TestBuildSelectCountStar(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:      "orders",
		Aggregates: []ast.AggregateExpr{{Func: ast.AggCount, Column: "", Alias: "cnt"}},
		Type:       ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT COUNT(*) AS "cnt" FROM "orders"`, result.SQL)
	assert.Nil(t, result.Args)
}

func TestBuildSelectCountStarInGroupBy(t *testing.T) {
	d := New()
	node := ast.SelectNode{
		Table:      "orders",
		Columns:    []string{"status"},
		GroupBy:    []string{"status"},
		Aggregates: []ast.AggregateExpr{{Func: ast.AggCount, Column: "", Alias: "cnt"}},
		Type:       ast.QuerySelect,
	}
	result := d.BuildSelect(node)
	assert.Equal(t, `SELECT "status", COUNT(*) AS "cnt" FROM "orders" GROUP BY "status"`, result.SQL)
}

func TestPostgres_BuildDelete_CompositeKey(t *testing.T) {
	p := New()
	got := p.BuildDelete("order_lines", []string{"order_id", "line_no"}, []any{3, 1})
	assert.Equal(t, `DELETE FROM "order_lines" WHERE "order_id" = $1 AND "line_no" = $2`, got.SQL)
	assert.Equal(t, []any{3, 1}, got.Args)
}

func TestPostgres_BuildUpdate_CompositeKeyNumbersParamsAfterTheSets(t *testing.T) {
	p := New()
	got := p.BuildUpdate("order_lines",
		[]dialect.ColumnValue{{Column: "qty", Value: 5}},
		[]string{"order_id", "line_no"}, []any{3, 1})
	assert.Equal(t, `UPDATE "order_lines" SET "qty" = $1 WHERE "order_id" = $2 AND "line_no" = $3`, got.SQL)
	assert.Equal(t, []any{5, 3, 1}, got.Args)
}

func TestPostgres_BuildSoftDelete_CompositeKey(t *testing.T) {
	p := New()
	got := p.BuildSoftDelete("order_lines", []string{"order_id", "line_no"}, []any{3, 1})
	assert.Equal(t, `UPDATE "order_lines" SET "deleted_at" = NOW() WHERE "order_id" = $1 AND "line_no" = $2`, got.SQL)
	assert.Equal(t, []any{3, 1}, got.Args)
}

func TestPostgres_BuildDeleteVersioned_CompositeKeyReturnsTheFirstKeyColumn(t *testing.T) {
	p := New()
	got := p.BuildDeleteVersioned("order_lines", []string{"order_id", "line_no"}, []any{3, 1}, "version", 4)
	assert.Equal(t,
		`DELETE FROM "order_lines" WHERE "order_id" = $1 AND "line_no" = $2 AND "version" = $3 RETURNING "order_id"`,
		got.SQL)
	assert.Equal(t, []any{3, 1, 4}, got.Args)
}

func TestPostgres_BuildSoftDeleteVersioned_CompositeKey(t *testing.T) {
	p := New()
	got := p.BuildSoftDeleteVersioned("order_lines", []string{"order_id", "line_no"}, []any{3, 1}, "version", 4)
	assert.Equal(t,
		`UPDATE "order_lines" SET "deleted_at" = NOW(), "version" = "version" + 1 WHERE "order_id" = $1 AND "line_no" = $2 AND "version" = $3 RETURNING "order_id"`,
		got.SQL)
	assert.Equal(t, []any{3, 1, 4}, got.Args)
}

func TestPostgres_BuildUpdateVersioned_CompositeKey(t *testing.T) {
	p := New()
	got := p.BuildUpdateVersioned("order_lines",
		[]dialect.ColumnValue{{Column: "qty", Value: 5}},
		[]string{"order_id", "line_no"}, []any{3, 1}, "version", 4)
	assert.Equal(t,
		`UPDATE "order_lines" SET "qty" = $1, "version" = "version" + 1 WHERE "order_id" = $2 AND "line_no" = $3 AND "version" = $4 RETURNING "version"`,
		got.SQL)
	assert.Equal(t, []any{5, 3, 1, 4}, got.Args)
}
