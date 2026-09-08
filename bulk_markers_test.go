package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/internal/testmodels"
	"github.com/google/uuid"
)

// akBulkOrder is a local copy of the app-assigned-PK fixture for bulk tests.
type akBulkOrder struct {
	drel.Model[uuid.UUID]
	Name string
}

func akBulkOrderMeta() drel.ModelMeta[akBulkOrder] {
	return drel.ModelMeta[akBulkOrder]{
		Table:       "ak_orders",
		Columns:     []string{"id", "name", "created_at", "updated_at"},
		PKColumn:    "id",
		KeyStrategy: drel.KeyAppAssigned,
		GenerateKey: drel.UUIDv7Key,
		SetKey:      func(p *akBulkOrder, k any) { p.SetID(k.(uuid.UUID)) },
		KeyIsZero:   func(p *akBulkOrder) bool { var z uuid.UUID; return p.ID() == z },
		PKValue:     func(p *akBulkOrder) any { return p.ID() },
		InsertColumns: func(p *akBulkOrder) ([]string, []any) {
			return []string{"name"}, []any{p.Name}
		},
		ScanReturning: func(p *akBulkOrder, row drel.Row) error {
			idPtr, c, u := p.ScanPtrs()
			return row.Scan(idPtr, c, u)
		},
		ScanGenerated: func(p *akBulkOrder, row drel.Row) error {
			_, c, u := p.ScanPtrs()
			return row.Scan(c, u)
		},
		Scan: func(row drel.Row) (*akBulkOrder, error) {
			p := &akBulkOrder{}
			idPtr, c, u := p.ScanPtrs()
			if err := row.Scan(idPtr, &p.Name, c, u); err != nil {
				return nil, err
			}
			return p, nil
		},
		Snapshot: func(p *akBulkOrder) any { return p.Name },
		Diff: func(p *akBulkOrder, snap any) []drel.FieldChange {
			if p.Name != snap.(string) {
				return []drel.FieldChange{{Column: "name", Value: p.Name}}
			}
			return nil
		},
	}
}

func TestBulkInsert_AppAssignedPK_StampsAndPersists(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if _, err := eng.Exec(ctx, `CREATE TABLE ak_orders (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	repo := drel.NewRepository(eng, akBulkOrderMeta())

	orders := []*akBulkOrder{{Name: "a"}, {Name: "b"}}
	n, err := repo.BulkInsert(ctx, orders)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 inserted, got %d", n)
	}
	// Keys must have been stamped in-memory by the generator.
	for i, o := range orders {
		if o.ID() == (uuid.UUID{}) {
			t.Fatalf("order %d: app-assigned key not stamped", i)
		}
	}
	// And the stamped key must round-trip from the DB.
	got, err := repo.Find(ctx, orders[0].ID())
	if err != nil {
		t.Fatalf("find by stamped key failed: %v", err)
	}
	if got.ID() != orders[0].ID() {
		t.Fatalf("persisted id %v != stamped id %v", got.ID(), orders[0].ID())
	}
}

func TestBulkInsert_AppAssignedPK_ZeroKey_FailsLoudly(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if _, err := eng.Exec(ctx, `CREATE TABLE ak_orders (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	meta := akBulkOrderMeta()
	meta.GenerateKey = nil // no generator and app "forgets" to set the key
	repo := drel.NewRepository(eng, meta)

	n, err := repo.BulkInsert(ctx, []*akBulkOrder{{Name: "no-key"}})
	if err == nil {
		t.Fatal("expected a loud error for a zero app-assigned key, got nil")
	}
	if n != 0 {
		t.Fatalf("expected 0 on error, got %d", n)
	}
	cnt, _ := repo.Count(ctx)
	if cnt != 0 {
		t.Fatalf("expected nothing persisted, got %d", cnt)
	}
}

func TestBulkInsert_Audit_SetsCreatedAndUpdatedBy(t *testing.T) {
	ctx := drel.WithActor(context.Background(), "alice")
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if _, err := eng.Exec(ctx, `CREATE TABLE a_products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		price INTEGER NOT NULL,
		created_by TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	repo := drel.NewRepository(eng, testmodels.AuditProductMeta)

	p := testmodels.NewAuditProduct("widget", 10)
	n, err := repo.BulkInsert(ctx, []*testmodels.AuditProduct{p})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 inserted, got %d", n)
	}
	row := eng.QueryRow(ctx, "SELECT created_by, updated_by FROM a_products WHERE name = 'widget'")
	var createdBy, updatedBy string
	if err := row.Scan(&createdBy, &updatedBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != "alice" || updatedBy != "alice" {
		t.Fatalf("expected created_by/updated_by = alice, got %q/%q", createdBy, updatedBy)
	}
}

func TestBulkInsert_Versioned_InitializesVersionToOne(t *testing.T) {
	ctx := context.Background()
	eng, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if _, err := eng.Exec(ctx, `CREATE TABLE v_products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		price INTEGER NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	repo := drel.NewRepository(eng, testmodels.VersionedProductMeta)

	p := testmodels.NewVersionedProduct("widget", 10)
	n, err := repo.BulkInsert(ctx, []*testmodels.VersionedProduct{p})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 inserted, got %d", n)
	}
	// SetVersion(1) must have run on the in-memory entity.
	if p.Version() != 1 {
		t.Fatalf("expected in-memory version 1, got %d", p.Version())
	}
}
