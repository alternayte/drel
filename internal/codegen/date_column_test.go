package codegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A drel.Date field maps to a NOT NULL column. Date.Value never returns nil, so
// the zero<->NULL bridge that a value object opts into does not apply: a
// nullable column would let a row hold NULL for a field that cannot express it.
func TestBuildSchema_DateColumnIsNotNull(t *testing.T) {
	models := []ModelInfo{{
		Name: "XPEntry", TableName: "xp_entries", PKType: "int",
		Fields: []FieldInfo{
			{
				Name: "UserDay", GoType: "Date", LocalGoType: "Date",
				TypePkgPath: "github.com/alternayte/drel",
				ColumnName:  "user_day", IsExported: true,
				IsVO: true, IsDate: true, HasIsZero: true, IsComparable: true,
			},
			{
				Name: "Optional", GoType: "*Date", LocalGoType: "Date",
				TypePkgPath: "github.com/alternayte/drel",
				ColumnName:  "optional", IsExported: true, IsPointer: true,
				IsVO: true, IsDate: true, HasIsZero: true, IsComparable: true,
			},
		},
	}}

	s := BuildSchema(models, "postgres")
	require.Len(t, s.Tables, 1)

	byName := indexColumns(s.Tables[0].Columns)
	require.Contains(t, byName, "user_day")
	assert.Equal(t, "date", byName["user_day"].Type)
	assert.True(t, byName["user_day"].NotNull, "a drel.Date field must map to a NOT NULL column")

	// A pointer field is the way to say the column may hold NULL.
	require.Contains(t, byName, "optional")
	assert.False(t, byName["optional"].NotNull, "a *drel.Date field maps to a nullable column")
}
