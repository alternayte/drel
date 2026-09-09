package codegen

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	goVer := strings.TrimPrefix(runtime.Version(), "go")
	goMod := "module testmod\n\ngo " + goVer + "\n\nrequire github.com/alternayte/drel v0.0.0\n\nreplace github.com/alternayte/drel => " + findModuleRoot(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644))
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	}
	return dir
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root")
		}
		dir = parent
	}
}

func TestScanner_SimpleModel(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Product struct {
	drel.Model[int]
	name    string ` + "`db:\"name\"`" + `
	price   int    ` + "`db:\"price\"`" + `
	inStock bool   ` + "`db:\"in_stock\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)

	m := models[0]
	assert.Equal(t, "Product", m.Name)
	assert.Equal(t, "int", m.PKType)
	assert.Equal(t, "products", m.TableName)
	assert.False(t, m.HasSoftDelete)
	assert.False(t, m.HasVersioned)

	require.Len(t, m.Fields, 3)
	assert.Equal(t, "name", m.Fields[0].Name)
	assert.Equal(t, "name", m.Fields[0].ColumnName)
	assert.Equal(t, "string", m.Fields[0].GoType)
	assert.Equal(t, "price", m.Fields[1].ColumnName)
	assert.Equal(t, "in_stock", m.Fields[2].ColumnName)
}

func TestScanner_SoftDeleteEmbed(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Article struct {
	drel.Model[int]
	drel.SoftDelete
	title string ` + "`db:\"title\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.True(t, models[0].HasSoftDelete)
	assert.Equal(t, "articles", models[0].TableName)
}

func TestScanner_VersionedEmbed(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Item struct {
	drel.Model[int]
	drel.Versioned
	label string ` + "`db:\"label\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.True(t, models[0].HasVersioned)
}

func TestScanner_NoModelEmbedSkipped(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

type NotAModel struct {
	Name string
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	assert.Empty(t, models)
}

func TestScanner_FieldWithoutDBTagSkipped(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name   string ` + "`db:\"name\"`" + `
	secret string
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Len(t, models[0].Fields, 1)
	assert.Equal(t, "name", models[0].Fields[0].ColumnName)
}

func TestScanner_MultipleModels(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}

type Post struct {
	drel.Model[int]
	title string ` + "`db:\"title\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	assert.Len(t, models, 2)
}

func TestScanner_RelTagParsing(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/user.go": "package models\n\nimport \"github.com/alternayte/drel\"\n\ntype Post struct {\n\tdrel.Model[int]\n\ttitle string " + "`db:\"title\"`" + "\n}\n\ntype User struct {\n\tdrel.Model[int]\n\tname  string " + "`db:\"name\"`" + "\n\tposts []Post " + "`rel:\"has_many,fk=user_id\"`" + "\n}\n",
	})

	models, err := ScanPackages([]string{filepath.Join(dir, "models")}, dir)
	require.NoError(t, err)

	var user *ModelInfo
	for i := range models {
		if models[i].Name == "User" {
			user = &models[i]
			break
		}
	}
	require.NotNil(t, user)
	require.Len(t, user.Fields, 2)

	postsField := user.Fields[1]
	assert.Equal(t, "posts", postsField.Name)
	assert.Equal(t, "", postsField.ColumnName)
	require.NotNil(t, postsField.Relation)
	assert.Equal(t, "has_many", postsField.Relation.Type)
	assert.Equal(t, "user_id", postsField.Relation.FK)
}

func TestParseRelTagStructured(t *testing.T) {
	tests := []struct {
		tag  string
		want *RelationFieldInfo
	}{
		{"has_many,fk=user_id", &RelationFieldInfo{Type: "has_many", FK: "user_id"}},
		{"has_one,fk=user_id", &RelationFieldInfo{Type: "has_one", FK: "user_id"}},
		{"belongs_to,fk=user_id", &RelationFieldInfo{Type: "belongs_to", FK: "user_id"}},
		{"many_to_many,join=user_tags", &RelationFieldInfo{Type: "many_to_many", JoinTable: "user_tags"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got := parseRelTagStructured(tt.tag)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseRelTag_ManyToManyConventionDefaults(t *testing.T) {
	ri := parseRelTagStructured("many_to_many")
	assert.Equal(t, "many_to_many", ri.Type)
	assert.Empty(t, ri.FK)
	assert.Empty(t, ri.JoinTable)
	assert.Empty(t, ri.RefColumn)
}

func TestParseRelTag_ManyToManyWithRef(t *testing.T) {
	ri := parseRelTagStructured("many_to_many,join=taggings,fk=writer_id,ref=label_id")
	assert.Equal(t, "many_to_many", ri.Type)
	assert.Equal(t, "taggings", ri.JoinTable)
	assert.Equal(t, "writer_id", ri.FK)
	assert.Equal(t, "label_id", ri.RefColumn)
}

func TestParseDBTag_Options(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		col  string
		opts dbTagOpts
	}{
		{"unique", `db:"email,unique"`, "email", dbTagOpts{unique: true}},
		{"index", `db:"age,index"`, "age", dbTagOpts{indexed: true}},
		{"named index", `db:"x,index=ix"`, "x", dbTagOpts{indexed: true, indexName: "ix"}},
		{"check", `db:"y,check=y > 0"`, "y", dbTagOpts{check: "y > 0"}},
		{"plain", `db:"name"`, "name", dbTagOpts{}},
		{"check with in-list", `db:"role,check=role IN ('admin','user')"`, "role", dbTagOpts{check: "role IN ('admin','user')"}},
		{"check with func commas", `db:"x,check=substr(x,1,2) = 'ab'"`, "x", dbTagOpts{check: "substr(x,1,2) = 'ab'"}},
		{"unique then check with comma", `db:"role,unique,check=role IN ('a','b')"`, "role", dbTagOpts{unique: true, check: "role IN ('a','b')"}},
		{"default string", `db:"role,default=user"`, "role", dbTagOpts{def: "user"}},
		{"default with comma value", `db:"flags,default=ARRAY['a','b']"`, "flags", dbTagOpts{def: "ARRAY['a','b']"}},
		{"type override", `db:"meta,type=jsonb"`, "meta", dbTagOpts{typ: "jsonb"}},
		{"unique then default", `db:"role,unique,default=user"`, "role", dbTagOpts{unique: true, def: "user"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col, opts, err := parseDBTag(tt.tag)
			require.NoError(t, err)
			assert.Equal(t, tt.col, col)
			assert.Equal(t, tt.opts, opts)
		})
	}
}

func TestScanner_IndexAndCheckTags(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Account struct {
	drel.Model[int]
	email string ` + "`db:\"email,unique\"`" + `
	age   int    ` + "`db:\"age,index\"`" + `
	first string ` + "`db:\"first,index=ix_name\"`" + `
	score int    ` + "`db:\"score,check=score > 0\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)

	byCol := map[string]FieldInfo{}
	for _, f := range models[0].Fields {
		byCol[f.ColumnName] = f
	}

	assert.True(t, byCol["email"].Unique)
	assert.True(t, byCol["age"].Indexed)
	assert.Empty(t, byCol["age"].IndexName)
	assert.True(t, byCol["first"].Indexed)
	assert.Equal(t, "ix_name", byCol["first"].IndexName)
	assert.Equal(t, "score > 0", byCol["score"].CheckExpr)
}

func TestScanner_DefaultAndTypeTags(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Account struct {
	drel.Model[int]
	role string ` + "`db:\"role,default=user\"`" + `
	meta string ` + "`db:\"meta,type=jsonb\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)

	byCol := map[string]FieldInfo{}
	for _, f := range models[0].Fields {
		byCol[f.ColumnName] = f
	}
	assert.Equal(t, "user", byCol["role"].Default)
	assert.Equal(t, "jsonb", byCol["meta"].TypeOverride)
}

func TestScanner_RejectsUnsignedPK(t *testing.T) {
	for _, pk := range []string{"uint", "uint8", "uint16", "uint32", "uint64"} {
		t.Run(pk, func(t *testing.T) {
			dir := setupTestModule(t, map[string]string{
				"models/model.go": `package models

import "github.com/alternayte/drel"

type Widget struct {
	drel.Model[` + pk + `]
	name string ` + "`db:\"name\"`" + `
}
`,
			})

			_, err := ScanPackages([]string{"./models"}, dir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsigned integer primary keys")
			assert.Contains(t, err.Error(), "Widget")
			assert.Contains(t, err.Error(), pk)
		})
	}
}

func TestScanner_AcceptsSignedAndUUIDPK(t *testing.T) {
	// Signed integer PKs (the common case) must pass the guard.
	// UUID PKs are covered by the emitter tests; we don't import uuid here
	// because the test module's go.mod only resolves via drel's replace directive.
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Signed struct {
	drel.Model[int64]
	name string ` + "`db:\"name\"`" + `
}

type Also struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 2)
}

func TestScanner_StringEnum_PreservesDeclarationOrder(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Status string

const (
	StatusNew      Status = "new"
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusZebra    Status = "zebra"
)

type Order struct {
	drel.Model[int]
	status Status ` + "`db:\"status\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)

	var f *FieldInfo
	for i := range models[0].Fields {
		if models[0].Fields[i].ColumnName == "status" {
			f = &models[0].Fields[i]
		}
	}
	require.NotNil(t, f)
	assert.True(t, f.IsEnum)
	assert.False(t, f.EnumIsInt)
	assert.Equal(t, "string", f.EnumBaseType)
	// Declaration order preserved, NOT alphabetized ("approved" would sort first).
	assert.Equal(t, []string{"new", "pending", "approved", "zebra"}, f.EnumValues)
}

func TestScanner_IntEnum_KindAndOrder(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Priority int

const (
	PriorityLow    Priority = 0
	PriorityMedium Priority = 1
	PriorityHigh   Priority = 2
)

type Ticket struct {
	drel.Model[int]
	priority Priority ` + "`db:\"priority\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)

	var f *FieldInfo
	for i := range models[0].Fields {
		if models[0].Fields[i].ColumnName == "priority" {
			f = &models[0].Fields[i]
		}
	}
	require.NotNil(t, f)
	assert.True(t, f.IsEnum)
	assert.True(t, f.EnumIsInt)
	assert.Equal(t, "int", f.EnumBaseType)
	assert.Equal(t, []string{"0", "1", "2"}, f.EnumValues)
}

func TestParseDBTag_Default(t *testing.T) {
	col, opts, err := parseDBTag(`db:"role,default=user"`)
	require.NoError(t, err)
	assert.Equal(t, "role", col)
	assert.Equal(t, "user", opts.def)
}

func TestScanner_EnumDefault_RecordedOnField(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type Account struct {
	drel.Model[int]
	role Role ` + "`db:\"role,default=user\"`" + `
}
`,
	})
	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)

	var f *FieldInfo
	for i := range models[0].Fields {
		if models[0].Fields[i].ColumnName == "role" {
			f = &models[0].Fields[i]
		}
	}
	require.NotNil(t, f)
	assert.Equal(t, "user", f.Default)
}

func TestParseDBTag_UnknownOptionErrors(t *testing.T) {
	cases := []struct {
		tag   string
		token string
	}{
		{`db:"email,uniqe"`, "uniqe"},
		{`db:"age,idex"`, "idex"},
		{`db:"x,chek=x > 0"`, "chek=x > 0"},
	}
	for _, tc := range cases {
		t.Run(tc.tag, func(t *testing.T) {
			_, _, err := parseDBTag(tc.tag)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.token)
		})
	}

	// Known options still parse without error.
	_, _, err := parseDBTag(`db:"role,unique,check=role IN ('a','b'),default=a"`)
	require.NoError(t, err)
}

func TestScanner_UnknownTagOptionFailsLoud(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Account struct {
	drel.Model[int]
	email string ` + "`db:\"email,uniqe\"`" + `
}
`,
	})

	_, err := ScanPackages([]string{"./models"}, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uniqe")
	assert.Contains(t, err.Error(), "email")
}

func TestScanPackages_DeterministicOrder(t *testing.T) {
	// Two packages whose names sort the opposite way to a naive append order:
	// "zpkg" defines "Apple", "apkg" defines "Zebra". A final sort by
	// (PkgPath, Name) must put testmod/apkg.Zebra before testmod/zpkg.Apple.
	dir := setupTestModule(t, map[string]string{
		"zpkg/model.go": `package zpkg

import "github.com/alternayte/drel"

type Apple struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"apkg/model.go": `package apkg

import "github.com/alternayte/drel"

type Zebra struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./apkg", "./zpkg"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 2)

	// Deterministic: sorted by PkgPath then Name regardless of pattern order.
	assert.Equal(t, "testmod/apkg", models[0].PkgPath)
	assert.Equal(t, "Zebra", models[0].Name)
	assert.Equal(t, "testmod/zpkg", models[1].PkgPath)
	assert.Equal(t, "Apple", models[1].Name)

	// Re-scanning with the patterns reversed yields the identical order.
	models2, err := ScanPackages([]string{"./zpkg", "./apkg"}, dir)
	require.NoError(t, err)
	require.Len(t, models2, 2)
	assert.Equal(t, models[0].PkgPath, models2[0].PkgPath)
	assert.Equal(t, models[0].Name, models2[0].Name)
	assert.Equal(t, models[1].PkgPath, models2[1].PkgPath)
	assert.Equal(t, models[1].Name, models2[1].Name)
}

// TestScanner_ClassifiesJSONAndArrayFields verifies that the scanner correctly
// sets IsJSON/IsArray on slice and map fields, and records TypeOverride verbatim
// when a type= db tag is present.
func TestScanner_ClassifiesJSONAndArrayFields(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Settings struct {
	Theme string ` + "`json:\"theme\"`" + `
}

type Doc struct {
	drel.Model[int]
	Tags  []string          ` + "`db:\"tags\"`" + `
	Nums  []int             ` + "`db:\"nums\"`" + `
	Meta  map[string]string ` + "`db:\"meta\"`" + `
	Prefs Settings          ` + "`db:\"prefs\"`" + `
	Arr   []string          ` + "`db:\"arr,type=text[]\"`" + `
}
`,
	})

	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	require.Len(t, models, 1)

	byName := map[string]FieldInfo{}
	for _, f := range models[0].Fields {
		byName[f.Name] = f
	}

	// []string default -> JSON array
	assert.True(t, byName["Tags"].IsArray, "Tags should be IsArray")
	assert.True(t, byName["Tags"].IsJSON, "Tags should be IsJSON (slices serialize as JSON by default)")
	assert.Empty(t, byName["Tags"].TypeOverride)

	// []int default -> JSON array
	assert.True(t, byName["Nums"].IsArray)
	assert.True(t, byName["Nums"].IsJSON)

	// map -> JSON (object), not an array
	assert.True(t, byName["Meta"].IsJSON)
	assert.False(t, byName["Meta"].IsArray)

	// plain struct with json affinity -> JSON
	assert.True(t, byName["Prefs"].IsJSON)
	assert.False(t, byName["Prefs"].IsArray)

	// explicit type override is recorded verbatim
	assert.Equal(t, "text[]", byName["Arr"].TypeOverride)
	assert.True(t, byName["Arr"].IsArray)
}

// TestParseDBTag_TypeOverride_Scanner verifies that the type= db tag option is
// parsed correctly from the scanner's parseDBTag helper and stored in opts.typ.
func TestParseDBTag_TypeOverride_Scanner(t *testing.T) {
	col, opts, err := parseDBTag(`db:"payload,type=jsonb"`)
	require.NoError(t, err)
	assert.Equal(t, "payload", col)
	assert.Equal(t, "jsonb", opts.typ)
}

// scanSource writes src as a single-file package and scans it, requiring
// success. It exists because most scanner tests build multi-file modules via
// setupTestModule directly; this is a convenience for single-source cases.
func scanSource(t *testing.T, src string) []ModelInfo {
	t.Helper()
	dir := setupTestModule(t, map[string]string{
		"models/model.go": src,
	})
	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	return models
}

func TestParseDBTag_RenamedFrom(t *testing.T) {
	col, opts, err := parseDBTag(`db:"email_address,renamed_from=email"`)
	require.NoError(t, err)
	assert.Equal(t, "email_address", col)
	assert.Equal(t, "email", opts.renamedFrom)
}

func TestParseDBTag_RenamedFromAlongsideOtherOptions(t *testing.T) {
	col, opts, err := parseDBTag(`db:"email_address,unique,renamed_from=email,check=email_address <> ''"`)
	require.NoError(t, err)
	assert.Equal(t, "email_address", col)
	assert.True(t, opts.unique)
	assert.Equal(t, "email", opts.renamedFrom)
	assert.Equal(t, "email_address <> ''", opts.check)
}

func TestParseDBTag_TableOption(t *testing.T) {
	col, opts, err := parseDBTag(`db:"table=orders,renamed_from=purchases"`)
	require.NoError(t, err)
	assert.Equal(t, "table=orders", col,
		"the first position is the column name; table= in it is not an option here")
	_ = opts
}

func TestParseDBTag_EmptyRenamedFromIsRejected(t *testing.T) {
	_, _, err := parseDBTag(`db:"email,renamed_from="`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "renamed_from")
}

func TestScan_FieldCarriesItsRenameMarker(t *testing.T) {
	models := scanSource(t, `
		package m
		import "github.com/alternayte/drel"
		type User struct {
			drel.Model[int]
			EmailAddress string `+"`"+`db:"email_address,renamed_from=email"`+"`"+`
		}
	`)
	require.Len(t, models, 1)
	var f FieldInfo
	for _, x := range models[0].Fields {
		if x.Name == "EmailAddress" {
			f = x
		}
	}
	assert.Equal(t, "email_address", f.ColumnName)
	assert.Equal(t, "email", f.RenamedFrom)
}

func TestScan_ModelCarriesItsTableRenameMarker(t *testing.T) {
	models := scanSource(t, `
		package m
		import "github.com/alternayte/drel"
		type Order struct {
			drel.Model[int] `+"`"+`db:"table=orders,renamed_from=purchases"`+"`"+`
			Total int
		}
	`)
	require.Len(t, models, 1)
	assert.Equal(t, "orders", models[0].TableName)
	assert.Equal(t, "purchases", models[0].RenamedFrom)
}

// scanSourceErr is scanSource's error-returning twin, for tests that expect a
// scan to be rejected.
func scanSourceErr(t *testing.T, src string) ([]ModelInfo, error) {
	t.Helper()
	dir := setupTestModule(t, map[string]string{
		"models/model.go": src,
	})
	return ScanPackages([]string{"./models"}, dir)
}

func TestScan_ScalarKeyDefaultsToIDColumn(t *testing.T) {
	models := scanSource(t, `
package m

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	Name string
}
`)
	require.Len(t, models, 1)
	assert.False(t, models[0].KeyIsStruct)
	assert.Equal(t, []string{"id"}, models[0].PKColumns())
	assert.False(t, models[0].IsCompositeKey())
}

func TestScan_ScalarKeyTakesItsColumnNameFromTheEmbeddedTag(t *testing.T) {
	models := scanSource(t, `
package m

import "github.com/alternayte/drel"

type Country struct {
	drel.Model[string] `+"`"+`db:"code"`+"`"+`
	Name string
}
`)
	require.Len(t, models, 1)
	assert.Equal(t, []string{"code"}, models[0].PKColumns())
	assert.Equal(t, "string", models[0].Key[0].GoType)
	assert.Equal(t, "", models[0].Key[0].FieldName)
}

func TestScan_StructKeyGivesOneColumnPerField(t *testing.T) {
	models := scanSource(t, `
package m

import "github.com/alternayte/drel"

type OrderLineKey struct {
	OrderID int `+"`"+`db:"order_id"`+"`"+`
	LineNo  int `+"`"+`db:"line_no"`+"`"+`
}

type OrderLine struct {
	drel.Model[OrderLineKey]
	Qty int
}
`)
	var ol ModelInfo
	for _, m := range models {
		if m.Name == "OrderLine" {
			ol = m
		}
	}
	assert.True(t, ol.KeyIsStruct)
	assert.True(t, ol.IsCompositeKey())
	assert.Equal(t, []string{"order_id", "line_no"}, ol.PKColumns())
	assert.Equal(t, "OrderID", ol.Key[0].FieldName)
	assert.Equal(t, "int", ol.Key[0].GoType)
}

func TestScan_StructKeyFieldWithoutATagUsesSnakeCase(t *testing.T) {
	models := scanSource(t, `
package m

import "github.com/alternayte/drel"

type K struct {
	TenantID int
	SeqNo    int
}

type Row struct {
	drel.Model[K]
}
`)
	var r ModelInfo
	for _, m := range models {
		if m.Name == "Row" {
			r = m
		}
	}
	assert.Equal(t, []string{"tenant_id", "seq_no"}, r.PKColumns())
}

func TestScan_StructKeyWithAnUnexportedFieldIsRejected(t *testing.T) {
	_, err := scanSourceErr(t, `
package m

import "github.com/alternayte/drel"

type K struct {
	TenantID int
	seq      int
}

type Row struct {
	drel.Model[K]
}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seq")
	assert.Contains(t, err.Error(), "unexported")
}

func TestScan_StructKeyWithAnUnsupportedFieldTypeIsRejected(t *testing.T) {
	_, err := scanSourceErr(t, `
package m

import "github.com/alternayte/drel"

type K struct {
	A int
	B float64
}

type Row struct {
	drel.Model[K]
}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "B")
}

func TestScan_EmptyStructKeyIsRejected(t *testing.T) {
	_, err := scanSourceErr(t, `
package m

import "github.com/alternayte/drel"

type K struct{}

type Row struct {
	drel.Model[K]
}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fields")
}

func TestScan_StructKeyFieldRecordsItsTypePackage(t *testing.T) {
	dir := setupTestModule(t, map[string]string{
		"ids/ids.go": `package ids

type TenantID string
`,
		"models/model.go": `package m

import (
	"github.com/alternayte/drel"
	"testmod/ids"
)

type MembershipKey struct {
	TenantID ids.TenantID ` + "`" + `db:"tenant_id"` + "`" + `
	Seq      int          ` + "`" + `db:"seq"` + "`" + `
}

type Membership struct {
	drel.Model[MembershipKey]
	Role string
}
`,
	})
	models, err := ScanPackages([]string{"./models"}, dir)
	require.NoError(t, err)
	var ms ModelInfo
	for _, m := range models {
		if m.Name == "Membership" {
			ms = m
		}
	}
	require.Len(t, ms.Key, 2)
	assert.Equal(t, "testmod/ids", ms.Key[0].PkgPath,
		"a key field from another package must record that package")
	assert.Equal(t, "TenantID", ms.Key[0].GoType)
	assert.Equal(t, "", ms.Key[1].PkgPath, "a builtin key field has no package")
}

func TestScan_KeyColumnsRecordTheirNormalizationKind(t *testing.T) {
	models := scanSource(t, `
package m

import "github.com/alternayte/drel"

type LineNo int
type SKU string

type EntryKey struct {
	LineNo LineNo `+"`"+`db:"line_no"`+"`"+`
	SKU    SKU    `+"`"+`db:"sku"`+"`"+`
	Big    int64  `+"`"+`db:"big"`+"`"+`
}

type Entry struct {
	drel.Model[EntryKey]
	Note string
}
`)
	var e ModelInfo
	for _, m := range models {
		if m.Name == "Entry" {
			e = m
		}
	}
	require.Len(t, e.Key, 3)
	assert.Equal(t, "LineNo", e.Key[0].GoType)
	assert.Equal(t, "int", e.Key[0].UnderlyingGoType)
	assert.Equal(t, "SKU", e.Key[1].GoType)
	assert.Equal(t, "string", e.Key[1].UnderlyingGoType)
	assert.Equal(t, "int64", e.Key[2].GoType)
	assert.Equal(t, "int", e.Key[2].UnderlyingGoType,
		"every signed integer width normalizes through the same int rule")
}

func TestScan_ScalarNamedIntKeyRecordsItsKind(t *testing.T) {
	models := scanSource(t, `
package m

import "github.com/alternayte/drel"

type AccountID int

type Account struct {
	drel.Model[AccountID]
	Name string
}
`)
	require.Len(t, models, 1)
	require.Len(t, models[0].Key, 1)
	assert.Equal(t, "int", models[0].Key[0].UnderlyingGoType)
}
