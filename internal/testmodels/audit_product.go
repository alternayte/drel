package testmodels

import (
	"github.com/alternayte/drel"
)

type AuditProduct struct {
	drel.Model[int]
	drel.Audit
	name  string
	price int
}

func NewAuditProduct(name string, price int) *AuditProduct {
	return &AuditProduct{name: name, price: price}
}

func (p *AuditProduct) Name() string     { return p.name }
func (p *AuditProduct) Price() int       { return p.price }
func (p *AuditProduct) SetName(n string) { p.name = n }
func (p *AuditProduct) SetPrice(pr int)  { p.price = pr }

type apSnapshot struct {
	name  string
	price int
}

var AuditProductMeta = drel.ModelMeta[AuditProduct]{
	Table:     "a_products",
	Columns:   []string{"id", "name", "price", "created_by", "updated_by", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	HasAudit:  true,
	AuditSetCreate: func(p *AuditProduct, actor string) {
		createdByPtr, updatedByPtr := p.AuditPtrs()
		*createdByPtr = actor
		*updatedByPtr = actor
	},
	AuditSetUpdate: func(p *AuditProduct, actor string) {
		_, updatedByPtr := p.AuditPtrs()
		*updatedByPtr = actor
	},
	Scan: func(row drel.Row) (*AuditProduct, error) {
		p := &AuditProduct{}
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		createdByPtr, updatedByPtr := p.AuditPtrs()
		err := row.Scan(idPtr, &p.name, &p.price, createdByPtr, updatedByPtr, createdAtPtr, updatedAtPtr)
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	Snapshot: func(p *AuditProduct) any {
		return apSnapshot{name: p.name, price: p.price}
	},
	Diff: func(p *AuditProduct, snap any) []drel.FieldChange {
		s := snap.(apSnapshot)
		var changes []drel.FieldChange
		if p.name != s.name {
			changes = append(changes, drel.FieldChange{Column: "name", Value: p.name})
		}
		if p.price != s.price {
			changes = append(changes, drel.FieldChange{Column: "price", Value: p.price})
		}
		return changes
	},
	PKValue: func(p *AuditProduct) any { return p.ID() },
	InsertColumns: func(p *AuditProduct) ([]string, []any) {
		return []string{"name", "price", "created_by", "updated_by"}, []any{p.name, p.price, p.CreatedBy(), p.UpdatedBy()}
	},
	ScanReturning: func(p *AuditProduct, row drel.Row) error {
		idPtr, createdAtPtr, updatedAtPtr := p.ScanPtrs()
		return row.Scan(idPtr, createdAtPtr, updatedAtPtr)
	},
}
