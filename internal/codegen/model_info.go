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
}

type FieldInfo struct {
	Name       string
	GoType     string
	ColumnName string
	// RenamedFrom, when set, is this column's previous name. The migration
	// differ turns it into an ALTER TABLE ... RENAME COLUMN rather than a drop
	// and an add.
	RenamedFrom      string
	IsExported       bool
	RelTag           string
	Relation         *RelationFieldInfo
	IsVO             bool     // implements sql.Scanner + driver.Valuer (single-column VO)
	VOBaseType       string   // single-column VO: underlying basic Go type (e.g. "string", "int64"); empty if not derivable
	HasEqual         bool     // single-column VO defines an Equal(T) bool method usable for diffing
	IsComparable     bool     // single-column VO's Go type is comparable with == / != (types.Comparable)
	HasIsZero        bool     // single-column VO defines IsZero() bool, enabling the zero<->NULL bridge
	IsMultiColVO     bool     // implements drel.MultiColumnMapper (multi-column VO)
	MultiColPrefix   string   // db tag used as column prefix for multi-column VOs
	MultiColNames    []string // expanded sub-column names from the db tag (multi-column VOs)
	MultiColTypes    []string // resolved SQL types per sub-column (default "text"; multi-column VOs)
	LocalGoType      string   // type name without package qualifier, for same-package generated code
	TypePkgPath      string   // import path of the field type's package (empty for primitives/same-package)
	IsPointer        bool     // whether the field is a pointer type
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
