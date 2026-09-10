package sqlite

import (
	"fmt"
	"strings"
	"time"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dialect"
)

type SQLite struct{}

func New() *SQLite { return &SQLite{} }

func (s *SQLite) Name() string { return "sqlite" }

// dedupLastWins returns the change set with duplicate columns collapsed to a
// single entry, keeping the LAST assignment for each column and preserving the
// order of first appearance. This prevents "duplicate column name" SQL errors
// (e.g. an audit updated_by appended twice).
func dedupLastWins(changes []dialect.ColumnValue) []dialect.ColumnValue {
	idx := make(map[string]int, len(changes))
	out := make([]dialect.ColumnValue, 0, len(changes))
	for _, cv := range changes {
		if i, ok := idx[cv.Column]; ok {
			out[i] = cv
			continue
		}
		idx[cv.Column] = len(out)
		out = append(out, cv)
	}
	return out
}

func (s *SQLite) SupportsReturning() bool        { return true }
func (s *SQLite) UsesQuestionPlaceholders() bool { return true }

func (s *SQLite) Now() string { return "CURRENT_TIMESTAMP" }

func (s *SQLite) Explain(query string) (string, bool) { return "", false }

// AdvisoryLockSQL reports that SQLite has no advisory-lock primitive. SQLite
// serializes writers at the database level, so the runtime treats advisory
// locks as a documented no-op (returns supported=false). The mode argument is
// ignored.
func (s *SQLite) AdvisoryLockSQL(key int64, mode dialect.AdvisoryLockMode) (dialect.Result, bool) {
	return dialect.Result{}, false
}

func (s *SQLite) BuildSelect(node ast.SelectNode) dialect.Result {
	var b strings.Builder
	var args []any

	switch node.Type {
	case ast.QueryCount:
		b.WriteString("SELECT COUNT(*) FROM ")
		b.WriteString(quoteIdent(node.Table))
	case ast.QueryExists:
		b.WriteString("SELECT EXISTS(SELECT 1 FROM ")
		b.WriteString(quoteIdent(node.Table))
	default:
		b.WriteString("SELECT ")
		if node.Distinct {
			b.WriteString("DISTINCT ")
		}
		for i, col := range node.Columns {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteCol(col))
		}
		for aggIdx, agg := range node.Aggregates {
			if len(node.Columns) > 0 || aggIdx > 0 {
				b.WriteString(", ")
			}
			if agg.CoalesceZero {
				b.WriteString("COALESCE(")
			}
			b.WriteString(aggFuncSQL(agg.Func))
			b.WriteString("(")
			if agg.Func == ast.AggCount && agg.Column == "" {
				b.WriteString("*")
			} else {
				if agg.Distinct {
					b.WriteString("DISTINCT ")
				}
				b.WriteString(quoteIdent(agg.Column))
			}
			b.WriteString(")")
			if agg.CoalesceZero {
				b.WriteString(", 0)")
			}
			if agg.Alias != "" {
				b.WriteString(" AS ")
				b.WriteString(quoteIdent(agg.Alias))
			}
		}
		if node.PartitionLimit != nil {
			b.WriteString(", ROW_NUMBER() OVER (PARTITION BY ")
			b.WriteString(quoteIdent(node.PartitionLimit.PartitionBy))
			if len(node.PartitionLimit.OrderBy) > 0 {
				b.WriteString(" ORDER BY ")
				writeOrderBy(&b, node.PartitionLimit.OrderBy)
			}
			b.WriteString(") AS ")
			b.WriteString(quoteIdent("_drel_rn"))
		}
		b.WriteString(" FROM ")
		b.WriteString(quoteIdent(node.Table))
		for _, j := range node.Joins {
			switch j.Type {
			case ast.JoinLeft:
				b.WriteString(" LEFT JOIN ")
			default:
				b.WriteString(" INNER JOIN ")
			}
			b.WriteString(quoteIdent(j.Table))
			b.WriteString(" ON ")
			b.WriteString(j.On)
		}
	}

	if node.Where != nil && !isEmptyClause(*node.Where) {
		b.WriteString(" WHERE ")
		writeWhere(&b, &args, *node.Where)
	}

	if node.Type != ast.QueryExists && node.Type != ast.QueryCount {
		if len(node.GroupBy) > 0 {
			b.WriteString(" GROUP BY ")
			for i, col := range node.GroupBy {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(quoteIdent(col))
			}
		}
		if node.Having != nil && !isEmptyClause(*node.Having) {
			b.WriteString(" HAVING ")
			writeWhere(&b, &args, *node.Having)
		}
	}

	if node.Type == ast.QueryExists {
		if node.Limit != nil {
			b.WriteString(fmt.Sprintf(" LIMIT %d", *node.Limit))
		}
		b.WriteString(")")
		return dialect.Result{SQL: b.String(), Args: args}
	}

	// COUNT carries only WHERE — ordering/limit/offset would corrupt the single-row result.
	if node.Type == ast.QueryCount {
		return dialect.Result{SQL: b.String(), Args: args}
	}

	if len(node.OrderBy) > 0 {
		b.WriteString(" ORDER BY ")
		writeOrderBy(&b, node.OrderBy)
	}

	if node.Limit != nil {
		b.WriteString(fmt.Sprintf(" LIMIT %d", *node.Limit))
	}

	if node.Offset != nil {
		b.WriteString(fmt.Sprintf(" OFFSET %d", *node.Offset))
	}

	if node.PartitionLimit != nil {
		inner := b.String()
		wrapped := "SELECT "
		for i, col := range node.Columns {
			if i > 0 {
				wrapped += ", "
			}
			wrapped += quoteIdent(col)
		}
		wrapped += " FROM (" + inner + ") AS " + quoteIdent("_drel_w") +
			" WHERE " + quoteIdent("_drel_rn") + fmt.Sprintf(" <= %d", node.PartitionLimit.Limit)
		return dialect.Result{SQL: wrapped, Args: args}
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

// isEmptyClause reports whether a WhereClause contributes nothing to SQL.
// A zero-value WhereClause (Predicate{}) has Children == nil and no Comparison
// or Raw; it is empty. A WhereClause with a non-nil (possibly empty) Children
// slice represents an explicit And()/Or() and is NOT empty — the emitter renders
// it as the logical identity constant.
func isEmptyClause(clause ast.WhereClause) bool {
	if clause.Comparison != nil || clause.Raw != nil {
		return false
	}
	if clause.Children != nil {
		return false
	}
	return true
}

func writeOrderBy(b *strings.Builder, orderBy []ast.OrderByExpr) {
	for i, ob := range orderBy {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteCol(ob.Column))
		if ob.Direction == ast.Desc {
			b.WriteString(" DESC")
		}
		// SQLite supports NULLS FIRST/LAST since 3.30 (2019); libSQL/Turso
		// ship newer. Required for null-aware keyset pagination parity.
		switch ob.Nulls {
		case ast.NullsFirst:
			b.WriteString(" NULLS FIRST")
		case ast.NullsLast:
			b.WriteString(" NULLS LAST")
		}
	}
}

// writeWhere writes a WHERE clause for SQLite.
// SQLite uses ? as native placeholder so Raw predicates pass through unchanged.
func writeWhere(b *strings.Builder, args *[]any, clause ast.WhereClause) {
	if clause.Raw != nil {
		raw := *clause.Raw
		argIdx := 0
		state := 0 // 0=normal, 1=single-quote, 2=double-quote
		for i := 0; i < len(raw); i++ {
			ch := raw[i]
			switch state {
			case 0: // normal
				if ch == '\'' {
					state = 1
					b.WriteByte(ch)
				} else if ch == '"' {
					state = 2
					b.WriteByte(ch)
				} else if ch == '?' {
					if argIdx < len(clause.RawArgs) {
						b.WriteByte('?')
						*args = append(*args, clause.RawArgs[argIdx])
						argIdx++
					} else {
						b.WriteByte(ch)
					}
				} else {
					b.WriteByte(ch)
				}
			case 1: // single-quoted string
				b.WriteByte(ch)
				if ch == '\'' {
					if i+1 < len(raw) && raw[i+1] == '\'' {
						// escaped quote ''
						i++
						b.WriteByte(raw[i])
					} else {
						state = 0
					}
				}
			case 2: // double-quoted identifier
				b.WriteByte(ch)
				if ch == '"' {
					state = 0
				}
			}
		}
		return
	}

	if clause.Comparison != nil {
		writeComparison(b, args, *clause.Comparison)
		return
	}

	switch clause.LogicalOp {
	case ast.LogicalNot:
		b.WriteString("NOT (")
		writeWhere(b, args, clause.Children[0])
		b.WriteString(")")
	case ast.LogicalAnd, ast.LogicalOr:
		sep := " AND "
		identity := "1"
		if clause.LogicalOp == ast.LogicalOr {
			sep = " OR "
			identity = "0"
		}
		nonEmpty := make([]ast.WhereClause, 0, len(clause.Children))
		for _, child := range clause.Children {
			if !isEmptyClause(child) {
				nonEmpty = append(nonEmpty, child)
			}
		}
		if len(nonEmpty) == 0 {
			b.WriteString(identity)
			return
		}
		b.WriteString("(")
		for i, child := range nonEmpty {
			if i > 0 {
				b.WriteString(sep)
			}
			writeWhere(b, args, child)
		}
		b.WriteString(")")
	}
}

// normalizeArg converts a time.Time value to UTC so that comparisons against
// SQLite's CURRENT_TIMESTAMP (which stores UTC text) always match correctly.
// All other argument types are returned unchanged.
func normalizeArg(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.UTC()
	}
	return v
}

func writeComparison(b *strings.Builder, args *[]any, cmp ast.ComparisonNode) {
	col := quoteCol(cmp.Column)

	switch cmp.Op {
	case ast.OpIsNull:
		b.WriteString(col + " IS NULL")
	case ast.OpIsNotNull:
		b.WriteString(col + " IS NOT NULL")
	case ast.OpIn:
		if len(cmp.Values) == 0 {
			// Empty IN matches nothing.
			b.WriteString("0")
			break
		}
		b.WriteString(col + " IN (")
		for i, v := range cmp.Values {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("?")
			*args = append(*args, normalizeArg(v))
		}
		b.WriteString(")")
	case ast.OpNotIn:
		if len(cmp.Values) == 0 {
			// Empty NOT IN matches everything.
			b.WriteString("1")
			break
		}
		b.WriteString(col + " NOT IN (")
		for i, v := range cmp.Values {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("?")
			*args = append(*args, normalizeArg(v))
		}
		b.WriteString(")")
	case ast.OpBetween:
		b.WriteString(fmt.Sprintf("%s BETWEEN ? AND ?", col))
		*args = append(*args, normalizeArg(cmp.Values[0]), normalizeArg(cmp.Values[1]))
	default:
		op := operatorToSQL(cmp.Op)
		b.WriteString(fmt.Sprintf("%s %s ?", col, op))
		*args = append(*args, normalizeArg(cmp.Value))
	}
}

// operatorToSQL maps AST operators to SQLite SQL operator strings.
// ILIKE maps to LIKE because SQLite LIKE is case-insensitive for ASCII by default.
func operatorToSQL(op ast.Operator) string {
	switch op {
	case ast.OpEq:
		return "="
	case ast.OpNEQ:
		return "!="
	case ast.OpGT:
		return ">"
	case ast.OpGTE:
		return ">="
	case ast.OpLT:
		return "<"
	case ast.OpLTE:
		return "<="
	case ast.OpLike:
		return "LIKE"
	case ast.OpILike:
		// SQLite LIKE is case-insensitive for ASCII; no ILIKE keyword.
		return "LIKE"
	default:
		return "="
	}
}

func (s *SQLite) BuildInsert(table string, columns []string, values []any, returningCols []string) dialect.Result {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" (")
	for i, col := range columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteIdent(col))
	}
	b.WriteString(") VALUES (")
	for i := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("?")
	}
	b.WriteString(")")
	// SQLite 3.35+ (and libSQL/Turso) support RETURNING.
	if len(returningCols) > 0 {
		b.WriteString(" RETURNING ")
		for i, col := range returningCols {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteIdent(col))
		}
	}
	return dialect.Result{SQL: b.String(), Args: values}
}

// pkWhere renders an AND-joined equality clause over the primary key columns.
// A one-column key renders exactly as the old single-column code rendered it.
func pkWhere(pkColumns []string) string {
	parts := make([]string, len(pkColumns))
	for i, c := range pkColumns {
		parts[i] = fmt.Sprintf("%s = ?", quoteIdent(c))
	}
	return strings.Join(parts, " AND ")
}

func (s *SQLite) BuildUpdate(table string, changes []dialect.ColumnValue, pkColumns []string, pkValues []any) dialect.Result {
	changes = dedupLastWins(changes)
	var b strings.Builder
	var args []any
	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" SET ")
	for i, cv := range changes {
		if i > 0 {
			b.WriteString(", ")
		}
		if raw, ok := cv.Value.(dialect.RawExpr); ok {
			b.WriteString(fmt.Sprintf("%s = %s", quoteIdent(cv.Column), raw.SQL))
		} else {
			b.WriteString(fmt.Sprintf("%s = ?", quoteIdent(cv.Column)))
			args = append(args, cv.Value)
		}
	}
	b.WriteString(" WHERE ")
	b.WriteString(pkWhere(pkColumns))
	args = append(args, pkValues...)
	return dialect.Result{SQL: b.String(), Args: args}
}

func (s *SQLite) BuildDelete(table string, pkColumns []string, pkValues []any) dialect.Result {
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", quoteIdent(table), pkWhere(pkColumns))
	return dialect.Result{SQL: sql, Args: append([]any(nil), pkValues...)}
}

func (s *SQLite) BuildSoftDelete(table string, pkColumns []string, pkValues []any) dialect.Result {
	sql := fmt.Sprintf(
		"UPDATE %s SET %s = CURRENT_TIMESTAMP WHERE %s",
		quoteIdent(table), quoteIdent("deleted_at"), pkWhere(pkColumns),
	)
	return dialect.Result{SQL: sql, Args: append([]any(nil), pkValues...)}
}

// BuildDeleteVersioned generates a versioned DELETE for SQLite. SQLite 3.35+
// supports RETURNING; the primary key is returned so the mutation layer can
// detect a concurrency conflict via no-rows (mirroring Postgres).
func (s *SQLite) BuildDeleteVersioned(table string, pkColumns []string, pkValues []any, versionCol string, currentVersion int) dialect.Result {
	sql := fmt.Sprintf(
		"DELETE FROM %s WHERE %s AND %s = ? RETURNING %s",
		quoteIdent(table), pkWhere(pkColumns), quoteIdent(versionCol), quoteIdent(pkColumns[0]),
	)
	args := append(append([]any(nil), pkValues...), currentVersion)
	return dialect.Result{SQL: sql, Args: args}
}

// BuildSoftDeleteVersioned generates a versioned soft-delete for SQLite.
// SQLite 3.35+ supports RETURNING; the primary key is returned so the mutation
// layer can detect a concurrency conflict via no-rows (mirroring Postgres).
func (s *SQLite) BuildSoftDeleteVersioned(table string, pkColumns []string, pkValues []any, versionCol string, currentVersion int) dialect.Result {
	sql := fmt.Sprintf(
		"UPDATE %s SET %s = CURRENT_TIMESTAMP, %s = %s + 1 WHERE %s AND %s = ? RETURNING %s",
		quoteIdent(table), quoteIdent("deleted_at"),
		quoteIdent(versionCol), quoteIdent(versionCol),
		pkWhere(pkColumns), quoteIdent(versionCol),
		quoteIdent(pkColumns[0]),
	)
	args := append(append([]any(nil), pkValues...), currentVersion)
	return dialect.Result{SQL: sql, Args: args}
}

// BuildUpdateVersioned generates a versioned UPDATE for SQLite. SQLite 3.35+
// supports RETURNING, so the new version is returned and read back by the
// mutation layer (mirroring Postgres).
func (s *SQLite) BuildUpdateVersioned(table string, changes []dialect.ColumnValue, pkColumns []string, pkValues []any, versionCol string, currentVersion int) dialect.Result {
	changes = dedupLastWins(changes)
	var b strings.Builder
	var args []any

	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" SET ")

	for i, cv := range changes {
		if i > 0 {
			b.WriteString(", ")
		}
		if raw, ok := cv.Value.(dialect.RawExpr); ok {
			b.WriteString(fmt.Sprintf("%s = %s", quoteIdent(cv.Column), raw.SQL))
		} else {
			b.WriteString(fmt.Sprintf("%s = ?", quoteIdent(cv.Column)))
			args = append(args, cv.Value)
		}
	}

	b.WriteString(fmt.Sprintf(", %s = %s + 1", quoteIdent(versionCol), quoteIdent(versionCol)))

	b.WriteString(" WHERE ")
	b.WriteString(pkWhere(pkColumns))
	args = append(args, pkValues...)

	b.WriteString(fmt.Sprintf(" AND %s = ?", quoteIdent(versionCol)))
	args = append(args, currentVersion)

	// SQLite 3.35+ supports RETURNING; surface the incremented version so the
	// mutation layer can read it back like Postgres.
	b.WriteString(fmt.Sprintf(" RETURNING %s", quoteIdent(versionCol)))

	return dialect.Result{SQL: b.String(), Args: args}
}

func (s *SQLite) BuildBulkInsert(table string, columns []string, rows [][]any) dialect.Result {
	var b strings.Builder
	var args []any

	b.WriteString("INSERT INTO ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" (")
	for i, col := range columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteIdent(col))
	}
	b.WriteString(") VALUES ")

	for ri, row := range rows {
		if ri > 0 {
			b.WriteString(", ")
		}
		b.WriteString("(")
		for ci, val := range row {
			if ci > 0 {
				b.WriteString(", ")
			}
			b.WriteString("?")
			args = append(args, val)
		}
		b.WriteString(")")
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (s *SQLite) BuildBulkUpdate(table string, sets []dialect.ColumnValue, where *ast.WhereClause) dialect.Result {
	var b strings.Builder
	var args []any

	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" SET ")
	for i, cv := range sets {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("%s = ?", quoteIdent(cv.Column)))
		args = append(args, cv.Value)
	}

	if where != nil {
		b.WriteString(" WHERE ")
		writeWhere(&b, &args, *where)
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (s *SQLite) BuildBulkDelete(table string, where *ast.WhereClause) dialect.Result {
	var b strings.Builder
	var args []any

	b.WriteString("DELETE FROM ")
	b.WriteString(quoteIdent(table))

	if where != nil {
		b.WriteString(" WHERE ")
		writeWhere(&b, &args, *where)
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (s *SQLite) BuildBulkSoftDelete(table string, where *ast.WhereClause) dialect.Result {
	var b strings.Builder
	var args []any

	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" SET ")
	b.WriteString(quoteIdent("deleted_at"))
	b.WriteString(" = CURRENT_TIMESTAMP WHERE ")
	b.WriteString(quoteIdent("deleted_at"))
	b.WriteString(" IS NULL")

	if where != nil {
		b.WriteString(" AND ")
		writeWhere(&b, &args, *where)
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (s *SQLite) BuildBulkUpsert(table string, columns []string, rows [][]any, conflictCols []string, updateCols []string, doNothing bool) dialect.Result {
	result := s.BuildBulkInsert(table, columns, rows)

	var b strings.Builder
	b.WriteString(result.SQL)
	b.WriteString(" ON CONFLICT (")
	for i, col := range conflictCols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteIdent(col))
	}
	if doNothing {
		b.WriteString(") DO NOTHING")
		return dialect.Result{SQL: b.String(), Args: result.Args}
	}
	b.WriteString(") DO UPDATE SET ")
	for i, col := range updateCols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("%s = EXCLUDED.%s", quoteIdent(col), quoteIdent(col)))
	}

	return dialect.Result{SQL: b.String(), Args: result.Args}
}

func aggFuncSQL(f ast.AggFunc) string {
	switch f {
	case ast.AggSum:
		return "SUM"
	case ast.AggAvg:
		return "AVG"
	case ast.AggMin:
		return "MIN"
	case ast.AggMax:
		return "MAX"
	case ast.AggCount:
		return "COUNT"
	default:
		return "COUNT"
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteCol quotes a possibly table-qualified column reference. "users.name"
// becomes "users"."name"; a bare "name" becomes "name". Each segment is quoted
// independently so the dot is a separator, not part of an identifier.
func quoteCol(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return quoteIdent(name[:i]) + "." + quoteIdent(name[i+1:])
	}
	return quoteIdent(name)
}
