package codegen

import (
	"fmt"
	"sort"
	"strings"
)

// Drift is the difference between the live database and the schema the models
// declare. It answers the question the snapshot cannot: what does the database
// hold that drel does not manage, and what does drel expect that is not there?
type Drift struct {
	// Unmanaged lists objects the database holds that no model declares. Each
	// one is invisible to the migration differ, so a later migration is planned
	// as though it did not exist.
	Unmanaged []DriftItem
	// Missing lists objects the models declare that the database does not hold.
	// The usual cause is an unapplied migration.
	Missing []DriftItem
	// Different lists objects both sides hold in a different shape.
	Different []DriftItem
}

// DriftItem is one object, named for a person to act on.
type DriftItem struct {
	Kind   string // "table", "column", "index", "foreign key", "check constraint", "enum"
	Table  string
	Name   string
	Detail string
}

func (d DriftItem) String() string {
	s := fmt.Sprintf("%s %s", d.Kind, d.Name)
	if d.Table != "" && d.Kind != "table" {
		s = fmt.Sprintf("%s %s.%s", d.Kind, d.Table, d.Name)
	}
	if d.Detail != "" {
		s += " (" + d.Detail + ")"
	}
	return s
}

// Empty reports whether the two schemas agree.
func (d Drift) Empty() bool {
	return len(d.Unmanaged) == 0 && len(d.Missing) == 0 && len(d.Different) == 0
}

// CompareSchemas reports how the live database differs from the declared
// schema.
//
// Column types are compared through a small normalisation table, because the
// server renders a type its own way: "character varying" for varchar,
// "timestamp with time zone" for timestamptz. A CHECK expression is reported
// but never compared: the server rewrites the predicate, so no textual
// comparison holds.
func CompareSchemas(live, declared Schema) Drift {
	var d Drift

	liveTables := indexTables(live.Tables)
	declaredTables := indexTables(declared.Tables)

	for _, t := range sortedTableNames(live.Tables) {
		if _, ok := declaredTables[t]; !ok {
			d.Unmanaged = append(d.Unmanaged, DriftItem{Kind: "table", Name: t})
		}
	}
	for _, t := range sortedTableNames(declared.Tables) {
		if _, ok := liveTables[t]; !ok {
			d.Missing = append(d.Missing, DriftItem{Kind: "table", Name: t})
		}
	}

	for _, name := range sortedTableNames(declared.Tables) {
		lt, ok := liveTables[name]
		if !ok {
			continue // already reported as a missing table
		}
		dt := declaredTables[name]
		compareTable(&d, lt, dt)
	}

	liveEnums := indexEnums(live.Enums)
	declaredEnums := indexEnums(declared.Enums)
	for _, e := range live.Enums {
		if _, ok := declaredEnums[e.Name]; !ok {
			d.Unmanaged = append(d.Unmanaged, DriftItem{Kind: "enum", Name: e.Name})
		}
	}
	for _, e := range declared.Enums {
		le, ok := liveEnums[e.Name]
		if !ok {
			d.Missing = append(d.Missing, DriftItem{Kind: "enum", Name: e.Name})
			continue
		}
		if strings.Join(le.Values, ",") != strings.Join(e.Values, ",") {
			d.Different = append(d.Different, DriftItem{
				Kind: "enum", Name: e.Name,
				Detail: fmt.Sprintf("database has %s, models declare %s",
					strings.Join(le.Values, "|"), strings.Join(e.Values, "|")),
			})
		}
	}
	return d
}

func compareTable(d *Drift, live, declared Table) {
	liveCols := indexColumns(live.Columns)
	declaredCols := indexColumns(declared.Columns)

	for _, c := range live.Columns {
		if _, ok := declaredCols[c.Name]; !ok {
			d.Unmanaged = append(d.Unmanaged, DriftItem{Kind: "column", Table: live.Name, Name: c.Name})
		}
	}
	for _, c := range declared.Columns {
		lc, ok := liveCols[c.Name]
		if !ok {
			d.Missing = append(d.Missing, DriftItem{Kind: "column", Table: live.Name, Name: c.Name})
			continue
		}
		if !sameSQLType(lc.Type, c.Type) {
			d.Different = append(d.Different, DriftItem{
				Kind: "column", Table: live.Name, Name: c.Name,
				Detail: fmt.Sprintf("database has %s, models declare %s", lc.Type, c.Type),
			})
		}
		if lc.Ref != c.Ref {
			switch {
			case c.Ref == "":
				d.Unmanaged = append(d.Unmanaged, DriftItem{
					Kind: "foreign key", Table: live.Name, Name: c.Name,
					Detail: "references " + lc.Ref,
				})
			case lc.Ref == "":
				d.Missing = append(d.Missing, DriftItem{
					Kind: "foreign key", Table: live.Name, Name: c.Name,
					Detail: "references " + c.Ref,
				})
			default:
				d.Different = append(d.Different, DriftItem{
					Kind: "foreign key", Table: live.Name, Name: c.Name,
					Detail: fmt.Sprintf("database references %s, models declare %s", lc.Ref, c.Ref),
				})
			}
		}
	}

	liveIdx := indexIndexes(live.Indexes)
	declaredIdx := indexIndexes(declared.Indexes)
	for _, i := range live.Indexes {
		if _, ok := declaredIdx[i.Name]; !ok {
			d.Unmanaged = append(d.Unmanaged, DriftItem{
				Kind: "index", Table: live.Name, Name: i.Name,
				Detail: describeIndex(i),
			})
		}
	}
	for _, i := range declared.Indexes {
		li, ok := liveIdx[i.Name]
		if !ok {
			d.Missing = append(d.Missing, DriftItem{
				Kind: "index", Table: live.Name, Name: i.Name, Detail: describeIndex(i),
			})
			continue
		}
		if !li.SameShape(i) {
			d.Different = append(d.Different, DriftItem{
				Kind: "index", Table: live.Name, Name: i.Name,
				Detail: fmt.Sprintf("database has %s, models declare %s", describeIndex(li), describeIndex(i)),
			})
		}
	}

	// A CHECK constraint is reported when drel did not name it. The expression
	// the server stores never matches the text a person wrote, so the
	// comparison is by name alone.
	declaredChecks := map[string]bool{}
	for _, c := range declared.Columns {
		if c.Check != "" {
			declaredChecks[checkConstraintName(declared.Name, c.Name)] = true
		}
	}
	for _, c := range live.Checks {
		if !declaredChecks[c.Name] {
			d.Unmanaged = append(d.Unmanaged, DriftItem{
				Kind: "check constraint", Table: live.Name, Name: c.Name, Detail: c.Expr,
			})
		}
	}
}

func describeIndex(i Index) string {
	s := "(" + strings.Join(i.Columns, ", ") + ")"
	if i.Unique {
		s = "unique " + s
	}
	if i.Where != "" {
		s += " where " + i.Where
	}
	return s
}

func sortedTableNames(tables []Table) []string {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// sqlTypeAliases maps a server's rendering of a type to drel's. Only exact
// synonyms belong here: a wrong entry hides a real difference.
var sqlTypeAliases = map[string]string{
	"character varying":           "varchar",
	"timestamp with time zone":    "timestamptz",
	"timestamp without time zone": "timestamp",
	"time with time zone":         "timetz",
	"double precision":            "double precision",
	"int2":                        "smallint",
	"int4":                        "integer",
	"int8":                        "bigint",
	"int":                         "integer",
	"bool":                        "boolean",
	"float8":                      "double precision",
	"float4":                      "real",
	"serial":                      "integer",
	"bigserial":                   "bigint",
	"smallserial":                 "smallint",
	"character":                   "char",
	"datetime":                    "timestamp",
}

// sameSQLType reports whether two type renderings name the same type. The
// declared side may carry a key constraint (SERIAL PRIMARY KEY), which the
// database reports as the underlying type plus a separate default.
func sameSQLType(live, declared string) bool {
	return normalizeSQLType(live) == normalizeSQLType(declared)
}

func normalizeSQLType(t string) string {
	n := strings.ToLower(strings.TrimSpace(t))
	n = strings.TrimSuffix(n, " primary key")
	n = strings.TrimSuffix(n, " autoincrement")
	n = strings.TrimSpace(strings.TrimSuffix(n, " primary key"))
	n = strings.Trim(n, `"`)
	// A length or precision does not change which type it is for this report.
	if i := strings.IndexByte(n, '('); i >= 0 {
		n = strings.TrimSpace(n[:i])
	}
	if alias, ok := sqlTypeAliases[n]; ok {
		return alias
	}
	return n
}
