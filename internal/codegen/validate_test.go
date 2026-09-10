package codegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateModels_DuplicateName(t *testing.T) {
	models := []ModelInfo{
		{Name: "User", PkgPath: "app/auth", PkgName: "auth",
			Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}}},
		{Name: "User", PkgPath: "app/billing", PkgName: "billing",
			Fields: []FieldInfo{{Name: "plan", GoType: "string", ColumnName: "plan"}}},
	}

	err := ValidateModels(models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app/auth")
	assert.Contains(t, err.Error(), "app/billing")
	assert.Contains(t, err.Error(), "Users")
}

func TestValidateModels_UnresolvedRelationTarget(t *testing.T) {
	models := []ModelInfo{
		{Name: "Post", PkgPath: "app/blog", PkgName: "blog",
			Fields: []FieldInfo{
				{Name: "title", GoType: "string", ColumnName: "title"},
				{Name: "Author", GoType: "*models.Author", Relation: &RelationFieldInfo{
					Type: "belongs_to", FK: "author_id", TargetModel: "Athor", // misspelled
				}},
			}},
	}

	err := ValidateModels(models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `model "Post"`)
	assert.Contains(t, err.Error(), `field "Author"`)
	assert.Contains(t, err.Error(), "Athor")
	assert.Contains(t, err.Error(), "drel.yaml")
}

func TestValidateModels_ColumnLessModel(t *testing.T) {
	models := []ModelInfo{
		{Name: "Org", PkgPath: "app/orgs", PkgName: "orgs",
			Fields: []FieldInfo{
				{Name: "Members", GoType: "[]*models.User", Relation: &RelationFieldInfo{
					Type: "has_many", FK: "org_id", TargetModel: "User",
				}},
			}},
		{Name: "User", PkgPath: "app/orgs", PkgName: "orgs",
			Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}}},
	}

	err := ValidateModels(models)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `model "Org"`)
	assert.Contains(t, err.Error(), "no db-mapped columns")
}

func TestValidateModels_Valid(t *testing.T) {
	models := []ModelInfo{
		{Name: "Author", PkgPath: "app/blog", PkgName: "blog",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name"},
				{Name: "Posts", GoType: "[]*models.Post", Relation: &RelationFieldInfo{
					Type: "has_many", FK: "author_id", TargetModel: "Post",
				}},
			}},
		{Name: "Post", PkgPath: "app/blog", PkgName: "blog",
			Fields: []FieldInfo{
				{Name: "title", GoType: "string", ColumnName: "title"},
				{Name: "Author", GoType: "*models.Author", Relation: &RelationFieldInfo{
					Type: "belongs_to", FK: "author_id", TargetModel: "Author",
				}},
			}},
	}

	require.NoError(t, ValidateModels(models))
}

func compositeKeyModel() ModelInfo {
	return ModelInfo{
		Name:      "OrderLine",
		TableName: "order_lines",
		PKType:    "OrderLineKey",
		Key: []KeyColumn{
			{FieldName: "OrderID", ColumnName: "order_id", GoType: "int"},
			{FieldName: "LineNo", ColumnName: "line_no", GoType: "int"},
		},
		KeyIsStruct: true,
	}
}

func TestValidateCompositeKeys_RejectsARelationshipToACompositeKeyTarget(t *testing.T) {
	invoice := ModelInfo{
		Name:      "Invoice",
		TableName: "invoices",
		PKType:    "int",
		Key:       []KeyColumn{{ColumnName: "id", GoType: "int"}},
		Fields: []FieldInfo{{
			Name:       "Lines",
			IsExported: true,
			Relation:   &RelationFieldInfo{Type: "has_many", TargetModel: "OrderLine", FK: "invoice_id"},
		}},
	}
	err := validateCompositeKeys([]ModelInfo{invoice, compositeKeyModel()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invoice.Lines")
	assert.Contains(t, err.Error(), "OrderLine")
	assert.Contains(t, err.Error(), "composite primary key")
}

func TestValidateCompositeKeys_AllowsACompositeKeyModelToPointAtASingleKeyModel(t *testing.T) {
	line := compositeKeyModel()
	line.Fields = []FieldInfo{{
		Name:       "Invoice",
		IsExported: true,
		Relation:   &RelationFieldInfo{Type: "belongs_to", TargetModel: "Invoice", FK: "invoice_id"},
	}}
	invoice := ModelInfo{
		Name: "Invoice", TableName: "invoices", PKType: "int",
		Key: []KeyColumn{{ColumnName: "id", GoType: "int"}},
	}
	assert.NoError(t, validateCompositeKeys([]ModelInfo{line, invoice}))
}

func TestValidateCompositeKeys_RejectsDuplicateKeyColumnNames(t *testing.T) {
	m := compositeKeyModel()
	m.Key[1].ColumnName = "order_id"
	err := validateCompositeKeys([]ModelInfo{m})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order_id")
}

func TestValidateCompositeKeys_RejectsAKeyColumnThatCollidesWithAField(t *testing.T) {
	m := compositeKeyModel()
	m.Fields = []FieldInfo{{Name: "OrderID", ColumnName: "order_id", GoType: "int", IsExported: true}}
	err := validateCompositeKeys([]ModelInfo{m})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order_id")
}

func TestValidateCompositeKeys_PassesForAPlainSingleKeyModel(t *testing.T) {
	m := ModelInfo{Name: "User", TableName: "users", PKType: "int",
		Key: []KeyColumn{{ColumnName: "id", GoType: "int"}}}
	assert.NoError(t, validateCompositeKeys([]ModelInfo{m}))
}

// A composite-key model cannot be the source of has_many, has_one, or
// many_to_many: the include loader binds one parent key value as the FK match
// argument, and a pivot table gets one column per side.
func TestValidateCompositeKeys_RejectsHasManyHasOneAndManyToManyOnACompositeKeyModel(t *testing.T) {
	for _, kind := range []string{"has_many", "has_one", "many_to_many"} {
		t.Run(kind, func(t *testing.T) {
			line := compositeKeyModel()
			line.Fields = []FieldInfo{{
				Name:       "Notes",
				IsExported: true,
				Relation:   &RelationFieldInfo{Type: kind, TargetModel: "Note", FK: "order_line_id"},
			}}
			note := ModelInfo{Name: "Note", TableName: "notes", PKType: "int",
				Key: []KeyColumn{{ColumnName: "id", GoType: "int"}}}

			err := validateCompositeKeys([]ModelInfo{line, note})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "OrderLine.Notes")
			assert.Contains(t, err.Error(), kind)
			assert.Contains(t, err.Error(), "Only belongs_to is supported from a composite-key model")
		})
	}
}

// The rejection must be narrow: belongs_to from a composite-key model is the
// one supported direction and must stay allowed.
func TestValidateCompositeKeys_StillAllowsBelongsToOnACompositeKeyModel(t *testing.T) {
	line := compositeKeyModel()
	line.Fields = []FieldInfo{{
		Name:       "Invoice",
		IsExported: true,
		Relation:   &RelationFieldInfo{Type: "belongs_to", TargetModel: "Invoice", FK: "invoice_id"},
	}}
	invoice := ModelInfo{Name: "Invoice", TableName: "invoices", PKType: "int",
		Key: []KeyColumn{{ColumnName: "id", GoType: "int"}}}
	assert.NoError(t, validateCompositeKeys([]ModelInfo{line, invoice}))
}

// A single-key model keeps every relationship kind.
func TestValidateCompositeKeys_AllowsHasManyOnASingleKeyModel(t *testing.T) {
	invoice := ModelInfo{
		Name: "Invoice", TableName: "invoices", PKType: "int",
		Key: []KeyColumn{{ColumnName: "id", GoType: "int"}},
		Fields: []FieldInfo{{
			Name:       "Notes",
			IsExported: true,
			Relation:   &RelationFieldInfo{Type: "has_many", TargetModel: "Note", FK: "invoice_id"},
		}},
	}
	note := ModelInfo{Name: "Note", TableName: "notes", PKType: "int",
		Key: []KeyColumn{{ColumnName: "id", GoType: "int"}}}
	assert.NoError(t, validateCompositeKeys([]ModelInfo{invoice, note}))
}
