package drel

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type akOrder struct {
	Model[uuid.UUID]
	Name string `db:"name"`
}

func akOrderMeta() ModelMeta[akOrder] {
	return ModelMeta[akOrder]{
		Table:       "ak_orders",
		Columns:     []string{"id", "name", "created_at", "updated_at"},
		PKColumns:   []string{"id"},
		KeyStrategy: KeyAppAssigned,
		GenerateKey: UUIDv7Key,
		SetKey:      func(p *akOrder, k any) { p.SetID(k.(uuid.UUID)) },
		KeyIsZero:   func(p *akOrder) bool { var z uuid.UUID; return p.ID() == z },
		PKValue:     func(p *akOrder) any { return p.ID() },
		InsertColumns: func(p *akOrder) ([]string, []any) {
			return []string{"name"}, []any{p.Name}
		},
		ScanReturning: func(p *akOrder, row Row) error {
			idPtr, c, u := p.ScanPtrs()
			return row.Scan(idPtr, c, u)
		},
		ScanGenerated: func(p *akOrder, row Row) error {
			_, c, u := p.ScanPtrs()
			return row.Scan(c, u)
		},
		Scan: func(row Row) (*akOrder, error) {
			p := &akOrder{}
			idPtr, c, u := p.ScanPtrs()
			if err := row.Scan(idPtr, &p.Name, c, u); err != nil {
				return nil, err
			}
			return p, nil
		},
		Snapshot: func(p *akOrder) any { return p.Name },
		Diff: func(p *akOrder, snap any) []FieldChange {
			if p.Name != snap.(string) {
				return []FieldChange{{Column: "name", Value: p.Name}}
			}
			return nil
		},
	}
}

func TestAppAssignedInsert_ZeroKey_FailsLoudly(t *testing.T) {
	ctx := context.Background()
	eng, err := NewEngine(":memory:")
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

	meta := akOrderMeta()
	meta.GenerateKey = nil // no generator: app is expected to supply the key, but we "forget"

	err = eng.Transaction(ctx, func(tx *Tx) error {
		o := &akOrder{Name: "no-key"} // key never set, stays zero
		NewTxRepository(tx, meta).Add(o)
		return tx.SaveChanges(ctx)
	})
	if err == nil {
		t.Fatal("expected a loud error for a zero app-assigned key, got nil")
	}
}

func TestAppAssignedInsert_SQLite_RoundTrip(t *testing.T) {
	ctx := context.Background()
	eng, err := NewEngine(":memory:")
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

	meta := akOrderMeta()
	var inserted akOrder
	err = eng.Transaction(ctx, func(tx *Tx) error {
		repo := NewTxRepository(tx, meta)
		o := &akOrder{Name: "widget"}
		repo.Add(o)
		if o.ID() == (uuid.UUID{}) {
			t.Fatal("id not stamped at Add")
		}
		inserted = *o
		return tx.SaveChanges(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := NewRepository(eng, meta).Find(ctx, inserted.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != inserted.ID() {
		t.Fatalf("persisted id %v != stamped id %v", got.ID(), inserted.ID())
	}
	if got.CreatedAt().IsZero() {
		t.Fatal("created_at not populated")
	}
	if got.UpdatedAt().IsZero() {
		t.Fatal("updated_at not populated")
	}
}
