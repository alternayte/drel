package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Changing a column's type to an enum, or to a timestamp, needs a USING clause:
// PostgreSQL refuses an assignment cast between text and either one.
func TestDiffSchemas_ColumnTypeChangeNeedsUsing(t *testing.T) {
	old := Schema{
		Tables: []Table{{
			Name: "quest_events",
			Columns: []Column{
				{Name: "id", Type: "bigint PRIMARY KEY", NotNull: true, PK: true},
				{Name: "to_state", Type: "text", NotNull: true},
				{Name: "started_at", Type: "text"},
			},
		}},
	}
	newSchema := Schema{
		Enums: []EnumDef{{Name: "queststate", Values: []string{"draft", "live"}}},
		Tables: []Table{{
			Name: "quest_events",
			Columns: []Column{
				{Name: "id", Type: "bigint PRIMARY KEY", NotNull: true, PK: true},
				{Name: "to_state", Type: `"queststate"`, NotNull: true},
				{Name: "started_at", Type: "timestamptz"},
			},
		}},
	}

	up, down, err := DiffSchemas(old, newSchema, "postgres")
	require.NoError(t, err)

	// to_state is NOT NULL: the cast stays plain, so the server names the value
	// it cannot parse. started_at is nullable, so '' maps to NULL.
	assert.Contains(t, up, `ALTER TABLE "quest_events" ALTER COLUMN "to_state" TYPE "queststate" USING "to_state"::"queststate";`)
	assert.Contains(t, up, `ALTER TABLE "quest_events" ALTER COLUMN "started_at" TYPE timestamptz USING NULLIF("started_at", '')::timestamptz;`)

	// The down direction casts back. The target is text, which takes any value,
	// so no NULLIF is needed either way.
	assert.Contains(t, down, `ALTER TABLE "quest_events" ALTER COLUMN "to_state" TYPE text USING "to_state"::text;`)
	assert.Contains(t, down, `ALTER TABLE "quest_events" ALTER COLUMN "started_at" TYPE text USING "started_at"::text;`)

	// The up file holds up statements only.
	assert.NotContains(t, up, "DROP TYPE")
	for _, line := range strings.Split(down, "\n") {
		assert.NotContains(t, up, `TYPE "queststate" USING "to_state"::text`, "down statement %q leaked into up", line)
	}
}

// A column whose type changes away from a CHECK-constrained shape must drop the
// stale constraint before the type change, or PostgreSQL re-checks the old
// expression against the new type.
func TestDiffSchemas_StaleCheckDroppedBeforeTypeChange(t *testing.T) {
	old := Schema{
		Tables: []Table{{
			Name: "quest_events",
			Columns: []Column{
				{Name: "to_state", Type: "text", NotNull: true, Check: `"to_state" IN ('draft', 'live')`},
			},
		}},
	}
	newSchema := Schema{
		Enums: []EnumDef{{Name: "queststate", Values: []string{"draft", "live"}}},
		Tables: []Table{{
			Name: "quest_events",
			Columns: []Column{
				{Name: "to_state", Type: `"queststate"`, NotNull: true},
			},
		}},
	}

	up, _, err := DiffSchemas(old, newSchema, "postgres")
	require.NoError(t, err)

	dropIdx := strings.Index(up, "DROP CONSTRAINT IF EXISTS")
	typeIdx := strings.Index(up, "ALTER COLUMN \"to_state\" TYPE")
	require.NotEqual(t, -1, dropIdx, "stale CHECK is never dropped:\n%s", up)
	require.NotEqual(t, -1, typeIdx)
	assert.Less(t, dropIdx, typeIdx, "the CHECK must be dropped before the type change:\n%s", up)
}
