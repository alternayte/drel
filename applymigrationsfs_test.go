package drel_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moduleMigrations builds the filesystem a module's //go:embed produces.
func moduleMigrations(pairs ...[3]string) fstest.MapFS {
	m := fstest.MapFS{}
	for _, p := range pairs {
		m[p[0]+".up.sql"] = &fstest.MapFile{Data: []byte(p[1])}
		m[p[0]+".down.sql"] = &fstest.MapFile{Data: []byte(p[2])}
	}
	return m
}

func TestApplyMigrationsFS_AppliesEveryModule(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	users := moduleMigrations([3]string{
		"20260101000000_create_users",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL);",
		"DROP TABLE users;",
	})
	posts := moduleMigrations([3]string{
		"20260102000000_create_posts",
		"CREATE TABLE posts (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL REFERENCES users(id));",
		"DROP TABLE posts;",
	})

	// The posts module references the users table, and it is passed first. The
	// version order must still apply users before posts.
	n, err := engine.ApplyMigrationsFS(ctx, posts, users)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	_, err = engine.Exec(ctx, `INSERT INTO users (id, name) VALUES (1, 'Alice')`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO posts (id, author_id) VALUES (1, 1)`)
	require.NoError(t, err)

	// A second run applies nothing.
	n, err = engine.ApplyMigrationsFS(ctx, posts, users)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestApplyMigrationsFS_DuplicateVersionFails(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()

	a := moduleMigrations([3]string{"20260909120000_add_posts", "CREATE TABLE posts (id INTEGER);", "DROP TABLE posts;"})
	b := moduleMigrations([3]string{"20260909120000_add_users", "CREATE TABLE users (id INTEGER);", "DROP TABLE users;"})

	_, err = engine.ApplyMigrationsFS(context.Background(), a, b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20260909120000")
	assert.Contains(t, err.Error(), "add_posts")
	assert.Contains(t, err.Error(), "add_users")

	// Nothing is applied when the merge fails.
	var n int
	require.Error(t, engine.QueryRow(context.Background(), `SELECT count(*) FROM posts`).Scan(&n))
}

func TestApplyMigrationsFS_NoModulesIsNoOp(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()

	n, err := engine.ApplyMigrationsFS(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
