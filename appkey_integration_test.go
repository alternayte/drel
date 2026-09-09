//go:build integration

package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

type akOrder struct {
	drel.Model[uuid.UUID]
	Name string `db:"name"`
}

func akOrderMeta() drel.ModelMeta[akOrder] {
	return drel.ModelMeta[akOrder]{
		Table:       "ak_orders",
		Columns:     []string{"id", "name", "created_at", "updated_at"},
		PKColumns:   []string{"id"},
		KeyStrategy: drel.KeyAppAssigned,
		GenerateKey: drel.UUIDv7Key,
		SetKey:      func(p *akOrder, k any) { p.SetID(k.(uuid.UUID)) },
		KeyIsZero:   func(p *akOrder) bool { var z uuid.UUID; return p.ID() == z },
		PKValue:     func(p *akOrder) any { return p.ID() },
		InsertColumns: func(p *akOrder) ([]string, []any) {
			return []string{"name"}, []any{p.Name}
		},
		ScanReturning: func(p *akOrder, row drel.Row) error {
			idPtr, c, u := p.ScanPtrs()
			return row.Scan(idPtr, c, u)
		},
		ScanGenerated: func(p *akOrder, row drel.Row) error {
			_, c, u := p.ScanPtrs()
			return row.Scan(c, u)
		},
		Scan: func(row drel.Row) (*akOrder, error) {
			p := &akOrder{}
			idPtr, c, u := p.ScanPtrs()
			if err := row.Scan(idPtr, &p.Name, c, u); err != nil {
				return nil, err
			}
			return p, nil
		},
		Snapshot: func(p *akOrder) any { return p.Name },
		Diff: func(p *akOrder, snap any) []drel.FieldChange {
			if p.Name != snap.(string) {
				return []drel.FieldChange{{Column: "name", Value: p.Name}}
			}
			return nil
		},
	}
}

func TestAppAssignedInsert_Postgres_RoundTrip(t *testing.T) {
	ctx := context.Background()
	eng := setupTestDB(t)

	if _, err := eng.Exec(ctx, `CREATE TABLE ak_orders (
		id uuid PRIMARY KEY,
		name text NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatal(err)
	}

	meta := akOrderMeta()
	var id uuid.UUID
	err := eng.Transaction(ctx, func(tx *drel.Tx) error {
		o := &akOrder{Name: "pg-widget"}
		drel.NewTxRepository(tx, meta).Add(o)
		id = o.ID()
		return tx.SaveChanges(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == (uuid.UUID{}) {
		t.Fatal("id not stamped")
	}

	got, err := drel.NewRepository(eng, meta).Find(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != id {
		t.Fatalf("pgx round-trip mismatch: persisted %v != %v", got.ID(), id)
	}
	if got.CreatedAt().IsZero() {
		t.Fatal("created_at not populated")
	}
}
