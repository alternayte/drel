package dialect_test

import (
	"testing"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dialect"
	"github.com/stretchr/testify/assert"
)

// fakeDialect is a minimal Dialect implementation used to assert the
// AdvisoryLockSQL contract shape without depending on a concrete dialect.
type fakeDialect struct{}

func (fakeDialect) Name() string                              { return "fake" }
func (fakeDialect) SupportsReturning() bool                   { return false }
func (fakeDialect) UsesQuestionPlaceholders() bool            { return false }
func (fakeDialect) Now() string                               { return "" }
func (fakeDialect) Explain(q string) (string, bool)           { return "", false }
func (fakeDialect) BuildSelect(ast.SelectNode) dialect.Result { return dialect.Result{} }
func (fakeDialect) BuildInsert(string, []string, []any, []string) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) BuildUpdate(string, []dialect.ColumnValue, []string, []any) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) BuildDelete(string, []string, []any) dialect.Result     { return dialect.Result{} }
func (fakeDialect) BuildSoftDelete(string, []string, []any) dialect.Result { return dialect.Result{} }
func (fakeDialect) BuildUpdateVersioned(string, []dialect.ColumnValue, []string, []any, string, int) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) BuildDeleteVersioned(string, []string, []any, string, int) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) BuildSoftDeleteVersioned(string, []string, []any, string, int) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) BuildBulkInsert(string, []string, [][]any) dialect.Result { return dialect.Result{} }
func (fakeDialect) BuildBulkUpdate(string, []dialect.ColumnValue, *ast.WhereClause) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) BuildBulkDelete(string, *ast.WhereClause) dialect.Result { return dialect.Result{} }
func (fakeDialect) BuildBulkSoftDelete(string, *ast.WhereClause) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) BuildBulkUpsert(string, []string, [][]any, []string, []string, bool) dialect.Result {
	return dialect.Result{}
}
func (fakeDialect) AdvisoryLockSQL(int64, dialect.AdvisoryLockMode) (dialect.Result, bool) {
	return dialect.Result{}, false
}

// Compile-time assertion that the contract is satisfiable.
var _ dialect.Dialect = fakeDialect{}

func TestAdvisoryLockMode_Values(t *testing.T) {
	// The two modes are distinct so the dialect can pick blocking vs non-blocking SQL.
	assert.NotEqual(t, dialect.AdvisoryLockBlocking, dialect.AdvisoryLockTry)
}
