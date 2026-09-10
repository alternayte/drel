//go:build integration

package drel_test

import (
	"context"
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
	require.Len(t, compositeModels, 6, "every composite-key test model must be scanned")
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

// compositeKeyList renders the keys of a page for a readable failure message.
func compositeKeyList(items []*testmodels.OrderLine) []testmodels.OrderLineKey {
	out := make([]testmodels.OrderLineKey, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID())
	}
	return out
}

// seedPagingLines inserts one line per line number, all under one order.
func seedPagingLines(t *testing.T, engine *drel.Engine, orderID int, qtyOf func(lineNo int) int, n int) {
	t.Helper()
	require.NoError(t, engine.WithTx(context.Background(), func(ctx context.Context) error {
		r := drel.NewTxRepository(drel.MustFromContext(ctx), testmodels.OrderLineMeta)
		for i := 1; i <= n; i++ {
			l := &testmodels.OrderLine{Qty: qtyOf(i)}
			l.SetID(testmodels.OrderLineKey{OrderID: orderID, LineNo: testmodels.LineNo(i)})
			r.Add(l)
		}
		return nil
	}))
}

// compositeKeyKeysetPaging proves that keyset pagination walks a composite-key
// table exactly once, that a cursor survives the gob round trip across a page
// boundary, and that Before returns the previous page.
func compositeKeyKeysetPaging(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.OrderLineMeta)
	const orderID = 300
	const rows = 7
	seedPagingLines(t, engine, orderID, func(lineNo int) int { return lineNo * 10 }, rows)

	// Every key column value must survive the driver and the cursor, so the
	// expected sequence is stated in full rather than counted.
	want := make([]testmodels.OrderLineKey, 0, rows)
	for i := 1; i <= rows; i++ {
		want = append(want, testmodels.OrderLineKey{OrderID: orderID, LineNo: testmodels.LineNo(i)})
	}

	mk := func() *drel.QueryBuilder[testmodels.OrderLine] {
		return repo.Where(testmodels.OrderLines.OrderID.Eq(orderID)).
			OrderBy(testmodels.OrderLines.Qty.Asc()).Take(3)
	}

	var got []testmodels.OrderLineKey
	cursor := ""
	var firstPage, secondPage *drel.CursorPage[testmodels.OrderLine]
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "forward paging must terminate")
		q := mk()
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		if pages == 0 {
			firstPage = page
		} else if pages == 1 {
			secondPage = page
		}
		got = append(got, compositeKeyList(page.Items)...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	assert.Equal(t, want, got, "forward paging must visit every row exactly once, in order")

	// The cursor of the first page carries the key columns of its last row
	// through gob and back through the driver. Replaying it must yield the same
	// page again, which proves the boundary values decoded to the same values
	// they encoded from.
	require.NotNil(t, secondPage)
	replay, err := mk().After(firstPage.NextCursor).Page(ctx)
	require.NoError(t, err)
	assert.Equal(t, compositeKeyList(secondPage.Items), compositeKeyList(replay.Items),
		"a cursor must decode to what it encoded across a page boundary")
	assert.Equal(t, want[3:6], compositeKeyList(replay.Items))

	// Backward paging returns the previous page in natural order.
	require.True(t, secondPage.HasPrev)
	back, err := mk().Before(secondPage.PreviousCursor).Page(ctx)
	require.NoError(t, err)
	assert.Equal(t, want[0:3], compositeKeyList(back.Items),
		"Before must return the previous page in natural order")
}

// compositeKeyKeysetTiebreak proves that every key column, not only the first,
// breaks a tie in the caller's ORDER BY. Every seeded row holds the same qty,
// so the caller's ordering ties across all six rows. The rows span two orders
// of three lines each, so an ordering that appended only order_id would still
// tie within an order: the cursor of the first page would then read
// "qty = 7 AND order_id > 300", which skips the third line of order 300.
func compositeKeyKeysetTiebreak(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.OrderLineMeta)
	const tieQty = 7
	orders := []int{400, 401}
	for _, orderID := range orders {
		seedPagingLines(t, engine, orderID, func(int) int { return tieQty }, 3)
	}

	var want []testmodels.OrderLineKey
	for _, orderID := range orders {
		for i := 1; i <= 3; i++ {
			want = append(want, testmodels.OrderLineKey{OrderID: orderID, LineNo: testmodels.LineNo(i)})
		}
	}

	var got []testmodels.OrderLineKey
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "forward paging must terminate")
		q := repo.Where(testmodels.OrderLines.Qty.Eq(tieQty)).
			Where(testmodels.OrderLines.OrderID.GTE(orders[0])).
			OrderBy(testmodels.OrderLines.Qty.Asc()).Take(2)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		got = append(got, compositeKeyList(page.Items)...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	assert.Equal(t, want, got,
		"a tie on the ordered column must be broken by every key column, with no skip and no repeat")
}

// compositeKeyOffsetPaging proves offset paging and its COUNT over a
// composite-key table.
func compositeKeyOffsetPaging(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.OrderLineMeta)
	const orderID = 500
	seedPagingLines(t, engine, orderID, func(lineNo int) int { return lineNo }, 5)

	mk := func() *drel.QueryBuilder[testmodels.OrderLine] {
		return repo.Where(testmodels.OrderLines.OrderID.Eq(orderID)).
			OrderBy(testmodels.OrderLines.Qty.Asc()).Take(2)
	}

	first, err := mk().PageOffset(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, first.Total, "the COUNT must run over the composite-key table")
	assert.Equal(t, 1, first.Page)
	assert.Equal(t, 3, first.TotalPages)
	assert.True(t, first.HasMore)
	assert.Equal(t, []testmodels.OrderLineKey{
		{OrderID: orderID, LineNo: 1}, {OrderID: orderID, LineNo: 2},
	}, compositeKeyList(first.Items))

	last, err := mk().Skip(4).PageOffset(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, last.Page)
	assert.False(t, last.HasMore)
	assert.Equal(t, []testmodels.OrderLineKey{{OrderID: orderID, LineNo: 5}},
		compositeKeyList(last.Items))
}

// compositeAuditActors reads the audit columns of one row with raw SQL, so a
// broken generated scan cannot mask a broken write.
func compositeAuditActors(t *testing.T, engine *drel.Engine, dialect string, orderID, lineNo int) (string, string) {
	t.Helper()
	sql := "SELECT created_by, updated_by FROM audit_order_lines WHERE order_id = " +
		ph(dialect, 1) + " AND line_no = " + ph(dialect, 2)
	var createdBy, updatedBy string
	require.NoError(t, engine.QueryRow(context.Background(), sql, orderID, lineNo).Scan(&createdBy, &updatedBy))
	return createdBy, updatedBy
}

// compositeKeyAudit proves the audit trait and a two-column key work together.
// The generated column list interleaves the audit columns with the key columns,
// so the insert, the scan and the update must all keep them aligned.
func compositeKeyAudit(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()
	repo := drel.NewRepository(engine, testmodels.AuditOrderLineMeta)
	key := testmodels.OrderLineKey{OrderID: 60, LineNo: 1}
	sibling := testmodels.OrderLineKey{OrderID: 60, LineNo: 2}

	createCtx := drel.WithActor(ctx, "creator")
	require.NoError(t, engine.WithTx(createCtx, func(ctx context.Context) error {
		r := drel.NewTxRepository(drel.MustFromContext(ctx), testmodels.AuditOrderLineMeta)
		for _, k := range []testmodels.OrderLineKey{key, sibling} {
			l := &testmodels.AuditOrderLine{Qty: 1}
			l.SetID(k)
			r.Add(l)
		}
		return nil
	}))

	for _, k := range []testmodels.OrderLineKey{key, sibling} {
		createdBy, updatedBy := compositeAuditActors(t, engine, dialect, k.OrderID, int(k.LineNo))
		assert.Equal(t, "creator", createdBy, "the insert must stamp created_by")
		assert.Equal(t, "creator", updatedBy, "the insert must stamp updated_by")
	}

	got, err := repo.Find(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, key, got.ID(), "the generated scan must keep the key columns aligned with the audit columns")
	assert.Equal(t, "creator", got.CreatedBy())
	assert.Equal(t, 1, got.Qty)

	updateCtx := drel.WithActor(ctx, "editor")
	require.NoError(t, engine.WithTx(updateCtx, func(ctx context.Context) error {
		l, err := drel.NewTxRepository(drel.MustFromContext(ctx), testmodels.AuditOrderLineMeta).Find(ctx, key)
		if err != nil {
			return err
		}
		l.Qty = 42
		return nil
	}))

	createdBy, updatedBy := compositeAuditActors(t, engine, dialect, key.OrderID, int(key.LineNo))
	assert.Equal(t, "creator", createdBy, "the update must not rewrite created_by")
	assert.Equal(t, "editor", updatedBy, "the update must stamp the new actor")

	sibCreatedBy, sibUpdatedBy := compositeAuditActors(t, engine, dialect, sibling.OrderID, int(sibling.LineNo))
	assert.Equal(t, "creator", sibCreatedBy)
	assert.Equal(t, "creator", sibUpdatedBy, "the update must not have matched on order_id alone")

	got, err = repo.Find(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, 42, got.Qty)
	assert.Equal(t, "editor", got.UpdatedBy())
}

func compositeKeyAllPaths(t *testing.T, engine *drel.Engine, dialect string) {
	createCompositeTables(t, engine, dialect)
	t.Run("plain", func(t *testing.T) { compositeKeyPlainPaths(t, engine, dialect) })
	t.Run("soft_delete", func(t *testing.T) { compositeKeySoftDelete(t, engine, dialect) })
	t.Run("versioned", func(t *testing.T) { compositeKeyVersioned(t, engine, dialect) })
	t.Run("soft_delete_versioned", func(t *testing.T) { compositeKeySoftDeleteVersioned(t, engine, dialect) })
	t.Run("bulk_insert", func(t *testing.T) { compositeKeyBulkInsert(t, engine, dialect) })
	t.Run("uuid_key", func(t *testing.T) { compositeKeyUUID(t, engine, dialect) })
	t.Run("audit", func(t *testing.T) { compositeKeyAudit(t, engine, dialect) })
	t.Run("keyset_paging", func(t *testing.T) { compositeKeyKeysetPaging(t, engine, dialect) })
	t.Run("keyset_tiebreak", func(t *testing.T) { compositeKeyKeysetTiebreak(t, engine, dialect) })
	t.Run("offset_paging", func(t *testing.T) { compositeKeyOffsetPaging(t, engine, dialect) })
}

func TestCompositeKey_Postgres(t *testing.T) {
	compositeKeyAllPaths(t, newTestEngine(t), "postgres")
}

func TestCompositeKey_SQLite(t *testing.T) {
	compositeKeyAllPaths(t, dreltest.NewSQLite(t), "sqlite")
}
