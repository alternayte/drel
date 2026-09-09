package codegen

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func ScanPackages(patterns []string, dir ...string) ([]ModelInfo, error) {
	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedName | packages.NeedFiles | packages.NeedDeps | packages.NeedImports,
	}
	if len(dir) > 0 && dir[0] != "" {
		cfg.Dir = dir[0]
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("codegen: load packages: %w", err)
	}

	var models []ModelInfo
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("codegen: package %s has errors: %v", pkg.PkgPath, pkg.Errors[0])
		}
		pkgModels, err := scanPackage(pkg)
		if err != nil {
			return nil, err
		}
		models = append(models, pkgModels...)
	}

	// Deterministic cross-package ordering: packages.Load order is not
	// contractual, so sort by import path then model name. Within a package
	// scope.Names() is already sorted, but across packages it is not. This
	// keeps the aggregated DB file byte-identical across runs/OSes/tool versions.
	sort.Slice(models, func(i, j int) bool {
		if models[i].PkgPath != models[j].PkgPath {
			return models[i].PkgPath < models[j].PkgPath
		}
		return models[i].Name < models[j].Name
	})
	return models, nil
}

func scanPackage(pkg *packages.Package) ([]ModelInfo, error) {
	var models []ModelInfo
	scope := pkg.Types.Scope()

	// Determine the filesystem directory from the package's Go files.
	var pkgDir string
	if len(pkg.GoFiles) > 0 {
		pkgDir = filepath.Dir(pkg.GoFiles[0])
	}

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		st, ok := tn.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}

		pkInfo, hasModel := findModelEmbed(st)
		if !hasModel {
			continue
		}

		mi := ModelInfo{
			Name:       tn.Name(),
			PkgPath:    pkg.PkgPath,
			PkgName:    pkg.Name,
			PKType:     pkInfo.Display,
			PKTypeFull: pkInfo.Full,
			PKTypePkg:  pkInfo.PkgPath,
			Dir:        pkgDir,
		}

		switch mi.PKType {
		case "uint", "uint8", "uint16", "uint32", "uint64":
			return nil, fmt.Errorf(
				"codegen: model %s: unsigned integer primary keys (%s) are not supported; use a signed integer (auto-increment) or uuid.UUID / string (app-assigned)",
				mi.Name, mi.PKType)
		}

		keyCols, keyIsStruct, kErr := buildKeyColumns(pkInfo, tn.Name(), pkg.PkgPath)
		if kErr != nil {
			return nil, kErr
		}
		mi.Key = keyCols
		mi.KeyIsStruct = keyIsStruct

		mi.TableName = inferTableName(tn.Name())
		_, modelOpts, mErr := parseModelTag(pkInfo.Tag)
		if mErr != nil {
			return nil, fmt.Errorf("codegen: model %s: %w", tn.Name(), mErr)
		}
		if modelOpts.table != "" {
			mi.TableName = modelOpts.table
		}
		mi.RenamedFrom = modelOpts.renamedFrom
		mi.HasSoftDelete, mi.HasVersioned, mi.HasAudit = detectEmbeds(st)
		flds, fErr := extractFields(st, pkg.PkgPath)
		if fErr != nil {
			return nil, fmt.Errorf("codegen: model %s: %w", tn.Name(), fErr)
		}
		mi.Fields = flds

		// Populate m2m convention defaults for relationship fields.
		for j := range mi.Fields {
			f := &mi.Fields[j]
			if f.Relation != nil && f.Relation.Type == "many_to_many" {
				sourceTable := inferTableName(tn.Name())
				targetTable := inferTableName(f.Relation.TargetModel)
				if f.Relation.JoinTable == "" {
					a, b := sourceTable, targetTable
					if a > b {
						a, b = b, a
					}
					f.Relation.JoinTable = singularize(a) + "_" + singularize(b)
				}
				if f.Relation.FK == "" {
					f.Relation.FK = singularize(sourceTable) + "_id"
				}
				if f.Relation.RefColumn == "" {
					f.Relation.RefColumn = singularize(targetTable) + "_id"
				}
			}
		}

		models = append(models, mi)
	}
	return models, nil
}

type pkTypeInfo struct {
	Display string     // short name for generated code (e.g., "int", "uuid.UUID")
	Full    string     // fully qualified (e.g., "github.com/google/uuid.UUID")
	PkgPath string     // import path (empty for primitives)
	Type    types.Type // the key type argument itself
	Tag     string     // raw struct tag on the embedded drel.Model field
}

func findModelEmbed(st *types.Struct) (info pkTypeInfo, found bool) {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Embedded() {
			continue
		}
		named, ok := f.Type().(*types.Named)
		if !ok {
			continue
		}
		obj := named.Obj()
		if obj.Name() != "Model" {
			continue
		}
		if obj.Pkg() == nil || !strings.HasSuffix(obj.Pkg().Path(), "drel") {
			continue
		}
		targs := named.TypeArgs()
		if targs == nil || targs.Len() != 1 {
			continue
		}
		t := targs.At(0)
		full := t.String()
		display := full
		pkgPath := ""
		if n, ok := t.(*types.Named); ok {
			display = localTypeName(t)
			if p := n.Obj().Pkg(); p != nil {
				pkgPath = p.Path()
				if pkgPath != "" {
					display = path.Base(pkgPath) + "." + display
				}
			}
		}
		return pkTypeInfo{Display: display, Full: full, PkgPath: pkgPath, Type: t, Tag: st.Tag(i)}, true
	}
	return pkTypeInfo{}, false
}

// buildKeyColumns resolves a model's primary key columns from the type argument
// of its embedded drel.Model. A scalar type argument yields one column, named by
// the first position of the embedded field's db tag and defaulting to "id". A
// struct type argument yields one column per exported field, named by that
// field's own db tag and defaulting to the snake-case field name.
func buildKeyColumns(pk pkTypeInfo, modelName, ownerPkgPath string) ([]KeyColumn, bool, error) {
	kst, isStruct := pk.Type.Underlying().(*types.Struct)
	if !isStruct {
		name := "id"
		col, _, err := parseModelTag(pk.Tag)
		if err != nil {
			return nil, false, fmt.Errorf("codegen: model %s: primary key tag: %w", modelName, err)
		}
		if col != "" {
			name = col
		}
		return []KeyColumn{{
			ColumnName:       name,
			GoType:           pk.Display,
			UnderlyingGoType: keyNormalizeKind(pk.Type),
		}}, false, nil
	}

	// A struct key takes every column name from the key struct's own field
	// tags, so a column name in the first position of the embedded field's db
	// tag has nowhere to apply. Reject it rather than ignore it. The other
	// options on that tag (table=, renamed_from=) describe the table, not the
	// key, and stay valid; scanPackage parses them separately.
	if pkCol, _, err := parseModelTag(pk.Tag); err != nil {
		return nil, false, fmt.Errorf("codegen: model %s: primary key tag: %w", modelName, err)
	} else if pkCol != "" {
		return nil, false, fmt.Errorf(
			"codegen: model %s: db tag %q on the embedded drel.Model names a key column, but composite key type %s takes its column names from its own field tags; remove %q and put a db tag on the %s field instead",
			modelName, pkCol, pk.Display, pkCol, pk.Display)
	}

	var cols []KeyColumn
	for i := 0; i < kst.NumFields(); i++ {
		f := kst.Field(i)
		if !f.Exported() {
			return nil, false, fmt.Errorf(
				"codegen: model %s: composite key type %s has unexported field %q; generated code cannot read it, export the field",
				modelName, pk.Display, f.Name())
		}
		if !isSupportedKeyFieldType(f.Type()) {
			return nil, false, fmt.Errorf(
				"codegen: model %s: composite key type %s field %q has unsupported type %s; key fields must be a signed integer, string, or uuid.UUID",
				modelName, pk.Display, f.Name(), f.Type().String())
		}
		col, _, err := parseDBTag(kst.Tag(i))
		if err != nil {
			return nil, false, fmt.Errorf("codegen: model %s: key field %s: %w", modelName, f.Name(), err)
		}
		if col == "" {
			col = toSnakeCase(f.Name())
		}
		// Mirror extractFields: record the field type's package unless it is
		// the model's own package, so the emitter can import and alias it.
		fieldPkg := typePkgPath(f.Type())
		if fieldPkg == ownerPkgPath {
			fieldPkg = ""
		}
		cols = append(cols, KeyColumn{
			FieldName:        f.Name(),
			ColumnName:       col,
			GoType:           localTypeName(f.Type()),
			PkgPath:          fieldPkg,
			UnderlyingGoType: keyNormalizeKind(f.Type()),
		})
	}
	if len(cols) == 0 {
		return nil, false, fmt.Errorf("codegen: model %s: composite key type %s has no fields", modelName, pk.Display)
	}
	return cols, true, nil
}

// keyNormalizeKind classifies a key type by how a raw driver value for it must
// be converted: "uuid.UUID", "string", or "int" for every signed integer
// width. It returns "" for a type the emitter cannot classify, which falls
// back to the display type. It mirrors isSupportedKeyFieldType.
func keyNormalizeKind(t types.Type) string {
	if n, ok := t.(*types.Named); ok {
		if o := n.Obj(); o != nil && o.Pkg() != nil &&
			o.Pkg().Path() == "github.com/google/uuid" && o.Name() == "UUID" {
			return "uuid.UUID"
		}
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return ""
	}
	switch b.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
		// NormalizeIntKey yields a Go int whatever the column's width.
		return "int"
	case types.String:
		return "string"
	}
	return ""
}

// isSupportedKeyFieldType reports whether a composite key field's type can be a
// primary key column: a signed integer, a string, or uuid.UUID (including named
// types over those).
func isSupportedKeyFieldType(t types.Type) bool {
	if n, ok := t.(*types.Named); ok {
		if o := n.Obj(); o != nil && o.Pkg() != nil &&
			o.Pkg().Path() == "github.com/google/uuid" && o.Name() == "UUID" {
			return true
		}
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	switch b.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64, types.String:
		return true
	}
	return false
}

func detectEmbeds(st *types.Struct) (softDelete, versioned, audit bool) {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Embedded() {
			continue
		}
		named, ok := f.Type().(*types.Named)
		if !ok {
			continue
		}
		switch named.Obj().Name() {
		case "SoftDelete":
			softDelete = true
		case "Versioned":
			versioned = true
		case "Audit":
			audit = true
		}
	}
	return
}

func extractFields(st *types.Struct, ownerPkgPath string) ([]FieldInfo, error) {
	var fields []FieldInfo
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Embedded() {
			continue
		}
		tag := st.Tag(i)
		relTag := parseRelTag(tag)

		// For multi-column VOs every comma-separated segment in the db tag is a
		// column name, not an option keyword. Check the type before strict-parsing
		// to avoid falsely rejecting column names as unknown options.
		isMultiCol := isMultiColumnMapper(f.Type())

		var dbCol string
		var dbOpts dbTagOpts
		if isMultiCol {
			// Lenient: just extract the first column name without option parsing.
			dbCol = firstDBTagSegment(tag)
		} else {
			var err error
			dbCol, dbOpts, err = parseDBTag(tag)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", f.Name(), err)
			}
		}

		if dbCol == "" && relTag == "" {
			continue
		}

		fi := FieldInfo{
			Name:         f.Name(),
			GoType:       f.Type().String(),
			ColumnName:   dbCol,
			IsExported:   f.Exported(),
			RelTag:       relTag,
			RenamedFrom:  dbOpts.renamedFrom,
			Unique:       dbOpts.unique,
			Indexed:      dbOpts.indexed,
			IndexName:    dbOpts.indexName,
			CheckExpr:    dbOpts.check,
			Default:      dbOpts.def,
			TypeOverride: dbOpts.typ,
		}

		if relTag != "" {
			fi.Relation = parseRelTagStructured(relTag)
			fi.LocalGoType = localTypeName(f.Type())
			if fi.Relation != nil {
				fi.Relation.TargetModel = targetModelName(f.Type())
			}
		}

		if dbCol != "" {
			goTypeStr := f.Type().String()
			if isPrimitiveType(goTypeStr) {
				fi.LocalGoType = goTypeStr
			} else {
				fi.LocalGoType = localTypeName(f.Type())
				fieldPkg := typePkgPath(f.Type())
				if fieldPkg != ownerPkgPath {
					fi.TypePkgPath = fieldPkg
				}
				fi.IsPointer = isPointerType(f.Type())
				if isScannerValuer(f.Type()) {
					fi.IsVO = true
					fi.VOBaseType = voBaseType(f.Type())
					fi.HasEqual = hasMethod(f.Type(), "Equal")
					fi.IsComparable = types.Comparable(voUnderlyingNamed(f.Type()))
					fi.HasIsZero = hasMethod(f.Type(), "IsZero")
				}
				if isMultiCol {
					fi.IsMultiColVO = true
					names := splitMultiColNames(rawDBTag(tag))
					fi.MultiColNames = names
					fi.MultiColPrefix = dbCol
					if len(names) > 0 {
						// Keep ColumnName as the first sub-column so columnFields()
						// continues to include this field; the full set lives in
						// MultiColNames. Options (unique/index/check) are NOT parsed
						// for multi-col VOs — every segment is a column name.
						fi.ColumnName = names[0]
						fi.Unique = false
						fi.Indexed = false
						fi.IndexName = ""
						fi.CheckExpr = ""
					}
					if hasDrelColumnTypes(f.Type()) {
						fi.MultiColTypes = defaultMultiColTypes(names)
					} else {
						fi.MultiColTypes = defaultMultiColTypes(names)
					}
				}
				if !isPrimitiveType(goTypeStr) && !fi.IsVO && !isMultiCol {
					enumValues, enumIsInt, enumBase := findEnumValues(f.Type())
					if len(enumValues) > 0 {
						fi.IsEnum = true
						fi.EnumValues = enumValues
						fi.EnumIsInt = enumIsInt
						fi.EnumBaseType = enumBase
					} else if enumBase != "" {
						// Named type over a comparable basic kind (e.g. type Priority int)
						// but with no declared const values -- not an enum, but still
						// comparable and safe to use with != in generated diff code.
						fi.IsNamedPrimitive = true
					} else if isJSONContainer(f.Type()) {
						fi.IsJSON = true
						fi.IsArray = isSliceType(f.Type())
					}
				}
			}
		}

		fields = append(fields, fi)
	}
	return fields, nil
}

// firstDBTagSegment returns just the column name (the first comma-separated
// segment) from a db struct tag, without parsing options. Used for multi-column
// VOs where every comma segment is a column name, not an option keyword.
func firstDBTagSegment(rawTag string) string {
	st := reflect.StructTag(rawTag)
	raw, ok := st.Lookup("db")
	if !ok || raw == "" {
		return ""
	}
	if i := strings.IndexByte(raw, ','); i >= 0 {
		return strings.TrimSpace(raw[:i])
	}
	return strings.TrimSpace(raw)
}

func parseRelTagStructured(tag string) *RelationFieldInfo {
	if tag == "" {
		return nil
	}
	parts := strings.Split(tag, ",")
	if len(parts) == 0 {
		return nil
	}
	ri := &RelationFieldInfo{Type: parts[0]}
	for _, part := range parts[1:] {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "fk":
			ri.FK = kv[1]
		case "join":
			ri.JoinTable = kv[1]
		case "ref":
			ri.RefColumn = kv[1]
		}
	}
	return ri
}

// dbTagOpts holds the options parsed from a db struct tag after the column name.
type dbTagOpts struct {
	unique      bool
	indexed     bool
	indexName   string
	check       string
	def         string // db tag option: column DEFAULT value (db:"...,default=expr")
	typ         string // db tag option: explicit SQL type override (db:"...,type=jsonb")
	table       string // db tag option: explicit table name (embedded drel.Model field only)
	renamedFrom string // db tag option: previous name, for migration rename detection
}

// parseDBTag splits a db struct tag into its column name and options. The first
// comma-separated element is the column name; subsequent elements are options
// tokenized by splitTagOptions (comma-safe: ignores commas inside single-quoted
// strings or parentheses so values like check=role IN ('a','b') survive intact):
//
//	db:"email,unique"                 — unique index
//	db:"age,index"                    — single-column index (auto-named)
//	db:"role,index=idx_role_age"      — named index; fields sharing the name compose
//	db:"age,check=age >= 0"           — column CHECK constraint
//	db:"role,check=role IN ('a','b')" — IN-list CHECK with embedded commas
//
// Returns an error if an unrecognized option token is encountered.
// Returns an empty column name when no db tag is present.
func parseDBTag(rawTag string) (string, dbTagOpts, error) {
	st := reflect.StructTag(rawTag)
	raw, ok := st.Lookup("db")
	if !ok || raw == "" {
		return "", dbTagOpts{}, nil
	}
	// The column name is the first comma-separated segment; options follow.
	name := raw
	rest := ""
	if i := strings.IndexByte(raw, ','); i >= 0 {
		name = raw[:i]
		rest = raw[i+1:]
	}
	col := strings.TrimSpace(name)

	var opts dbTagOpts
	for _, p := range splitTagOptions(rest) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		switch {
		case p == "unique":
			opts.unique = true
		case p == "index":
			opts.indexed = true
		case strings.HasPrefix(p, "index="):
			opts.indexed = true
			opts.indexName = strings.TrimSpace(strings.TrimPrefix(p, "index="))
		case strings.HasPrefix(p, "check="):
			opts.check = strings.TrimSpace(strings.TrimPrefix(p, "check="))
		case strings.HasPrefix(p, "default="):
			opts.def = strings.TrimSpace(strings.TrimPrefix(p, "default="))
		case strings.HasPrefix(p, "type="):
			opts.typ = strings.TrimSpace(strings.TrimPrefix(p, "type="))
		case strings.HasPrefix(p, "table="):
			opts.table = strings.TrimSpace(strings.TrimPrefix(p, "table="))
			if opts.table == "" {
				return "", dbTagOpts{}, fmt.Errorf("db tag option table= requires a value")
			}
		case strings.HasPrefix(p, "renamed_from="):
			opts.renamedFrom = strings.TrimSpace(strings.TrimPrefix(p, "renamed_from="))
			if opts.renamedFrom == "" {
				return "", dbTagOpts{}, fmt.Errorf("db tag option renamed_from= requires a value (the previous name)")
			}
		default:
			return "", dbTagOpts{}, fmt.Errorf("unknown db tag option %q (known: unique, index, index=, check=, default=, type=, table=, renamed_from=)", p)
		}
	}
	return col, opts, nil
}

// parseModelTag parses the db struct tag on a model's embedded drel.Model
// field. Unlike parseDBTag, every comma-separated segment is an option except
// an optional leading bare name, which is the primary key column name.
func parseModelTag(rawTag string) (pkColumn string, opts dbTagOpts, err error) {
	st := reflect.StructTag(rawTag)
	raw, ok := st.Lookup("db")
	if !ok || raw == "" {
		return "", dbTagOpts{}, nil
	}
	segments := splitTagOptions(raw)
	var optionSegments []string
	for i, s := range segments {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if i == 0 && !strings.ContainsRune(s, '=') && s != "unique" && s != "index" {
			pkColumn = s
			continue
		}
		optionSegments = append(optionSegments, s)
	}
	// Reuse parseDBTag's option handling by giving it an empty column name.
	_, opts, err = parseDBTag(fmt.Sprintf("db:%q", ","+strings.Join(optionSegments, ",")))
	return pkColumn, opts, err
}

// splitTagOptions splits a db-tag option list on commas, ignoring commas that
// appear inside single-quoted strings, parentheses, or square brackets so that
// option values like check=role IN ('a','b'), check=substr(x,1,2), and
// default=ARRAY['a','b'] survive intact.
func splitTagOptions(s string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0 // tracks nesting depth of ( ) and [ ]
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			inQuote = !inQuote
			cur.WriteByte(c)
		case (c == '(' || c == '[') && !inQuote:
			depth++
			cur.WriteByte(c)
		case (c == ')' || c == ']') && !inQuote:
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case c == ',' && depth == 0 && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// rawDBTag returns the raw db struct tag value (the full comma list, unsplit).
func rawDBTag(rawTag string) string {
	st := reflect.StructTag(rawTag)
	v, ok := st.Lookup("db")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// splitMultiColNames splits a multi-column VO db tag into its sub-column names,
// trimming whitespace and dropping empty segments.
func splitMultiColNames(rawDB string) []string {
	parts := strings.Split(rawDB, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseRelTag(rawTag string) string {
	st := reflect.StructTag(rawTag)
	return st.Get("rel")
}

// findEnumValues returns the enum's declared values (in source declaration
// order), whether the underlying kind is integer, and the Go base type string.
// A non-enum type returns (nil, false, "").
func findEnumValues(t types.Type) (values []string, isInt bool, baseType string) {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil, false, ""
	}
	basic, ok := named.Underlying().(*types.Basic)
	if !ok {
		return nil, false, ""
	}
	if basic.Kind() != types.String && !isIntegerBasicKind(basic.Kind()) {
		return nil, false, ""
	}

	typePkg := named.Obj().Pkg()
	if typePkg == nil {
		return nil, false, ""
	}

	type enumConst struct {
		val string
		pos token.Pos
	}
	scope := typePkg.Scope()
	var consts []enumConst
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		c, ok := obj.(*types.Const)
		if !ok {
			continue
		}
		if !types.Identical(c.Type(), named) {
			continue
		}
		val := c.Val().ExactString()
		if basic.Kind() == types.String {
			val = constant.StringVal(c.Val())
		}
		consts = append(consts, enumConst{val: val, pos: c.Pos()})
	}
	// Preserve source declaration order (scope.Names() is non-deterministic).
	sort.Slice(consts, func(i, j int) bool { return consts[i].pos < consts[j].pos })
	for _, c := range consts {
		values = append(values, c.val)
	}

	isInt = isIntegerBasicKind(basic.Kind())
	baseType = basic.String()
	return values, isInt, baseType
}

// voBaseType returns the underlying basic Go type name of a single-column VO
// (e.g. "string", "int64"), used to resolve the column's SQL type. It unwraps a
// pointer and reads the named type's underlying *types.Basic. For single-field
// structs, it inspects the sole field's type to infer the stored primitive.
// Returns "" when the underlying type cannot be reduced to a known basic kind,
// in which case DDL falls back to its default.
func voBaseType(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	// Case 1: named type with a basic underlying (e.g. type Email string).
	if basic, ok := named.Underlying().(*types.Basic); ok {
		return basic.Name()
	}
	// Case 2: single-field struct — infer from the one field's basic type
	// (e.g. type Email struct{ address string }).
	if st, ok := named.Underlying().(*types.Struct); ok && st.NumFields() == 1 {
		field := st.Field(0)
		ft := field.Type()
		if ptr2, ok := ft.(*types.Pointer); ok {
			ft = ptr2.Elem()
		}
		if basic, ok := ft.(*types.Basic); ok {
			return basic.Name()
		}
		// Single field is itself a named basic type.
		if n2, ok := ft.(*types.Named); ok {
			if basic, ok := n2.Underlying().(*types.Basic); ok {
				return basic.Name()
			}
		}
	}
	return ""
}

// voUnderlyingNamed unwraps a pointer field type to its named value type so
// comparability is assessed on the VO value (the form stored in the snapshot),
// not on a pointer (which is always comparable).
func voUnderlyingNamed(t types.Type) types.Type {
	if ptr, ok := t.(*types.Pointer); ok {
		return ptr.Elem()
	}
	return t
}

func isIntegerBasicKind(k types.BasicKind) bool {
	switch k {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return true
	}
	return false
}

func targetModelName(t types.Type) string {
	// Unwrap slice for has_many relations (e.g. []Post or []*Post).
	if sl, ok := t.(*types.Slice); ok {
		t = sl.Elem()
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}
