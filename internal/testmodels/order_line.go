package testmodels

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// LineNo is a named type over int. The driver returns int64 for the column, so
// the key path must convert rather than assert. This is the failure shape that
// the m2m relationship once had.
type LineNo int

// OrderLineKey is a two-column primary key with a named-type column.
type OrderLineKey struct {
	OrderID int    `db:"order_id"`
	LineNo  LineNo `db:"line_no"`
}

// OrderLine exercises insert, find, update and delete with a composite key.
type OrderLine struct {
	drel.Model[OrderLineKey]
	Qty int `db:"qty"`
}

// SoftOrderLine exercises the soft-delete path with a composite key.
type SoftOrderLine struct {
	drel.Model[OrderLineKey]
	drel.SoftDelete
	Qty int `db:"qty"`
}

// VersionedOrderLine exercises the versioned update and the versioned hard
// delete with a composite key.
type VersionedOrderLine struct {
	drel.Model[OrderLineKey]
	drel.Versioned
	Qty int `db:"qty"`
}

// SoftVersionedOrderLine exercises the versioned soft delete with a composite
// key.
type SoftVersionedOrderLine struct {
	drel.Model[OrderLineKey]
	drel.Versioned
	drel.SoftDelete
	Qty int `db:"qty"`
}

// TenantDocKey is a composite key whose first column is a uuid.
type TenantDocKey struct {
	TenantID uuid.UUID `db:"tenant_id"`
	DocNo    int       `db:"doc_no"`
}

// TenantDoc exercises a composite key that contains a uuid column.
type TenantDoc struct {
	drel.Model[TenantDocKey]
	Title string `db:"title"`
}
