package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// @ stands in for a back quote so the source can be a raw string literal.
var crossModuleUserModel = strings.ReplaceAll(`package users

import "github.com/alternayte/drel"

type User struct {
	drel.Model[int]
	Name string @db:"name"@
}
`, "@", "`")

var crossModulePostModel = strings.ReplaceAll(`package posts

import "github.com/alternayte/drel"

type Post struct {
	drel.Model[int]
	Title    string @db:"title"@
	AuthorID int    @db:"author_id,references=users"@
}
`, "@", "`")

var externalRefPostModel = strings.ReplaceAll(`package posts

import "github.com/alternayte/drel"

type Post struct {
	drel.Model[int]
	Title    string @db:"title"@
	TenantID int    @db:"tenant_id,references=tenants"@
}
`, "@", "`")

// A references= tag can name a table another module declares. The merged
// migrations apply in timestamp order, so if the other module has not migrated
// that table yet, this module's migration sorts first and Postgres rejects the
// CREATE. `migrate new` says so, because the failure surfaces at apply time in
// a different file.
func TestMigrateNew_WarnsWhenAReferenceTargetsAnUnmigratedModuleTable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI and loads packages")
	}
	bin := buildDrel(t)
	dir := writeTestModule(t, map[string]string{
		"users/model.go": crossModuleUserModel,
		"posts/model.go": crossModulePostModel,
		"drel.yaml":      "dialect: postgres\nmodules:\n  - name: users\n    packages: [\"./users\"]\n  - name: posts\n    packages: [\"./posts\"]\n",
	})

	// The users module has no migration yet, so the posts migration that names
	// users sorts before the table it references.
	out := runMigrateNewCommand(t, bin, dir, "posts", "init")
	for _, want := range []string{
		`module "posts" references table "users" owned by module "users"`,
		`generate the migration for "users" first`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	// Once the users migration exists, its version sorts before the next posts
	// migration and there is nothing to report.
	out = runMigrateNewCommand(t, bin, dir, "users", "init")
	if strings.Contains(out, "warning:") {
		t.Fatalf("unexpected warning for the module that migrates first:\n%s", out)
	}
	out = runMigrateNewCommand(t, bin, dir, "posts", "update")
	if strings.Contains(out, "warning:") {
		t.Fatalf("unexpected warning once the referenced table is migrated:\n%s", out)
	}
}

// A references= target that no module declares belongs to another system. drel
// cannot order it and must stay silent.
func TestMigrateNew_NoWarningForAnUnmodelledReferenceTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI and loads packages")
	}
	bin := buildDrel(t)
	dir := writeTestModule(t, map[string]string{
		"posts/model.go": externalRefPostModel,
		"drel.yaml":      "dialect: postgres\npackages:\n  - ./posts\noutput:\n  db: ./db/drel_gen.go\n  migrations: ./db/migrations\n",
	})

	out := runMigrateNewCommand(t, bin, dir, "", "init")
	if strings.Contains(out, "warning:") {
		t.Fatalf("unexpected warning for a table no module declares:\n%s", out)
	}
}

// runMigrateNewCommand runs `drel migrate new [--module m] <name>` in dir and
// returns the combined output. A non-zero exit fails the test, because every
// case here writes the migration.
func runMigrateNewCommand(t *testing.T, bin, dir, module, name string) string {
	t.Helper()
	args := []string{"migrate", "new"}
	if module != "" {
		args = append(args, "--module="+module)
	}
	args = append(args, name)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("drel %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
