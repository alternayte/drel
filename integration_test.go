//go:build integration

package drel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/internal/testmodels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *drel.Engine {
	t.Helper()
	engine := newTestEngine(t)

	_, err := engine.Exec(context.Background(), `
		CREATE TABLE products (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL,
			price      INTEGER NOT NULL,
			in_stock   BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	require.NoError(t, err)

	return engine
}

func seedProducts(t *testing.T, engine *drel.Engine) {
	t.Helper()
	ctx := context.Background()
	products := []struct {
		name    string
		price   int
		inStock bool
	}{
		{"Widget", 1000, true},
		{"Gadget", 2500, true},
		{"Doohickey", 500, false},
		{"Thingamajig", 1500, true},
		{"Whatchamacallit", 3000, false},
	}

	for _, p := range products {
		_, err := engine.Exec(ctx,
			"INSERT INTO products (name, price, in_stock) VALUES ($1, $2, $3)",
			p.name, p.price, p.inStock,
		)
		require.NoError(t, err)
	}
}

func TestIntegration_FindByID(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	product, err := repo.Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Widget", product.Name)
	assert.Equal(t, 1000, product.Price)
}

func TestIntegration_FindByID_NotFound(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	_, err := repo.Find(ctx, 999)
	assert.ErrorIs(t, err, drel.ErrNotFound)
}

func TestIntegration_All(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 5)
}

func TestIntegration_All_EmptyTable(t *testing.T) {
	engine := setupTestDB(t)
	// No seed
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.All(ctx)
	require.NoError(t, err)
	assert.Empty(t, products)
}

func TestIntegration_WhereEq(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.Where(testmodels.Products.Name.Eq("Widget")).All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 1)
	assert.Equal(t, "Widget", products[0].Name)
}

func TestIntegration_WhereGTE(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.Where(testmodels.Products.Price.GTE(2000)).All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 2)
}

func TestIntegration_WhereBoolIsTrue(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.Where(testmodels.Products.InStock.IsTrue()).All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 3)
}

func TestIntegration_WhereIn(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.Where(testmodels.Products.Name.In("Widget", "Gadget")).All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 2)
}

func TestIntegration_WhereOr(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.Where(
		drel.Or(
			testmodels.Products.Price.LT(600),
			testmodels.Products.Price.GT(2900),
		),
	).All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 2)
}

func TestIntegration_MultipleWhereConditions(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.
		Where(testmodels.Products.InStock.IsTrue()).
		Where(testmodels.Products.Price.GTE(1500)).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 2)
}

func TestIntegration_OrderByAsc(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.OrderBy(testmodels.Products.Price.Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 5)
	assert.Equal(t, 500, products[0].Price)
	assert.Equal(t, 3000, products[4].Price)
}

func TestIntegration_OrderByDesc(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.OrderBy(testmodels.Products.Price.Desc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 5)
	assert.Equal(t, 3000, products[0].Price)
	assert.Equal(t, 500, products[4].Price)
}

func TestIntegration_Limit(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.OrderBy(testmodels.Products.Price.Asc()).Limit(2).All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 2)
	assert.Equal(t, 500, products[0].Price)
	assert.Equal(t, 1000, products[1].Price)
}

func TestIntegration_Skip(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.OrderBy(testmodels.Products.Price.Asc()).Skip(3).All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 2)
	assert.Equal(t, 2500, products[0].Price)
	assert.Equal(t, 3000, products[1].Price)
}

func TestIntegration_LimitAndSkip(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.OrderBy(testmodels.Products.Price.Asc()).Skip(1).Limit(2).All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 2)
	assert.Equal(t, 1000, products[0].Price)
	assert.Equal(t, 1500, products[1].Price)
}

func TestIntegration_First(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	product, err := repo.Where(testmodels.Products.InStock.IsTrue()).
		OrderBy(testmodels.Products.Price.Asc()).
		First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Widget", product.Name)
}

func TestIntegration_First_NotFound(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	_, err := repo.Where(testmodels.Products.Price.GT(99999)).First(ctx)
	assert.ErrorIs(t, err, drel.ErrNotFound)
}

func TestIntegration_FirstOrNil(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	product, err := repo.Where(testmodels.Products.Price.GT(99999)).FirstOrNil(ctx)
	require.NoError(t, err)
	assert.Nil(t, product)
}

func TestIntegration_Count(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestIntegration_CountWithWhere(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	count, err := repo.Where(testmodels.Products.InStock.IsTrue()).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestIntegration_Exists(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	exists, err := repo.Where(testmodels.Products.Name.Eq("Widget")).Exists(ctx)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestIntegration_ExistsFalse(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	exists, err := repo.Where(testmodels.Products.Name.Eq("Nonexistent")).Exists(ctx)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestIntegration_Count_EmptyTable(t *testing.T) {
	engine := setupTestDB(t)
	// No seed
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestIntegration_StringColumn_Contains(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.Where(testmodels.Products.Name.Contains("get")).All(ctx)
	require.NoError(t, err)
	assert.Len(t, products, 2)
}

func TestIntegration_ComplexQuery(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	products, err := repo.
		Where(testmodels.Products.InStock.IsTrue()).
		Where(testmodels.Products.Price.GTE(1000)).
		Where(testmodels.Products.Price.LTE(2500)).
		OrderBy(testmodels.Products.Price.Desc()).
		Limit(2).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 2)
	assert.Equal(t, "Gadget", products[0].Name)
	assert.Equal(t, "Thingamajig", products[1].Name)
}

func TestIntegration_Transaction_Insert(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		p := &testmodels.Product{Name: "NewProduct", Price: 999, InStock: true}
		repo.Add(p)
		return nil
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	products, err := repo.All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 1)
	assert.Equal(t, "NewProduct", products[0].Name)
	assert.Equal(t, 999, products[0].Price)
}

func TestIntegration_Transaction_Update(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		product, err := repo.Find(ctx, 1)
		if err != nil {
			return err
		}
		product.Name = "UpdatedWidget"
		return nil
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	product, err := repo.Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "UpdatedWidget", product.Name)
}

func TestIntegration_Transaction_UpdateOnlyChangedFields(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		product, err := repo.Find(ctx, 1)
		if err != nil {
			return err
		}
		product.Price = 1234
		return nil
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	product, err := repo.Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 1234, product.Price)
	assert.Equal(t, "Widget", product.Name)
}

func TestIntegration_Transaction_Delete(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		product, err := repo.Find(ctx, 1)
		if err != nil {
			return err
		}
		return repo.Remove(product)
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, count)
}

func TestIntegration_Transaction_MultipleOps(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)

		newP := &testmodels.Product{Name: "BrandNew", Price: 777, InStock: true}
		repo.Add(newP)

		existing, err := repo.Find(ctx, 2)
		if err != nil {
			return err
		}
		existing.Name = "ModifiedGadget"

		toDelete, err := repo.Find(ctx, 3)
		if err != nil {
			return err
		}
		return repo.Remove(toDelete)
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count) // 5 original - 1 deleted + 1 added = 5

	gadget, err := repo.Where(testmodels.Products.Name.Eq("ModifiedGadget")).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ModifiedGadget", gadget.Name)
}

func TestIntegration_Transaction_Rollback(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		p := &testmodels.Product{Name: "ShouldNotExist", Price: 1, InStock: true}
		repo.Add(p)
		return fmt.Errorf("intentional rollback")
	})
	require.Error(t, err)

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestIntegration_Transaction_PanicRollback(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	ctx := context.Background()

	assert.Panics(t, func() {
		engine.Transaction(ctx, func(tx *drel.Tx) error {
			repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
			p := &testmodels.Product{Name: "ShouldNotExist", Price: 1, InStock: true}
			repo.Add(p)
			panic("intentional panic")
		})
	})

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestIntegration_Transaction_MidTxSaveChanges(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)

		first := &testmodels.Product{Name: "First", Price: 100, InStock: true}
		repo.Add(first)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}

		assert.NotEqual(t, 0, first.ID)

		second := &testmodels.Product{Name: "Second", Price: 200, InStock: true}
		repo.Add(second)
		return nil
	})
	require.NoError(t, err)

	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestIntegration_Transaction_Empty(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		return nil
	})
	require.NoError(t, err)
}

func TestIntegration_Transaction_NoOpUpdate(t *testing.T) {
	engine := setupTestDB(t)
	seedProducts(t, engine)
	ctx := context.Background()

	err := engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, testmodels.ProductMeta)
		_, err := repo.Find(ctx, 1)
		return err
	})
	require.NoError(t, err)
}
