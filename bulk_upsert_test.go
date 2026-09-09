package drel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/internal/testmodels"
	"github.com/google/uuid"
)

// akUpsertOrder reuses the app-assigned-PK shape from bulk_markers_test.go for
// upsert tests but lives in its own type to avoid table-name collisions.
type akUpsertOrder struct {
	drel.Model[uuid.UUID]
	Name string
}

func akUpsertOrderMeta() drel.ModelMeta[akUpsertOrder] {
	return drel.ModelMeta[akUpsertOrder]{
		Table:       "ak_upsert_orders",
		Columns:     []string{"id", "name", "created_at", "updated_at"},
		PKColumns:   []string{"id"},
		KeyStrategy: drel.KeyAppAssigned,
		GenerateKey: drel.UUIDv7Key,
		SetKey:      func(p *akUpsertOrder, k any) { p.SetID(k.(uuid.UUID)) },
		KeyIsZero:   func(p *akUpsertOrder) bool { var z uuid.UUID; return p.ID() == z },
		PKValue:     func(p *akUpsertOrder) any { return p.ID() },
		InsertColumns: func(p *akUpsertOrder) ([]string, []any) {
			return []string{"name"}, []any{p.Name}
		},
		ScanReturning: func(p *akUpsertOrder, row drel.Row) error {
			idPtr, c, u := p.ScanPtrs()
			return row.Scan(idPtr, c, u)
		},
		ScanGenerated: func(p *akUpsertOrder, row drel.Row) error {
			_, c, u := p.ScanPtrs()
			return row.Scan(c, u)
		},
		Scan: func(row drel.Row) (*akUpsertOrder, error) {
			p := &akUpsertOrder{}
			idPtr, c, u := p.ScanPtrs()
			if err := row.Scan(idPtr, &p.Name, c, u); err != nil {
				return nil, err
			}
			return p, nil
		},
		Snapshot: func(p *akUpsertOrder) any { return p.Name },
		Diff: func(p *akUpsertOrder, snap any) []drel.FieldChange {
			if p.Name != snap.(string) {
				return []drel.FieldChange{{Column: "name", Value: p.Name}}
			}
			return nil
		},
	}
}

// createUpsertOrdersTable creates the ak_upsert_orders table in the test engine.
func createUpsertOrdersTable(t *testing.T, eng *drel.Engine) {
	t.Helper()
	ctx := context.Background()
	if _, err := eng.Exec(ctx, `CREATE TABLE ak_upsert_orders (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
}

// TestBulkUpsert_AppAssignedPK_StampsKey verifies that BulkUpsert, like
// BulkInsert, stamps app-assigned keys via the generator and persists them.
func TestBulkUpsert_AppAssignedPK_StampsKey(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	createUpsertOrdersTable(t, eng)

	repo := drel.NewRepository(eng, akUpsertOrderMeta())

	idCol := drel.NewStringCol("id")
	nameCol := drel.NewStringCol("name")

	orders := []*akUpsertOrder{{Name: "alpha"}, {Name: "beta"}}
	n, err := repo.BulkUpsert(ctx, orders,
		drel.ConflictColumns(idCol),
		drel.UpdateOnConflict(nameCol),
	)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 upserted, got %d", n)
	}
	// Keys must have been stamped in-memory by the generator.
	for i, o := range orders {
		if o.ID() == (uuid.UUID{}) {
			t.Fatalf("order %d: app-assigned key not stamped on upsert", i)
		}
	}
	// Stamped key must round-trip from the DB.
	got, err := repo.Find(ctx, orders[0].ID())
	if err != nil {
		t.Fatalf("find by upserted key failed: %v", err)
	}
	if got.ID() != orders[0].ID() {
		t.Fatalf("persisted id %v != stamped id %v", got.ID(), orders[0].ID())
	}
}

// TestBulkUpsert_AppAssignedPK_ZeroKey_FailsLoudly mirrors the BulkInsert
// equivalent: a zero key with no generator must return a loud error, not a
// silent zero-PK write.
func TestBulkUpsert_AppAssignedPK_ZeroKey_FailsLoudly(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	createUpsertOrdersTable(t, eng)

	meta := akUpsertOrderMeta()
	meta.GenerateKey = nil // no generator; app forgets to set the key
	repo := drel.NewRepository(eng, meta)

	idCol := drel.NewStringCol("id")
	nameCol := drel.NewStringCol("name")

	n, err := repo.BulkUpsert(ctx, []*akUpsertOrder{{Name: "no-key"}},
		drel.ConflictColumns(idCol),
		drel.UpdateOnConflict(nameCol),
	)
	if err == nil {
		t.Fatal("expected a loud error for a zero app-assigned key on upsert, got nil")
	}
	if n != 0 {
		t.Fatalf("expected 0 on error, got %d", n)
	}
	cnt, _ := repo.Count(ctx)
	if cnt != 0 {
		t.Fatalf("expected nothing persisted, got %d", cnt)
	}
}

// TestBulkUpsert_Audit_SetsCreatedAndUpdatedBy verifies that BulkUpsert
// honors the audit marker, stamping created_by and updated_by on insert.
// Uses name as the conflict target (has UNIQUE constraint in this schema).
func TestBulkUpsert_Audit_SetsCreatedAndUpdatedBy(t *testing.T) {
	ctx := drel.WithActor(context.Background(), "bob")
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	// name has a UNIQUE constraint so it can serve as the conflict target.
	if _, err := eng.Exec(ctx, `CREATE TABLE a_products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		price INTEGER NOT NULL,
		created_by TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	repo := drel.NewRepository(eng, testmodels.AuditProductMeta)

	// Conflict on name; update price on conflict.
	nameCol := drel.NewStringCol("name")
	priceCol := drel.NewStringCol("price")

	p := testmodels.NewAuditProduct("gadget", 20)
	n, err := repo.BulkUpsert(ctx, []*testmodels.AuditProduct{p},
		drel.ConflictColumns(nameCol),
		drel.UpdateOnConflict(priceCol),
	)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 upserted, got %d", n)
	}
	row := eng.QueryRow(ctx, "SELECT created_by, updated_by FROM a_products WHERE name = 'gadget'")
	var createdBy, updatedBy string
	if err := row.Scan(&createdBy, &updatedBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != "bob" || updatedBy != "bob" {
		t.Fatalf("expected created_by/updated_by = bob, got %q/%q", createdBy, updatedBy)
	}
}

// TestBulkUpsert_Versioned_InitializesVersionToOne verifies BulkUpsert
// stamps version = 1 on entities on insert (mirroring BulkInsert behavior).
// Uses name as the conflict target (has UNIQUE constraint in this schema).
func TestBulkUpsert_Versioned_InitializesVersionToOne(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	// name has a UNIQUE constraint so it can serve as the conflict target.
	if _, err := eng.Exec(ctx, `CREATE TABLE v_products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		price INTEGER NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	repo := drel.NewRepository(eng, testmodels.VersionedProductMeta)

	nameCol := drel.NewStringCol("name")
	priceCol := drel.NewStringCol("price")

	p := testmodels.NewVersionedProduct("gadget", 20)
	n, err := repo.BulkUpsert(ctx, []*testmodels.VersionedProduct{p},
		drel.ConflictColumns(nameCol),
		drel.UpdateOnConflict(priceCol),
	)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 upserted, got %d", n)
	}
	if p.Version() != 1 {
		t.Fatalf("expected in-memory version 1 after upsert, got %d", p.Version())
	}
}

// TestBulkUpsert_ConflictUpdates verifies the upsert path actually updates a
// conflicting row rather than inserting a duplicate.
func TestBulkUpsert_ConflictUpdates(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	createUpsertOrdersTable(t, eng)

	repo := drel.NewRepository(eng, akUpsertOrderMeta())
	idCol := drel.NewStringCol("id")
	nameCol := drel.NewStringCol("name")

	// Insert initial row.
	initial := &akUpsertOrder{Name: "original"}
	n, err := repo.BulkUpsert(ctx, []*akUpsertOrder{initial},
		drel.ConflictColumns(idCol),
		drel.UpdateOnConflict(nameCol),
	)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 inserted, got %d", n)
	}

	// Upsert the same ID with a new name — should update, not duplicate.
	updated := &akUpsertOrder{}
	updated.SetID(initial.ID())
	updated.Name = "renamed"
	n, err = repo.BulkUpsert(ctx, []*akUpsertOrder{updated},
		drel.ConflictColumns(idCol),
		drel.UpdateOnConflict(nameCol),
	)
	if err != nil {
		t.Fatal(err)
	}
	// SQLite returns 1 for the updated row.
	if n != 1 {
		t.Fatalf("expected 1 affected on update, got %d", n)
	}

	cnt, _ := repo.Count(ctx)
	if cnt != 1 {
		t.Fatalf("expected only 1 row total (update, not duplicate), got %d", cnt)
	}
}

// TestBulkUpsert_ErrorRollsBack_ReturnsZero proves that a constraint violation
// in a single-batch BulkUpsert rolls back the transaction and returns 0.
func TestBulkUpsert_ErrorRollsBack_ReturnsZero(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// name has a UNIQUE constraint; duplicate names in the same non-ON-CONFLICT
	// column will cause the upsert to fail.
	if _, err := eng.Exec(ctx, `CREATE TABLE products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		price INTEGER NOT NULL,
		in_stock BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	repo := drel.NewRepository(eng, testmodels.ProductMeta)

	// Conflict on id (PK); two rows with the same name collide on the UNIQUE(name)
	// constraint (which is NOT the conflict target), so SQLite raises an error.
	idCol := drel.NewStringCol("id")
	priceCol := drel.NewStringCol("price")

	products := []*testmodels.Product{
		{Name: "dup", Price: 1},
		{Name: "dup", Price: 2},
	}
	n, err := repo.BulkUpsert(ctx, products,
		drel.ConflictColumns(idCol),
		drel.UpdateOnConflict(priceCol),
	)
	if err == nil {
		t.Fatal("expected a unique-violation error, got nil")
	}
	if n != 0 {
		t.Fatalf("expected 0 on rollback, got %d", n)
	}
	cnt, _ := repo.Count(ctx)
	if cnt != 0 {
		t.Fatalf("expected 0 rows persisted after rollback, got %d", cnt)
	}
}

// TestBulkUpsert_MultiBatch_ErrorRollsBack_ReturnsZero proves the multi-batch
// rollback fix for BulkUpsert: when batch 1 succeeds (incrementing the running
// total) and batch 2 fails, the returned count must be 0, not the partial batch-1
// total. This exercises the same return-0-on-exec-error path as BulkInsert.
//
// ProductMeta has 3 insert columns, so safeBatchSize(3) = 1000. We upsert 1001
// rows where the first 1000 have unique names (batch 1 succeeds on the PK
// conflict target) and row 1001 duplicates row 0's name (batch 2 fails on the
// UNIQUE(name) constraint, which is not the conflict target).
func TestBulkUpsert_MultiBatch_ErrorRollsBack_ReturnsZero(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if _, err := eng.Exec(ctx, `CREATE TABLE products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		price INTEGER NOT NULL,
		in_stock BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	repo := drel.NewRepository(eng, testmodels.ProductMeta)

	// Conflict on id (PK); UNIQUE(name) is a separate constraint — duplicating a
	// name within a batch raises an error without the ON CONFLICT DO UPDATE firing.
	idCol := drel.NewStringCol("id")
	priceCol := drel.NewStringCol("price")

	const firstBatch = 1000
	products := make([]*testmodels.Product, firstBatch+1)
	for i := 0; i < firstBatch; i++ {
		products[i] = &testmodels.Product{Name: fmt.Sprintf("item-%04d", i), Price: i + 1}
	}
	// Row 1001 collides with row 0 on the UNIQUE(name) constraint (not the PK
	// conflict target), so the second batch fails.
	products[firstBatch] = &testmodels.Product{Name: "item-0000", Price: 9999}

	n, err := repo.BulkUpsert(ctx, products,
		drel.ConflictColumns(idCol),
		drel.UpdateOnConflict(priceCol),
	)
	if err == nil {
		t.Fatal("expected a unique-violation error on the second batch, got nil")
	}
	// The fix: return 0, not the partial 1000 accumulated before the failure.
	if n != 0 {
		t.Fatalf("expected 0 on rollback (got %d): partial count leaked despite full-tx rollback", n)
	}
	cnt, _ := repo.Count(ctx)
	if cnt != 0 {
		t.Fatalf("expected 0 rows persisted after rollback, got %d", cnt)
	}
}

// TestBulkUpsert_RequiresConflictColumns verifies that calling BulkUpsert
// without ConflictColumns returns a loud error (no silent no-op).
func TestBulkUpsert_RequiresConflictColumns(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	createUpsertOrdersTable(t, eng)
	repo := drel.NewRepository(eng, akUpsertOrderMeta())

	nameCol := drel.NewStringCol("name")
	_, err = repo.BulkUpsert(ctx, []*akUpsertOrder{{Name: "x"}},
		drel.UpdateOnConflict(nameCol), // ConflictColumns intentionally omitted
	)
	if err == nil {
		t.Fatal("expected error when ConflictColumns is omitted, got nil")
	}
}

// TestBulkUpsert_RequiresUpdateOnConflict verifies that calling BulkUpsert
// without UpdateOnConflict returns a loud error.
func TestBulkUpsert_RequiresUpdateOnConflict(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	createUpsertOrdersTable(t, eng)
	repo := drel.NewRepository(eng, akUpsertOrderMeta())

	idCol := drel.NewStringCol("id")
	_, err = repo.BulkUpsert(ctx, []*akUpsertOrder{{Name: "x"}},
		drel.ConflictColumns(idCol), // UpdateOnConflict intentionally omitted
	)
	if err == nil {
		t.Fatal("expected error when UpdateOnConflict is omitted, got nil")
	}
}
