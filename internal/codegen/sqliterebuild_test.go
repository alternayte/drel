package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rebuildFixture() (old, new Table) {
	old = Table{
		Name: "notes",
		Columns: []Column{
			{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
			{Name: "body", Type: "TEXT", NotNull: false},
			{Name: "tag", Type: "TEXT", NotNull: true},
		},
		Indexes: []Index{{Name: "idx_notes_tag", Columns: []string{"tag"}}},
	}
	new = old
	new.Columns = []Column{
		{Name: "id", Type: "INTEGER PRIMARY KEY AUTOINCREMENT", NotNull: true, PK: true},
		{Name: "body", Type: "TEXT", NotNull: true}, // nullability change
		{Name: "tag", Type: "TEXT", NotNull: true},
	}
	return old, new
}

func TestSQLiteRebuild_UsesDeferForeignKeysNotForeignKeysOff(t *testing.T) {
	old, new := rebuildFixture()
	sql := strings.Join(sqliteRebuild(new, old), "\n")
	assert.Contains(t, sql, "PRAGMA defer_foreign_keys = ON;")
	assert.NotContains(t, strings.ToLower(sql), "pragma foreign_keys",
		"PRAGMA foreign_keys is a no-op inside a transaction, so emitting it would silently do nothing")
}

func TestSQLiteRebuild_CopiesOnlyTheColumnsPresentInBothShapes(t *testing.T) {
	old, new := rebuildFixture()
	new.Columns = append(new.Columns, Column{Name: "pinned", Type: "INTEGER", NotNull: false})
	sql := strings.Join(sqliteRebuild(new, old), "\n")
	assert.Contains(t, sql,
		`INSERT INTO "notes__drel_new" ("id", "body", "tag") SELECT "id", "body", "tag" FROM "notes";`)
	assert.NotContains(t, sql, `"pinned"`+" FROM")
}

func TestSQLiteRebuild_NamesColumnsExplicitlyNeverSelectStar(t *testing.T) {
	old, new := rebuildFixture()
	sql := strings.Join(sqliteRebuild(new, old), "\n")
	assert.NotContains(t, sql, "SELECT *",
		"relying on column order would silently mis-map columns")
}

func TestSQLiteRebuild_DropsRenamesAndRecreatesTheIndexes(t *testing.T) {
	old, new := rebuildFixture()
	stmts := sqliteRebuild(new, old)
	sql := strings.Join(stmts, "\n")
	assert.Contains(t, sql, `DROP TABLE "notes";`)
	assert.Contains(t, sql, `ALTER TABLE "notes__drel_new" RENAME TO "notes";`)
	assert.Contains(t, sql, `CREATE INDEX "idx_notes_tag" ON "notes" ("tag");`)

	dropAt, renameAt, indexAt := -1, -1, -1
	for i, s := range stmts {
		switch {
		case strings.HasPrefix(s, `DROP TABLE "notes"`):
			dropAt = i
		case strings.Contains(s, "RENAME TO"):
			renameAt = i
		case strings.HasPrefix(s, "CREATE INDEX"):
			indexAt = i
		}
	}
	require.NotEqual(t, -1, dropAt)
	assert.Less(t, dropAt, renameAt)
	assert.Less(t, renameAt, indexAt, "indexes must be recreated after the rename")
}

func TestSQLiteRebuild_CarriesTheCompositePrimaryKey(t *testing.T) {
	old := Table{
		Name: "order_lines",
		Columns: []Column{
			{Name: "order_id", Type: "INTEGER", NotNull: true, PK: true},
			{Name: "line_no", Type: "INTEGER", NotNull: true, PK: true},
			{Name: "qty", Type: "INTEGER", NotNull: false},
		},
		PrimaryKey: []string{"order_id", "line_no"},
	}
	new := old
	new.Columns = []Column{
		{Name: "order_id", Type: "INTEGER", NotNull: true, PK: true},
		{Name: "line_no", Type: "INTEGER", NotNull: true, PK: true},
		{Name: "qty", Type: "INTEGER", NotNull: true},
	}
	sql := strings.Join(sqliteRebuild(new, old), "\n")
	assert.Contains(t, sql, `PRIMARY KEY ("order_id", "line_no")`)
}

func TestDiffTable_OneRebuildForTwoColumnChangesOnOneTable(t *testing.T) {
	old := Table{Name: "notes", Columns: []Column{
		{Name: "body", Type: "TEXT", NotNull: false},
		{Name: "tag", Type: "TEXT", NotNull: false},
	}}
	new := Table{Name: "notes", Columns: []Column{
		{Name: "body", Type: "TEXT", NotNull: true},
		{Name: "tag", Type: "INTEGER", NotNull: false},
	}}

	up, down, err := diffTable(old, new, "sqlite")
	require.NoError(t, err)
	// PRAGMA defer_foreign_keys appears exactly once per rebuild, so counting it
	// counts rebuilds. The scratch table name appears several times inside one
	// rebuild (CREATE, INSERT, ALTER), so it cannot be counted instead.
	assert.Equal(t, 1, strings.Count(strings.Join(up, "\n"), "PRAGMA defer_foreign_keys"),
		"two column changes on one table must produce exactly one rebuild")
	assert.NotContains(t, strings.Join(up, "\n"), "WARNING")
	assert.Contains(t, strings.Join(down, "\n"), "__drel_new",
		"the down migration rebuilds back to the old shape")

	// The whole rebuild must be ONE element, because DiffSchemas reverses the
	// down slice element-wise. Spread across elements it would run backwards.
	require.Len(t, up, 1)
	require.Len(t, down, 1)
}

func TestDiffTable_RebuildSurvivesTheDownReversal(t *testing.T) {
	old := Schema{Tables: []Table{{Name: "notes", Columns: []Column{
		{Name: "body", Type: "TEXT", NotNull: false},
	}}}}
	new := Schema{Tables: []Table{{Name: "notes", Columns: []Column{
		{Name: "body", Type: "TEXT", NotNull: true},
	}}}}

	_, down, err := DiffSchemas(old, new, "sqlite")
	require.NoError(t, err)
	createAt := strings.Index(down, "CREATE TABLE")
	insertAt := strings.Index(down, "INSERT INTO")
	dropAt := strings.Index(down, "DROP TABLE")
	renameAt := strings.Index(down, "RENAME TO")
	require.NotEqual(t, -1, createAt)
	assert.Less(t, createAt, insertAt, "the down rebuild must not be reversed internally")
	assert.Less(t, insertAt, dropAt)
	assert.Less(t, dropAt, renameAt)
}

func TestDiffTable_RenameAndRebuildOnOneTableKeepsTheRenamedColumnsData(t *testing.T) {
	old := Table{Name: "notes", Columns: []Column{
		{Name: "body", Type: "TEXT", NotNull: false},
	}}
	new := Table{Name: "notes", Columns: []Column{
		{Name: "content", Type: "TEXT", NotNull: true, RenamedFrom: "body"},
	}}

	up, _, err := diffTable(old, new, "sqlite")
	require.NoError(t, err)
	joined := strings.Join(up, "\n")
	assert.Contains(t, joined, `RENAME COLUMN "body" TO "content"`)
	assert.Contains(t, joined, `INSERT INTO "notes__drel_new" ("content") SELECT "content" FROM "notes";`,
		"the rebuild runs after the rename, so its source column is already the new name")
	assert.NotContains(t, joined, `SELECT "body" FROM`)
}

func TestDiffTable_PostgresIsUnaffected(t *testing.T) {
	old := Table{Name: "notes", Columns: []Column{{Name: "body", Type: "text", NotNull: false}}}
	new := Table{Name: "notes", Columns: []Column{{Name: "body", Type: "text", NotNull: true}}}

	up, _, err := diffTable(old, new, "postgres")
	require.NoError(t, err)
	assert.Equal(t, []string{`ALTER TABLE "notes" ALTER COLUMN "body" SET NOT NULL;`}, up)
}
