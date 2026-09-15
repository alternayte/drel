package codegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func indexSchema(idx Index) Schema {
	return Schema{Tables: []Table{{
		Name:    "user_quests",
		Columns: []Column{{Name: "user_id", Type: "text"}},
		Indexes: []Index{idx},
	}}}
}

// The server rewrites a predicate when it stores it, so two renderings of the
// same rule are not a difference.
func TestCompareSchemas_IndexPredicateTextIsNotADifference(t *testing.T) {
	live := indexSchema(Index{
		Name: "uq_one_active", Columns: []string{"user_id"}, Unique: true,
		Where: `(state = ANY (ARRAY['assigned'::queststate, 'in_progress'::queststate]))`,
	})
	declared := indexSchema(Index{
		Name: "uq_one_active", Columns: []string{"user_id"}, Unique: true,
		Where: "state IN ('assigned', 'in_progress')",
	})

	assert.True(t, CompareSchemas(live, declared).Empty())
}

// Gaining or losing a predicate is a real difference: a full index and a
// partial one cover different rows.
func TestCompareSchemas_IndexPredicatePresenceIsADifference(t *testing.T) {
	full := Index{Name: "uq_one_active", Columns: []string{"user_id"}, Unique: true}
	partial := full
	partial.Where = "state = 'active'"

	d := CompareSchemas(indexSchema(full), indexSchema(partial))
	require.Len(t, d.Different, 1, "a full index against a partial one is a difference")

	d = CompareSchemas(indexSchema(partial), indexSchema(full))
	require.Len(t, d.Different, 1, "a partial index against a full one is a difference")
}

// Columns and uniqueness are still compared exactly.
func TestCompareSchemas_IndexShapeIsStillCompared(t *testing.T) {
	live := indexSchema(Index{Name: "idx", Columns: []string{"user_id"}, Where: "a"})
	declared := indexSchema(Index{Name: "idx", Columns: []string{"user_id", "state"}, Where: "b"})
	require.Len(t, CompareSchemas(live, declared).Different, 1, "different columns are a difference")

	live = indexSchema(Index{Name: "idx", Columns: []string{"user_id"}, Where: "a"})
	declared = indexSchema(Index{Name: "idx", Columns: []string{"user_id"}, Unique: true, Where: "b"})
	require.Len(t, CompareSchemas(live, declared).Different, 1, "different uniqueness is a difference")
}

// The differ compares drel's own rendering on both sides, so it still compares
// the predicate text: a changed predicate must recreate the index.
func TestDiffSchemas_StillRecreatesOnAChangedPredicate(t *testing.T) {
	old := indexSchema(Index{Name: "idx", Columns: []string{"user_id"}, Where: "state = 'a'"})
	newS := indexSchema(Index{Name: "idx", Columns: []string{"user_id"}, Where: "state = 'b'"})

	up, _, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `DROP INDEX IF EXISTS "idx";`)
	assert.Contains(t, up, `WHERE state = 'b'`)
}
