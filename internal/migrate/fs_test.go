package migrate_test

import (
	"testing"
	"testing/fstest"

	"github.com/alternayte/drel/internal/migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moduleFS builds an in-memory migration set, as //go:embed produces for a
// module directory.
func moduleFS(pairs ...[2]string) fstest.MapFS {
	m := fstest.MapFS{}
	for _, p := range pairs {
		m[p[0]+".up.sql"] = &fstest.MapFile{Data: []byte(p[1])}
		m[p[0]+".down.sql"] = &fstest.MapFile{Data: []byte("-- down " + p[0])}
	}
	return m
}

func TestParseMigrationFS_ReadsAPairInOrder(t *testing.T) {
	fsys := moduleFS(
		[2]string{"20260102030405_create_posts", "CREATE TABLE posts (id INT);"},
		[2]string{"20260101020304_create_users", "CREATE TABLE users (id INT);"},
	)

	got, err := migrate.ParseMigrationFS(fsys)
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "20260101020304", got[0].Version)
	assert.Equal(t, "create_users", got[0].Name)
	assert.Equal(t, "CREATE TABLE users (id INT);", got[0].UpSQL)
	assert.Equal(t, "-- down 20260101020304_create_users", got[0].DownSQL)
	assert.Equal(t, "20260102030405", got[1].Version)
}

func TestParseMigrationFS_EmptyIsEmpty(t *testing.T) {
	got, err := migrate.ParseMigrationFS(fstest.MapFS{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestMergeMigrations_OrdersByVersionAcrossModules(t *testing.T) {
	posts := moduleFS([2]string{"20260102000000_create_posts", "CREATE TABLE posts (id INT);"})
	users := moduleFS(
		[2]string{"20260101000000_create_users", "CREATE TABLE users (id INT);"},
		[2]string{"20260103000000_add_email", "ALTER TABLE users ADD COLUMN email TEXT;"},
	)

	got, err := migrate.MergeMigrations(posts, users)
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.Equal(t, []string{"20260101000000", "20260102000000", "20260103000000"},
		[]string{got[0].Version, got[1].Version, got[2].Version},
		"the merged set must run in timestamp order, not module order")
}

func TestMergeMigrations_DuplicateVersionFails(t *testing.T) {
	posts := moduleFS([2]string{"20260909120000_add_posts", "CREATE TABLE posts (id INT);"})
	users := moduleFS([2]string{"20260909120000_add_users", "CREATE TABLE users (id INT);"})

	_, err := migrate.MergeMigrations(posts, users)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20260909120000")
	assert.Contains(t, err.Error(), "add_posts")
	assert.Contains(t, err.Error(), "add_users")
}

func TestMergeMigrations_NoModulesIsEmpty(t *testing.T) {
	got, err := migrate.MergeMigrations()
	require.NoError(t, err)
	assert.Empty(t, got)
}
