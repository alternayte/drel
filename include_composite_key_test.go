package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/require"
)

// A composite-key model (order line item) that is a relationship *source* —
// it has a belongs_to Product field. The plan allows this (only a
// composite-key *target* is forbidden), so IncludableQuery.Find and
// TxIncludableQuery.Find must be able to look up such a model by its full
// key, not just its first key column.

type ckLineItemKey struct {
	OrderID int
	LineNo  int
}

type ckLineItem struct {
	OrderID   int
	LineNo    int
	ProductID int
	Qty       int
	Product   *ckProduct
}

type ckProduct struct {
	ID   int
	Name string
}

func ckLineItemMeta() drel.ModelMeta[ckLineItem] {
	return drel.ModelMeta[ckLineItem]{
		Table:     "ck_line_items",
		Columns:   []string{"order_id", "line_no", "product_id", "qty"},
		PKColumns: []string{"order_id", "line_no"},
		KeyValues: func(k any) []any {
			key := k.(ckLineItemKey)
			return []any{key.OrderID, key.LineNo}
		},
		Scan: func(r drel.Row) (*ckLineItem, error) {
			li := &ckLineItem{}
			return li, r.Scan(&li.OrderID, &li.LineNo, &li.ProductID, &li.Qty)
		},
		PKValue: func(li *ckLineItem) any {
			return ckLineItemKey{OrderID: li.OrderID, LineNo: li.LineNo}
		},
		ColumnValue: func(li *ckLineItem, i int) any {
			return [...]any{li.OrderID, li.LineNo, li.ProductID, li.Qty}[i]
		},
	}
}

func ckProductMeta() drel.ModelMeta[ckProduct] {
	return drel.ModelMeta[ckProduct]{
		Table:     "ck_products",
		Columns:   []string{"id", "name"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*ckProduct, error) {
			p := &ckProduct{}
			return p, r.Scan(&p.ID, &p.Name)
		},
		PKValue:     func(p *ckProduct) any { return p.ID },
		ColumnValue: func(p *ckProduct, i int) any { return [...]any{p.ID, p.Name}[i] },
	}
}

func ckProductRel() drel.IncludeSpec {
	productMeta := ckProductMeta()
	return drel.NewIncludeSpec(&drel.RelationInfo{
		Name:        "Product",
		Type:        drel.BelongsTo,
		FKColumn:    "product_id",
		RelatedMeta: drel.ToMetaBase(&productMeta),
		FieldSetter: func(parent any, related any) {
			li := parent.(*ckLineItem)
			if related != nil {
				li.Product = related.(*ckProduct)
			}
		},
	})
}

func setupCompositeKeyEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE TABLE ck_products (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE ck_line_items (order_id INTEGER NOT NULL, line_no INTEGER NOT NULL, product_id INTEGER NOT NULL, qty INTEGER NOT NULL, PRIMARY KEY (order_id, line_no))`,
		`INSERT INTO ck_products (id, name) VALUES (1,'Widget'),(2,'Gadget')`,
		// Two orders, each with two lines, so a first-key-column-only lookup
		// (order_id=5) would ambiguously match more than one row.
		`INSERT INTO ck_line_items (order_id, line_no, product_id, qty) VALUES (5,1,1,3),(5,2,2,7),(6,1,2,1)`,
	} {
		_, err := engine.Exec(ctx, ddl)
		require.NoError(t, err)
	}
	return engine
}

// TestIncludableQuery_Find_CompositeKey proves Repository.Include(...).Find
// looks up a composite-key source model by its whole key, not just the
// first key column.
func TestIncludableQuery_Find_CompositeKey(t *testing.T) {
	engine := setupCompositeKeyEngine(t)
	ctx := context.Background()

	repo := drel.NewRepository(engine, ckLineItemMeta())
	item, err := repo.Include(ckProductRel()).Find(ctx, ckLineItemKey{OrderID: 5, LineNo: 2})
	require.NoError(t, err)
	require.Equal(t, 5, item.OrderID)
	require.Equal(t, 2, item.LineNo)
	require.Equal(t, 7, item.Qty)
	require.NotNil(t, item.Product)
	require.Equal(t, "Gadget", item.Product.Name)
}

// TestTxIncludableQuery_Find_CompositeKey is the transactional twin of
// TestIncludableQuery_Find_CompositeKey.
func TestTxIncludableQuery_Find_CompositeKey(t *testing.T) {
	engine := setupCompositeKeyEngine(t)
	ctx := context.Background()

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.NewTxRepository(drel.MustFromContext(ctx), ckLineItemMeta())
		item, err := repo.Include(ckProductRel()).Find(ctx, ckLineItemKey{OrderID: 5, LineNo: 2})
		require.NoError(t, err)
		require.Equal(t, 5, item.OrderID)
		require.Equal(t, 2, item.LineNo)
		require.Equal(t, 7, item.Qty)
		require.NotNil(t, item.Product)
		require.Equal(t, "Gadget", item.Product.Name)
		return nil
	})
	require.NoError(t, err)
}
