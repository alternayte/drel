//go:build integration

package drel_test

import (
	"context"
	"go/format"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/dreltest"
	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/testmodels"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The composite-key tables are created from the generated DDL, not from
// hand-written SQL. The schema emitter and the code emitter therefore both run
// against a real database in these tests.
var (
	compositeModelsOnce sync.Once
	compositeModels     []codegen.ModelInfo
	compositeScanErr    error
)

// compositeSchema returns the generated DDL of every composite-key test model.
func compositeSchema(t *testing.T, dialect string) string {
	t.Helper()
	compositeModelsOnce.Do(func() {
		models, err := codegen.ScanPackages([]string{"./internal/testmodels"}, ".")
		if err != nil {
			compositeScanErr = err
			return
		}
		for _, m := range models {
			if strings.Contains(m.Name, "OrderLine") || m.Name == "TenantDoc" {
				compositeModels = append(compositeModels, m)
			}
		}
	})
	require.NoError(t, compositeScanErr, "scan the composite-key test models")
	require.Len(t, compositeModels, 5, "every composite-key test model must be scanned")
	for _, m := range compositeModels {
		require.Len(t, m.Key, 2, "model %s must have a two-column key", m.Name)
	}
	return codegen.GenerateSchema(compositeModels, dialect)
}

// createCompositeTables applies the generated DDL statement by statement.
func createCompositeTables(t *testing.T, engine *drel.Engine, dialect string) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range strings.Split(compositeSchema(t, dialect), ";") {
		if strings.TrimSpace(stripSQLComments(stmt)) == "" {
			continue
		}
		_, err := engine.Exec(ctx, stmt)
		require.NoError(t, err, "the generated DDL must apply: %s", stmt)
	}
}

// stripSQLComments removes the comment lines of a generated schema.
func stripSQLComments(sql string) string {
	var keep []string
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// ph returns the placeholder for the first bind parameter of a dialect.
func ph(dialect string, n int) string {
	if dialect == "sqlite" {
		return "?"
	}
	return "$" + strconv.Itoa(n)
}

// compositeIntCount counts the rows of a table that match both key columns.
func compositeIntCount(t *testing.T, engine *drel.Engine, dialect, table, extra string, orderID, lineNo int) int {
	t.Helper()
	sql := "SELECT count(*) FROM " + table + " WHERE order_id = " + ph(dialect, 1) +
		" AND line_no = " + ph(dialect, 2) + extra
	var n int
	require.NoError(t, engine.QueryRow(context.Background(), sql, orderID, lineNo).Scan(&n))
	return n
}

// compositeKeyPlainPaths proves insert, find, update and delete with a
// two-column key whose second column is a named type over int.
func compositeKeyPlainPaths(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.OrderLineMeta)
	key := testmodels.OrderLineKey{OrderID: 3, LineNo: 1}
	sibling := testmodels.OrderLineKey{OrderID: 3, LineNo: 2}

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.OrderLineMeta)
		a := &testmodels.OrderLine{Qty: 2}
		a.SetID(key)
		b := &testmodels.OrderLine{Qty: 5}
		b.SetID(sibling)
		r.Add(a)
		r.Add(b)
		return nil
	}))

	// Find must round-trip the key through the driver unchanged. The named
	// LineNo column comes back as int64 from both drivers.
	got, err := repo.Find(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, key, got.ID(), "a composite key must round-trip through the driver")
	assert.Equal(t, testmodels.LineNo(1), got.ID().LineNo)
	assert.Equal(t, 2, got.Qty)

	// UPDATE must match on both key columns.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		l, err := drel.NewTxRepository(tx, testmodels.OrderLineMeta).Find(ctx, key)
		if err != nil {
			return err
		}
		l.Qty = 9
		return nil
	}))

	got, err = repo.Find(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, 9, got.Qty, "the UPDATE must match on both key columns")
	other, err := repo.Find(ctx, sibling)
	require.NoError(t, err)
	assert.Equal(t, 5, other.Qty, "the UPDATE must not have matched on order_id alone")

	// DELETE must match on both key columns.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.OrderLineMeta)
		l, err := r.Find(ctx, key)
		if err != nil {
			return err
		}
		return r.Remove(l)
	}))

	_, err = repo.Find(ctx, key)
	assert.ErrorIs(t, err, drel.ErrNotFound, "the deleted row must be gone")
	survivor, err := repo.Find(ctx, sibling)
	require.NoError(t, err)
	assert.Equal(t, 5, survivor.Qty, "the DELETE must not have matched on order_id alone")
}

// compositeKeySoftDelete proves the soft-delete path with a two-column key.
func compositeKeySoftDelete(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.SoftOrderLineMeta)
	key := testmodels.OrderLineKey{OrderID: 7, LineNo: 1}
	sibling := testmodels.OrderLineKey{OrderID: 7, LineNo: 2}

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.SoftOrderLineMeta)
		a := &testmodels.SoftOrderLine{Qty: 1}
		a.SetID(key)
		b := &testmodels.SoftOrderLine{Qty: 4}
		b.SetID(sibling)
		r.Add(a)
		r.Add(b)
		return nil
	}))

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.SoftOrderLineMeta)
		l, err := r.Find(ctx, key)
		if err != nil {
			return err
		}
		return r.Remove(l)
	}))

	_, err := repo.Find(ctx, key)
	assert.ErrorIs(t, err, drel.ErrNotFound, "the soft-deleted row must be filtered out")
	assert.Equal(t, 1,
		compositeIntCount(t, engine, dialect, "soft_order_lines", " AND deleted_at IS NOT NULL", 7, 1),
		"the soft delete must have stamped deleted_at on the matched row")
	assert.Equal(t, 1,
		compositeIntCount(t, engine, dialect, "soft_order_lines", " AND deleted_at IS NULL", 7, 2),
		"the soft delete must not have matched on order_id alone")
	kept, err := repo.Find(ctx, sibling)
	require.NoError(t, err)
	assert.Equal(t, 4, kept.Qty)
}

// compositeKeyVersioned proves the versioned update and the versioned hard
// delete with a two-column key.
func compositeKeyVersioned(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.VersionedOrderLineMeta)
	key := testmodels.OrderLineKey{OrderID: 5, LineNo: 1}
	sibling := testmodels.OrderLineKey{OrderID: 5, LineNo: 2}

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.VersionedOrderLineMeta)
		a := &testmodels.VersionedOrderLine{Qty: 1}
		a.SetID(key)
		b := &testmodels.VersionedOrderLine{Qty: 2}
		b.SetID(sibling)
		r.Add(a)
		r.Add(b)
		return nil
	}))

	// BuildUpdateVersioned.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		l, err := drel.NewTxRepository(tx, testmodels.VersionedOrderLineMeta).Find(ctx, key)
		if err != nil {
			return err
		}
		l.Qty = 11
		return nil
	}))

	got, err := repo.Find(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, 11, got.Qty)
	assert.Equal(t, 2, got.Version(), "the versioned update must bump the version of the matched row")
	other, err := repo.Find(ctx, sibling)
	require.NoError(t, err)
	assert.Equal(t, 2, other.Qty)
	assert.Equal(t, 1, other.Version(), "the versioned update must not have matched on order_id alone")

	// A stale version must conflict, which proves the version predicate is
	// combined with both key columns rather than replacing them.
	err = engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		l, err := drel.NewTxRepository(tx, testmodels.VersionedOrderLineMeta).Find(ctx, key)
		if err != nil {
			return err
		}
		l.Qty = 12
		*l.VersionPtr() = 99
		return nil
	})
	assert.ErrorIs(t, err, drel.ErrConcurrencyConflict)

	// BuildDeleteVersioned.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.VersionedOrderLineMeta)
		l, err := r.Find(ctx, key)
		if err != nil {
			return err
		}
		return r.Remove(l)
	}))

	assert.Equal(t, 0, compositeIntCount(t, engine, dialect, "versioned_order_lines", "", 5, 1))
	assert.Equal(t, 1, compositeIntCount(t, engine, dialect, "versioned_order_lines", "", 5, 2),
		"the versioned delete must not have matched on order_id alone")
}

// compositeKeySoftDeleteVersioned proves the versioned soft delete with a
// two-column key.
func compositeKeySoftDeleteVersioned(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.SoftVersionedOrderLineMeta)
	key := testmodels.OrderLineKey{OrderID: 9, LineNo: 1}
	sibling := testmodels.OrderLineKey{OrderID: 9, LineNo: 2}

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.SoftVersionedOrderLineMeta)
		a := &testmodels.SoftVersionedOrderLine{Qty: 1}
		a.SetID(key)
		b := &testmodels.SoftVersionedOrderLine{Qty: 2}
		b.SetID(sibling)
		r.Add(a)
		r.Add(b)
		return nil
	}))

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.SoftVersionedOrderLineMeta)
		l, err := r.Find(ctx, key)
		if err != nil {
			return err
		}
		return r.Remove(l)
	}))

	_, err := repo.Find(ctx, key)
	assert.ErrorIs(t, err, drel.ErrNotFound)
	assert.Equal(t, 1, compositeIntCount(t, engine, dialect, "soft_versioned_order_lines",
		" AND deleted_at IS NOT NULL AND version = 2", 9, 1),
		"the versioned soft delete must stamp deleted_at and bump the version of the matched row")
	assert.Equal(t, 1, compositeIntCount(t, engine, dialect, "soft_versioned_order_lines",
		" AND deleted_at IS NULL AND version = 1", 9, 2),
		"the versioned soft delete must not have matched on order_id alone")
}

// compositeKeyUUID proves a composite key whose first column is a uuid.
func compositeKeyUUID(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.TenantDocMeta)
	tenant := uuid.MustParse("018f3f1a-0000-7000-8000-000000000001")
	otherTenant := uuid.MustParse("018f3f1a-0000-7000-8000-000000000002")

	key := testmodels.TenantDocKey{TenantID: tenant, DocNo: 1}
	sameTenant := testmodels.TenantDocKey{TenantID: tenant, DocNo: 2}
	sameDocNo := testmodels.TenantDocKey{TenantID: otherTenant, DocNo: 1}

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.TenantDocMeta)
		for _, k := range []testmodels.TenantDocKey{key, sameTenant, sameDocNo} {
			d := &testmodels.TenantDoc{Title: "draft"}
			d.SetID(k)
			r.Add(d)
		}
		return nil
	}))

	got, err := repo.Find(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, key, got.ID(), "a uuid key column must round-trip through the driver")

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		d, err := drel.NewTxRepository(tx, testmodels.TenantDocMeta).Find(ctx, key)
		if err != nil {
			return err
		}
		d.Title = "final"
		return nil
	}))

	got, err = repo.Find(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "final", got.Title)
	for _, k := range []testmodels.TenantDocKey{sameTenant, sameDocNo} {
		sib, err := repo.Find(ctx, k)
		require.NoError(t, err)
		assert.Equal(t, "draft", sib.Title, "the UPDATE must match on both key columns")
	}

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.TenantDocMeta)
		d, err := r.Find(ctx, key)
		if err != nil {
			return err
		}
		return r.Remove(d)
	}))

	_, err = repo.Find(ctx, key)
	assert.ErrorIs(t, err, drel.ErrNotFound)
	for _, k := range []testmodels.TenantDocKey{sameTenant, sameDocNo} {
		_, err := repo.Find(ctx, k)
		require.NoError(t, err, "the DELETE must match on both key columns")
	}
}

// compositeKeyBulkInsert proves the bulk insert path binds every key column.
func compositeKeyBulkInsert(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.OrderLineMeta)

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, testmodels.OrderLineMeta)
		var lines []*testmodels.OrderLine
		for i := 1; i <= 3; i++ {
			l := &testmodels.OrderLine{Qty: i * 10}
			l.SetID(testmodels.OrderLineKey{OrderID: 42, LineNo: testmodels.LineNo(i)})
			lines = append(lines, l)
		}
		n, err := r.BulkInsert(ctx, lines)
		if err != nil {
			return err
		}
		assert.Equal(t, 3, n)
		return nil
	}))

	for i := 1; i <= 3; i++ {
		got, err := repo.Find(ctx, testmodels.OrderLineKey{OrderID: 42, LineNo: testmodels.LineNo(i)})
		require.NoError(t, err, "bulk insert must write every key column")
		assert.Equal(t, i*10, got.Qty)
	}
}

func compositeKeyAllPaths(t *testing.T, engine *drel.Engine, dialect string) {
	createCompositeTables(t, engine, dialect)
	t.Run("plain", func(t *testing.T) { compositeKeyPlainPaths(t, engine, dialect) })
	t.Run("soft_delete", func(t *testing.T) { compositeKeySoftDelete(t, engine, dialect) })
	t.Run("versioned", func(t *testing.T) { compositeKeyVersioned(t, engine, dialect) })
	t.Run("soft_delete_versioned", func(t *testing.T) { compositeKeySoftDeleteVersioned(t, engine, dialect) })
	t.Run("bulk_insert", func(t *testing.T) { compositeKeyBulkInsert(t, engine, dialect) })
	t.Run("uuid_key", func(t *testing.T) { compositeKeyUUID(t, engine, dialect) })
}

// TestCompositeKey_GeneratedFilesAreCurrent proves the checked-in generated
// files of the composite-key test models are the emitter's current output. The
// integration tests above exercise those files, so a stale file would prove
// nothing about the emitter.
func TestCompositeKey_GeneratedFilesAreCurrent(t *testing.T) {
	compositeSchema(t, "postgres") // populates compositeModels
	for _, m := range compositeModels {
		src, err := codegen.EmitModelFileChecked(m)
		require.NoError(t, err)
		want, err := format.Source([]byte(src))
		require.NoError(t, err)
		path := filepath.Join("internal", "testmodels", strings.ToLower(m.Name)+"_drel.go")
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(want), string(got), "%s is stale; regenerate it", path)
	}
}

func TestCompositeKey_Postgres(t *testing.T) {
	compositeKeyAllPaths(t, newTestEngine(t), "postgres")
}

func TestCompositeKey_SQLite(t *testing.T) {
	compositeKeyAllPaths(t, dreltest.NewSQLite(t), "sqlite")
}
