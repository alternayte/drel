package testmodels

import (
	"github.com/alternayte/drel"
)

type VersionedProduct struct {
	drel.Model[int]
	drel.Versioned
	name  string
	price int
}

func NewVersionedProduct(name string, price int) *VersionedProduct {
	return &VersionedProduct{name: name, price: price}
}

func (p *VersionedProduct) Name() string     { return p.name }
func (p *VersionedProduct) Price() int       { return p.price }
func (p *VersionedProduct) SetName(n string) { p.name = n }
func (p *VersionedProduct) SetPrice(pr int)  { p.price = pr }

type vpSnapshot struct {
	name  string
	price int
}

var VersionedProductMeta = drel.ModelMeta[VersionedProduct]{
	Table:        "v_products",
	Columns:      []string{"id", "name", "price", "version", "created_at", "updated_at"},
	PKColumns:    []string{"id"},
	HasVersioned: true,
	VersionValue: func(p *VersionedProduct) int { return p.Version() },
	SetVersion:   func(p *VersionedProduct, v int) { *p.VersionPtr() = v },
	Scan: func(row drel.Row) (*VersionedProduct, error) {
		p := &VersionedProduct{}
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		err := row.Scan(idPtr, &p.name, &p.price, p.VersionPtr(), createdAtPtr, updatedAtPtr)
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	Snapshot: func(p *VersionedProduct) any {
		return vpSnapshot{name: p.name, price: p.price}
	},
	Diff: func(p *VersionedProduct, snap any) []drel.FieldChange {
		s := snap.(vpSnapshot)
		var changes []drel.FieldChange
		if p.name != s.name {
			changes = append(changes, drel.FieldChange{Column: "name", Value: p.name})
		}
		if p.price != s.price {
			changes = append(changes, drel.FieldChange{Column: "price", Value: p.price})
		}
		return changes
	},
	PKValue: func(p *VersionedProduct) any { return p.ID() },
	InsertColumns: func(p *VersionedProduct) ([]string, []any) {
		return []string{"name", "price"}, []any{p.name, p.price}
	},
	ScanReturning: func(p *VersionedProduct, row drel.Row) error {
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		return row.Scan(idPtr, createdAtPtr, updatedAtPtr)
	},
}
