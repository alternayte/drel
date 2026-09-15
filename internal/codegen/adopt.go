package codegen

import "sort"

// AdoptLiveObjects records into a snapshot the objects the database holds that
// the models do not declare, for the tables the snapshot already covers.
//
// The migration differ works from the snapshot. An index or a constraint
// written by hand is absent from it, so every later migration is planned as
// though the object did not exist -- and PostgreSQL then re-checks a stale
// constraint against a column whose type just changed. Recording the object
// makes the differ able to drop it.
//
// Only tables already in the snapshot are touched: a table no model owns
// belongs to another system, and drel does not take it over.
//
// The returned items say what was recorded, so a person can see it. Adoption is
// not approval: the next `migrate new` emits a DROP for every adopted object
// the models do not declare. That SQL is reviewed before it is applied, and
// declaring the object on its model keeps it.
func AdoptLiveObjects(snapshot, live, declared Schema) (Schema, []DriftItem) {
	liveTables := indexTables(live.Tables)
	declaredTables := indexTables(declared.Tables)
	var adopted []DriftItem

	for i := range snapshot.Tables {
		t := &snapshot.Tables[i]
		lt, ok := liveTables[t.Name]
		if !ok {
			continue // the table is not in the database yet
		}
		dt := declaredTables[t.Name]

		adopted = append(adopted, adoptChecks(t, lt, dt)...)
		adopted = append(adopted, adoptIndexes(t, lt, dt)...)
		adopted = append(adopted, adoptForeignKeys(t, lt, dt)...)
	}
	return snapshot, adopted
}

// adoptChecks records the table's named CHECK constraints that no column of the
// model declares. A declared check is named chk_<table>_<column> and the differ
// already maintains it.
func adoptChecks(t *Table, live, declared Table) []DriftItem {
	declaredNames := map[string]bool{}
	for _, c := range declared.Columns {
		if c.Check != "" {
			declaredNames[checkConstraintName(declared.Name, c.Name)] = true
		}
	}
	known := map[string]bool{}
	for _, c := range t.Checks {
		known[c.Name] = true
	}

	var adopted []DriftItem
	for _, c := range live.Checks {
		if declaredNames[c.Name] || known[c.Name] {
			continue
		}
		t.Checks = append(t.Checks, c)
		adopted = append(adopted, DriftItem{
			Kind: "check constraint", Table: t.Name, Name: c.Name, Detail: c.Expr,
		})
	}
	sort.Slice(t.Checks, func(i, j int) bool { return t.Checks[i].Name < t.Checks[j].Name })
	return adopted
}

func adoptIndexes(t *Table, live, declared Table) []DriftItem {
	declaredIdx := indexIndexes(declared.Indexes)
	known := indexIndexes(t.Indexes)

	var adopted []DriftItem
	for _, idx := range live.Indexes {
		if _, ok := declaredIdx[idx.Name]; ok {
			continue
		}
		if _, ok := known[idx.Name]; ok {
			continue
		}
		t.Indexes = append(t.Indexes, idx)
		adopted = append(adopted, DriftItem{
			Kind: "index", Table: t.Name, Name: idx.Name, Detail: describeIndex(idx),
		})
	}
	sort.Slice(t.Indexes, func(i, j int) bool { return t.Indexes[i].Name < t.Indexes[j].Name })
	return adopted
}

// adoptForeignKeys records a key the database holds on a column the snapshot
// already covers and the models do not point anywhere.
func adoptForeignKeys(t *Table, live, declared Table) []DriftItem {
	liveCols := indexColumns(live.Columns)
	declaredCols := indexColumns(declared.Columns)

	var adopted []DriftItem
	for i := range t.Columns {
		c := &t.Columns[i]
		lc, ok := liveCols[c.Name]
		if !ok || lc.Ref == "" {
			continue
		}
		if dc, ok := declaredCols[c.Name]; ok && dc.Ref != "" {
			continue // the model declares the key; the differ maintains it
		}
		if c.Ref != "" {
			continue // already recorded
		}
		c.Ref, c.RefColumn = lc.Ref, lc.RefColumn
		c.OnDelete, c.OnUpdate, c.Deferrable = lc.OnDelete, lc.OnUpdate, lc.Deferrable
		adopted = append(adopted, DriftItem{
			Kind: "foreign key", Table: t.Name, Name: c.Name, Detail: "references " + lc.Ref,
		})
	}
	return adopted
}
