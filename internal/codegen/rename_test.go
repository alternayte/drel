package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffSchemas_ColumnRenameEmitsRenameNotDropAndAdd(t *testing.T) {
	old := Schema{Tables: []Table{pgTable("users",
		Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
		Column{Name: "email", Type: "text", NotNull: true},
	)}}
	new := Schema{Tables: []Table{pgTable("users",
		Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
		Column{Name: "email_address", Type: "text", NotNull: true, RenamedFrom: "email"},
	)}}

	up, down, err := DiffSchemas(old, new, "postgres")
	require.NoError(t, err)
	assert.Equal(t, `ALTER TABLE "users" RENAME COLUMN "email" TO "email_address";`, up)
	assert.Equal(t, `ALTER TABLE "users" RENAME COLUMN "email_address" TO "email";`, down)
	assert.NotContains(t, up, "DROP COLUMN")
	assert.NotContains(t, up, "ADD COLUMN")
}

func TestDiffSchemas_RenameAndTypeChangeTogether(t *testing.T) {
	old := Schema{Tables: []Table{pgTable("users",
		Column{Name: "email", Type: "text", NotNull: true},
	)}}
	new := Schema{Tables: []Table{pgTable("users",
		Column{Name: "email_address", Type: "varchar(320)", NotNull: true, RenamedFrom: "email"},
	)}}

	up, _, err := DiffSchemas(old, new, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `RENAME COLUMN "email" TO "email_address"`)
	assert.Contains(t, up, `ALTER COLUMN "email_address" TYPE varchar(320)`)
	renameAt := indexOf(up, "RENAME COLUMN")
	alterAt := indexOf(up, "ALTER COLUMN")
	assert.Less(t, renameAt, alterAt, "the rename must come before the change to the new name")
}

func TestDiffSchemas_StaleMarkerIsIgnored(t *testing.T) {
	// The migration already ran: the old schema knows only the new name.
	old := Schema{Tables: []Table{pgTable("users",
		Column{Name: "email_address", Type: "text", NotNull: true},
	)}}
	new := Schema{Tables: []Table{pgTable("users",
		Column{Name: "email_address", Type: "text", NotNull: true, RenamedFrom: "email"},
	)}}

	up, down, err := DiffSchemas(old, new, "postgres")
	require.NoError(t, err)
	assert.Equal(t, "", up, "a marker whose old column is gone is spent, not an error")
	assert.Equal(t, "", down)
}

func TestDiffSchemas_AmbiguousMarkerIsRejected(t *testing.T) {
	// Both names exist in the old schema, so a rename would destroy one of them.
	old := Schema{Tables: []Table{pgTable("users",
		Column{Name: "email", Type: "text", NotNull: true},
		Column{Name: "email_address", Type: "text", NotNull: true},
	)}}
	new := Schema{Tables: []Table{pgTable("users",
		Column{Name: "email_address", Type: "text", NotNull: true, RenamedFrom: "email"},
	)}}

	_, _, err := DiffSchemas(old, new, "postgres")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email")
	assert.Contains(t, err.Error(), "email_address")
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestDiffSchemas_TwoColumnsClaimingTheSameOldNameIsRejected(t *testing.T) {
	old := Schema{Tables: []Table{pgTable("users",
		Column{Name: "email", Type: "text", NotNull: true},
	)}}
	new := Schema{Tables: []Table{pgTable("users",
		Column{Name: "a", Type: "text", NotNull: true, RenamedFrom: "email"},
		Column{Name: "b", Type: "text", NotNull: true, RenamedFrom: "email"},
	)}}

	_, _, err := DiffSchemas(old, new, "postgres")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email")
}

// indexOf returns the byte offset of sub in s, or -1.
func indexOf(s, sub string) int { return strings.Index(s, sub) }

func TestDiffSchemas_TableRenameEmitsRenameNotDropAndCreate(t *testing.T) {
	old := Schema{Tables: []Table{pgTable("purchases",
		Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
	)}}
	new := Schema{Tables: []Table{{
		Name:        "orders",
		RenamedFrom: "purchases",
		Columns: []Column{
			{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
		},
	}}}

	up, down, err := DiffSchemas(old, new, "postgres")
	require.NoError(t, err)
	assert.Equal(t, `ALTER TABLE "purchases" RENAME TO "orders";`, up)
	assert.Equal(t, `ALTER TABLE "orders" RENAME TO "purchases";`, down)
	assert.NotContains(t, up, "DROP TABLE")
	assert.NotContains(t, up, "CREATE TABLE")
}

func TestDiffSchemas_TableAndColumnRenameTogetherAreOrdered(t *testing.T) {
	old := Schema{Tables: []Table{pgTable("purchases",
		Column{Name: "total", Type: "integer", NotNull: true},
	)}}
	new := Schema{Tables: []Table{{
		Name:        "orders",
		RenamedFrom: "purchases",
		Columns: []Column{
			{Name: "total_cents", Type: "integer", NotNull: true, RenamedFrom: "total"},
		},
	}}}

	up, _, err := DiffSchemas(old, new, "postgres")
	require.NoError(t, err)
	tableAt := indexOf(up, "RENAME TO")
	colAt := indexOf(up, "RENAME COLUMN")
	assert.Less(t, tableAt, colAt, "the table rename must precede the column rename")
	assert.Contains(t, up, `ALTER TABLE "orders" RENAME COLUMN "total" TO "total_cents";`)
}

func TestDiffSchemas_StaleTableMarkerIsIgnored(t *testing.T) {
	old := Schema{Tables: []Table{pgTable("orders",
		Column{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
	)}}
	new := Schema{Tables: []Table{{
		Name:        "orders",
		RenamedFrom: "purchases",
		Columns: []Column{
			{Name: "id", Type: "SERIAL PRIMARY KEY", NotNull: true, PK: true},
		},
	}}}

	up, down, err := DiffSchemas(old, new, "postgres")
	require.NoError(t, err)
	assert.Equal(t, "", up)
	assert.Equal(t, "", down)
}

func TestDiffSchemas_AmbiguousTableMarkerIsRejected(t *testing.T) {
	old := Schema{Tables: []Table{
		pgTable("purchases", Column{Name: "id", Type: "integer", NotNull: true}),
		pgTable("orders", Column{Name: "id", Type: "integer", NotNull: true}),
	}}
	new := Schema{Tables: []Table{{
		Name:        "orders",
		RenamedFrom: "purchases",
		Columns:     []Column{{Name: "id", Type: "integer", NotNull: true}},
	}}}

	_, _, err := DiffSchemas(old, new, "postgres")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}
