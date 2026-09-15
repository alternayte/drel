package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The conformance corpus declares one model package that uses every field shape
// drel supports, generates it, and compiles the result. A classification or
// rendering bug in the emitter shows up here as a build failure rather than in a
// user's project. Add a case for every shape the emitter learns to handle.
//
// The corpus is deliberately one package: shapes that only break when two models
// share a declaration (an enum type, for instance) cannot be caught by a
// per-model fixture.

const conformanceValueObjects = `package shapes

import (
	"database/sql/driver"
	"fmt"
)

// Email is a single-column value object over a string.
type Email struct{ address string }

func NewEmail(s string) Email { return Email{address: s} }

func (e Email) String() string { return e.address }
func (e Email) IsZero() bool   { return e.address == "" }

func (e Email) Value() (driver.Value, error) {
	if e.address == "" {
		return nil, nil
	}
	return e.address, nil
}

func (e *Email) Scan(src any) error {
	if src == nil {
		e.address = ""
		return nil
	}
	s, ok := src.(string)
	if !ok {
		return fmt.Errorf("Email.Scan: expected string, got %T", src)
	}
	e.address = s
	return nil
}

// Cents is a single-column value object over an int64.
type Cents struct{ n int64 }

func (c Cents) Int64() int64                 { return c.n }
func (c Cents) Value() (driver.Value, error) { return c.n, nil }

func (c *Cents) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		c.n = v
	case nil:
		c.n = 0
	}
	return nil
}

// Money is a multi-column value object.
type Money struct {
	amount   int
	currency string
}

func (m Money) DrelColumns() []string      { return []string{"amount", "currency"} }
func (m Money) DrelValues() ([]any, error) { return []any{m.amount, m.currency}, nil }

func (m *Money) DrelScanMulti(v []any) error {
	if len(v) != 2 {
		return nil
	}
	switch a := v[0].(type) {
	case int64:
		m.amount = int(a)
	case int:
		m.amount = a
	}
	if c, ok := v[1].(string); ok {
		m.currency = c
	}
	return nil
}
`

const conformanceTypes = `package shapes

// State is a string enum. Two models below share it, so its Values()/IsValid()
// helpers must be declared exactly one time in the package.
type State string

const (
	StateDraft State = "draft"
	StateLive  State = "live"
)

// Level is an integer enum.
type Level int

const (
	LevelLow  Level = 1
	LevelHigh Level = 2
)

// Priority is a named type over a basic kind with no constants: comparable, but
// not an enum.
type Priority int

// Fact is a plain struct used as a JSON element type of the same package.
type Fact struct {
	Key   string ` + "`json:\"key\"`" + `
	Value string ` + "`json:\"value\"`" + `
}

// Facts is a named slice type over a same-package struct.
type Facts []Fact
`

// A second package, so the corpus covers a type declared outside the model
// package: Go forbids a method on a non-local type, so its enum helpers must
// not be emitted into the model package.
const conformanceForeign = `package kinds

// Tier is an enum declared outside the model package.
type Tier string

const (
	TierFree Tier = "free"
	TierPaid Tier = "paid"
)
`

const conformanceModels = `package shapes

import (
	"time"

	"github.com/alternayte/drel"
	"testmod/kinds"
)

// Everything exercises one field of each supported shape.
type Everything struct {
	drel.Model[int]

	// Primitives, nullable and not.
	name    string   ` + "`db:\"name\"`" + `
	nick    *string  ` + "`db:\"nick\"`" + `
	count   int      ` + "`db:\"count\"`" + `
	big     int64    ` + "`db:\"big\"`" + `
	ratio   float64  ` + "`db:\"ratio\"`" + `
	active  bool     ` + "`db:\"active\"`" + `

	// Time, nullable and not.
	startedAt *time.Time ` + "`db:\"started_at\"`" + `
	endedAt   time.Time  ` + "`db:\"ended_at\"`" + `

	// Enums and named primitives. tier is declared in another package.
	state    State      ` + "`db:\"state\"`" + `
	level    Level      ` + "`db:\"level\"`" + `
	priority Priority   ` + "`db:\"priority\"`" + `
	tier     kinds.Tier ` + "`db:\"tier\"`" + `

	// Value objects, single- and multi-column.
	email   Email ` + "`db:\"email,unique\"`" + `
	balance Cents ` + "`db:\"balance\"`" + `
	price   Money ` + "`db:\"price_amount,price_currency\"`" + `

	// JSON containers: an unnamed slice and map of same-package types, and a
	// named slice type over the same element.
	facts   []Fact            ` + "`db:\"facts\"`" + `
	labels  map[string]string ` + "`db:\"labels\"`" + `
	extra   Facts             ` + "`db:\"extra\"`" + `
	stamps  []time.Time       ` + "`db:\"stamps\"`" + `

	// db tag options.
	slug  string ` + "`db:\"slug,index\"`" + `
	notes string ` + "`db:\"notes,default='none'\"`" + `
	day   time.Time ` + "`db:\"day,type=date\"`" + `

	// A foreign key to a table drel does not model, with referential actions.
	ownerID string ` + "`db:\"owner_id,references=auth_users.id,on_delete=cascade,on_update=restrict,deferrable\"`" + `

	// One field joining two named indexes, one of them a partial unique index.
	userID int ` + "`db:\"user_id,index=idx_everything_user_state,unique_index=uq_everything_active(state = 'draft')\"`" + `
	tag    string ` + "`db:\"tag,index=idx_everything_user_state\"`" + `
}

// Indexed declares an index on the model, which is the only way to cover a
// trait column such as created_at.
type Indexed struct {
	drel.Model[int] ` + "`db:\"index=idx_indexed_recent[owner,created_at],unique_index=uq_indexed_live[owner](live)\"`" + `
	owner           string ` + "`db:\"owner\"`" + `
	live            bool   ` + "`db:\"live\"`" + `
}

// Shared declares the same enum type as Everything. Only one of the two
// generated files may declare the State helpers.
type Shared struct {
	drel.Model[int]
	state State ` + "`db:\"state\"`" + `
}

// StringKey has a named string primary key column.
type StringKey struct {
	drel.Model[string] ` + "`db:\"code\"`" + `
	label              string ` + "`db:\"label\"`" + `
}
`

func TestCodegenConformance(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"kinds/tier.go":    conformanceForeign,
		"shapes/vo.go":     conformanceValueObjects,
		"shapes/types.go":  conformanceTypes,
		"shapes/models.go": conformanceModels,
		"drel.yaml":        "packages:\n  - ./shapes\noutput:\n  db: ./db/drel_gen.go\n",
	})
	runGenerateIn(t, dir)

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, "shapes", name))
		require.NoError(t, err, "generated file %s", name)
		return string(b)
	}
	everything := read("everything_drel.go")
	shared := read("shared_drel.go")
	norm := strings.Join(strings.Fields(everything), " ")

	// Time is a timestamp column, not a JSON container.
	assert.Contains(t, norm, "StartedAt drel.ComparableColumn[*time.Time]")
	assert.Contains(t, norm, "EndedAt drel.TimeColumn")
	assert.NotContains(t, everything, "drel.JSON[*time.Time]")
	assert.NotContains(t, everything, "drel.JSON[time.Time]")

	// A composite type of same-package elements renders unqualified; one of a
	// foreign package renders through the file's import alias.
	assert.Contains(t, everything, "drel.JSON[[]Fact]")
	assert.Contains(t, everything, "drel.JSON[map[string]string]")
	assert.Contains(t, everything, "drel.JSON[[]time.Time]")

	// A shared enum declares its helpers exactly one time in the package.
	both := everything + shared
	assert.Equal(t, 1, strings.Count(both, "func (r State) IsValid()"))
	assert.Equal(t, 1, strings.Count(both, "func StateValues()"))
	assert.Equal(t, 1, strings.Count(both, "func (r Level) IsValid()"))

	// An enum declared in another package gets no helpers here: Go forbids a
	// method on a non-local type. The column still carries the value set.
	assert.NotContains(t, both, "func (r Tier) IsValid()")
	assert.NotContains(t, both, "func TierValues()")

	// A foreign key, a partial unique index and a model-level index reach the
	// schema. They are declarations, not generated Go, so the DDL is the proof.
	models, err := ScanPackages([]string{"./shapes"}, dir)
	require.NoError(t, err)
	ddl := GenerateSchema(models, "postgres")
	assert.Contains(t, ddl, `REFERENCES "auth_users"("id") ON DELETE CASCADE ON UPDATE RESTRICT DEFERRABLE INITIALLY DEFERRED`)
	assert.Contains(t, ddl, `CREATE UNIQUE INDEX IF NOT EXISTS "uq_everything_active" ON "everythings" ("user_id") WHERE state = 'draft';`)
	assert.Contains(t, ddl, `CREATE INDEX IF NOT EXISTS "idx_everything_user_state" ON "everythings" ("user_id", "tag");`)
	assert.Contains(t, ddl, `CREATE INDEX IF NOT EXISTS "idx_indexed_recent" ON "indexeds" ("owner", "created_at");`)
	assert.Contains(t, ddl, `CREATE UNIQUE INDEX IF NOT EXISTS "uq_indexed_live" ON "indexeds" ("owner") WHERE live;`)
	assert.Contains(t, ddl, `"day" date`)

	// The package compiles. This is the assertion that catches a rendering bug
	// the string assertions above do not name.
	buildIn(t, dir)
}

// A type that maps to no column shape is rejected by name. There is no
// catch-all: before classifyField, an unrecognised struct became a jsonb column.
func TestCodegenConformance_RejectsUnmappableTypes(t *testing.T) {
	cases := []struct {
		name  string
		field string
		want  string
	}{
		{
			name:  "channel",
			field: "ch chan int `db:\"ch\"`",
			want:  "maps to no column shape",
		},
		{
			name:  "byte slice",
			field: "payload []byte `db:\"payload\"`",
			want:  "[]byte columns are not supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := setupGenerateModule(t, map[string]string{
				"shapes/model.go": "package shapes\n\nimport \"github.com/alternayte/drel\"\n\ntype Thing struct {\n\tdrel.Model[int]\n\t" + tc.field + "\n}\n",
				"drel.yaml":       "packages:\n  - ./shapes\noutput:\n  db: ./db/drel_gen.go\n",
			})
			origDir, err := os.Getwd()
			require.NoError(t, err)
			t.Cleanup(func() { os.Chdir(origDir) })
			require.NoError(t, os.Chdir(dir))

			err = Generate("drel.yaml")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// A formatting error names the emitted line. gofmt reports only a position, and
// a rendering bug in the emitter is unreadable without the source line.
func TestFormatGenerated_QuotesOffendingLine(t *testing.T) {
	_, err := formatGenerated("package p\n\nvar x drel.JSON[[]example.com/app.Fact]\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "emitted line 3")
	assert.Contains(t, err.Error(), "drel.JSON[[]example.com/app.Fact]")
}
