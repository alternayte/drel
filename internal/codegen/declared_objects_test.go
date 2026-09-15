package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A field may join several named indexes, and an index may carry a predicate.
func TestBuildIndexes_MultipleAndPartial(t *testing.T) {
	m := ModelInfo{
		Name: "Quest", TableName: "quests",
		Fields: []FieldInfo{
			{Name: "UserID", GoType: "int", ColumnName: "user_id", IndexNames: []IndexMembership{
				{Name: "idx_quest_user_state"},
				{Name: "uq_quest_active", Unique: true, Where: "state = 'active'"},
			}},
			{Name: "State", GoType: "string", ColumnName: "state", IndexNames: []IndexMembership{
				{Name: "idx_quest_user_state"},
			}},
		},
	}
	idx := buildIndexes(m)
	require.Len(t, idx, 2)

	assert.Equal(t, "idx_quest_user_state", idx[0].Name)
	assert.Equal(t, []string{"user_id", "state"}, idx[0].Columns)
	assert.False(t, idx[0].Unique)

	assert.Equal(t, "uq_quest_active", idx[1].Name)
	assert.Equal(t, []string{"user_id"}, idx[1].Columns)
	assert.True(t, idx[1].Unique)
	assert.Equal(t, "state = 'active'", idx[1].Where)
}

// A model-level declaration names its columns, so it can index a trait column
// such as created_at, which has no Go field to tag.
func TestBuildIndexes_ModelLevelCoversTraitColumns(t *testing.T) {
	m := ModelInfo{
		Name: "Quest", TableName: "quests",
		Fields:  []FieldInfo{{Name: "UserID", GoType: "int", ColumnName: "user_id"}},
		Indexes: []IndexDecl{{Name: "idx_quest_recent", Columns: []string{"user_id", "created_at"}}},
	}
	idx := buildIndexes(m)
	require.Len(t, idx, 1)
	assert.Equal(t, []string{"user_id", "created_at"}, idx[0].Columns)
}

func TestParseDBTag_IndexAndForeignKeyOptions(t *testing.T) {
	col, opts, err := parseDBTag(`db:"user_id,references=auth_users.id,on_delete=cascade,on_update=restrict,deferrable,index=idx_a,unique_index=uq_b(state = 'active')"`)
	require.NoError(t, err)
	assert.Equal(t, "user_id", col)
	assert.Equal(t, "auth_users", opts.references)
	assert.Equal(t, "id", opts.refColumn)
	assert.Equal(t, "CASCADE", opts.onDelete)
	assert.Equal(t, "RESTRICT", opts.onUpdate)
	assert.True(t, opts.deferrable)
	require.Len(t, opts.indexes, 2)
	assert.Equal(t, indexOpt{name: "idx_a"}, opts.indexes[0])
	assert.Equal(t, indexOpt{name: "uq_b", unique: true, where: "state = 'active'"}, opts.indexes[1])
}

func TestParseDBTag_ModelLevelIndexColumns(t *testing.T) {
	_, opts, err := parseDBTag(`db:",index=idx_recent[user_id,created_at]"`)
	require.NoError(t, err)
	require.Len(t, opts.indexes, 1)
	assert.Equal(t, []string{"user_id", "created_at"}, opts.indexes[0].columns)
}

func TestParseDBTag_RejectsUnknownForeignKeyAction(t *testing.T) {
	_, _, err := parseDBTag(`db:"user_id,references=auth_users,on_delete=explode"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown action")
}

// A foreign key declared on an existing column becomes an ALTER. Nothing was
// emitted before: the differ ignored the reference entirely.
func TestDiffSchemas_ForeignKeyAddedToExistingColumn(t *testing.T) {
	old := Schema{Tables: []Table{{
		Name:    "quests",
		Columns: []Column{{Name: "user_id", Type: "text", NotNull: true}},
	}}}
	newS := Schema{Tables: []Table{{
		Name: "quests",
		Columns: []Column{{
			Name: "user_id", Type: "text", NotNull: true,
			Ref: "auth_users", RefColumn: "id", OnDelete: "CASCADE", Deferrable: true,
		}},
	}}}

	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	assert.Contains(t, up, `ALTER TABLE "quests" DROP CONSTRAINT IF EXISTS "fk_quests_user_id";`)
	assert.Contains(t, up, `ALTER TABLE "quests" ADD CONSTRAINT "fk_quests_user_id" FOREIGN KEY ("user_id") REFERENCES "auth_users"("id") ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;`)
	assert.Contains(t, down, `ALTER TABLE "quests" DROP CONSTRAINT IF EXISTS "fk_quests_user_id";`)
}

// An index that keeps its name while its shape changes is dropped and recreated.
func TestDiffSchemas_IndexShapeChange(t *testing.T) {
	old := Schema{Tables: []Table{{
		Name:    "quests",
		Columns: []Column{{Name: "user_id", Type: "text"}},
		Indexes: []Index{{Name: "uq_quest_active", Columns: []string{"user_id"}, Unique: true}},
	}}}
	newS := Schema{Tables: []Table{{
		Name:    "quests",
		Columns: []Column{{Name: "user_id", Type: "text"}},
		Indexes: []Index{{Name: "uq_quest_active", Columns: []string{"user_id"}, Unique: true, Where: "state = 'active'"}},
	}}}

	up, _, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)
	dropIdx := strings.Index(up, `DROP INDEX IF EXISTS "uq_quest_active";`)
	createIdx := strings.Index(up, `WHERE state = 'active'`)
	require.NotEqual(t, -1, dropIdx, "the index is never dropped:\n%s", up)
	require.NotEqual(t, -1, createIdx, "the predicate never reaches the DDL:\n%s", up)
	assert.Less(t, dropIdx, createIdx)
}

// A constraint written by hand and then adopted is dropped BEFORE the column
// type changes. PostgreSQL re-checks a surviving constraint against the new
// type, which is what broke the enum migration.
func TestDiffSchemas_AdoptedCheckDroppedBeforeTypeChange(t *testing.T) {
	old := Schema{Tables: []Table{{
		Name:    "quest_events",
		Columns: []Column{{Name: "to_state", Type: "text", NotNull: true}},
		Checks: []CheckConstraint{{
			Name: "ck_quest_events_to_state",
			Expr: `to_state = ANY (ARRAY['draft'::text, 'live'::text])`,
		}},
	}}}
	newS := Schema{
		Enums: []EnumDef{{Name: "queststate", Values: []string{"draft", "live"}}},
		Tables: []Table{{
			Name:    "quest_events",
			Columns: []Column{{Name: "to_state", Type: `"queststate"`, NotNull: true}},
		}},
	}

	up, down, err := DiffSchemas(old, newS, "postgres")
	require.NoError(t, err)

	dropIdx := strings.Index(up, `DROP CONSTRAINT IF EXISTS "ck_quest_events_to_state";`)
	typeIdx := strings.Index(up, `ALTER COLUMN "to_state" TYPE`)
	require.NotEqual(t, -1, dropIdx, "the adopted constraint is never dropped:\n%s", up)
	require.NotEqual(t, -1, typeIdx)
	assert.Less(t, dropIdx, typeIdx, "the constraint must go before the type change:\n%s", up)

	// The down restores it, after the column type is reverted.
	restoreIdx := strings.Index(down, `ADD CONSTRAINT "ck_quest_events_to_state"`)
	revertIdx := strings.Index(down, `ALTER COLUMN "to_state" TYPE text`)
	require.NotEqual(t, -1, restoreIdx, "the down does not restore the constraint:\n%s", down)
	assert.Less(t, revertIdx, restoreIdx, "the type must be reverted before the constraint returns:\n%s", down)
}

// Adoption records what the database holds and the models do not declare.
func TestAdoptLiveObjects(t *testing.T) {
	snapshot := Schema{Tables: []Table{{
		Name:    "quests",
		Columns: []Column{{Name: "user_id", Type: "text"}, {Name: "state", Type: "text"}},
	}}}
	live := Schema{Tables: []Table{{
		Name: "quests",
		Columns: []Column{
			{Name: "user_id", Type: "text", Ref: "auth_users", RefColumn: "id", OnDelete: "CASCADE"},
			{Name: "state", Type: "text"},
		},
		Indexes: []Index{{Name: "one_active_quest", Columns: []string{"user_id"}, Unique: true, Where: "state = 'active'"}},
		Checks:  []CheckConstraint{{Name: "ck_quests_state", Expr: "state IN ('draft')"}},
	}}}
	declared := Schema{Tables: []Table{{
		Name:    "quests",
		Columns: []Column{{Name: "user_id", Type: "text"}, {Name: "state", Type: "text"}},
	}}}

	merged, adopted := AdoptLiveObjects(snapshot, live, declared)
	require.Len(t, adopted, 3)

	tbl := merged.Tables[0]
	require.Len(t, tbl.Checks, 1)
	assert.Equal(t, "ck_quests_state", tbl.Checks[0].Name)
	require.Len(t, tbl.Indexes, 1)
	assert.Equal(t, "one_active_quest", tbl.Indexes[0].Name)
	assert.Equal(t, "auth_users", tbl.Columns[0].Ref)
	assert.Equal(t, "CASCADE", tbl.Columns[0].OnDelete)

	// Adoption is idempotent.
	again, adoptedAgain := AdoptLiveObjects(merged, live, declared)
	assert.Empty(t, adoptedAgain)
	assert.Len(t, again.Tables[0].Checks, 1)
	assert.Len(t, again.Tables[0].Indexes, 1)
}

// A table no model owns is not taken over: it belongs to another system.
func TestAdoptLiveObjects_IgnoresTablesNotInTheSnapshot(t *testing.T) {
	snapshot := Schema{Tables: []Table{{Name: "quests"}}}
	live := Schema{Tables: []Table{
		{Name: "quests"},
		{Name: "auth_users", Indexes: []Index{{Name: "idx_auth", Columns: []string{"id"}}}},
	}}
	merged, adopted := AdoptLiveObjects(snapshot, live, Schema{Tables: []Table{{Name: "quests"}}})
	assert.Empty(t, adopted)
	assert.Len(t, merged.Tables, 1)
}
