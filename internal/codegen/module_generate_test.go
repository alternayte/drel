package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoModuleProject builds a project with two feature slices, each owning its
// models and its migration directory.
func twoModuleProject(t *testing.T) string {
	t.Helper()
	return setupGenerateModule(t, map[string]string{
		"internal/features/users/model.go": `package users

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"internal/features/posts/model.go": `package posts

import "github.com/alternayte/drel"

type Post struct {
	drel.Model[int]
	title string ` + "`db:\"title\"`" + `
}
`,
		"drel.yaml": `modules:
  - name: users
    packages: [./internal/features/users]
  - name: posts
    packages: [./internal/features/posts]

output:
  db: ./internal/db/drel_gen.go

dialect: postgres
`,
	})
}

func TestGenerateModule_AggregatesEveryModule(t *testing.T) {
	dir := twoModuleProject(t)

	require.NoError(t, GenerateModule(filepath.Join(dir, "drel.yaml"), ""))

	dbFile, err := os.ReadFile(filepath.Join(dir, "internal/db/drel_gen.go"))
	require.NoError(t, err)

	// One DB struct reaches both slices, so models in more than one package do
	// not force one package.
	assert.Contains(t, string(dbFile), "Users *users.UserRepository")
	assert.Contains(t, string(dbFile), "Posts *posts.PostRepository")

	for _, f := range []string{
		"internal/features/users/user_drel.go",
		"internal/features/posts/post_drel.go",
	} {
		_, err := os.Stat(filepath.Join(dir, f))
		assert.NoError(t, err, "%s must be generated", f)
	}
}

func TestGenerateModule_UnknownModuleFails(t *testing.T) {
	dir := twoModuleProject(t)

	err := GenerateModule(filepath.Join(dir, "drel.yaml"), "billing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "billing")
	assert.Contains(t, err.Error(), "users")
}

// TestGenerateModule_WritesTheEmbedForEachModule proves the module keeps its
// own migrations and that the generated embed file compiles.
func TestGenerateModule_WritesTheEmbedForEachModule(t *testing.T) {
	dir := twoModuleProject(t)

	// A module directory with no SQL yet must not get an embed file: a
	// //go:embed pattern that matches nothing does not compile.
	require.NoError(t, GenerateModule(filepath.Join(dir, "drel.yaml"), ""))
	_, err := os.Stat(filepath.Join(dir, "internal/features/users/migrations/migrations_drel.go"))
	assert.True(t, os.IsNotExist(err), "an empty module must not get an embed file")

	// Write a migration into each module, as `drel migrate new --module` does.
	for _, m := range []struct{ module, sql string }{
		{"users", "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);"},
		{"posts", "CREATE TABLE posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);"},
	} {
		mDir := filepath.Join(dir, "internal/features", m.module, "migrations")
		require.NoError(t, os.MkdirAll(mDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(mDir, "20260101000000_init.up.sql"), []byte(m.sql), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(mDir, "20260101000000_init.down.sql"), []byte("DROP TABLE "+m.module+";"), 0644))
	}

	require.NoError(t, GenerateModule(filepath.Join(dir, "drel.yaml"), ""))

	for _, module := range []string{"users", "posts"} {
		path := filepath.Join(dir, "internal/features", module, "migrations/migrations_drel.go")
		content, err := os.ReadFile(path)
		require.NoError(t, err, "module %s must get an embed file", module)
		assert.Contains(t, string(content), "//go:embed *.sql")
		assert.Contains(t, string(content), "var FS embed.FS")
		assert.Contains(t, string(content), `"`+module+`"`)
	}

	// The whole project, generated code and embeds included, must compile.
	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	out, err := build.CombinedOutput()
	require.NoError(t, err, "generated project must compile: %s", string(out))
}

// TestGenerateModule_OneModuleOnlyWritesItsEmbed proves --module scopes the
// migration embeds.
func TestGenerateModule_OneModuleOnlyWritesItsEmbed(t *testing.T) {
	dir := twoModuleProject(t)

	for _, module := range []string{"users", "posts"} {
		mDir := filepath.Join(dir, "internal/features", module, "migrations")
		require.NoError(t, os.MkdirAll(mDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(mDir, "20260101000000_init.up.sql"),
			[]byte("CREATE TABLE "+module+" (id SERIAL PRIMARY KEY);"), 0644))
	}

	require.NoError(t, GenerateModule(filepath.Join(dir, "drel.yaml"), "users"))

	_, err := os.Stat(filepath.Join(dir, "internal/features/users/migrations/migrations_drel.go"))
	assert.NoError(t, err, "the named module gets its embed")

	_, err = os.Stat(filepath.Join(dir, "internal/features/posts/migrations/migrations_drel.go"))
	assert.True(t, os.IsNotExist(err), "another module must be left alone")
}
