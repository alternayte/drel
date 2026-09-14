package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runGenerateIn runs the pipeline with dir as the working directory and builds
// the result, so a generated file that does not compile fails the test.
func runGenerateIn(t *testing.T, dir string) {
	t.Helper()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(dir))
	require.NoError(t, Generate("drel.yaml"))
}

func buildIn(t *testing.T, dir string) {
	t.Helper()
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	out, err := tidy.CombinedOutput()
	require.NoError(t, err, "go mod tidy failed: %s", string(out))

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	out, err = build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", string(out))
}

// Issue 2: an application with no slice must be able to hold a drel.yaml.
func TestGenerate_EmptyModuleList(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "drel.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("modules: []\noutput:\n  db: ./db/drel_gen.go\n"), 0644))

	cfg, err := LoadConfig(cfgPath)
	require.NoError(t, err)
	assert.Empty(t, cfg.ModuleList())

	dir := setupGenerateModule(t, map[string]string{
		"drel.yaml": "modules: []\noutput:\n  db: ./db/drel_gen.go\n",
	})
	runGenerateIn(t, dir)

	dbFile := filepath.Join(dir, "db", "drel_gen.go")
	assert.FileExists(t, dbFile)
	buildIn(t, dir)
}

// Issue 7: a time.Time model field must map to a timestamp column, not jsonb.
func TestGenerate_TimeFieldIsNotJSON(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/run.go": `package models

import (
	"time"

	"github.com/alternayte/drel"
)

type Run struct {
	drel.Model[int]
	startedAt *time.Time ` + "`db:\"started_at\"`" + `
	endedAt   time.Time  ` + "`db:\"ended_at\"`" + `
}
`,
		"drel.yaml": "packages:\n  - ./models\noutput:\n  db: ./db/drel_gen.go\n",
	})
	runGenerateIn(t, dir)

	content, err := os.ReadFile(filepath.Join(dir, "models", "run_drel.go"))
	require.NoError(t, err)
	got := string(content)
	assert.NotContains(t, got, "drel.JSON[")
	assert.Contains(t, strings.Join(strings.Fields(got), " "), "StartedAt drel.ComparableColumn[*time.Time]")
	assert.Contains(t, strings.Join(strings.Fields(got), " "), "EndedAt drel.TimeColumn")
	buildIn(t, dir)
}

// Issue 7b: the DDL for a time field is a timestamp column.
func TestSchema_TimeFieldColumnType(t *testing.T) {
	assert.Equal(t, "timestamptz", GoTypeToSQL("time.Time", "postgres"))
	assert.Equal(t, "timestamptz", GoTypeToSQL("*time.Time", "postgres"))
}

// Issue 8: two models of one package that share an enum type must compile.
func TestGenerate_SharedEnumAcrossModels(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/quest.go": `package models

import "github.com/alternayte/drel"

type QuestState string

const (
	QuestStateDraft  QuestState = "draft"
	QuestStateActive QuestState = "active"
)

type Quest struct {
	drel.Model[int]
	state QuestState ` + "`db:\"state\"`" + `
}

type QuestEvent struct {
	drel.Model[int]
	toState QuestState ` + "`db:\"to_state\"`" + `
}
`,
		"drel.yaml": "packages:\n  - ./models\noutput:\n  db: ./db/drel_gen.go\n",
	})
	runGenerateIn(t, dir)

	quest, err := os.ReadFile(filepath.Join(dir, "models", "quest_drel.go"))
	require.NoError(t, err)
	event, err := os.ReadFile(filepath.Join(dir, "models", "questevent_drel.go"))
	require.NoError(t, err)
	total := strings.Count(string(quest), "func (r QuestState) IsValid()") +
		strings.Count(string(event), "func (r QuestState) IsValid()")
	assert.Equal(t, 1, total, "QuestState.IsValid must be declared exactly one time")
	buildIn(t, dir)
}

// Issue 10: a slice of a struct declared in the model package must generate.
func TestGenerate_SliceOfSamePackageStruct(t *testing.T) {
	dir := setupGenerateModule(t, map[string]string{
		"models/result.go": `package models

import "github.com/alternayte/drel"

type Fact struct {
	Key   string ` + "`json:\"key\"`" + `
	Value string ` + "`json:\"value\"`" + `
}

type Result struct {
	drel.Model[int]
	facts []Fact ` + "`db:\"facts\"`" + `
}
`,
		"drel.yaml": "packages:\n  - ./models\noutput:\n  db: ./db/drel_gen.go\n",
	})
	runGenerateIn(t, dir)

	content, err := os.ReadFile(filepath.Join(dir, "models", "result_drel.go"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "drel.JSON[[]Fact]")
	buildIn(t, dir)
}
