package codegen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "drel.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0644))
	return path
}

func TestLoadConfig_ModulesBlock(t *testing.T) {
	path := writeConfig(t, `
modules:
  - name: posts
    packages: [./internal/features/posts]
    migrations: ./internal/features/posts/migrations
  - name: users
    packages: [./internal/features/users]

output:
  db: ./internal/db/drel_gen.go
dialect: postgres
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	mods := cfg.ModuleList()
	require.Len(t, mods, 2)

	assert.Equal(t, "posts", mods[0].Name)
	assert.Equal(t, []string{"./internal/features/posts"}, mods[0].Packages)
	assert.Equal(t, "./internal/features/posts/migrations", mods[0].Migrations)

	// A module without an explicit migrations path defaults to a migrations
	// directory inside its own first package, so a slice stays self-contained.
	assert.Equal(t, "./internal/features/users/migrations", mods[1].Migrations)

	assert.Equal(t, []string{"./internal/features/posts", "./internal/features/users"},
		cfg.AllPackages())
}

func TestLoadConfig_PackagesBecomeOneModule(t *testing.T) {
	path := writeConfig(t, `
packages:
  - ./features/users
  - ./features/posts

output:
  db: ./db/drel_gen.go
  migrations: ./db/migrations
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	mods := cfg.ModuleList()
	require.Len(t, mods, 1, "a config without modules is one module")
	assert.Equal(t, DefaultModuleName, mods[0].Name)
	assert.Equal(t, []string{"./features/users", "./features/posts"}, mods[0].Packages)
	assert.Equal(t, "./db/migrations", mods[0].Migrations)
}

func TestLoadConfig_ModulesAndPackagesConflict(t *testing.T) {
	path := writeConfig(t, `
packages: [./features/users]
modules:
  - name: posts
    packages: [./features/posts]
`)

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "packages")
	assert.Contains(t, err.Error(), "modules")
}

func TestLoadConfig_ModuleNeedsANameAndPackages(t *testing.T) {
	noName := writeConfig(t, "modules:\n  - packages: [./features/posts]\n")
	_, err := LoadConfig(noName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")

	noPackages := writeConfig(t, "modules:\n  - name: posts\n")
	_, err = LoadConfig(noPackages)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "packages")
}

func TestLoadConfig_DuplicateModuleNameFails(t *testing.T) {
	path := writeConfig(t, `
modules:
  - name: posts
    packages: [./a]
  - name: posts
    packages: [./b]
`)
	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "posts")
}

func TestConfig_Module_SelectsOne(t *testing.T) {
	path := writeConfig(t, `
modules:
  - name: posts
    packages: [./features/posts]
  - name: users
    packages: [./features/users]
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	mod, err := cfg.Module("users")
	require.NoError(t, err)
	assert.Equal(t, "users", mod.Name)

	_, err = cfg.Module("billing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "billing")
	assert.Contains(t, err.Error(), "posts", "the error must list the modules that exist")
}
