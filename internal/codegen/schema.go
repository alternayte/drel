package codegen

import (
	"fmt"
	"strings"
)

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// checkConstraintName returns the deterministic name for a column's CHECK
// constraint, matching the idx_/uq_ naming convention used elsewhere.
func checkConstraintName(table, column string) string {
	return fmt.Sprintf("chk_%s_%s", table, column)
}

// GoTypeToSQL maps a Go type string to its corresponding SQL type for the given dialect.
// Pointer types are unwrapped to their base type. Unknown types default to "text"/"TEXT".
// Supported dialects: "postgres" (default), "sqlite".
func GoTypeToSQL(goType string, dialect string) string {
	if strings.HasPrefix(goType, "*") {
		return GoTypeToSQL(goType[1:], dialect)
	}
	if dialect == "sqlite" {
		return goTypeToSQLite(goType)
	}
	return goTypeToPostgres(goType)
}

func goTypeToPostgres(goType string) string {
	switch goType {
	case "int", "int32":
		return "integer"
	case "int8", "int16":
		return "smallint"
	case "int64":
		return "bigint"
	case "string":
		return "text"
	case "bool":
		return "boolean"
	case "float32":
		return "real"
	case "float64":
		return "double precision"
	case "time.Time":
		return "timestamptz"
	case "github.com/google/uuid.UUID", "uuid.UUID":
		return "uuid"
	default:
		return "text"
	}
}

func goTypeToSQLite(goType string) string {
	switch goType {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return "INTEGER"
	case "string":
		return "TEXT"
	case "bool":
		return "INTEGER"
	case "float32", "float64":
		return "REAL"
	case "time.Time":
		return "DATETIME"
	case "github.com/google/uuid.UUID", "uuid.UUID":
		return "TEXT"
	default:
		return "TEXT"
	}
}

// GenerateCreateTable emits a CREATE TABLE statement for a single model.
// fks maps column names to referenced table names for foreign key constraints.
// dialect controls SQL type mappings and syntax ("postgres" or "sqlite").
func GenerateCreateTable(m ModelInfo, fks map[string]string, dialect string) string {
	return createTableSQL(buildTable(m, fks, dialect), dialect)
}

// createTableSQL emits a CREATE TABLE statement from a structured Table.
// The PK column is emitted first, followed by the remaining columns. For Postgres,
// CHECK constraints are emitted as named table-level constraints so they can be
// altered in place via ALTER TABLE DROP/ADD CONSTRAINT. SQLite keeps CHECK inline.
// A composite PrimaryKey (used by pivot tables) is emitted as a trailing table-level
// PRIMARY KEY constraint.
func createTableSQL(t Table, dialect string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("CREATE TABLE %s (\n", quoteIdent(t.Name)))

	first := true
	emit := func(c Column) {
		if first {
			b.WriteString("    ")
			first = false
		} else {
			b.WriteString(",\n    ")
		}
		b.WriteString(columnDefSQL(c, dialect))
	}

	// PK column first.
	for _, c := range t.Columns {
		if c.PK {
			emit(c)
		}
	}
	for _, c := range t.Columns {
		if !c.PK {
			emit(c)
		}
	}

	// Postgres: emit CHECK constraints as named table-level constraints so they
	// can be dropped/added by name in a later diff.
	if dialect != "sqlite" {
		for _, c := range t.Columns {
			if c.Check != "" {
				b.WriteString(fmt.Sprintf(",\n    CONSTRAINT %s CHECK (%s)",
					quoteIdent(checkConstraintName(t.Name, c.Name)), c.Check))
			}
		}
	}

	if len(t.PrimaryKey) > 0 {
		cols := make([]string, len(t.PrimaryKey))
		for i, c := range t.PrimaryKey {
			cols[i] = quoteIdent(c)
		}
		b.WriteString(fmt.Sprintf(",\n    PRIMARY KEY (%s)", strings.Join(cols, ", ")))
	}

	b.WriteString("\n);\n")
	return b.String()
}

// columnDefSQL renders a single column definition (without leading indentation),
// in the order: name, type, NOT NULL, CHECK, REFERENCES, DEFAULT. For Postgres,
// CHECK constraints are emitted separately as named table-level constraints (see
// createTableSQL) so they can be altered in place; SQLite keeps them inline
// because it cannot ALTER a CHECK without a table rebuild.
func columnDefSQL(c Column, dialect string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s", quoteIdent(c.Name), c.Type))
	// The PK type string already embeds "PRIMARY KEY" (and NOT NULL semantics),
	// so do not append an additional NOT NULL for PK columns.
	if c.NotNull && !c.PK {
		b.WriteString(" NOT NULL")
	}
	if c.Check != "" && dialect == "sqlite" {
		b.WriteString(fmt.Sprintf(" CHECK(%s)", c.Check))
	}
	if c.Ref != "" {
		b.WriteString(fmt.Sprintf(" REFERENCES %s(%s)", quoteIdent(c.Ref), quoteIdent("id")))
	}
	if c.Default != "" {
		b.WriteString(fmt.Sprintf(" DEFAULT %s", c.Default))
	}
	return b.String()
}

// createIndexSQL emits a CREATE [UNIQUE] INDEX statement for an index on a table.
func createIndexSQL(table string, idx Index) string {
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = quoteIdent(c)
	}
	unique := ""
	if idx.Unique {
		unique = "UNIQUE "
	}
	return fmt.Sprintf("CREATE %sINDEX %s ON %s (%s);\n",
		unique, quoteIdent(idx.Name), quoteIdent(table), strings.Join(cols, ", "))
}

// GenerateSchema emits the full schema DDL for a slice of models, including
// enum type definitions, foreign key constraints, and many-to-many pivot tables.
// dialect controls SQL type mappings and syntax ("postgres" or "sqlite").
func GenerateSchema(models []ModelInfo, dialect string) string {
	fks := collectFKs(models)
	pivots := collectPivotTables(models, dialect)

	var b strings.Builder

	// SQLite has no CREATE TYPE; enums are handled inline via CHECK constraints.
	var enums []string
	if dialect != "sqlite" {
		enums = collectEnums(models)
		for _, e := range enums {
			b.WriteString(e)
			b.WriteString("\n")
		}
	}

	for i, m := range models {
		if i > 0 || len(enums) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(GenerateCreateTable(m, fks, dialect))
	}
	for _, p := range pivots {
		b.WriteString("\n")
		b.WriteString(p)
	}

	// Emit CREATE [UNIQUE] INDEX statements for every index in the schema,
	// after all tables/pivots, in table-then-index order.
	schema := BuildSchema(models, dialect)
	var idxBuf strings.Builder
	for _, t := range schema.Tables {
		for _, idx := range t.Indexes {
			idxBuf.WriteString(createIndexSQL(t.Name, idx))
		}
	}
	if idxBuf.Len() > 0 {
		b.WriteString("\n")
		b.WriteString(idxBuf.String())
	}

	return b.String()
}

// collectPivotTables generates CREATE TABLE DDL for many-to-many join tables.
// Each join table is emitted only once, even when both sides of the relation
// reference the same JoinTable name.
func collectPivotTables(models []ModelInfo, dialect string) []string {
	modelTable := map[string]string{}
	modelPK := map[string]string{}
	for _, m := range models {
		modelTable[m.Name] = m.TableName
		modelPK[m.Name] = m.PKType
	}

	seen := map[string]bool{}
	var pivots []string

	for _, m := range models {
		for _, f := range m.Fields {
			if f.Relation == nil || f.Relation.Type != "many_to_many" {
				continue
			}
			jt := f.Relation.JoinTable
			if jt == "" || seen[jt] {
				continue
			}
			seen[jt] = true

			targetTable, ok := modelTable[f.Relation.TargetModel]
			if !ok {
				continue
			}

			srcPKType := GoTypeToSQL(m.PKType, dialect)
			tgtPKType := GoTypeToSQL(modelPK[f.Relation.TargetModel], dialect)

			var b strings.Builder
			b.WriteString(fmt.Sprintf("CREATE TABLE %s (\n", quoteIdent(jt)))
			b.WriteString(fmt.Sprintf("    %s %s NOT NULL REFERENCES %s(%s),\n",
				quoteIdent(f.Relation.FK), srcPKType, quoteIdent(m.TableName), quoteIdent("id")))
			b.WriteString(fmt.Sprintf("    %s %s NOT NULL REFERENCES %s(%s),\n",
				quoteIdent(f.Relation.RefColumn), tgtPKType, quoteIdent(targetTable), quoteIdent("id")))
			b.WriteString(fmt.Sprintf("    PRIMARY KEY (%s, %s)\n",
				quoteIdent(f.Relation.FK), quoteIdent(f.Relation.RefColumn)))
			b.WriteString(");\n")

			pivots = append(pivots, b.String())
		}
	}
	return pivots
}

// GenerateDropSchema emits DROP TABLE statements in reverse order to respect
// foreign key dependencies.
func GenerateDropSchema(models []ModelInfo) string {
	var b strings.Builder
	for i := len(models) - 1; i >= 0; i-- {
		b.WriteString(fmt.Sprintf("DROP TABLE IF EXISTS %s;\n", quoteIdent(models[i].TableName)))
	}
	return b.String()
}

// DiffSchemas compares an old (previous snapshot) schema against a new (desired)
// schema and produces incremental up/down migration SQL containing only the
// changes. It returns ("", "") when the schemas are equivalent.
//
// Coverage (up order; down is the sensible inverse):
//   - new enums (Postgres only): CREATE TYPE / down DROP TYPE
//   - new tables: CREATE TABLE + indexes / down DROP TABLE
//   - dropped tables: DROP TABLE / down recreate
//   - per-table column add/drop, type changes, NOT NULL changes
//   - per-table index add/drop
//
// SQLite cannot ALTER COLUMN TYPE or SET/DROP NOT NULL; those changes are emitted
// as clearly-marked WARNING comments rather than silently skipped. A column
// declared with a renamed_from marker emits RENAME COLUMN instead of a drop
// and an add; an ambiguous marker is rejected with an error rather than guessed.
func DiffSchemas(old, newSchema Schema, dialect string) (upSQL, downSQL string, err error) {
	var up, down []string

	oldEnums := indexEnums(old.Enums)
	newEnums := indexEnums(newSchema.Enums)
	oldTables := indexTables(old.Tables)
	newTables := indexTables(newSchema.Tables)

	tableRenames, err := resolveTableRenames(old, newSchema)
	if err != nil {
		return "", "", err
	}
	for _, ot := range old.Tables { // old-schema order, for a stable diff
		nn, renamed := tableRenames[ot.Name]
		if !renamed {
			continue
		}
		up = append(up, fmt.Sprintf("ALTER TABLE %s RENAME TO %s;",
			quoteIdent(ot.Name), quoteIdent(nn)))
		down = append(down, fmt.Sprintf("ALTER TABLE %s RENAME TO %s;",
			quoteIdent(nn), quoteIdent(ot.Name)))
	}

	// Re-index the old tables under their new names so the passes below treat a
	// renamed table as present in both schemas.
	for oldName, newName := range tableRenames {
		t := oldTables[oldName]
		t.Name = newName
		oldTables[newName] = t
		delete(oldTables, oldName)
	}

	// A slice copy of old.Tables with renamed entries relabelled, for the passes
	// below that iterate the slice (rather than the oldTables map) and would
	// otherwise treat a renamed table as dropped.
	oldTablesSlice := make([]Table, len(old.Tables))
	copy(oldTablesSlice, old.Tables)
	for i := range oldTablesSlice {
		if nn, renamed := tableRenames[oldTablesSlice[i].Name]; renamed {
			oldTablesSlice[i].Name = nn
		}
	}

	// 1. New enums (Postgres only).
	if dialect != "sqlite" {
		for _, e := range newSchema.Enums {
			if _, ok := oldEnums[e.Name]; !ok {
				up = append(up, fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);",
					quoteIdent(e.Name), quoteEnumValues(e.Values)))
				down = append(down, fmt.Sprintf("DROP TYPE %s;", quoteIdent(e.Name)))
			}
		}
	}

	// 2. New tables (preserve new-schema order).
	for _, t := range newSchema.Tables {
		if _, ok := oldTables[t.Name]; ok {
			continue
		}
		up = append(up, strings.TrimRight(createTableSQL(t, dialect), "\n"))
		for _, idx := range t.Indexes {
			up = append(up, strings.TrimRight(createIndexSQL(t.Name, idx), "\n"))
		}
		down = append(down, fmt.Sprintf("DROP TABLE IF EXISTS %s;", quoteIdent(t.Name)))
	}

	// 3. Dropped tables (in old, not in new) — emit drops in FK-safe order so
	// that child/pivot tables (which hold REFERENCES to parents) are dropped
	// before the parent tables they reference. Without this ordering, Postgres
	// refuses to drop a parent table while its FK referents still exist.
	droppedTables := fkSafeDropOrder(oldTablesSlice, newTables)
	for _, t := range droppedTables {
		up = append(up, fmt.Sprintf("DROP TABLE IF EXISTS %s;", quoteIdent(t.Name)))
		down = append(down, strings.TrimRight(createTableSQL(t, dialect), "\n"))
		for _, idx := range t.Indexes {
			down = append(down, strings.TrimRight(createIndexSQL(t.Name, idx), "\n"))
		}
	}

	// 4. Tables present in both — diff columns and indexes (new-schema order).
	for _, nt := range newSchema.Tables {
		ot, ok := oldTables[nt.Name]
		if !ok {
			continue
		}
		tu, td, dErr := diffTable(ot, nt, dialect)
		if dErr != nil {
			return "", "", dErr
		}
		up = append(up, tu...)
		down = append(down, td...)
	}

	// 4b. Enums present in both — diff their value sets (Postgres string enums
	// only; int enums and SQLite enums diff through the column CHECK path).
	if dialect != "sqlite" {
		for _, ne := range newSchema.Enums {
			oe, ok := oldEnums[ne.Name]
			if !ok {
				continue
			}
			eu, ed := diffEnumValues(oe, ne)
			up = append(up, eu...)
			down = append(down, ed...)
		}
	}

	// 5. Dropped enums (Postgres only) — drop after dependent tables are gone.
	if dialect != "sqlite" {
		for _, e := range old.Enums {
			if _, ok := newEnums[e.Name]; !ok {
				up = append(up, fmt.Sprintf("DROP TYPE %s;", quoteIdent(e.Name)))
				down = append(down, fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);",
					quoteIdent(e.Name), quoteEnumValues(e.Values)))
			}
		}
	}

	if len(up) == 0 && len(down) == 0 {
		return "", "", nil
	}
	// The down migration must undo the up steps in reverse order so that
	// dependencies hold (e.g. a recreated table's enum type is created before
	// the table, and a new table is dropped before the enum it depends on).
	for i, j := 0, len(down)-1; i < j; i, j = i+1, j-1 {
		down[i], down[j] = down[j], down[i]
	}
	return strings.Join(up, "\n"), strings.Join(down, "\n"), nil
}

// resolveTableRenames matches each new table that declares a RenamedFrom marker
// against the old schema, and returns a map from old table name to new table
// name. The stale and ambiguous rules match resolveColumnRenames.
func resolveTableRenames(old, new Schema) (map[string]string, error) {
	oldTables := indexTables(old.Tables)
	renames := make(map[string]string)
	claimed := make(map[string]string)

	for _, nt := range new.Tables {
		if nt.RenamedFrom == "" {
			continue
		}
		if _, exists := oldTables[nt.RenamedFrom]; !exists {
			continue // spent marker
		}
		if _, alsoExists := oldTables[nt.Name]; alsoExists {
			return nil, fmt.Errorf(
				"codegen: ambiguous rename: table %q says renamed_from=%q, but both %q and %q already exist; "+
					"remove the marker, or drop one of the tables first",
				nt.Name, nt.RenamedFrom, nt.RenamedFrom, nt.Name)
		}
		if prev, dup := claimed[nt.RenamedFrom]; dup {
			return nil, fmt.Errorf(
				"codegen: tables %q and %q both declare renamed_from=%q; a table can be renamed to one name only",
				prev, nt.Name, nt.RenamedFrom)
		}
		claimed[nt.RenamedFrom] = nt.Name
		renames[nt.RenamedFrom] = nt.Name
	}
	return renames, nil
}

// resolveColumnRenames matches each new column that declares a RenamedFrom
// marker against the old schema, and returns the {oldName, newName} pairs that
// are real renames.
//
// A marker whose old column is absent from the old schema is spent: the rename
// already ran, and the schemas agree. It is ignored, not an error.
//
// A marker is rejected when it is ambiguous — when both the old and the new
// name exist in the old schema, so applying the rename would destroy the
// existing new-named column — or when two new columns claim the same old name.
func resolveColumnRenames(old, new Table) ([][2]string, error) {
	oldCols := indexColumns(old.Columns)
	claimed := make(map[string]string)

	var renames [][2]string
	for _, nc := range new.Columns {
		if nc.RenamedFrom == "" {
			continue
		}
		if _, exists := oldCols[nc.RenamedFrom]; !exists {
			continue // spent marker
		}
		if _, alsoExists := oldCols[nc.Name]; alsoExists {
			return nil, fmt.Errorf(
				"codegen: table %s: ambiguous rename: column %q says renamed_from=%q, but both %q and %q already exist; "+
					"remove the marker, or drop one of the columns first",
				old.Name, nc.Name, nc.RenamedFrom, nc.RenamedFrom, nc.Name)
		}
		if prev, dup := claimed[nc.RenamedFrom]; dup {
			return nil, fmt.Errorf(
				"codegen: table %s: columns %q and %q both declare renamed_from=%q; a column can be renamed to one name only",
				old.Name, prev, nc.Name, nc.RenamedFrom)
		}
		claimed[nc.RenamedFrom] = nc.Name
		renames = append(renames, [2]string{nc.RenamedFrom, nc.Name})
	}
	return renames, nil
}

// diffTable diffs the columns and indexes of a table that exists in both schemas.
func diffTable(old, new Table, dialect string) (up, down []string, err error) {
	renames, err := resolveColumnRenames(old, new)
	if err != nil {
		return nil, nil, err
	}

	// Emit the renames first, so every later statement can name the new column.
	renamedOld := make(map[string]string, len(renames)) // old name -> new name
	for _, r := range renames {
		renamedOld[r[0]] = r[1]
		up = append(up, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;",
			quoteIdent(new.Name), quoteIdent(r[0]), quoteIdent(r[1])))
		down = append(down, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;",
			quoteIdent(new.Name), quoteIdent(r[1]), quoteIdent(r[0])))
	}

	// Index the old columns under their new names, so a renamed column is
	// "present in both" for the add/drop/modify passes below.
	oldCols := make(map[string]Column, len(old.Columns))
	for _, c := range old.Columns {
		name := c.Name
		if nn, renamed := renamedOld[name]; renamed {
			name = nn
		}
		oldCols[name] = c
	}
	newCols := indexColumns(new.Columns)

	// Added columns (preserve new-table order).
	for _, c := range new.Columns {
		if _, ok := oldCols[c.Name]; !ok {
			// Adding a NOT NULL column without a default fails on a non-empty
			// table; surface this for the reviewer rather than failing silently.
			if c.NotNull && c.Default == "" {
				up = append(up, fmt.Sprintf(`-- NOTE: adding NOT NULL column %s to existing table %s; ensure the table is empty or add a DEFAULT / backfill before applying`,
					quoteIdent(c.Name), quoteIdent(new.Name)))
			}
			up = append(up, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", quoteIdent(new.Name), columnDefSQL(c, dialect)))
			down = append(down, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", quoteIdent(new.Name), quoteIdent(c.Name)))
		}
	}

	// Dropped columns (preserve old-table order). A renamed column is looked
	// up by its new name — it is present in newCols under that name, so it is
	// not dropped here; the rename statement above already handled it.
	for _, c := range old.Columns {
		name := c.Name
		if nn, renamed := renamedOld[name]; renamed {
			name = nn
		}
		if _, ok := newCols[name]; !ok {
			up = append(up, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", quoteIdent(old.Name), quoteIdent(c.Name)))
			down = append(down, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", quoteIdent(old.Name), columnDefSQL(c, dialect)))
		}
	}

	// Modified columns (present in both).
	rebuild := false
	for _, nc := range new.Columns {
		oc, ok := oldCols[nc.Name]
		if !ok {
			continue
		}
		mu, md, needsRebuild := diffColumn(new.Name, oc, nc, dialect)
		up = append(up, mu...)
		down = append(down, md...)
		rebuild = rebuild || needsRebuild
	}

	if rebuild {
		// SQLite cannot ALTER these properties in place. One rebuild carries
		// every such change on this table, so two changed columns produce one
		// rebuild rather than two.
		//
		// The rebuild runs AFTER the rename statements emitted above, so by then
		// the live table already carries the new column names. Its source shape
		// must therefore be the old table relabelled, or sharedColumnNames finds
		// no match for a renamed column and silently drops its data.
		source := relabelColumns(old, renamedOld)

		// Append each direction as ONE element. DiffSchemas reverses the down
		// slice element-wise; a rebuild spread across elements would execute
		// backwards (RENAME before DROP before INSERT before CREATE).
		up = append(up, strings.Join(sqliteRebuild(new, source), "\n"))
		down = append(down, strings.Join(sqliteRebuild(source, new), "\n"))
	}

	// Index diffs. A rebuild already recreated every index the new table
	// declares, so these passes are skipped to avoid a duplicate CREATE INDEX.
	if !rebuild {
		oldIdx := indexIndexes(old.Indexes)
		newIdx := indexIndexes(new.Indexes)
		for _, idx := range new.Indexes {
			if _, ok := oldIdx[idx.Name]; !ok {
				up = append(up, strings.TrimRight(createIndexSQL(new.Name, idx), "\n"))
				down = append(down, fmt.Sprintf("DROP INDEX %s;", quoteIdent(idx.Name)))
			}
		}
		for _, idx := range old.Indexes {
			if _, ok := newIdx[idx.Name]; !ok {
				up = append(up, fmt.Sprintf("DROP INDEX %s;", quoteIdent(idx.Name)))
				down = append(down, strings.TrimRight(createIndexSQL(old.Name, idx), "\n"))
			}
		}
	}

	return up, down, nil
}

// diffColumn emits ALTER statements for a column whose definition changed
// between the old and new schema (type, NOT NULL, DEFAULT, or CHECK).
//
// SQLite cannot ALTER any of these properties in place. For that dialect the
// function emits no SQL and reports needsRebuild instead, so that diffTable can
// emit one table rebuild covering every changed column on the table.
func diffColumn(table string, old, new Column, dialect string) (up, down []string, needsRebuild bool) {
	if old.Type != new.Type {
		if dialect == "sqlite" {
			needsRebuild = true
		} else {
			up = append(up, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;",
				quoteIdent(table), quoteIdent(new.Name), new.Type))
			down = append(down, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;",
				quoteIdent(table), quoteIdent(new.Name), old.Type))
		}
	}

	if old.NotNull != new.NotNull {
		if dialect == "sqlite" {
			needsRebuild = true
		} else {
			upClause, downClause := "SET NOT NULL", "DROP NOT NULL"
			if !new.NotNull {
				upClause, downClause = "DROP NOT NULL", "SET NOT NULL"
			}
			up = append(up, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s %s;",
				quoteIdent(table), quoteIdent(new.Name), upClause))
			down = append(down, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s %s;",
				quoteIdent(table), quoteIdent(new.Name), downClause))
		}
	}

	if old.Default != new.Default {
		if dialect == "sqlite" {
			needsRebuild = true
		} else {
			up = append(up, alterDefaultSQL(table, new.Name, new.Default))
			down = append(down, alterDefaultSQL(table, old.Name, old.Default))
		}
	}

	if old.Check != new.Check {
		if dialect == "sqlite" {
			needsRebuild = true
		} else {
			name := checkConstraintName(table, new.Name)
			// UP: drop the old constraint (if any), add the new one (if any).
			if old.Check != "" {
				up = append(up, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s;",
					quoteIdent(table), quoteIdent(name)))
			}
			if new.Check != "" {
				up = append(up, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s);",
					quoteIdent(table), quoteIdent(name), new.Check))
			}
			// DOWN: drop the new constraint (if any), restore the old one (if any).
			if new.Check != "" {
				down = append(down, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s;",
					quoteIdent(table), quoteIdent(name)))
			}
			if old.Check != "" {
				down = append(down, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s);",
					quoteIdent(table), quoteIdent(name), old.Check))
			}
		}
	}

	return up, down, needsRebuild
}

// diffEnumValues emits migration SQL for added/removed values of a Postgres
// string enum that exists in both schemas. Additions use ALTER TYPE ADD VALUE
// (which cannot run inside a transaction and is not trivially reversible);
// removals require a full type rebuild and surface as a loud WARNING.
func diffEnumValues(old, new EnumDef) (up, down []string) {
	oldSet := map[string]bool{}
	for _, v := range old.Values {
		oldSet[v] = true
	}
	newSet := map[string]bool{}
	for _, v := range new.Values {
		newSet[v] = true
	}

	// Additions (preserve new declaration order).
	for _, v := range new.Values {
		if oldSet[v] {
			continue
		}
		up = append(up,
			fmt.Sprintf("-- NOTE: ALTER TYPE ... ADD VALUE cannot run inside a transaction; run this migration outside a transaction block"),
			fmt.Sprintf("ALTER TYPE %s ADD VALUE '%s';", quoteIdent(new.Name), strings.ReplaceAll(v, "'", "''")))
		down = append(down,
			fmt.Sprintf("-- WARNING: cannot drop enum value '%s' from %s; removing an enum value requires recreating the type. Apply manually.",
				strings.ReplaceAll(v, "'", "''"), quoteIdent(new.Name)))
	}

	// Removals.
	for _, v := range old.Values {
		if newSet[v] {
			continue
		}
		up = append(up,
			fmt.Sprintf("-- WARNING: enum value '%s' removed from %s; Postgres cannot drop an enum value in place — recreate the type manually.",
				strings.ReplaceAll(v, "'", "''"), quoteIdent(new.Name)))
		down = append(down,
			fmt.Sprintf("-- WARNING: re-adding enum value '%s' to %s requires ALTER TYPE ... ADD VALUE outside a transaction.",
				strings.ReplaceAll(v, "'", "''"), quoteIdent(new.Name)))
	}

	return up, down
}

// alterDefaultSQL emits SET/DROP DEFAULT for a Postgres column.
func alterDefaultSQL(table, column, def string) string {
	if def == "" {
		return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;", quoteIdent(table), quoteIdent(column))
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;", quoteIdent(table), quoteIdent(column), def)
}

// fkSafeDropOrder returns the subset of tables (those NOT in keepTables)
// sorted for safe DROP execution: a table that holds a REFERENCES FK to another
// table in the same drop set is emitted first, before the table it references.
// This prevents Postgres "cannot drop table because other objects depend on it"
// errors when rolling back the first migration.
//
// Algorithm: iterative Kahn-style topo-sort on the "is referenced by" relation.
// A table T is ready to DROP when no other yet-to-be-dropped table holds a
// Column.Ref pointing to T (i.e. nobody FKs into T any more).
func fkSafeDropOrder(tables []Table, keepTables map[string]Table) []Table {
	// Collect only the tables being dropped.
	var toSort []Table
	for _, t := range tables {
		if _, keep := keepTables[t.Name]; !keep {
			toSort = append(toSort, t)
		}
	}
	if len(toSort) == 0 {
		return nil
	}

	emitted := map[string]bool{}
	ordered := make([]Table, 0, len(toSort))

	for len(ordered) < len(toSort) {
		progress := false
		for _, t := range toSort {
			if emitted[t.Name] {
				continue
			}
			// T is safe to drop when no other not-yet-dropped table in the set
			// holds a Column.Ref pointing to T.
			referencedByPending := false
			for _, other := range toSort {
				if emitted[other.Name] || other.Name == t.Name {
					continue
				}
				for _, c := range other.Columns {
					if c.Ref == t.Name {
						referencedByPending = true
						break
					}
				}
				if referencedByPending {
					break
				}
			}
			if !referencedByPending {
				emitted[t.Name] = true
				ordered = append(ordered, t)
				progress = true
			}
		}
		if !progress {
			// Cycle or self-referential FKs; emit remaining in original order
			// to avoid an infinite loop.
			for _, t := range toSort {
				if !emitted[t.Name] {
					ordered = append(ordered, t)
					emitted[t.Name] = true
				}
			}
			break
		}
	}

	return ordered
}

func indexEnums(enums []EnumDef) map[string]EnumDef {
	m := make(map[string]EnumDef, len(enums))
	for _, e := range enums {
		m[e.Name] = e
	}
	return m
}

func indexTables(tables []Table) map[string]Table {
	m := make(map[string]Table, len(tables))
	for _, t := range tables {
		m[t.Name] = t
	}
	return m
}

func indexColumns(cols []Column) map[string]Column {
	m := make(map[string]Column, len(cols))
	for _, c := range cols {
		m[c.Name] = c
	}
	return m
}

func indexIndexes(idxs []Index) map[string]Index {
	m := make(map[string]Index, len(idxs))
	for _, i := range idxs {
		m[i.Name] = i
	}
	return m
}

// collectFKs builds a map of column name → referenced table for belongs_to relations.
func collectFKs(models []ModelInfo) map[string]string {
	modelTable := map[string]string{}
	for _, m := range models {
		modelTable[m.Name] = m.TableName
	}

	fks := map[string]string{}
	for _, m := range models {
		for _, f := range m.Fields {
			if f.Relation != nil && f.Relation.Type == "belongs_to" && f.Relation.FK != "" && f.Relation.TargetModel != "" {
				if target, ok := modelTable[f.Relation.TargetModel]; ok {
					fks[f.Relation.FK] = target
				}
			}
		}
	}
	return fks
}

// collectEnums discovers enum fields across all models and returns CREATE TYPE statements.
// Integer enums are excluded; they use integer columns with a CHECK constraint instead.
func collectEnums(models []ModelInfo) []string {
	seen := map[string]bool{}
	var enums []string
	for _, m := range models {
		for _, f := range columnFields(m.Fields) {
			if !f.IsEnum || len(f.EnumValues) == 0 || f.EnumIsInt {
				continue
			}
			name := strings.ToLower(f.LocalGoType)
			if seen[name] {
				continue
			}
			seen[name] = true
			quoted := make([]string, len(f.EnumValues))
			for i, v := range f.EnumValues {
				quoted[i] = fmt.Sprintf("'%s'", strings.ReplaceAll(v, "'", "''"))
			}
			enums = append(enums, fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);", quoteIdent(name), strings.Join(quoted, ", ")))
		}
	}
	return enums
}
