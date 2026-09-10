package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// drelModuleRoot walks up from the test's working directory to the repository
// module root.
func drelModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root")
		}
		dir = parent
	}
}

// buildDrel builds the CLI once for a test and returns the binary path.
func buildDrel(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "drel")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/drel")
	cmd.Dir = drelModuleRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build drel: %v\n%s", err, out)
	}
	return bin
}

// writeTestModule writes a throwaway Go module that depends on the repository
// copy of drel, plus the given files, and returns its directory.
func writeTestModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	goVer := strings.TrimPrefix(runtime.Version(), "go")
	goMod := "module testmod\n\ngo " + goVer +
		"\n\nrequire github.com/alternayte/drel v0.0.0\n\nreplace github.com/alternayte/drel => " + drelModuleRoot(t) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// compositeHasManyModel declares has_many on a composite-key model, which
// ValidateModels rejects. "@" stands in for a back quote so the source can be
// a raw string literal.
var compositeHasManyModel = strings.ReplaceAll(`package models

import "github.com/alternayte/drel"

type OrderLineKey struct {
	OrderID int @db:"order_id"@
	LineNo  int @db:"line_no"@
}

type OrderLine struct {
	drel.Model[OrderLineKey]
	Qty   int    @db:"qty"@
	Notes []*Note @rel:"has_many,fk=order_line_id"@
}

type Note struct {
	drel.Model[int]
	Body        string @db:"body"@
	OrderLineID int    @db:"order_line_id"@
}
`, "@", "`")

// runMigrateNew must validate the models before it builds a schema. Without
// the check a rejected model still reaches BuildSchema and writes DDL that is
// silently wrong.
func TestMigrateNew_RejectsAModelThatFailsValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI and loads packages")
	}
	bin := buildDrel(t)
	dir := writeTestModule(t, map[string]string{
		"models/model.go": compositeHasManyModel,
		"drel.yaml":       "dialect: postgres\npackages:\n  - ./models\noutput:\n  db: ./db/drel_gen.go\n  migrations: ./db/migrations\n",
	})

	cmd := exec.Command(bin, "migrate", "new", "initial")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("expected drel migrate new to fail, but it succeeded:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("expected exit status 1, got %v:\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"drel migrate new:", "composite primary key", "has_many"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stderr missing %q:\n%s", want, got)
		}
	}
	// No migration must be written when validation fails.
	if entries, err := os.ReadDir(filepath.Join(dir, "db", "migrations")); err == nil && len(entries) > 0 {
		t.Fatalf("migration files were written despite the validation failure: %v", entries)
	}
}
