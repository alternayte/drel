package codegen

import (
	"fmt"
	"strings"
)

// rebuildSuffix names the scratch table used during a SQLite rebuild.
const rebuildSuffix = "__drel_new"

// sqliteRebuild emits the statements that change table `source`'s shape into
// table `target`'s shape, preserving the rows and the indexes.
//
// SQLite cannot ALTER a column's type, nullability, default, or CHECK
// constraint in place, so the only correct migration is a rebuild: create the
// new shape under a scratch name, copy the rows, drop the original, and rename.
//
// Two constraints shape the emitted SQL.
//
// The migration runner executes each migration inside one transaction
// (internal/migrate/migrate.go). PRAGMA foreign_keys is a documented no-op
// inside a transaction, so this emits PRAGMA defer_foreign_keys = ON instead,
// which defers enforcement to commit time and resets itself there.
//
// The INSERT names its columns explicitly and copies only the columns present
// in both shapes. Reliance on column order would silently mis-map a column when
// the new shape adds or drops one.
//
// The trailing PRAGMA foreign_key_check is advisory only. It reports violations
// as a result set rather than as an error, so it cannot abort the transaction
// and must never be relied on as enforcement. It is emitted so that an operator
// who reads or runs the migration by hand can see the violations.
func sqliteRebuild(target Table, source Table) []string {
	scratch := target.Name + rebuildSuffix

	shared := sharedColumnNames(target, source)
	quotedShared := make([]string, len(shared))
	for i, c := range shared {
		quotedShared[i] = quoteIdent(c)
	}
	cols := strings.Join(quotedShared, ", ")

	scratchTable := target
	scratchTable.Name = scratch

	stmts := []string{
		"PRAGMA defer_foreign_keys = ON;",
		strings.TrimRight(createTableSQL(scratchTable, "sqlite"), "\n"),
	}
	if len(shared) > 0 {
		stmts = append(stmts, fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s;",
			quoteIdent(scratch), cols, cols, quoteIdent(source.Name)))
	}
	stmts = append(stmts,
		fmt.Sprintf("DROP TABLE %s;", quoteIdent(source.Name)),
		fmt.Sprintf("ALTER TABLE %s RENAME TO %s;", quoteIdent(scratch), quoteIdent(target.Name)),
	)
	for _, idx := range target.Indexes {
		stmts = append(stmts, strings.TrimRight(createIndexSQL(target.Name, idx), "\n"))
	}
	stmts = append(stmts, "PRAGMA foreign_key_check;")
	return stmts
}

// sharedColumnNames returns the columns present in both shapes, in target
// order. Only these can be copied; a column the target adds has no source data,
// and a column the target drops has nowhere to go.
func sharedColumnNames(target, source Table) []string {
	inSource := make(map[string]bool, len(source.Columns))
	for _, c := range source.Columns {
		inSource[c.Name] = true
	}
	var out []string
	for _, c := range target.Columns {
		if inSource[c.Name] {
			out = append(out, c.Name)
		}
	}
	return out
}

// relabelColumns returns a copy of t whose column names, index columns, and
// primary key entries are mapped through renames (old name -> new name). It
// describes the table's shape after the RENAME COLUMN statements have run.
//
// The result is used in BOTH rebuild roles, so every column reference it holds
// must be relabelled, not only Column.Name. In the up direction it is the
// rebuild's source, where only the column names are read. In the down direction
// diffTable passes the same value as the rebuild's TARGET, where createTableSQL
// and createIndexSQL emit its primary key and its index columns verbatim. A
// reference left under the old name produces DDL that fails with "no such
// column".
//
// Column.Check is free-form SQL and cannot be relabelled safely, so a renamed
// column that carries one is rejected with an actionable error instead.
func relabelColumns(t Table, renames map[string]string) (Table, error) {
	if len(renames) == 0 {
		return t, nil
	}
	out := t

	out.Columns = make([]Column, len(t.Columns))
	copy(out.Columns, t.Columns)
	for i := range out.Columns {
		nn, ok := renames[out.Columns[i].Name]
		if !ok {
			continue
		}
		if out.Columns[i].Check != "" {
			return Table{}, fmt.Errorf("table %q: column %q is renamed to %q and also carries a CHECK constraint (%s); SQLite must rebuild the table for this change and the CHECK cannot be rewritten safely. Split the change into two migrations: rename the column in one, then change the column in the next",
				t.Name, out.Columns[i].Name, nn, out.Columns[i].Check)
		}
		out.Columns[i].Name = nn
	}

	if len(t.Indexes) > 0 {
		out.Indexes = make([]Index, len(t.Indexes))
		copy(out.Indexes, t.Indexes)
		for i := range out.Indexes {
			out.Indexes[i].Columns = relabelNames(t.Indexes[i].Columns, renames)
		}
	}
	out.PrimaryKey = relabelNames(t.PrimaryKey, renames)

	return out, nil
}

// relabelNames returns a fresh slice with each name mapped through renames.
// It never mutates the input slice: the caller's Table is shared with the
// snapshot and must stay untouched.
func relabelNames(names []string, renames map[string]string) []string {
	if names == nil {
		return nil
	}
	out := make([]string, len(names))
	for i, n := range names {
		if nn, ok := renames[n]; ok {
			out[i] = nn
			continue
		}
		out[i] = n
	}
	return out
}
