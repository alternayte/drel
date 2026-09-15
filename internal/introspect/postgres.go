package introspect

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver"
)

// bookkeepingTables are drel's own ledger and lock. They belong to no model, so
// reporting them as unmanaged would be noise on every run.
var bookkeepingTables = map[string]bool{
	"drel_migrations":     true,
	"drel_migration_lock": true,
}

const pgColumnsQuery = `
SELECT c.relname, a.attname,
       format_type(a.atttypid, a.atttypmod),
       a.attnotnull,
       COALESCE(pg_get_expr(d.adbin, d.adrelid), '')
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = 'public' AND c.relkind = 'r' AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`

const pgIndexesQuery = `
SELECT c.relname, i.relname, ix.indisunique, ix.indisprimary,
       COALESCE(pg_get_expr(ix.indpred, ix.indrelid), ''),
       ARRAY(SELECT a.attname
             FROM unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k.attnum
             ORDER BY k.ord)
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class c ON c.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
ORDER BY c.relname, i.relname`

const pgForeignKeysQuery = `
SELECT c.relname, a.attname, rc.relname, ra.attname,
       con.confdeltype::text, con.confupdtype::text, con.condeferrable
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_class rc ON rc.oid = con.confrelid
JOIN unnest(con.conkey) WITH ORDINALITY AS ck(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ck.attnum
JOIN unnest(con.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = ck.ord
JOIN pg_attribute ra ON ra.attrelid = con.confrelid AND ra.attnum = fk.attnum
WHERE con.contype = 'f' AND n.nspname = 'public'
ORDER BY c.relname, a.attname`

const pgChecksQuery = `
SELECT c.relname, con.conname, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE con.contype = 'c' AND n.nspname = 'public'
ORDER BY c.relname, con.conname`

const pgEnumsQuery = `
SELECT t.typname, e.enumlabel
FROM pg_type t
JOIN pg_enum e ON e.enumtypid = t.oid
JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE n.nspname = 'public'
ORDER BY t.typname, e.enumsortorder`

// postgresSchema reads the live Postgres schema of the "public" namespace.
func postgresSchema(ctx context.Context, q Querier) (codegen.Schema, error) {
	var s codegen.Schema

	err := collectRows(ctx, q, pgColumnsQuery, func(rows driver.Rows) error {
		var tbl, col, typ, def string
		var notNull bool
		if err := rows.Scan(&tbl, &col, &typ, &notNull, &def); err != nil {
			return err
		}
		if bookkeepingTables[tbl] {
			return nil
		}
		t := table(&s, tbl)
		t.Columns = append(t.Columns, codegen.Column{
			Name: col, Type: typ, NotNull: notNull, Default: def,
		})
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("introspect: columns: %w", err)
	}

	err = collectRows(ctx, q, pgIndexesQuery, func(rows driver.Rows) error {
		var tbl, name, pred string
		var unique, primary bool
		var cols []string
		if err := rows.Scan(&tbl, &name, &unique, &primary, &pred, &cols); err != nil {
			return err
		}
		if bookkeepingTables[tbl] || primary {
			// The primary key's index is implied by the column, not declared.
			return nil
		}
		table(&s, tbl).Indexes = append(table(&s, tbl).Indexes, codegen.Index{
			Name: name, Columns: cols, Unique: unique, Where: pred,
		})
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("introspect: indexes: %w", err)
	}

	err = collectRows(ctx, q, pgForeignKeysQuery, func(rows driver.Rows) error {
		var tbl, col, refTbl, refCol string
		var delAction, updAction string
		var deferrable bool
		if err := rows.Scan(&tbl, &col, &refTbl, &refCol, &delAction, &updAction, &deferrable); err != nil {
			return err
		}
		if bookkeepingTables[tbl] {
			return nil
		}
		c := column(table(&s, tbl), col)
		if c == nil {
			return nil
		}
		c.Ref, c.RefColumn = refTbl, refCol
		c.OnDelete, c.OnUpdate = pgFKAction(delAction), pgFKAction(updAction)
		c.Deferrable = deferrable
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("introspect: foreign keys: %w", err)
	}

	err = collectRows(ctx, q, pgChecksQuery, func(rows driver.Rows) error {
		var tbl, name, def string
		if err := rows.Scan(&tbl, &name, &def); err != nil {
			return err
		}
		if bookkeepingTables[tbl] {
			return nil
		}
		t := table(&s, tbl)
		t.Checks = append(t.Checks, codegen.CheckConstraint{Name: name, Expr: checkExpr(def)})
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("introspect: check constraints: %w", err)
	}

	enums := map[string][]string{}
	err = collectRows(ctx, q, pgEnumsQuery, func(rows driver.Rows) error {
		var name, label string
		if err := rows.Scan(&name, &label); err != nil {
			return err
		}
		enums[name] = append(enums[name], label)
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("introspect: enums: %w", err)
	}
	names := make([]string, 0, len(enums))
	for n := range enums {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s.Enums = append(s.Enums, codegen.EnumDef{Name: n, Values: enums[n]})
	}

	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].Name < s.Tables[j].Name })
	return s, nil
}

// pgFKAction maps pg_constraint's single-character action code to SQL. 'a' (no
// action) is the default and reads as no clause at all, matching what drel
// emits when the tag declares nothing.
func pgFKAction(code string) string {
	switch code {
	case "c":
		return "CASCADE"
	case "r":
		return "RESTRICT"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default:
		return ""
	}
}

// checkExpr strips the "CHECK (" wrapper pg_get_constraintdef adds, leaving the
// predicate. The server's own rendering of the predicate rarely matches the
// text a person wrote, so the expression is reported, never diffed.
func checkExpr(def string) string {
	d := strings.TrimSpace(def)
	if !strings.HasPrefix(d, "CHECK (") || !strings.HasSuffix(d, ")") {
		return d
	}
	return strings.TrimSpace(d[len("CHECK (") : len(d)-1])
}
