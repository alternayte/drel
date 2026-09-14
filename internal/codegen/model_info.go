package codegen

type ModelInfo struct {
	Name       string
	PkgPath    string
	PkgName    string
	PKType     string // display type for generated code (e.g., "int", "uuid.UUID")
	PKTypeFull string // fully qualified (e.g., "github.com/google/uuid.UUID")
	PKTypePkg  string // import path for external PK types (empty for primitives)
	TableName  string
	// RenamedFrom, when set, is this table's previous name. The migration
	// differ turns it into an ALTER TABLE ... RENAME TO.
	RenamedFrom   string
	Fields        []FieldInfo
	HasSoftDelete bool
	HasVersioned  bool
	HasAudit      bool
	Dir           string // filesystem directory of the package
	// Module is the feature slice this model belongs to. It is empty for a
	// config that lists packages instead of modules.
	Module string
	// Key lists the primary key columns in key order. A scalar key type has
	// exactly one entry whose FieldName is empty.
	Key []KeyColumn
	// KeyIsStruct reports whether the key type argument is a struct.
	KeyIsStruct bool
	// EnumOwners names the enum types whose Values()/IsValid() helpers this
	// model's generated file declares. Two models of one package that share an
	// enum type must not declare the helpers two times, so assignEnumOwners
	// gives each enum type to exactly one model of the package. A nil map means
	// the model owns every enum it uses, which is the case when a caller emits
	// one model on its own.
	EnumOwners map[string]bool
}

// KeyColumn is one column of a model's primary key.
type KeyColumn struct {
	FieldName  string // exported Go field on the key struct; empty for a scalar key
	ColumnName string // database column name
	GoType     string // local Go type name, e.g. "int" or "UUID"
	// PkgPath is the import path of the field's type. It is empty for a
	// builtin type and for a type declared in the model's own package.
	PkgPath string
	// UnderlyingGoType is the normalization kind of the column: "int" for any
	// signed integer, "string", or "uuid.UUID". It selects the conversion
	// rule and names the type the conversion helper returns, which is not
	// always GoType: a named type or a sized integer needs a conversion back.
	UnderlyingGoType string
}

// PKColumns returns the primary key column names in key order.
func (m ModelInfo) PKColumns() []string {
	out := make([]string, len(m.Key))
	for i, k := range m.Key {
		out[i] = k.ColumnName
	}
	return out
}

// IsCompositeKey reports whether the model's primary key spans more than one
// column.
func (m ModelInfo) IsCompositeKey() bool { return len(m.Key) > 1 }

type FieldInfo struct {
	Name       string
	GoType     string
	ColumnName string
	// RenamedFrom, when set, is this column's previous name. The migration
	// differ turns it into an ALTER TABLE ... RENAME COLUMN rather than a drop
	// and an add.
	RenamedFrom    string
	IsExported     bool
	RelTag         string
	Relation       *RelationFieldInfo
	IsVO           bool     // implements sql.Scanner + driver.Valuer (single-column VO)
	VOBaseType     string   // single-column VO: underlying basic Go type (e.g. "string", "int64"); empty if not derivable
	HasEqual       bool     // single-column VO defines an Equal(T) bool method usable for diffing
	IsComparable   bool     // single-column VO's Go type is comparable with == / != (types.Comparable)
	HasIsZero      bool     // single-column VO defines IsZero() bool, enabling the zero<->NULL bridge
	IsMultiColVO   bool     // implements drel.MultiColumnMapper (multi-column VO)
	MultiColPrefix string   // db tag used as column prefix for multi-column VOs
	MultiColNames  []string // expanded sub-column names from the db tag (multi-column VOs)
	MultiColTypes  []string // resolved SQL types per sub-column (default "text"; multi-column VOs)
	LocalGoType    string   // type name without package qualifier, for same-package generated code
	TypePkgPath    string   // import path of the field type's package (empty for primitives/same-package)
	// TypeRefPkgs lists the import paths an unnamed composite type refers to,
	// e.g. []time.Time needs "time". LocalGoType then holds placeholders that
	// the emitter replaces with the file's import aliases.
	TypeRefPkgs      []string
	IsPointer        bool // whether the field is a pointer type
	IsEnum           bool
	EnumValues       []string
	EnumIsInt        bool   // enum's underlying basic kind is integer (not string)
	EnumBaseType     string // Go base type of the enum, e.g. "string", "int", "int64"
	IsNamedPrimitive bool   // named type over a comparable basic kind with no enum consts (e.g. type Priority int)
	Unique           bool   // db tag option: unique index on this column
	Indexed          bool   // db tag option: index on this column
	IndexName        string // explicit index name (db:"...,index=name"); fields sharing a name form a composite index
	CheckExpr        string // db tag option: column CHECK constraint expression (db:"...,check=expr")
	Default          string // db tag option: column DEFAULT value (db:"...,default=expr")
	TypeOverride     string // db tag option: explicit SQL type (db:"...,type=jsonb"); overrides inference
	IsJSON           bool   // map/slice/struct mapped as JSON/jsonb (default for non-primitive, non-VO, non-enum container types)
	IsArray          bool   // slice field (JSON array by default; native T[] when TypeOverride is set on Postgres)
}

// RelationFieldInfo holds parsed relationship metadata from a `rel:"..."` struct tag.
type RelationFieldInfo struct {
	Type        string // "has_many", "has_one", "belongs_to", "many_to_many"
	FK          string // foreign key column
	JoinTable   string // for many_to_many
	RefColumn   string // target FK column in pivot table (many-to-many)
	TargetModel string // target model name for foreign key resolution
}
