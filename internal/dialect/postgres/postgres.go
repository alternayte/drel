package postgres

import (
	"fmt"
	"strings"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dialect"
)

type Postgres struct{}

func New() *Postgres { return &Postgres{} }

func (p *Postgres) Name() string { return "postgres" }

// dedupLastWins returns the change set with duplicate columns collapsed to a
// single entry, keeping the LAST assignment for each column and preserving the
// order of first appearance. This prevents "multiple assignments to the same
// column" SQL errors (e.g. an audit updated_by appended twice).
func dedupLastWins(changes []dialect.ColumnValue) []dialect.ColumnValue {
	idx := make(map[string]int, len(changes))
	out := make([]dialect.ColumnValue, 0, len(changes))
	for _, cv := range changes {
		if i, ok := idx[cv.Column]; ok {
			out[i] = cv // last wins; keep original position
			continue
		}
		idx[cv.Column] = len(out)
		out = append(out, cv)
	}
	return out
}

func (p *Postgres) SupportsReturning() bool        { return true }
func (p *Postgres) UsesQuestionPlaceholders() bool { return false }

func (p *Postgres) Now() string { return "NOW()" }

func (p *Postgres) Explain(query string) (string, bool) { return "EXPLAIN " + query, true }

// AdvisoryLockSQL returns the SQL to acquire a transaction-scoped advisory lock.
// Transaction-scoped locks (pg_advisory_xact_lock) auto-release on commit or
// rollback, so the runtime never needs an explicit unlock.
func (p *Postgres) AdvisoryLockSQL(key int64, mode dialect.AdvisoryLockMode) (dialect.Result, bool) {
	fn := "pg_advisory_xact_lock"
	if mode == dialect.AdvisoryLockTry {
		fn = "pg_try_advisory_xact_lock"
	}
	return dialect.Result{
		SQL:  "SELECT " + fn + "($1)",
		Args: []any{key},
	}, true
}

func (p *Postgres) BuildSelect(node ast.SelectNode) dialect.Result {
	var b strings.Builder
	var args []any
	paramIdx := 1

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
		paramIdx = writeWhere(&b, &args, *node.Where, paramIdx)
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
			paramIdx = writeWhere(&b, &args, *node.Having, paramIdx)
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

// isEmptyClause reports whether a WhereClause contributes nothing to SQL:
// no comparison, no raw fragment, and Children is nil (zero-value Predicate{}).
// A WhereClause with a non-nil (but possibly empty) Children slice is NOT empty —
// it represents an explicit And()/Or() call, which the emitter renders as the
// logical identity (TRUE for And, FALSE for Or). This distinction matters because
// LogicalAnd == 0, so we cannot tell apart Predicate{} from And() by LogicalOp
// alone; the nil vs. non-nil Children slice is the carrier.
func isEmptyClause(clause ast.WhereClause) bool {
	if clause.Comparison != nil || clause.Raw != nil {
		return false
	}
	if clause.Children != nil {
		// Non-nil slice means an explicit And()/Or(); recurse to check if all
		// children are themselves empty — the emitter will collapse them to an
		// identity constant, which is valid SQL.
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
		switch ob.Nulls {
		case ast.NullsFirst:
			b.WriteString(" NULLS FIRST")
		case ast.NullsLast:
			b.WriteString(" NULLS LAST")
		}
	}
}

func writeWhere(b *strings.Builder, args *[]any, clause ast.WhereClause, paramIdx int) int {
	if clause.Raw != nil {
		raw := *clause.Raw
		argIdx := 0
		state := 0 // 0=normal, 1=single-quote, 2=double-quote, 3=dollar-quote
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
				} else if ch == '$' && i+1 < len(raw) && raw[i+1] == '$' {
					state = 3
					b.WriteByte(ch)
					i++
					b.WriteByte(raw[i])
				} else if ch == '?' {
					if argIdx < len(clause.RawArgs) {
						b.WriteString(fmt.Sprintf("$%d", paramIdx))
						*args = append(*args, clause.RawArgs[argIdx])
						argIdx++
						paramIdx++
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
			case 3: // dollar-quoted string
				b.WriteByte(ch)
				if ch == '$' && i+1 < len(raw) && raw[i+1] == '$' {
					i++
					b.WriteByte(raw[i])
					state = 0
				}
			}
		}
		return paramIdx
	}

	if clause.Comparison != nil {
		return writeComparison(b, args, *clause.Comparison, paramIdx)
	}

	switch clause.LogicalOp {
	case ast.LogicalNot:
		b.WriteString("NOT (")
		paramIdx = writeWhere(b, args, clause.Children[0], paramIdx)
		b.WriteString(")")
	case ast.LogicalAnd, ast.LogicalOr:
		sep := " AND "
		identity := "TRUE"
		if clause.LogicalOp == ast.LogicalOr {
			sep = " OR "
			identity = "FALSE"
		}
		nonEmpty := make([]ast.WhereClause, 0, len(clause.Children))
		for _, child := range clause.Children {
			if !isEmptyClause(child) {
				nonEmpty = append(nonEmpty, child)
			}
		}
		if len(nonEmpty) == 0 {
			b.WriteString(identity)
			return paramIdx
		}
		b.WriteString("(")
		for i, child := range nonEmpty {
			if i > 0 {
				b.WriteString(sep)
			}
			paramIdx = writeWhere(b, args, child, paramIdx)
		}
		b.WriteString(")")
	}

	return paramIdx
}

func writeComparison(b *strings.Builder, args *[]any, cmp ast.ComparisonNode, paramIdx int) int {
	col := quoteCol(cmp.Column)

	switch cmp.Op {
	case ast.OpIsNull:
		b.WriteString(col + " IS NULL")
	case ast.OpIsNotNull:
		b.WriteString(col + " IS NOT NULL")
	case ast.OpIn:
		if len(cmp.Values) == 0 {
			// Empty IN matches nothing.
			b.WriteString("FALSE")
			break
		}
		b.WriteString(col + " IN (")
		for i, v := range cmp.Values {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("$%d", paramIdx))
			*args = append(*args, v)
			paramIdx++
		}
		b.WriteString(")")
	case ast.OpNotIn:
		if len(cmp.Values) == 0 {
			// Empty NOT IN matches everything.
			b.WriteString("TRUE")
			break
		}
		b.WriteString(col + " NOT IN (")
		for i, v := range cmp.Values {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("$%d", paramIdx))
			*args = append(*args, v)
			paramIdx++
		}
		b.WriteString(")")
	case ast.OpBetween:
		b.WriteString(fmt.Sprintf("%s BETWEEN $%d AND $%d", col, paramIdx, paramIdx+1))
		*args = append(*args, cmp.Values[0], cmp.Values[1])
		paramIdx += 2
	default:
		op := operatorToSQL(cmp.Op)
		b.WriteString(fmt.Sprintf("%s %s $%d", col, op, paramIdx))
		*args = append(*args, cmp.Value)
		paramIdx++
	}

	return paramIdx
}

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
		return "ILIKE"
	default:
		return "="
	}
}

func (p *Postgres) BuildInsert(table string, columns []string, values []any, returningCols []string) dialect.Result {
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
		b.WriteString(fmt.Sprintf("$%d", i+1))
	}
	b.WriteString(")")
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

// pkWhere renders an AND-joined equality clause over the primary key columns,
// numbering placeholders from startIdx. It returns the clause text (without a
// leading "WHERE") and the next free placeholder index. A one-column key
// renders exactly as the old single-column code rendered it.
func pkWhere(pkColumns []string, startIdx int) (string, int) {
	var b strings.Builder
	idx := startIdx
	for i, c := range pkColumns {
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString(fmt.Sprintf("%s = $%d", quoteIdent(c), idx))
		idx++
	}
	return b.String(), idx
}

func (p *Postgres) BuildUpdate(table string, changes []dialect.ColumnValue, pkColumns []string, pkValues []any) dialect.Result {
	changes = dedupLastWins(changes)
	var b strings.Builder
	var args []any
	paramIdx := 1
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
			b.WriteString(fmt.Sprintf("%s = $%d", quoteIdent(cv.Column), paramIdx))
			args = append(args, cv.Value)
			paramIdx++
		}
	}
	where, _ := pkWhere(pkColumns, paramIdx)
	b.WriteString(" WHERE ")
	b.WriteString(where)
	args = append(args, pkValues...)
	return dialect.Result{SQL: b.String(), Args: args}
}

func (p *Postgres) BuildDelete(table string, pkColumns []string, pkValues []any) dialect.Result {
	where, _ := pkWhere(pkColumns, 1)
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", quoteIdent(table), where)
	return dialect.Result{SQL: sql, Args: append([]any(nil), pkValues...)}
}

func (p *Postgres) BuildSoftDelete(table string, pkColumns []string, pkValues []any) dialect.Result {
	where, _ := pkWhere(pkColumns, 1)
	sql := fmt.Sprintf("UPDATE %s SET %s = NOW() WHERE %s",
		quoteIdent(table), quoteIdent("deleted_at"), where)
	return dialect.Result{SQL: sql, Args: append([]any(nil), pkValues...)}
}

func (p *Postgres) BuildDeleteVersioned(table string, pkColumns []string, pkValues []any, versionCol string, currentVersion int) dialect.Result {
	where, idx := pkWhere(pkColumns, 1)
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s AND %s = $%d RETURNING %s",
		quoteIdent(table), where, quoteIdent(versionCol), idx, quoteIdent(pkColumns[0]))
	args := append(append([]any(nil), pkValues...), currentVersion)
	return dialect.Result{SQL: sql, Args: args}
}

func (p *Postgres) BuildSoftDeleteVersioned(table string, pkColumns []string, pkValues []any, versionCol string, currentVersion int) dialect.Result {
	where, idx := pkWhere(pkColumns, 1)
	sql := fmt.Sprintf("UPDATE %s SET %s = NOW(), %s = %s + 1 WHERE %s AND %s = $%d RETURNING %s",
		quoteIdent(table), quoteIdent("deleted_at"),
		quoteIdent(versionCol), quoteIdent(versionCol),
		where, quoteIdent(versionCol), idx, quoteIdent(pkColumns[0]))
	args := append(append([]any(nil), pkValues...), currentVersion)
	return dialect.Result{SQL: sql, Args: args}
}

func (p *Postgres) BuildUpdateVersioned(table string, changes []dialect.ColumnValue, pkColumns []string, pkValues []any, versionCol string, currentVersion int) dialect.Result {
	changes = dedupLastWins(changes)
	var b strings.Builder
	var args []any
	paramIdx := 1

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
			b.WriteString(fmt.Sprintf("%s = $%d", quoteIdent(cv.Column), paramIdx))
			args = append(args, cv.Value)
			paramIdx++
		}
	}

	b.WriteString(fmt.Sprintf(", %s = %s + 1", quoteIdent(versionCol), quoteIdent(versionCol)))

	where, nextIdx := pkWhere(pkColumns, paramIdx)
	b.WriteString(" WHERE ")
	b.WriteString(where)
	args = append(args, pkValues...)

	b.WriteString(fmt.Sprintf(" AND %s = $%d", quoteIdent(versionCol), nextIdx))
	args = append(args, currentVersion)

	b.WriteString(fmt.Sprintf(" RETURNING %s", quoteIdent(versionCol)))

	return dialect.Result{SQL: b.String(), Args: args}
}

func (p *Postgres) BuildBulkInsert(table string, columns []string, rows [][]any) dialect.Result {
	var b strings.Builder
	var args []any
	paramIdx := 1

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
			b.WriteString(fmt.Sprintf("$%d", paramIdx))
			args = append(args, val)
			paramIdx++
		}
		b.WriteString(")")
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (p *Postgres) BuildBulkUpdate(table string, sets []dialect.ColumnValue, where *ast.WhereClause) dialect.Result {
	var b strings.Builder
	var args []any
	paramIdx := 1

	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" SET ")
	for i, cv := range sets {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("%s = $%d", quoteIdent(cv.Column), paramIdx))
		args = append(args, cv.Value)
		paramIdx++
	}

	if where != nil {
		b.WriteString(" WHERE ")
		writeWhere(&b, &args, *where, paramIdx)
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (p *Postgres) BuildBulkDelete(table string, where *ast.WhereClause) dialect.Result {
	var b strings.Builder
	var args []any
	paramIdx := 1

	b.WriteString("DELETE FROM ")
	b.WriteString(quoteIdent(table))

	if where != nil {
		b.WriteString(" WHERE ")
		writeWhere(&b, &args, *where, paramIdx)
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (p *Postgres) BuildBulkSoftDelete(table string, where *ast.WhereClause) dialect.Result {
	var b strings.Builder
	var args []any
	paramIdx := 1

	b.WriteString("UPDATE ")
	b.WriteString(quoteIdent(table))
	b.WriteString(" SET ")
	b.WriteString(quoteIdent("deleted_at"))
	b.WriteString(" = NOW() WHERE ")
	b.WriteString(quoteIdent("deleted_at"))
	b.WriteString(" IS NULL")

	if where != nil {
		b.WriteString(" AND ")
		writeWhere(&b, &args, *where, paramIdx)
	}

	return dialect.Result{SQL: b.String(), Args: args}
}

func (p *Postgres) BuildBulkUpsert(table string, columns []string, rows [][]any, conflictCols []string, updateCols []string, doNothing bool) dialect.Result {
	result := p.BuildBulkInsert(table, columns, rows)

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
