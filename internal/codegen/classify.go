package codegen

import (
	"go/types"
	"strings"
)

// FieldKind is the closed set of column shapes codegen understands. Every
// db-mapped field gets exactly one kind, decided one time by classifyField.
//
// The set is closed on purpose. Before it existed, an if/else ladder ended in
// "any struct, slice, map or array is JSON", so every type codegen did not
// recognise silently became a jsonb column -- time.Time among them. A type that
// matches no kind is now KindUnsupported and the scan rejects it by name.
type FieldKind int

const (
	// KindUnsupported is a type codegen cannot map. It is rejected, never guessed.
	KindUnsupported FieldKind = iota
	// KindPrimitive is a basic Go type, or a pointer to one.
	KindPrimitive
	// KindBytes is []byte. Postgres bytea and SQLite BLOB are not implemented
	// yet, so it is rejected with its own message rather than silently mapped.
	KindBytes
	// KindTime is time.Time or *time.Time, which every driver maps natively.
	KindTime
	// KindMultiColVO implements drel.MultiColumnMapper: one field, many columns.
	KindMultiColVO
	// KindSingleColVO implements sql.Scanner and driver.Valuer.
	KindSingleColVO
	// KindEnum is a named type over a string or integer with declared constants.
	KindEnum
	// KindNamedPrimitive is a named type over a basic kind with no constants.
	KindNamedPrimitive
	// KindJSON is a slice, map, array or plain struct, stored as JSON.
	KindJSON
)

func (k FieldKind) String() string {
	switch k {
	case KindPrimitive:
		return "primitive"
	case KindBytes:
		return "[]byte"
	case KindTime:
		return "time"
	case KindMultiColVO:
		return "multi-column value object"
	case KindSingleColVO:
		return "value object"
	case KindEnum:
		return "enum"
	case KindNamedPrimitive:
		return "named primitive"
	case KindJSON:
		return "JSON"
	default:
		return "unsupported"
	}
}

// isByteSlice reports whether t is []byte (after unwrapping a pointer).
func isByteSlice(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	sl, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := sl.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Byte
}

// classifyField decides the one kind of a db-mapped field.
//
// The order is the precedence, and it matters: a named type that implements
// sql.Scanner and driver.Valuer is a value object even when its underlying kind
// is a string that also has declared constants. isMultiCol comes from the
// caller because a multi-column value object is recognised before the db tag is
// parsed (every tag segment is a column name, not an option).
func classifyField(t types.Type, isMultiCol bool) FieldKind {
	if isPrimitiveType(strings.TrimPrefix(t.String(), "*")) {
		return KindPrimitive
	}
	if isMultiCol || isMultiColumnMapper(t) {
		return KindMultiColVO
	}
	if isScannerValuer(t) {
		// A value object wins over every shape below, including a named type
		// over []byte or over a basic kind with constants: its own Scan and
		// Value decide how the column is written and read.
		return KindSingleColVO
	}
	if isTimeType(t) {
		return KindTime
	}
	if isByteSlice(t) {
		return KindBytes
	}
	if values, _, base := findEnumValues(t); len(values) > 0 {
		return KindEnum
	} else if base != "" {
		return KindNamedPrimitive
	}
	if isJSONContainer(t) {
		return KindJSON
	}
	return KindUnsupported
}
