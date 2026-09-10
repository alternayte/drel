package orders

import "github.com/alternayte/drel"

// OrderLineKey is a two-column composite primary key. Each exported field
// becomes one key column, named by its own db tag; field order is the key
// order.
type OrderLineKey struct {
	OrderID int `db:"order_id"`
	LineNo  int `db:"line_no"`
}

// OrderLine is keyed by OrderLineKey. A composite key is always
// application-assigned: set it with SetID before Add.
type OrderLine struct {
	drel.Model[OrderLineKey]
	Qty int `db:"qty"`
}

func NewOrderLine(key OrderLineKey, qty int) *OrderLine {
	l := &OrderLine{Qty: qty}
	l.SetID(key)
	return l
}
