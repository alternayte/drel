package codegen

import (
	"go/types"
	"strings"
)

func isScannerValuer(t types.Type) bool {
	return hasMethod(t, "Scan") && hasMethod(t, "Value")
}

func isMultiColumnMapper(t types.Type) bool {
	return hasMethod(t, "DrelColumns") && hasMethod(t, "DrelValues") && hasMethod(t, "DrelScanMulti")
}

// hasDrelColumnTypes reports whether a multi-column VO exposes the optional
// DrelColumnTypes() []string hook for explicit sub-column SQL types.
func hasDrelColumnTypes(t types.Type) bool {
	return hasMethod(t, "DrelColumnTypes")
}

// defaultMultiColTypes returns one "text" entry per sub-column name. The
// optional DrelColumnTypes() hook is detected via hasDrelColumnTypes; W2-G1
// defaults every sub-column to text, leaving explicit per-column type overrides
// to the db-tag type= work (W2-G6/G9).
func defaultMultiColTypes(names []string) []string {
	out := make([]string, len(names))
	for i := range out {
		out[i] = "text"
	}
	return out
}

func hasMethod(t types.Type, name string) bool {
	mset := types.NewMethodSet(t)
	if mset.Lookup(nil, name) != nil {
		return true
	}
	ptr := types.NewPointer(t)
	ptrMset := types.NewMethodSet(ptr)
	return ptrMset.Lookup(nil, name) != nil
}

func localTypeName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return t.String()
	}
	return named.Obj().Name()
}

func typePkgPath(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	pkg := named.Obj().Pkg()
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

func isPointerType(t types.Type) bool {
	_, ok := t.(*types.Pointer)
	return ok
}

func isPrimitiveType(goType string) bool {
	switch goType {
	case "string", "bool",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64",
		"byte", "rune":
		return true
	}
	return false
}

// isSliceType reports whether t is a slice (after unwrapping a pointer).
func isSliceType(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

// isMapType reports whether t is a map (after unwrapping a pointer).
func isMapType(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	_, ok := t.Underlying().(*types.Map)
	return ok
}

// isTimeType reports whether t is time.Time (after unwrapping a pointer). Its
// underlying type is a struct, so every struct rule must exclude it: the
// drivers map time.Time to a timestamp column natively.
func isTimeType(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	pkg := named.Obj().Pkg()
	return pkg != nil && pkg.Path() == "time" && named.Obj().Name() == "Time"
}

// isJSONContainer reports whether t is a slice, map, array, or plain struct that
// should be mapped as a JSON column. []byte is excluded (it maps to bytea/BLOB,
// handled as a primitive elsewhere if ever added). time.Time is excluded: it is
// a struct, but the drivers map it to a timestamp column.
func isJSONContainer(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if isTimeType(t) {
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Slice:
		if b, ok := u.Elem().Underlying().(*types.Basic); ok && b.Kind() == types.Byte {
			return false // []byte is bytea/BLOB, not JSON
		}
		return true
	case *types.Map, *types.Array, *types.Struct:
		return true
	default:
		return false
	}
}

// pkgMarkPrefix/pkgMarkSuffix delimit a package-path placeholder inside a
// rendered type string. A composite type such as []Fact can name types from
// several packages, and the import alias of each one is only known when the
// file is emitted, so the scanner writes a placeholder and the emitter
// replaces it with the resolved alias.
const (
	pkgMarkPrefix = "\x00"
	pkgMarkSuffix = "\x00"
)

func pkgMark(pkgPath string) string {
	return pkgMarkPrefix + pkgPath + pkgMarkSuffix
}

// isUnnamedComposite reports whether t (after unwrapping a pointer) is a type
// literal rather than a named type: []Fact, map[string]Fact, [3]Fact. Such a
// type has no local name, so it must be rendered element by element.
func isUnnamedComposite(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if _, named := t.(*types.Named); named {
		return false
	}
	switch t.(type) {
	case *types.Slice, *types.Map, *types.Array:
		return true
	}
	return false
}

// compositeTypeString renders an unnamed composite type as Go source. Types of
// the owner package are unqualified; every other package is written as a
// placeholder that resolvePkgMarks replaces with the file's import alias. It
// also returns the import paths the rendered type needs.
func compositeTypeString(t types.Type, ownerPkg string) (string, []string) {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	var pkgs []string
	seen := make(map[string]bool)
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == ownerPkg {
			return ""
		}
		if !seen[p.Path()] {
			seen[p.Path()] = true
			pkgs = append(pkgs, p.Path())
		}
		return pkgMark(p.Path())
	}
	return types.TypeString(t, qualifier), pkgs
}

// resolvePkgMarks replaces each package placeholder of a rendered composite
// type with the import alias the emitted file uses for that package.
func resolvePkgMarks(rendered string, alias func(pkgPath string) string) string {
	var b strings.Builder
	for {
		start := strings.Index(rendered, pkgMarkPrefix)
		if start < 0 {
			b.WriteString(rendered)
			return b.String()
		}
		rest := rendered[start+len(pkgMarkPrefix):]
		end := strings.Index(rest, pkgMarkSuffix)
		if end < 0 {
			b.WriteString(rendered)
			return b.String()
		}
		b.WriteString(rendered[:start])
		b.WriteString(alias(rest[:end]))
		rendered = rest[end+len(pkgMarkSuffix):]
	}
}
