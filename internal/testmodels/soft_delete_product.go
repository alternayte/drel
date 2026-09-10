package testmodels

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/internal/ast"
)

type SoftDeleteProduct struct {
	drel.Model[int]
	drel.SoftDelete
	name  string
	price int
}

func NewSoftDeleteProduct(name string, price int) *SoftDeleteProduct {
	return &SoftDeleteProduct{name: name, price: price}
}

func (p *SoftDeleteProduct) Name() string     { return p.name }
func (p *SoftDeleteProduct) Price() int       { return p.price }
func (p *SoftDeleteProduct) SetName(n string) { p.name = n }
func (p *SoftDeleteProduct) SetPrice(pr int)  { p.price = pr }

type sdProductSnapshot struct {
	name  string
	price int
}

var SoftDeleteProducts = struct {
	ID        drel.OrderedColumn[int]
	Name      drel.StringColumn
	Price     drel.OrderedColumn[int]
	DeletedAt drel.ComparableColumn[*time.Time]
}{
	ID:        drel.NewOrderedCol[int]("id"),
	Name:      drel.NewStringCol("name"),
	Price:     drel.NewOrderedCol[int]("price"),
	DeletedAt: drel.NewComparableCol[*time.Time]("deleted_at"),
}

var SoftDeleteProductMeta = drel.ModelMeta[SoftDeleteProduct]{
	Table:         "sd_products",
	Columns:       []string{"id", "name", "price", "deleted_at", "created_at", "updated_at"},
	PKColumns:     []string{"id"},
	HasSoftDelete: true,
	Filters: []drel.NamedFilter{
		{Name: "soft_delete", Clause: ast.WhereClause{
			Comparison: &ast.ComparisonNode{Column: "deleted_at", Op: ast.OpIsNull},
		}},
	},
	Scan: func(row drel.Row) (*SoftDeleteProduct, error) {
		p := &SoftDeleteProduct{}
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		err := row.Scan(idPtr, &p.name, &p.price, p.DeletedAtPtr(), createdAtPtr, updatedAtPtr)
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	Snapshot: func(p *SoftDeleteProduct) any {
		return sdProductSnapshot{name: p.name, price: p.price}
	},
	Diff: func(p *SoftDeleteProduct, snap any) []drel.FieldChange {
		s := snap.(sdProductSnapshot)
		var changes []drel.FieldChange
		if p.name != s.name {
			changes = append(changes, drel.FieldChange{Column: "name", Value: p.name})
		}
		if p.price != s.price {
			changes = append(changes, drel.FieldChange{Column: "price", Value: p.price})
		}
		return changes
	},
	PKValue: func(p *SoftDeleteProduct) any { return p.ID() },
	InsertColumns: func(p *SoftDeleteProduct) ([]string, []any) {
		return []string{"name", "price"}, []any{p.name, p.price}
	},
	ScanReturning: func(p *SoftDeleteProduct, row drel.Row) error {
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		return row.Scan(idPtr, createdAtPtr, updatedAtPtr)
	},
}
