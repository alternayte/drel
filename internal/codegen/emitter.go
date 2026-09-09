package codegen

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
)

// subColExport derives the exported struct-field name for a multi-col VO
// sub-column, e.g. ("balance", "balance_amount") -> "BalanceAmount".
// Underscores delimit words that are individually title-cased.
func subColExport(colName string) string {
	parts := strings.Split(colName, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(exportName(p))
	}
	return b.String()
}

// exportName capitalises the first letter of a name.
func exportName(name string) string {
	if name == "" {
		return name
	}
	rs := []rune(name)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

// fieldDisplayType returns the type name to use in generated code.
// Same-package types are unqualified; external types are package-qualified using resolved aliases.
func fieldDisplayType(f FieldInfo, aliases map[string]string) string {
	if f.TypePkgPath != "" && f.LocalGoType != "" {
		alias := path.Base(f.TypePkgPath)
		if a, ok := aliases[f.TypePkgPath]; ok {
			alias = a
		}
		qualified := alias + "." + f.LocalGoType
		if f.IsPointer {
			return "*" + qualified
		}
		return qualified
	}
	if f.LocalGoType != "" {
		if f.IsPointer {
			return "*" + f.LocalGoType
		}
		return f.LocalGoType
	}
	return f.GoType
}

func resolvePKDisplay(m ModelInfo, aliases map[string]string) string {
	if m.PKTypePkg == "" {
		return m.PKType
	}
	alias := path.Base(m.PKTypePkg)
	if a, ok := aliases[m.PKTypePkg]; ok {
		alias = a
	}
	parts := strings.SplitN(m.PKType, ".", 2)
	if len(parts) == 2 {
		return alias + "." + parts[1]
	}
	return m.PKType
}

// columnTypeName returns the drel column type for a Go type.
func columnTypeName(goType string) string {
	switch goType {
	case "string":
		return "drel.StringColumn"
	case "bool":
		return "drel.BoolColumn"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return fmt.Sprintf("drel.OrderedColumn[%s]", goType)
	case "time.Time":
		return "drel.TimeColumn"
	default:
		// *time.Time, uuid.UUID, and any other comparable/VO types get
		// ComparableColumn[T] so callers have access to range operators.
		return fmt.Sprintf("drel.ComparableColumn[%s]", goType)
	}
}

// usesJSONWrapper reports whether the field round-trips through drel.JSON[T]
// (default for JSON/array fields). A TypeOverride pointing at a native SQL type
// (e.g. text[]) bypasses the wrapper and passes the Go value straight to the
// driver, which encodes it natively (pgx slices -> arrays).
func usesJSONWrapper(f FieldInfo) bool {
	return f.IsJSON && f.TypeOverride == ""
}

// isSliceGoType / isMapGoType classify the diff strategy from the Go type string.
func isSliceGoType(goType string) bool {
	return strings.HasPrefix(strings.TrimPrefix(goType, "*"), "[]")
}

func isMapGoType(goType string) bool {
	return strings.HasPrefix(strings.TrimPrefix(goType, "*"), "map[")
}

// isMappableField reports whether codegen can emit valid DDL + scan/value/diff
// for the field. Anything else is rejected loudly instead of emitting text + !=.
func isMappableField(f FieldInfo) bool {
	if f.TypeOverride != "" {
		return true
	}
	if isPrimitiveType(strings.TrimPrefix(f.GoType, "*")) {
		return true
	}
	// time.Time and *time.Time are handled natively by the drivers.
	switch f.GoType {
	case "time.Time", "*time.Time":
		return true
	}
	if f.LocalGoType == "time.Time" || f.LocalGoType == "*time.Time" {
		return true
	}
	// Named primitive types (e.g. type Priority int with no enum consts) are
	// comparable with != and generate valid diff code.
	if f.IsNamedPrimitive {
		return true
	}
	return f.IsVO || f.IsMultiColVO || f.IsEnum || f.IsJSON
}

// columnConstructor returns the drel constructor call for a Go type and column name.
func columnConstructor(goType, colName string) string {
	switch goType {
	case "string":
		return fmt.Sprintf("drel.NewStringCol(%q)", colName)
	case "bool":
		return fmt.Sprintf("drel.NewBoolCol(%q)", colName)
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return fmt.Sprintf("drel.NewOrderedCol[%s](%q)", goType, colName)
	case "time.Time":
		return fmt.Sprintf("drel.NewTimeCol(%q)", colName)
	default:
		// *time.Time, uuid.UUID, and any other comparable/VO types.
		return fmt.Sprintf("drel.NewComparableCol[%s](%q)", goType, colName)
	}
}

func buildModelImportAliases(m ModelInfo) map[string]string {
	aliases := make(map[string]string)
	aliasCount := make(map[string]int)

	addPkg := func(pkgPath string) {
		if pkgPath == "" || pkgPath == "time" || pkgPath == m.PkgPath {
			return
		}
		if _, ok := aliases[pkgPath]; ok {
			return
		}
		base := path.Base(pkgPath)
		aliasCount[base]++
		if aliasCount[base] == 1 {
			aliases[pkgPath] = base
		} else {
			aliases[pkgPath] = fmt.Sprintf("%s%d", base, aliasCount[base])
		}
	}

	if m.PKTypePkg != "" {
		addPkg(m.PKTypePkg)
	}
	// A struct key's field types live in their own packages, which nothing
	// else in the model necessarily imports.
	for _, kc := range m.Key {
		if kc.PkgPath != "" {
			addPkg(kc.PkgPath)
		}
	}
	for _, f := range columnFields(m.Fields) {
		if f.TypePkgPath != "" {
			addPkg(f.TypePkgPath)
		}
	}
	return aliases
}

func emitImports(b *strings.Builder, m ModelInfo, extAliases map[string]string) {
	// "context" is always needed (FindAll etc. take a context.Context parameter).
	// "time" is needed only when *time.Time appears directly in the generated
	// column-refs struct (HasSoftDelete emits ComparableColumn[*time.Time]) or
	// when a user field explicitly lives in the "time" package. TimeColumn and
	// the ScanPtrs()/CreatedAt()/UpdatedAt() helpers are in the drel package, so
	// they do NOT pull in "time" on their own.
	stdImports := map[string]bool{"context": true}

	if m.HasSoftDelete {
		stdImports["time"] = true
	}
	if m.PKTypePkg == "time" {
		stdImports["time"] = true
	}
	for _, f := range columnFields(m.Fields) {
		if f.TypePkgPath == "time" {
			stdImports["time"] = true
		}
		if f.IsMultiColVO {
			stdImports["fmt"] = true
		}
		if f.IsJSON && isSliceGoType(f.GoType) {
			stdImports["slices"] = true
		}
		if f.IsJSON && isMapGoType(f.GoType) {
			stdImports["maps"] = true
		}
	}

	b.WriteString("import (\n")
	var stdList []string
	for pkg := range stdImports {
		stdList = append(stdList, pkg)
	}
	sort.Strings(stdList)
	for _, pkg := range stdList {
		b.WriteString(fmt.Sprintf("\t%q\n", pkg))
	}

	b.WriteString("\n\t\"github.com/alternayte/drel\"\n")

	var extList []string
	for pkg := range extAliases {
		extList = append(extList, pkg)
	}
	sort.Strings(extList)
	for _, pkg := range extList {
		alias := extAliases[pkg]
		b.WriteString(fmt.Sprintf("\t%s %q\n", alias, pkg))
	}
	b.WriteString(")\n\n")
}

// columnFields returns only the fields that map to database columns (non-relationship fields).
func columnFields(fields []FieldInfo) []FieldInfo {
	var out []FieldInfo
	for _, f := range fields {
		if f.ColumnName != "" {
			out = append(out, f)
		}
	}
	return out
}

// EmitModelFileChecked generates the per-model file, returning an error if the
// model uses unsupported features. Multi-column value objects are fully
// supported (see emitMultiValHelpers / expanded scan/diff/DDL).
func EmitModelFileChecked(m ModelInfo) (string, error) {
	for _, f := range m.Fields {
		if f.IsVO && !f.HasEqual && !f.IsComparable {
			return "", fmt.Errorf("drel: model %q field %q implements sql.Scanner + driver.Valuer (single-column value object) but its type is not comparable (contains a slice, map, or func) and has no Equal(T) bool method; add an Equal method so change-tracking can diff it", m.Name, f.Name)
		}
	}
	// Every db-mapped scalar field is compared with != in the generated diff
	// function, so its Go type must be comparable. Reject anything that is not a
	// primitive, time.Time, a single-column VO, enum, or JSON/array before emitting
	// code that would only fail at the user's subsequent `go build`.
	for _, f := range m.Fields {
		if f.ColumnName != "" && f.Relation == nil && !isMappableField(f) {
			return "", fmt.Errorf("drel: model %q field %q has unsupported column type %q: it is not a primitive, value object, enum, JSON/array, and has no `db:\"...,type=...\"` override. Add a type override, implement sql.Scanner+driver.Valuer, or remove the db tag", m.Name, f.Name, f.GoType)
		}
	}
	return EmitModelFile(m), nil
}

// isComparableForDiff reports whether a db-mapped field's Go type can be safely
// compared with != in the generated diff function. Primitives and time.Time are
// comparable; VOs and enums are handled by their own == support and are filtered
// out by the caller before reaching here. Named types over a comparable basic
// kind (e.g. type Priority int) with no enum consts are also accepted.
func isComparableForDiff(f FieldInfo) bool {
	if isPrimitiveType(f.GoType) {
		return true
	}
	// time.Time is comparable and is the one non-primitive framework type the
	// scanner supports directly (matching extractFields' supported set).
	switch f.GoType {
	case "time.Time", "*time.Time":
		return true
	}
	if f.LocalGoType == "time.Time" || f.LocalGoType == "*time.Time" {
		return true
	}
	// Named types whose underlying kind is a comparable basic type (e.g.
	// type Priority int) are comparable with != and generate valid diff code.
	if f.IsNamedPrimitive {
		return true
	}
	return false
}

// EmitModelFile generates the complete per-model _drel.go file content.
func EmitModelFile(m ModelInfo) string {
	var b strings.Builder
	lower := strings.ToLower(m.Name)
	varPlural := pluralize(m.Name)

	aliases := buildModelImportAliases(m)

	// Header
	b.WriteString("// Code generated by drel. DO NOT EDIT.\n\n")
	b.WriteString(fmt.Sprintf("package %s\n\n", m.PkgName))
	emitImports(&b, m, aliases)

	// --- Column references ---
	emitColumnRefs(&b, m, varPlural, aliases)

	// --- Generated enum validators (IsValid / Values) ---
	emitEnumValidators(&b, m)

	// --- Multi-column VO value helpers ---
	emitMultiValHelpers(&b, m, lower, aliases)

	// --- All columns list ---
	allCols := allColumns(m)

	// --- Scan function ---
	emitScanFunc(&b, m, lower, allCols, aliases)

	// --- Snapshot struct + function ---
	emitSnapshot(&b, m, lower, aliases)

	// --- Diff function ---
	emitDiff(&b, m, lower, aliases)

	// --- PK value ---
	b.WriteString(fmt.Sprintf("func %sPKValue(p *%s) any {\n\treturn p.ID()\n}\n\n", lower, m.Name))

	// --- Key splitter (struct keys only) ---
	emitKeyValues(&b, m, lower)

	// --- Column value ---
	emitColumnValue(&b, m, lower, allCols, aliases)

	// --- Insert columns ---
	emitInsertColumns(&b, m, lower, aliases)

	// --- Scan returning ---
	emitScanReturning(&b, m, lower)

	// --- Key normalizer (matches pivot keys to the canonical PK type) ---
	emitNormalizeKey(&b, m, lower, aliases)

	// --- Key funcs (app-assigned PKs only; a struct key is always app-assigned) ---
	if m.KeyIsStruct || isAppAssignedPK(m.PKType) {
		emitKeyFuncs(&b, m, lower)
	}

	// --- ModelMeta ---
	emitMeta(&b, m, lower, varPlural, allCols)

	// --- Typed repository wrappers ---
	emitTypedRepos(&b, m)

	return b.String()
}

func emitColumnRefs(b *strings.Builder, m ModelInfo, varPlural string, aliases map[string]string) {
	colFields := columnFields(m.Fields)
	// Struct type definition
	b.WriteString(fmt.Sprintf("var %s = struct {\n", varPlural))
	// ID column - always present, derived from PKType
	pkDisplay := resolvePKDisplay(m, aliases)
	b.WriteString(fmt.Sprintf("\tID %s\n", columnTypeName(pkDisplay)))
	for _, f := range colFields {
		if f.IsMultiColVO {
			for _, sub := range f.MultiColNames {
				b.WriteString(fmt.Sprintf("\t%s drel.Column[any]\n", subColExport(sub)))
			}
			continue
		}
		b.WriteString(fmt.Sprintf("\t%s %s\n", exportName(f.Name), columnTypeName(fieldDisplayType(f, aliases))))
	}
	if m.HasSoftDelete {
		b.WriteString("\tDeletedAt drel.ComparableColumn[*time.Time]\n")
	}
	if m.HasVersioned {
		b.WriteString("\tVersion drel.OrderedColumn[int]\n")
	}
	if m.HasAudit {
		b.WriteString("\tCreatedBy drel.StringColumn\n")
		b.WriteString("\tUpdatedBy drel.StringColumn\n")
	}
	b.WriteString("\tCreatedAt drel.TimeColumn\n")
	b.WriteString("\tUpdatedAt drel.TimeColumn\n")
	b.WriteString("}{\n")

	// Struct literal values
	b.WriteString(fmt.Sprintf("\tID: %s,\n", columnConstructor(pkDisplay, "id")))
	for _, f := range colFields {
		if f.IsMultiColVO {
			for _, sub := range f.MultiColNames {
				b.WriteString(fmt.Sprintf("\t%s: drel.NewCol[any](%q),\n", subColExport(sub), sub))
			}
			continue
		}
		b.WriteString(fmt.Sprintf("\t%s: %s,\n", exportName(f.Name), columnConstructor(fieldDisplayType(f, aliases), f.ColumnName)))
	}
	if m.HasSoftDelete {
		b.WriteString("\tDeletedAt: drel.NewComparableCol[*time.Time](\"deleted_at\"),\n")
	}
	if m.HasVersioned {
		b.WriteString("\tVersion: drel.NewOrderedCol[int](\"version\"),\n")
	}
	if m.HasAudit {
		b.WriteString("\tCreatedBy: drel.NewStringCol(\"created_by\"),\n")
		b.WriteString("\tUpdatedBy: drel.NewStringCol(\"updated_by\"),\n")
	}
	b.WriteString("\tCreatedAt: drel.NewTimeCol(\"created_at\"),\n")
	b.WriteString("\tUpdatedAt: drel.NewTimeCol(\"updated_at\"),\n")
	b.WriteString("}\n\n")
}

// emitEnumValidators emits, for each distinct enum type used by the model, a
// `func (r <Enum>) IsValid() bool` and a `func <Enum>Values() []<Enum>` helper.
// String-enum values are quoted; integer-enum values are bare numeric literals.
func emitEnumValidators(b *strings.Builder, m ModelInfo) {
	seen := map[string]bool{}
	for _, f := range columnFields(m.Fields) {
		if !f.IsEnum || len(f.EnumValues) == 0 {
			continue
		}
		typeName := f.LocalGoType
		if seen[typeName] {
			continue
		}
		seen[typeName] = true

		lits := make([]string, len(f.EnumValues))
		for i, v := range f.EnumValues {
			if f.EnumIsInt {
				lits[i] = fmt.Sprintf("%s(%s)", typeName, v)
			} else {
				lits[i] = fmt.Sprintf("%s(%q)", typeName, v)
			}
		}

		// Values()
		b.WriteString(fmt.Sprintf("func %sValues() []%s {\n", typeName, typeName))
		b.WriteString(fmt.Sprintf("\treturn []%s{%s}\n", typeName, strings.Join(lits, ", ")))
		b.WriteString("}\n\n")

		// IsValid()
		b.WriteString(fmt.Sprintf("func (r %s) IsValid() bool {\n", typeName))
		b.WriteString(fmt.Sprintf("\tfor _, v := range %sValues() {\n", typeName))
		b.WriteString("\t\tif r == v {\n\t\t\treturn true\n\t\t}\n")
		b.WriteString("\t}\n\treturn false\n}\n\n")
	}
}

// allColumns returns the full ordered list of column names for this model.
func allColumns(m ModelInfo) []string {
	cols := []string{"id"}
	for _, f := range columnFields(m.Fields) {
		if f.IsMultiColVO {
			cols = append(cols, f.MultiColNames...)
			continue
		}
		cols = append(cols, f.ColumnName)
	}
	if m.HasSoftDelete {
		cols = append(cols, "deleted_at")
	}
	if m.HasVersioned {
		cols = append(cols, "version")
	}
	if m.HasAudit {
		cols = append(cols, "created_by", "updated_by")
	}
	cols = append(cols, "created_at", "updated_at")
	return cols
}

func emitMultiValHelpers(b *strings.Builder, m ModelInfo, lower string, aliases map[string]string) {
	for _, f := range columnFields(m.Fields) {
		if !f.IsMultiColVO {
			continue
		}
		voType := fieldDisplayType(f, aliases)
		b.WriteString(fmt.Sprintf("func %sMultiVals(v %s) []any {\n", lower, voType))
		b.WriteString("\tvals, err := v.DrelValues()\n")
		b.WriteString(fmt.Sprintf("\tif err != nil {\n\t\tpanic(fmt.Sprintf(%q, err))\n\t}\n", "drel: "+m.Name+" multi-column VO DrelValues: %v"))
		b.WriteString("\treturn vals\n}\n\n")
	}
}

func emitScanFunc(b *strings.Builder, m ModelInfo, lower string, allCols []string, aliases map[string]string) {
	b.WriteString(fmt.Sprintf("func scan%s(row drel.Row) (*%s, error) {\n", exportName(lower), m.Name))
	b.WriteString(fmt.Sprintf("\tp := &%s{}\n", m.Name))
	b.WriteString("\tidPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()\n")
	if m.HasAudit {
		b.WriteString("\tcreatedByPtr, updatedByPtr := p.AuditPtrs()\n")
	}

	// Pre-declare multi-col VO scan temporaries.
	for _, f := range columnFields(m.Fields) {
		if f.IsMultiColVO {
			b.WriteString(fmt.Sprintf("\tvar %sVals = make([]any, %d)\n", f.Name, len(f.MultiColNames)))
		}
	}

	// Build scan args
	var scanArgs []string
	scanArgs = append(scanArgs, "idPtr")
	for _, f := range columnFields(m.Fields) {
		if f.IsMultiColVO {
			for i := range f.MultiColNames {
				scanArgs = append(scanArgs, fmt.Sprintf("&%sVals[%d]", f.Name, i))
			}
			continue
		}
		if f.IsVO && f.HasIsZero {
			// The VO's own Scan(nil) handles a SQL NULL; its Value() returns nil
			// for the zero value, so the column round-trips zero <-> NULL.
			b.WriteString(fmt.Sprintf("\t// %s is a nullable value object: Scan(nil)->zero, Value()==nil for zero->NULL\n", f.ColumnName))
		}
		if usesJSONWrapper(f) {
			scanArgs = append(scanArgs, fmt.Sprintf("drel.JSON[%s]{V: &p.%s}", fieldDisplayType(f, aliases), f.Name))
		} else {
			scanArgs = append(scanArgs, fmt.Sprintf("&p.%s", f.Name))
		}
	}
	if m.HasSoftDelete {
		scanArgs = append(scanArgs, "p.DeletedAtPtr()")
	}
	if m.HasVersioned {
		scanArgs = append(scanArgs, "p.VersionPtr()")
	}
	if m.HasAudit {
		scanArgs = append(scanArgs, "createdByPtr", "updatedByPtr")
	}
	scanArgs = append(scanArgs, "createdAtPtr", "updatedAtPtr")

	b.WriteString(fmt.Sprintf("\terr := row.Scan(%s)\n", strings.Join(scanArgs, ", ")))
	b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	// Reconstruct multi-col VO fields from scan temporaries.
	for _, f := range columnFields(m.Fields) {
		if f.IsMultiColVO {
			b.WriteString(fmt.Sprintf("\tif err := p.%s.DrelScanMulti(%sVals); err != nil {\n\t\treturn nil, err\n\t}\n", f.Name, f.Name))
		}
	}
	b.WriteString("\treturn p, nil\n}\n\n")
}

func emitSnapshot(b *strings.Builder, m ModelInfo, lower string, aliases map[string]string) {
	colFields := columnFields(m.Fields)
	// Snapshot struct
	b.WriteString(fmt.Sprintf("type %sSnapshot struct {\n", lower))
	for _, f := range colFields {
		if f.IsMultiColVO {
			b.WriteString(fmt.Sprintf("\t%sVals []any\n", f.Name))
			continue
		}
		b.WriteString(fmt.Sprintf("\t%s %s\n", f.Name, fieldDisplayType(f, aliases)))
	}
	b.WriteString("}\n\n")

	// Snapshot function
	b.WriteString(fmt.Sprintf("func snapshot%s(p *%s) any {\n", exportName(lower), m.Name))
	b.WriteString(fmt.Sprintf("\treturn %sSnapshot{", lower))
	for i, f := range colFields {
		if i > 0 {
			b.WriteString(", ")
		}
		if f.IsMultiColVO {
			b.WriteString(fmt.Sprintf("%sVals: %sMultiVals(p.%s)", f.Name, lower, f.Name))
			continue
		}
		b.WriteString(fmt.Sprintf("%s: p.%s", f.Name, f.Name))
	}
	b.WriteString("}\n}\n\n")
}

func emitDiff(b *strings.Builder, m ModelInfo, lower string, aliases map[string]string) {
	b.WriteString(fmt.Sprintf("func diff%s(p *%s, snap any) []drel.FieldChange {\n", exportName(lower), m.Name))
	b.WriteString(fmt.Sprintf("\ts := snap.(%sSnapshot)\n", lower))
	b.WriteString("\tvar changes []drel.FieldChange\n")
	for _, f := range columnFields(m.Fields) {
		if f.IsMultiColVO {
			b.WriteString(fmt.Sprintf("\t{\n\t\tcur := %sMultiVals(p.%s)\n", lower, f.Name))
			for i, sub := range f.MultiColNames {
				b.WriteString(fmt.Sprintf("\t\tif cur[%d] != s.%sVals[%d] {\n", i, f.Name, i))
				b.WriteString(fmt.Sprintf("\t\t\tchanges = append(changes, drel.FieldChange{Column: %q, Value: cur[%d]})\n", sub, i))
				b.WriteString("\t\t}\n")
			}
			b.WriteString("\t}\n")
			continue
		}
		valExpr := fmt.Sprintf("p.%s", f.Name)
		switch {
		case f.IsJSON && isSliceGoType(f.GoType):
			b.WriteString(fmt.Sprintf("\tif !slices.Equal(p.%s, s.%s) {\n", f.Name, f.Name))
			if usesJSONWrapper(f) {
				valExpr = fmt.Sprintf("drel.JSON[%s]{V: &p.%s}", fieldDisplayType(f, aliases), f.Name)
			}
		case f.IsJSON && isMapGoType(f.GoType):
			b.WriteString(fmt.Sprintf("\tif !maps.Equal(p.%s, s.%s) {\n", f.Name, f.Name))
			if usesJSONWrapper(f) {
				valExpr = fmt.Sprintf("drel.JSON[%s]{V: &p.%s}", fieldDisplayType(f, aliases), f.Name)
			}
		case f.IsJSON:
			// Struct/other JSON: compare marshaled bytes; emit drel.JSONEqual helper.
			b.WriteString(fmt.Sprintf("\tif !drel.JSONEqual(p.%s, s.%s) {\n", f.Name, f.Name))
			valExpr = fmt.Sprintf("drel.JSON[%s]{V: &p.%s}", fieldDisplayType(f, aliases), f.Name)
		default:
			cond := fmt.Sprintf("p.%s != s.%s", f.Name, f.Name)
			if f.IsVO && f.HasEqual {
				cond = fmt.Sprintf("!p.%s.Equal(s.%s)", f.Name, f.Name)
			}
			b.WriteString(fmt.Sprintf("\tif %s {\n", cond))
		}
		b.WriteString(fmt.Sprintf("\t\tchanges = append(changes, drel.FieldChange{Column: %q, Value: %s})\n", f.ColumnName, valExpr))
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn changes\n}\n\n")
}

func emitColumnValue(b *strings.Builder, m ModelInfo, lower string, allCols []string, aliases map[string]string) {
	b.WriteString(fmt.Sprintf("func %sColumnValue(p *%s, idx int) any {\n", lower, m.Name))
	b.WriteString("\tswitch idx {\n")

	idx := 0
	// id (index 0)
	b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.ID()\n", idx))
	idx++

	// User-defined columns
	for _, f := range columnFields(m.Fields) {
		if f.IsMultiColVO {
			for i := range f.MultiColNames {
				b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn %sMultiVals(p.%s)[%d]\n", idx, lower, f.Name, i))
				idx++
			}
			continue
		}
		if usesJSONWrapper(f) {
			b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn drel.JSON[%s]{V: &p.%s}\n", idx, fieldDisplayType(f, aliases), f.Name))
		} else {
			b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.%s\n", idx, f.Name))
		}
		idx++
	}

	// Optional embeds
	if m.HasSoftDelete {
		b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.DeletedAt()\n", idx))
		idx++
	}
	if m.HasVersioned {
		b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.Version()\n", idx))
		idx++
	}
	if m.HasAudit {
		b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.CreatedBy()\n", idx))
		idx++
		b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.UpdatedBy()\n", idx))
		idx++
	}

	// created_at, updated_at
	b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.CreatedAt()\n", idx))
	idx++
	b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn p.UpdatedAt()\n", idx))

	b.WriteString("\t}\n\treturn nil\n}\n\n")
}

func emitInsertColumns(b *strings.Builder, m ModelInfo, lower string, aliases map[string]string) {
	b.WriteString(fmt.Sprintf("func %sInsertColumns(p *%s) ([]string, []any) {\n", lower, m.Name))
	var colNames []string
	var colVals []string
	for _, f := range columnFields(m.Fields) {
		if f.IsMultiColVO {
			for i, sub := range f.MultiColNames {
				colNames = append(colNames, fmt.Sprintf("%q", sub))
				colVals = append(colVals, fmt.Sprintf("%sMultiVals(p.%s)[%d]", lower, f.Name, i))
			}
			continue
		}
		colNames = append(colNames, fmt.Sprintf("%q", f.ColumnName))
		if usesJSONWrapper(f) {
			colVals = append(colVals, fmt.Sprintf("drel.JSON[%s]{V: &p.%s}", fieldDisplayType(f, aliases), f.Name))
		} else {
			colVals = append(colVals, fmt.Sprintf("p.%s", f.Name))
		}
	}
	if m.HasAudit {
		colNames = append(colNames, `"created_by"`, `"updated_by"`)
		colVals = append(colVals, "p.CreatedBy()", "p.UpdatedBy()")
	}
	b.WriteString(fmt.Sprintf("\treturn []string{%s}, []any{%s}\n", strings.Join(colNames, ", "), strings.Join(colVals, ", ")))
	b.WriteString("}\n\n")
}

func emitScanReturning(b *strings.Builder, m ModelInfo, lower string) {
	b.WriteString(fmt.Sprintf("func %sScanReturning(p *%s, row drel.Row) error {\n", lower, m.Name))
	b.WriteString("\tidPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()\n")
	b.WriteString("\treturn row.Scan(idPtr, createdAtPtr, updatedAtPtr)\n")
	b.WriteString("}\n\n")
}

// isAppAssignedPK reports whether the model's PK is application-assigned
// (anything other than an integer type, e.g. uuid.UUID or string).
func isAppAssignedPK(pkType string) bool {
	switch pkType {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return false
	default:
		return true
	}
}

// emitKeyValues emits the splitter that turns a struct key into one value per
// key column, in key order. A scalar key needs no splitter: a nil KeyValues is
// the single-column default.
func emitKeyValues(b *strings.Builder, m ModelInfo, lower string) {
	if !m.KeyIsStruct {
		return
	}
	b.WriteString(fmt.Sprintf("// %sKeyValues splits a %s into one value per key column, in key order.\n", lower, m.PKType))
	b.WriteString(fmt.Sprintf("func %sKeyValues(key any) []any {\n", lower))
	b.WriteString(fmt.Sprintf("\tk := key.(%s)\n", m.PKType))
	parts := make([]string, len(m.Key))
	for i, kc := range m.Key {
		parts[i] = "k." + kc.FieldName
	}
	b.WriteString(fmt.Sprintf("\treturn []any{%s}\n}\n\n", strings.Join(parts, ", ")))
}

// normalizeKeyExpr returns the expression that converts one raw driver value
// into a value of kind, which must be "uuid.UUID", "string", or "int". It is
// the single source of the per-type conversion rule, shared by the scalar and
// the struct key normalizers.
func normalizeKeyExpr(kind, valExpr string) string {
	switch {
	case kind == "uuid.UUID":
		return fmt.Sprintf("drel.NormalizeUUIDKey(%s)", valExpr)
	case isAppAssignedPK(kind):
		// string or other app-assigned key: identity.
		return valExpr
	default:
		// integer key column.
		return fmt.Sprintf("drel.NormalizeIntKey(%s)", valExpr)
	}
}

// keyColumnDisplayType returns the type name to write for a key column in
// generated code, package-qualified with the file's own import alias. It
// mirrors fieldDisplayType.
func keyColumnDisplayType(kc KeyColumn, aliases map[string]string) string {
	if kc.PkgPath == "" {
		return kc.GoType
	}
	alias := path.Base(kc.PkgPath)
	if a, ok := aliases[kc.PkgPath]; ok {
		alias = a
	}
	return alias + "." + kc.GoType
}

// normalizeKind returns the normalization kind for a key column: the rule that
// converts a raw driver value, and the type that rule yields. It falls back to
// the display type for a column the scanner did not classify.
func normalizeKind(kc KeyColumn) string {
	if kc.UnderlyingGoType != "" {
		return kc.UnderlyingGoType
	}
	if kc.PkgPath == "github.com/google/uuid" && kc.GoType == "UUID" {
		return "uuid.UUID"
	}
	return kc.GoType
}

// normalizeKeyParts returns the expression that converts valExpr, the type to
// assert that expression to, and the type to convert the asserted value into
// before it is stored. convert is empty when no conversion is needed.
//
// drel.NormalizeIntKey yields a Go int whatever the column's width, and a
// named type is never the dynamic type of a driver value, so the assertion is
// always to the kind and the declared type is reached by conversion.
func normalizeKeyParts(kc KeyColumn, aliases map[string]string, valExpr string) (expr, assertType, convert string) {
	kind := normalizeKind(kc)
	display := keyColumnDisplayType(kc, aliases)
	expr = normalizeKeyExpr(kind, valExpr)
	if kind == "uuid.UUID" {
		// The aliased name is the only spelling of this type in the file.
		return expr, display, ""
	}
	if display == kind {
		return expr, kind, ""
	}
	return expr, kind, display
}

func emitNormalizeKey(b *strings.Builder, m ModelInfo, lower string, aliases map[string]string) {
	if m.KeyIsStruct {
		emitStructNormalizeKey(b, m, lower, aliases)
		return
	}
	kc := scalarKeyColumn(m, aliases)
	expr, assertType, convert := normalizeKeyParts(kc, aliases, "v")
	b.WriteString(fmt.Sprintf("func %sNormalizeKey(v any) any {\n", lower))
	if convert == "" {
		b.WriteString(fmt.Sprintf("\treturn %s\n", expr))
		b.WriteString("}\n\n")
		return
	}
	b.WriteString(fmt.Sprintf("\tx, ok := %s.(%s)\n", expr, assertType))
	b.WriteString("\tif !ok {\n\t\treturn v\n\t}\n")
	b.WriteString(fmt.Sprintf("\treturn %s(x)\n", convert))
	b.WriteString("}\n\n")
}

// scalarKeyColumn describes a scalar primary key as a single key column. The
// key type is spelled with the model's own PK display name, which carries the
// import alias, so the column's own PkgPath is not used.
func scalarKeyColumn(m ModelInfo, aliases map[string]string) KeyColumn {
	kc := KeyColumn{GoType: resolvePKDisplay(m, aliases)}
	if len(m.Key) == 1 {
		kc.UnderlyingGoType = m.Key[0].UnderlyingGoType
	}
	return kc
}

func emitStructNormalizeKey(b *strings.Builder, m ModelInfo, lower string, aliases map[string]string) {
	b.WriteString(fmt.Sprintf("// %sNormalizeKey converts per-column driver values into a canonical %s,\n", lower, m.PKType))
	b.WriteString("// so a key read back from the driver compares equal to PKValue.\n")
	b.WriteString(fmt.Sprintf("func %sNormalizeKey(v any) any {\n", lower))
	b.WriteString("\tvals, ok := v.([]any)\n")
	b.WriteString("\tif !ok {\n\t\treturn v\n\t}\n")
	b.WriteString(fmt.Sprintf("\tif len(vals) != %d {\n\t\treturn v\n\t}\n", len(m.Key)))
	b.WriteString(fmt.Sprintf("\tvar k %s\n", m.PKType))
	for i, kc := range m.Key {
		expr, assertType, convert := normalizeKeyParts(kc, aliases, fmt.Sprintf("vals[%d]", i))
		b.WriteString(fmt.Sprintf("\tf%d, ok := %s.(%s)\n", i, expr, assertType))
		b.WriteString("\tif !ok {\n\t\treturn v\n\t}\n")
		if convert == "" {
			b.WriteString(fmt.Sprintf("\tk.%s = f%d\n", kc.FieldName, i))
			continue
		}
		b.WriteString(fmt.Sprintf("\tk.%s = %s(f%d)\n", kc.FieldName, convert, i))
	}
	b.WriteString("\treturn k\n}\n\n")
}

func emitKeyFuncs(b *strings.Builder, m ModelInfo, lower string) {
	// emitTypedRepos renders FindByID with the raw m.PKType (e.g. "uuid.UUID");
	// use the same so the type matches the generated import alias.
	pkType := m.PKType

	b.WriteString(fmt.Sprintf("func %sKeyIsZero(p *%s) bool {\n", lower, m.Name))
	b.WriteString(fmt.Sprintf("\tvar zero %s\n", pkType))
	b.WriteString("\treturn p.ID() == zero\n}\n\n")

	b.WriteString(fmt.Sprintf("func %sScanGenerated(p *%s, row drel.Row) error {\n", lower, m.Name))
	b.WriteString("\t_, createdAtPtr, updatedAtPtr := p.ScanPtrs()\n")
	b.WriteString("\treturn row.Scan(createdAtPtr, updatedAtPtr)\n}\n\n")
}

func emitTypedRepos(b *strings.Builder, m ModelInfo) {
	pkType := m.PKType

	b.WriteString(fmt.Sprintf("\ntype %sRepository struct {\n", m.Name))
	b.WriteString(fmt.Sprintf("\t*drel.Repository[%s]\n", m.Name))
	b.WriteString("}\n\n")

	b.WriteString(fmt.Sprintf("func (r *%sRepository) FindByID(ctx context.Context, id %s) (*%s, error) {\n", m.Name, pkType, m.Name))
	b.WriteString("\treturn r.Find(ctx, id)\n")
	b.WriteString("}\n\n")

	b.WriteString(fmt.Sprintf("type Tx%sRepository struct {\n", m.Name))
	b.WriteString(fmt.Sprintf("\t*drel.TxRepository[%s]\n", m.Name))
	b.WriteString("}\n\n")

	b.WriteString(fmt.Sprintf("func (r *Tx%sRepository) FindByID(ctx context.Context, id %s) (*%s, error) {\n", m.Name, pkType, m.Name))
	b.WriteString("\treturn r.Find(ctx, id)\n")
	b.WriteString("}\n")
}

func emitMeta(b *strings.Builder, m ModelInfo, lower, varPlural string, allCols []string) {
	b.WriteString(fmt.Sprintf("var %sMeta = drel.ModelMeta[%s]{\n", m.Name, m.Name))
	b.WriteString(fmt.Sprintf("\tTable:   %q,\n", m.TableName))

	quoted := make([]string, len(allCols))
	for i, c := range allCols {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	b.WriteString(fmt.Sprintf("\tColumns: []string{%s},\n", strings.Join(quoted, ", ")))
	pkQuoted := make([]string, len(m.PKColumns()))
	for i, c := range m.PKColumns() {
		pkQuoted[i] = fmt.Sprintf("%q", c)
	}
	b.WriteString(fmt.Sprintf("\tPKColumns: []string{%s},\n", strings.Join(pkQuoted, ", ")))
	b.WriteString(fmt.Sprintf("\tScan:          scan%s,\n", exportName(lower)))
	b.WriteString(fmt.Sprintf("\tSnapshot:      snapshot%s,\n", exportName(lower)))
	b.WriteString(fmt.Sprintf("\tDiff:          diff%s,\n", exportName(lower)))
	b.WriteString(fmt.Sprintf("\tPKValue:       %sPKValue,\n", lower))
	if m.KeyIsStruct {
		b.WriteString(fmt.Sprintf("\tKeyValues:     %sKeyValues,\n", lower))
	}
	b.WriteString(fmt.Sprintf("\tInsertColumns: %sInsertColumns,\n", lower))
	b.WriteString(fmt.Sprintf("\tScanReturning: %sScanReturning,\n", lower))
	b.WriteString(fmt.Sprintf("\tColumnValue:   %sColumnValue,\n", lower))
	b.WriteString(fmt.Sprintf("\tNormalizeKey:  %sNormalizeKey,\n", lower))

	// A struct key is always application-assigned.
	if m.KeyIsStruct || isAppAssignedPK(m.PKType) {
		pkType := m.PKType
		b.WriteString("\tKeyStrategy: drel.KeyAppAssigned,\n")
		if m.PKType == "uuid.UUID" {
			b.WriteString("\tGenerateKey: drel.UUIDv7Key,\n")
		}
		b.WriteString(fmt.Sprintf("\tSetKey:      func(p *%s, key any) { p.SetID(key.(%s)) },\n", m.Name, pkType))
		b.WriteString(fmt.Sprintf("\tKeyIsZero:   %sKeyIsZero,\n", lower))
		b.WriteString(fmt.Sprintf("\tScanGenerated: %sScanGenerated,\n", lower))
	}

	if m.HasSoftDelete {
		b.WriteString("\tHasSoftDelete: true,\n")
		b.WriteString("\tFilters: []drel.NamedFilter{drel.SoftDeleteFilter},\n")
	}
	if m.HasVersioned {
		b.WriteString("\tHasVersioned: true,\n")
		b.WriteString(fmt.Sprintf("\tVersionValue: func(p *%s) int { return p.Version() },\n", m.Name))
		b.WriteString(fmt.Sprintf("\tSetVersion:   func(p *%s, v int) { *p.VersionPtr() = v },\n", m.Name))
	}
	if m.HasAudit {
		b.WriteString("\tHasAudit: true,\n")
		b.WriteString(fmt.Sprintf("\tAuditSetCreate: func(p *%s, actor string) {\n", m.Name))
		b.WriteString("\t\tcreatedByPtr, updatedByPtr := p.AuditPtrs()\n")
		b.WriteString("\t\t*createdByPtr = actor\n")
		b.WriteString("\t\t*updatedByPtr = actor\n")
		b.WriteString("\t},\n")
		b.WriteString(fmt.Sprintf("\tAuditSetUpdate: func(p *%s, actor string) {\n", m.Name))
		b.WriteString("\t\t_, updatedByPtr := p.AuditPtrs()\n")
		b.WriteString("\t\t*updatedByPtr = actor\n")
		b.WriteString("\t},\n")
	}

	b.WriteString("}\n")
}
