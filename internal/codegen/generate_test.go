package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGenerateModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	goVersion := strings.TrimPrefix(runtime.Version(), "go")
	parts := strings.SplitN(goVersion, ".", 3)
	goDirective := parts[0] + "." + parts[1]

	moduleRoot := findModuleRoot(t)

	goMod := "module testmod\n\ngo " + goDirective + "\n\n" +
		"require github.com/alternayte/drel v0.0.0\n\n" +
		"replace github.com/alternayte/drel => " + moduleRoot + "\n"

	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644))

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	}

	// Run go mod tidy so packages.Load can resolve dependencies.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidyOut, err := tidy.CombinedOutput()
	require.NoError(t, err, "go mod tidy in setup failed: %s", string(tidyOut))

	return dir
}

func TestGenerate_EndToEnd(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/product.go": `package models

import "github.com/alternayte/drel"

type Product struct {
	drel.Model[int]
	name    string ` + "`db:\"name\"`" + `
	price   int    ` + "`db:\"price\"`" + `
	inStock bool   ` + "`db:\"in_stock\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	// Save and restore working directory.
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	// Run the full codegen pipeline.
	err = Generate("drel.yaml")
	require.NoError(t, err)

	// Verify model file was generated.
	modelFile := filepath.Join(dir, "models", "product_drel.go")
	assert.FileExists(t, modelFile)

	modelContent, err := os.ReadFile(modelFile)
	require.NoError(t, err)
	modelStr := string(modelContent)
	modelNorm := strings.Join(strings.Fields(modelStr), " ") // gofmt-alignment-insensitive

	assert.Contains(t, modelStr, "var Products = struct {")
	assert.Contains(t, modelStr, "var ProductMeta = drel.ModelMeta[Product]{")
	assert.Contains(t, modelNorm, "Name drel.StringColumn")
	assert.Contains(t, modelNorm, "Price drel.OrderedColumn[int]")
	assert.Contains(t, modelNorm, "InStock drel.BoolColumn")

	// Verify DB file was generated.
	dbFile := filepath.Join(dir, "db", "drel_gen.go")
	assert.FileExists(t, dbFile)

	dbContent, err := os.ReadFile(dbFile)
	require.NoError(t, err)
	dbStr := string(dbContent)

	assert.Contains(t, dbStr, "type DB struct {")
	assert.Contains(t, dbStr, "Products *models.ProductRepository")

	// Run go mod tidy and go build to verify generated code compiles.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidyOut, err := tidy.CombinedOutput()
	require.NoError(t, err, "go mod tidy failed: %s", string(tidyOut))

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(buildOut))
}

func TestGenerate_MultipleModels(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/user.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name  string ` + "`db:\"name\"`" + `
	email string ` + "`db:\"email\"`" + `
}
`,
		"models/post.go": `package models

import "github.com/alternayte/drel"

type Post struct {
	drel.Model[int]
	title string ` + "`db:\"title\"`" + `
	body  string ` + "`db:\"body\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	// Save and restore working directory.
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	// Run the full codegen pipeline.
	err = Generate("drel.yaml")
	require.NoError(t, err)

	// Verify both model files were generated.
	userFile := filepath.Join(dir, "models", "user_drel.go")
	postFile := filepath.Join(dir, "models", "post_drel.go")
	assert.FileExists(t, userFile)
	assert.FileExists(t, postFile)

	// Verify DB file has both repositories.
	dbFile := filepath.Join(dir, "db", "drel_gen.go")
	assert.FileExists(t, dbFile)

	dbContent, err := os.ReadFile(dbFile)
	require.NoError(t, err)
	dbStr := string(dbContent)

	assert.Contains(t, dbStr, "type DB struct {")
	assert.Contains(t, dbStr, "Users *models.UserRepository")
	assert.Contains(t, dbStr, "Posts *models.PostRepository")

	// Run go mod tidy and go build to verify generated code compiles.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidyOut, err := tidy.CombinedOutput()
	require.NoError(t, err, "go mod tidy failed: %s", string(tidyOut))

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(buildOut))
}

func TestResolveModuleRoot_WalksToGoMod(t *testing.T) {
	// ResolveModuleRoot must find the nearest ancestor containing go.mod.
	// NOTE: migrate new no longer calls ResolveModuleRoot — it uses cfgDir
	// directly (matching Generate). This test covers the helper itself.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0644))
	sub := filepath.Join(root, "deploy")
	require.NoError(t, os.MkdirAll(sub, 0755))
	assert.Equal(t, root, ResolveModuleRoot(sub))
}

func TestResolveModuleRoot_ConfigInSubdir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0644))
	sub := filepath.Join(root, "config", "nested")
	require.NoError(t, os.MkdirAll(sub, 0755))

	got := ResolveModuleRoot(sub)
	assert.Equal(t, root, got)
}

func TestResolveModuleRoot_ConfigAtRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0644))

	got := ResolveModuleRoot(root)
	assert.Equal(t, root, got)
}

func TestResolveModuleRoot_NoGoMod_FallsBackToStart(t *testing.T) {
	// A directory with no go.mod anywhere above it falls back to startDir,
	// preserving today's behaviour for configs that *are* at the module root.
	start := filepath.Join(t.TempDir(), "x", "y")
	require.NoError(t, os.MkdirAll(start, 0755))

	got := ResolveModuleRoot(start)
	assert.Equal(t, start, got)
}

func TestGenerate_ConfigInSubdirOfModule(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		// Models live under the config subdirectory — packages in drel.yaml are
		// resolved relative to the config file's directory, not the module root.
		"config/models/product.go": `package models

import "github.com/alternayte/drel"

type Product struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"config/drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	// Config lives in <module>/config; ./models resolves to <module>/config/models.
	err := Generate(filepath.Join(dir, "config", "drel.yaml"))
	require.NoError(t, err)

	// Model file is written next to the scanned package (config-dir-rooted).
	assert.FileExists(t, filepath.Join(dir, "config", "models", "product_drel.go"))
	// DB output stays anchored to the config's directory.
	assert.FileExists(t, filepath.Join(dir, "config", "db", "drel_gen.go"))
}

func TestGenerate_WithValueObjects(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/types.go": `package models

import (
	"database/sql/driver"
	"fmt"
)

type Email struct{ address string }

func NewEmail(addr string) Email { return Email{address: addr} }
func (e Email) String() string   { return e.address }
func (e Email) Value() (driver.Value, error) { return e.address, nil }
func (e *Email) Scan(src any) error {
	s, ok := src.(string)
	if !ok { return fmt.Errorf("Email.Scan: expected string, got %T", src) }
	e.address = s
	return nil
}

type Role string
const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)
`,
		"models/user.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name  string ` + "`db:\"name\"`" + `
	email Email  ` + "`db:\"email\"`" + `
	role  Role   ` + "`db:\"role\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	// Save and restore working directory.
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	// Run the full codegen pipeline.
	err = Generate("drel.yaml")
	require.NoError(t, err)

	// Verify model file was generated.
	modelFile := filepath.Join(dir, "models", "user_drel.go")
	assert.FileExists(t, modelFile)

	modelContent, err := os.ReadFile(modelFile)
	require.NoError(t, err)
	modelStr := string(modelContent)
	modelNorm := strings.Join(strings.Fields(modelStr), " ") // gofmt-alignment-insensitive

	// Verify VO type uses unqualified name (Email, not testmod/models.Email).
	assert.Contains(t, modelStr, "drel.ComparableColumn[Email]")
	assert.Contains(t, modelStr, `drel.NewComparableCol[Email]("email")`)

	// Verify string-based enum type uses unqualified name.
	assert.Contains(t, modelStr, "drel.ComparableColumn[Role]")
	assert.Contains(t, modelStr, `drel.NewComparableCol[Role]("role")`)

	// Verify snapshot uses local types.
	assert.Contains(t, modelNorm, "email Email")
	assert.Contains(t, modelNorm, "role Role")

	// Run go mod tidy and go build to verify generated code compiles.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidyOut, err := tidy.CombinedOutput()
	require.NoError(t, err, "go mod tidy failed: %s", string(tidyOut))

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(buildOut))
}

func TestGenerate_DuplicateModelName(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"auth/user.go": `package auth

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"billing/user.go": `package billing

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	plan string ` + "`db:\"plan\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./auth
  - ./billing
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Users")
	// No DB file should exist after a validation failure.
	assert.NoFileExists(t, filepath.Join(dir, "db", "drel_gen.go"))
}

func TestGenerate_UnresolvedTarget(t *testing.T) {
	// Post references Author (from the authors package) via rel tag, but the
	// authors package is NOT listed in drel.yaml. The scanner loads the posts
	// package cleanly (imports resolve), but ValidateModels must catch "Author" as
	// an unresolved target and return an error naming the field and drel.yaml.
	dir := setupGenerateModule(t, map[string]string{
		"authors/author.go": `package authors

import "github.com/alternayte/drel"

type Author struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"posts/post.go": `package posts

import (
	"github.com/alternayte/drel"
	"testmod/authors"
)

type Post struct {
	drel.Model[int]
	title  string          ` + "`db:\"title\"`" + `
	Author *authors.Author ` + "`rel:\"belongs_to,fk=author_id\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./posts
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Author")
	assert.Contains(t, err.Error(), "drel.yaml")
}

func TestGenerate_ColumnLessModel(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/models.go": `package models

import "github.com/alternayte/drel"

type Org struct {
	drel.Model[int]
	Members []*User ` + "`rel:\"has_many,fk=org_id\"`" + `
}

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no db-mapped columns")
}

func TestGenerate_AtomicOnEmitError(t *testing.T) {
	// A valid model and a model with an unsupported channel field in the same
	// package. The unsupported field makes EmitModelFileChecked error; nothing
	// should be written for EITHER model and no DB file should appear.
	dir := setupGenerateModule(t, map[string]string{
		"models/models.go": `package models

import "github.com/alternayte/drel"

type Good struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}

type Bad struct {
	drel.Model[int]
	signal chan int ` + "`db:\"signal\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signal")

	// Atomicity: NO per-model file and NO db file were written.
	assert.NoFileExists(t, filepath.Join(dir, "models", "good_drel.go"))
	assert.NoFileExists(t, filepath.Join(dir, "models", "bad_drel.go"))
	assert.NoFileExists(t, filepath.Join(dir, "db", "drel_gen.go"))
}

func TestGenerate_RemovesStaleFile(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/user.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"models/post.go": `package models

import "github.com/alternayte/drel"

type Post struct {
	drel.Model[int]
	title string ` + "`db:\"title\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	// First generation produces both per-model files.
	require.NoError(t, Generate("drel.yaml"))
	postFile := filepath.Join(dir, "models", "post_drel.go")
	require.FileExists(t, postFile)

	// Delete the Post model source, then regenerate.
	require.NoError(t, os.Remove(filepath.Join(dir, "models", "post.go")))
	require.NoError(t, Generate("drel.yaml"))

	// The stale post_drel.go (generated by drel, no longer backing a model) is gone.
	assert.NoFileExists(t, postFile)
	// The remaining model is still there.
	assert.FileExists(t, filepath.Join(dir, "models", "user_drel.go"))
}

func TestGenerate_MultiPackageDeterministic(t *testing.T) {
	// Two packages with a cross-package relation, generated twice; the DB file
	// must be byte-identical across runs (proves the cross-package sort).
	files := map[string]string{
		"blog/author.go": `package blog

import "github.com/alternayte/drel"

type Author struct {
	drel.Model[int]
	name  string  ` + "`db:\"name\"`" + `
	Posts []*Post ` + "`rel:\"has_many,fk=author_id\"`" + `
}
`,
		"blog/post.go": `package blog

import "github.com/alternayte/drel"

type Post struct {
	drel.Model[int]
	title    string  ` + "`db:\"title\"`" + `
	authorID int     ` + "`db:\"author_id\"`" + `
	Author   *Author ` + "`rel:\"belongs_to,fk=author_id\"`" + `
}
`,
		"shop/order.go": `package shop

import "github.com/alternayte/drel"

type Order struct {
	drel.Model[int]
	total int ` + "`db:\"total\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./blog
  - ./shop
output:
  db: ./db/drel_gen.go
`,
	}
	dir := setupGenerateModule(t, files)

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	dbFile := filepath.Join(dir, "db", "drel_gen.go")

	require.NoError(t, Generate("drel.yaml"))
	first, err := os.ReadFile(dbFile)
	require.NoError(t, err)

	require.NoError(t, Generate("drel.yaml"))
	second, err := os.ReadFile(dbFile)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second), "regenerated DB file must be byte-identical")

	// And it must compile + vet clean.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidyOut, err := tidy.CombinedOutput()
	require.NoError(t, err, "go mod tidy failed: %s", string(tidyOut))

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(buildOut))

	vet := exec.Command("go", "vet", "./...")
	vet.Dir = dir
	vetOut, err := vet.CombinedOutput()
	require.NoError(t, err, "go vet failed: %s", string(vetOut))
}

// TestGenerate_AtomicOnRegenWithValidationError proves that regenerating over an
// existing good tree does NOT destroy previously-generated files when the
// pipeline fails (validation error). The pre-existing *_drel.go must survive.
func TestGenerate_AtomicOnRegenWithValidationError(t *testing.T) {
	// Start with a valid tree: one model with a column.
	dir := setupGenerateModule(t, map[string]string{
		"models/user.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	// First generation must succeed, producing user_drel.go.
	require.NoError(t, Generate("drel.yaml"))
	userFile := filepath.Join(dir, "models", "user_drel.go")
	require.FileExists(t, userFile)

	// Now introduce a column-less model in the same package (validation error).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "models", "org.go"), []byte(`package models

import "github.com/alternayte/drel"

type Org struct {
	drel.Model[int]
}
`), 0644))

	// Regen must fail.
	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no db-mapped columns")

	// The pre-existing user_drel.go must NOT have been deleted.
	assert.FileExists(t, userFile, "pre-existing user_drel.go must survive a failed regen")
}

// TestGenerate_AtomicOnRegenWithEmitError proves atomicity when the error
// occurs during the emit phase (not validation) in a regen over an existing tree.
func TestGenerate_AtomicOnRegenWithEmitError(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/user.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	// First generation must succeed.
	require.NoError(t, Generate("drel.yaml"))
	userFile := filepath.Join(dir, "models", "user_drel.go")
	require.FileExists(t, userFile)

	// Add a model with an unsupported type (channel) — triggers an emit error.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "models", "bad.go"), []byte(`package models

import "github.com/alternayte/drel"

type Bad struct {
	drel.Model[int]
	signal chan int `+"`db:\"signal\"`"+`
}
`), 0644))

	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signal")

	// user_drel.go must NOT have been deleted by the failed regen.
	assert.FileExists(t, userFile, "pre-existing user_drel.go must survive a failed regen")
}

// TestGenerate_NamedPrimitiveField proves that a named type whose underlying
// kind is a comparable basic type (e.g. type Priority int with no enum consts)
// is accepted and emits valid code. Regression guard for the isComparableForDiff
// over-rejection of valid comparable named primitives.
func TestGenerate_NamedPrimitiveField(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/types.go": `package models

// Priority is a named int type with no enum const values — NOT an enum, just
// a comparable named primitive. It must be accepted by codegen.
type Priority int
`,
		"models/task.go": `package models

import "github.com/alternayte/drel"

type Task struct {
	drel.Model[int]
	title    string   ` + "`db:\"title\"`" + `
	priority Priority ` + "`db:\"priority\"`" + `
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	err = Generate("drel.yaml")
	require.NoError(t, err, "named primitive type with no enum consts must be accepted")

	modelFile := filepath.Join(dir, "models", "task_drel.go")
	assert.FileExists(t, modelFile)

	// Generated code must compile and vet clean.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidyOut, err := tidy.CombinedOutput()
	require.NoError(t, err, "go mod tidy failed: %s", string(tidyOut))

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(buildOut))
}

// TestMigrateNewScanMatchesGenerate is a regression guard for the invariant that
// migrate new and generate resolve package patterns against the same base
// directory (cfgDir, i.e. the directory containing drel.yaml). Previously,
// migrate new called ResolveModuleRoot(cfgDir) which resolved ./models relative
// to the module root when the config lived in a subdirectory, causing it to scan
// a different (or nonexistent) package set than generate. The test creates a
// module where drel.yaml lives in a subdir and models live under that subdir, then
// asserts that ScanPackages(cfgDir) finds the model and
// ScanPackages(ResolveModuleRoot(cfgDir)) does not — proving the two paths
// diverged and that cfgDir is the correct choice.
func TestMigrateNewScanMatchesGenerate(t *testing.T) {
	// Module root has no models directory; models live under config/ subdir.
	dir := setupGenerateModule(t, map[string]string{
		"config/models/user.go": `package models

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"config/drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	cfgPath := filepath.Join(dir, "config", "drel.yaml")
	cfgDir := filepath.Join(dir, "config")

	// Path that Generate and the fixed migrate new both use: cfgDir.
	modelsFromCfgDir, err := ScanPackages([]string{"./models"}, cfgDir)
	require.NoError(t, err, "ScanPackages(cfgDir) must succeed")
	require.Len(t, modelsFromCfgDir, 1, "ScanPackages(cfgDir) must find the User model")
	assert.Equal(t, "User", modelsFromCfgDir[0].Name)

	// Path the old (buggy) migrate new used: resolve to module root.
	// The module root has no ./models directory, so the scan finds nothing (or errors).
	moduleRoot := ResolveModuleRoot(cfgDir)
	assert.NotEqual(t, cfgDir, moduleRoot,
		"test setup requires config to live in a subdir of the module root")

	modelsFromModuleRoot, _ := ScanPackages([]string{"./models"}, moduleRoot)
	assert.Empty(t, modelsFromModuleRoot,
		"ScanPackages(moduleRoot) must find no models — the old migrate-new path was wrong")

	// Prove Generate (which uses cfgDir) succeeds for the same config.
	require.NoError(t, Generate(cfgPath), "Generate must succeed with cfgDir-rooted scan")
	assert.FileExists(t, filepath.Join(cfgDir, "models", "user_drel.go"),
		"Generate must write the model file next to the source package")
}

// TestGenerate_JSONModelCompiles is a regression guard for the JSON/array codegen
// path. It runs the full codegen pipeline on a model with []string and
// map[string]string fields and then runs `go build ./...` on the output to prove
// the generated diff code compiles (slices and maps are not !=-comparable in Go,
// so the emitter must use slices.Equal / maps.Equal instead of !=).
func TestGenerate_JSONModelCompiles(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/model.go": `package models

import "github.com/alternayte/drel"

type Doc struct {
	drel.Model[int]
	Tags []string          ` + "`db:\"tags\"`" + `
	Meta map[string]string ` + "`db:\"meta\"`" + `
}
`,
		"drel.yaml": "packages:\n  - ./models\noutput:\n  db: ./db/drel_gen.go\ndialect: postgres\n",
	})

	require.NoError(t, Generate(filepath.Join(dir, "drel.yaml")))

	// The generated package (model + drel_gen.go) must compile — this is the
	// regression guard for the "slices/maps are not !=-comparable" finding.
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "generated code failed to compile:\n%s", out)
}

// TestGenerate_JSONNonComparableSliceCompiles pins issue #14. A JSON column
// whose Go type is an unnamed slice of a struct that is not comparable cannot
// use slices.Equal, so the generated diff must fall back to drel.JSONEqual.
// Before the fix the generator reported no error and `go build` failed in the
// generated file.
func TestGenerate_JSONNonComparableSliceCompiles(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/summary.go": `package models

import "github.com/alternayte/drel"

type Claim struct {
	Text    string  ` + "`json:\"text\"`" + `
	ItemIDs []int64 ` + "`json:\"item_ids\"`" + `
}

type Summary struct {
	drel.Model[int64]
	Claims []Claim ` + "`db:\"claims\"`" + `
}
`,
		"drel.yaml": "packages:\n  - ./models\noutput:\n  db: ./db/drel_gen.go\ndialect: postgres\n",
	})

	require.NoError(t, Generate(filepath.Join(dir, "drel.yaml")))

	generated, err := os.ReadFile(filepath.Join(dir, "models", "summary_drel.go"))
	require.NoError(t, err)
	assert.Contains(t, string(generated), "!drel.JSONEqual(p.Claims, s.Claims)")
	assert.NotContains(t, string(generated), "slices.Equal")

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "generated code failed to compile:\n%s", out)
}

// TestGenerate_ModelPackageUsesItsOwnGeneratedSymbols pins issue #10. A module
// owns its models, so the slice code that uses the repositories lives in the
// model package. That code names the generated symbols (XMeta, the column set,
// XRepository, TxXRepository) before generation has run, and generation removes
// the *_drel.go file that declares them before it scans.
func TestGenerate_ModelPackageUsesItsOwnGeneratedSymbols(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/widget.go": `package models

import "github.com/alternayte/drel"

type Widget struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"models/service.go": `package models

import (
	"context"

	"github.com/alternayte/drel"
)

func findWidget(ctx context.Context, id int) (*Widget, error) {
	return drel.NewTxRepository(drel.MustFromContext(ctx), WidgetMeta).Find(ctx, id)
}

var _ = Widgets.CreatedAt.Asc()
var _ WidgetRepository
var _ TxWidgetRepository
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	// First generation: service.go names symbols that do not exist yet.
	require.NoError(t, Generate("drel.yaml"))

	// A model change must regenerate while service.go still names the old
	// generated symbols.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "models", "widget.go"), []byte(`package models

import "github.com/alternayte/drel"

type Widget struct {
	drel.Model[int]
	title string `+"`db:\"title\"`"+`
}
`), 0644))
	require.NoError(t, Generate("drel.yaml"))

	content, err := os.ReadFile(filepath.Join(dir, "models", "widget_drel.go"))
	require.NoError(t, err)
	assert.Contains(t, string(content), `"title"`)
	assert.NotContains(t, string(content), `"name"`)

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(out))
}

// TestGenerate_RejectsUndefinedNonGeneratedName proves the tolerance in
// TestGenerate_ModelPackageUsesItsOwnGeneratedSymbols is narrow: a hand-written
// reference to a name generation will not emit still fails the scan.
func TestGenerate_RejectsUndefinedNonGeneratedName(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/widget.go": `package models

import "github.com/alternayte/drel"

type Widget struct {
	drel.Model[int]
	name string ` + "`db:\"name\"`" + `
}
`,
		"models/service.go": `package models

func findWidget() {
	_ = WidgteMeta
}
`,
		"drel.yaml": `packages:
  - ./models
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undefined: WidgteMeta")
	assert.NoFileExists(t, filepath.Join(dir, "models", "widget_drel.go"))
}

// TestGenerate_CrossModuleGeneratedSymbols pins issue #13. A pipeline slice
// reads another slice's rows in one transaction, so a hand-written file in one
// scanned package names the generated symbols of another scanned package. The
// scan must accept them exactly as it accepts a package's own symbols, and
// still fail on any other undefined name.
func TestGenerate_CrossModuleGeneratedSymbols(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"internal/ingest/item.go": `package ingest

import "github.com/alternayte/drel"

type Item struct {
	drel.Model[int64]
	Status string ` + "`db:\"status\"`" + `
}
`,
		"internal/stories/story.go": `package stories

import "github.com/alternayte/drel"

type Story struct {
	drel.Model[int64]
	Title string ` + "`db:\"title\"`" + `
}
`,
		"internal/stories/filter.go": `package stories

import (
	"context"

	"testmod/internal/ingest"

	"github.com/alternayte/drel"
)

func findItems(ctx context.Context) ([]*ingest.Item, error) {
	repo := drel.NewTxRepository(drel.MustFromContext(ctx), ingest.ItemMeta)
	return repo.Where(ingest.Items.Status.Eq("new")).All(ctx)
}
`,
		// An explicit import alias must resolve too: types.Imports reports the
		// declared package name, not the alias, so this exercises the syntax pass.
		"internal/stories/alias.go": `package stories

import ig "testmod/internal/ingest"

var _ = ig.ItemMeta
var _ ig.Item
`,
		"drel.yaml": `modules:
  - name: ingest
    packages: [./internal/ingest]
  - name: stories
    packages: [./internal/stories]
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, Generate("drel.yaml"))
	assert.FileExists(t, filepath.Join(dir, "internal", "ingest", "item_drel.go"))
	assert.FileExists(t, filepath.Join(dir, "internal", "stories", "story_drel.go"))

	// A model change must regenerate while stories still names ingest's
	// generated symbols.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "ingest", "item.go"), []byte(`package ingest

import "github.com/alternayte/drel"

type Item struct {
	drel.Model[int64]
	Status string `+"`db:\"status\"`"+`
	Note   string `+"`db:\"note\"`"+`
}
`), 0644))
	require.NoError(t, Generate("drel.yaml"))

	content, err := os.ReadFile(filepath.Join(dir, "internal", "ingest", "item_drel.go"))
	require.NoError(t, err)
	assert.Contains(t, string(content), `"note"`)
	assert.Contains(t, string(content), `"status"`)

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(out))
}

// TestGenerate_RejectsUndefinedCrossModuleName proves the cross-module
// tolerance is narrow: a qualified name that generation will not emit fails.
func TestGenerate_RejectsUndefinedCrossModuleName(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"internal/ingest/item.go": `package ingest

import "github.com/alternayte/drel"

type Item struct {
	drel.Model[int64]
	Status string ` + "`db:\"status\"`" + `
}
`,
		"internal/stories/filter.go": `package stories

import "testmod/internal/ingest"

func findItems() {
	_ = ingest.BogusMeta
}
`,
		"drel.yaml": `modules:
  - name: ingest
    packages: [./internal/ingest]
  - name: stories
    packages: [./internal/stories]
output:
  db: ./db/drel_gen.go
`,
	})

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))

	err = Generate("drel.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undefined: ingest.BogusMeta")
	assert.NoFileExists(t, filepath.Join(dir, "internal", "ingest", "item_drel.go"))
}
