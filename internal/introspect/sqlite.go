package introspect

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver"
)

const sqliteTablesQuery = `
SELECT name FROM sqlite_master
WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
ORDER BY name`

const sqliteIndexesQuery = `
SELECT m.tbl_name, m.name, COALESCE(m.sql, '')
FROM sqlite_master m
WHERE m.type = 'index' AND m.name NOT LIKE 'sqlite_%'
ORDER BY m.tbl_name, m.name`

// sqlitePartialPredicate pulls the predicate out of a CREATE INDEX statement.
// SQLite stores the original text, so the predicate reads as written.
var sqlitePartialPredicate = regexp.MustCompile(`(?is)\sWHERE\s+(.+?)\s*;?\s*$`)

// sqliteSchema reads the live SQLite (or libSQL) schema.
func sqliteSchema(ctx context.Context, q Querier) (codegen.Schema, error) {
	var s codegen.Schema

	var tables []string
	err := collectRows(ctx, q, sqliteTablesQuery, func(rows driver.Rows) error {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !bookkeepingTables[name] {
			tables = append(tables, name)
		}
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("introspect: tables: %w", err)
	}

	for _, name := range tables {
		t := table(&s, name)

		err := collectRows(ctx, q,
			fmt.Sprintf("SELECT name, type, \"notnull\", COALESCE(dflt_value, '') FROM pragma_table_info(%s)", sqliteLiteral(name)),
			func(rows driver.Rows) error {
				var col, typ, def string
				var notNull int64
				if err := rows.Scan(&col, &typ, &notNull, &def); err != nil {
					return err
				}
				t.Columns = append(t.Columns, codegen.Column{
					Name: col, Type: typ, NotNull: notNull != 0, Default: def,
				})
				return nil
			})
		if err != nil {
			return s, fmt.Errorf("introspect: columns of %s: %w", name, err)
		}

		err = collectRows(ctx, q,
			fmt.Sprintf(`SELECT "table", "from", "to", on_delete, on_update FROM pragma_foreign_key_list(%s)`, sqliteLiteral(name)),
			func(rows driver.Rows) error {
				var refTbl, col, refCol, onDelete, onUpdate string
				if err := rows.Scan(&refTbl, &col, &refCol, &onDelete, &onUpdate); err != nil {
					return err
				}
				c := column(t, col)
				if c == nil {
					return nil
				}
				c.Ref, c.RefColumn = refTbl, refCol
				c.OnDelete, c.OnUpdate = sqliteFKAction(onDelete), sqliteFKAction(onUpdate)
				return nil
			})
		if err != nil {
			return s, fmt.Errorf("introspect: foreign keys of %s: %w", name, err)
		}
	}

	err = collectRows(ctx, q, sqliteIndexesQuery, func(rows driver.Rows) error {
		var tbl, name, sql string
		if err := rows.Scan(&tbl, &name, &sql); err != nil {
			return err
		}
		if bookkeepingTables[tbl] {
			return nil
		}
		idx := codegen.Index{
			Name:   name,
			Unique: strings.Contains(strings.ToUpper(sql), "UNIQUE INDEX"),
		}
		if m := sqlitePartialPredicate.FindStringSubmatch(sql); m != nil {
			idx.Where = strings.TrimSpace(m[1])
		}
		t := table(&s, tbl)
		t.Indexes = append(t.Indexes, idx)
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("introspect: indexes: %w", err)
	}

	// The column list of each index comes from its own pragma, because the
	// CREATE statement's text is not a reliable parse target.
	for ti := range s.Tables {
		for ii := range s.Tables[ti].Indexes {
			idx := &s.Tables[ti].Indexes[ii]
			err := collectRows(ctx, q,
				fmt.Sprintf("SELECT name FROM pragma_index_info(%s) ORDER BY seqno", sqliteLiteral(idx.Name)),
				func(rows driver.Rows) error {
					var col string
					if err := rows.Scan(&col); err != nil {
						return err
					}
					idx.Columns = append(idx.Columns, col)
					return nil
				})
			if err != nil {
				return s, fmt.Errorf("introspect: columns of index %s: %w", idx.Name, err)
			}
		}
	}

	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].Name < s.Tables[j].Name })
	return s, nil
}

// sqliteFKAction normalises the pragma's action text. "NO ACTION" is the
// default and reads as no clause at all, matching what drel emits when the tag
// declares nothing.
func sqliteFKAction(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "NO ACTION" || v == "" {
		return ""
	}
	return v
}

// sqliteLiteral quotes a string for a SQL literal. The values are object names
// read from the database itself, never user input, but a name holding a quote
// would still break the statement.
func sqliteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
