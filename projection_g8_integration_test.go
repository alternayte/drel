//go:build integration

package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// g8Order is a minimal model for projection/aggregate integration tests.
type g8Order struct {
	ID       int
	UserID   int
	Category string
	Amount   float64
}

var g8OrderMeta = drel.ModelMeta[g8Order]{
	Table:     "g8_orders",
	Columns:   []string{"id", "user_id", "category", "amount"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*g8Order, error) {
		o := &g8Order{}
		err := row.Scan(&o.ID, &o.UserID, &o.Category, &o.Amount)
		return o, err
	},
	PKValue: func(o *g8Order) any { return o.ID },
	InsertColumns: func(o *g8Order) ([]string, []any) {
		return []string{"user_id", "category", "amount"},
			[]any{o.UserID, o.Category, o.Amount}
	},
	ScanReturning: func(o *g8Order, row drel.Row) error { return row.Scan(&o.ID) },
}

func setupG8(t *testing.T) (*drel.Engine, *drel.Repository[g8Order]) {
	t.Helper()
	engine := setupTestDB(t)
	ctx := context.Background()

	_, err := engine.Exec(ctx, `
		CREATE TABLE g8_users (
			id   SERIAL PRIMARY KEY,
			name TEXT NOT NULL
		)
	`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `
		CREATE TABLE g8_orders (
			id       SERIAL PRIMARY KEY,
			user_id  INTEGER NOT NULL REFERENCES g8_users(id),
			category TEXT NOT NULL,
			amount   NUMERIC NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = engine.Exec(ctx, "INSERT INTO g8_users (id, name) VALUES (1, 'Alice'), (2, 'Bob')")
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `
		INSERT INTO g8_orders (user_id, category, amount) VALUES
			(1, 'books', 10.00),
			(1, 'books', 20.00),
			(1, 'toys', 5.00),
			(2, 'books', 7.00)
	`)
	require.NoError(t, err)

	return engine, drel.NewRepository(engine, g8OrderMeta)
}

func TestIntegration_G8_DistinctCategories(t *testing.T) {
	_, repo := setupG8(t)
	ctx := context.Background()

	type catDTO struct {
		Category string `db:"category"`
	}
	qb := repo.Distinct().OrderBy(drel.NewStringCol("category").Asc())
	out, err := drel.Select[catDTO](ctx, qb, drel.ColRef("category"))
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "books", out[0].Category)
	assert.Equal(t, "toys", out[1].Category)
}

func TestIntegration_G8_CountStarAndDistinctPerGroup(t *testing.T) {
	_, repo := setupG8(t)
	ctx := context.Background()

	type statDTO struct {
		Category string `db:"category"`
		Rows     int    `db:"rows"`
		Buyers   int    `db:"buyers"`
	}
	qb := repo.OrderBy(drel.NewStringCol("category").Asc())
	out, err := drel.GroupBy[statDTO](ctx, qb,
		[]drel.GroupSpec{drel.Group(drel.ColRef("category"))},
		[]drel.AliasedAgg{
			drel.As("rows", drel.CountStar()),
			drel.As("buyers", drel.CountDistinct(drel.ColRef("user_id"))),
		},
	)
	require.NoError(t, err)
	require.Len(t, out, 2)
	// books: 3 rows (Alice x2, Bob x1), 2 distinct buyers.
	assert.Equal(t, "books", out[0].Category)
	assert.Equal(t, 3, out[0].Rows)
	assert.Equal(t, 2, out[0].Buyers)
	// toys: 1 row, 1 buyer.
	assert.Equal(t, "toys", out[1].Category)
	assert.Equal(t, 1, out[1].Rows)
	assert.Equal(t, 1, out[1].Buyers)
}

func TestIntegration_G8_EmptySetSumZero(t *testing.T) {
	_, repo := setupG8(t)
	ctx := context.Background()

	qb := repo.Where(drel.NewStringCol("category").Eq("nonexistent"))
	total, err := drel.Aggregate[float64](ctx, qb, drel.Sum(drel.ColRef("amount")))
	require.NoError(t, err)
	assert.InDelta(t, 0.0, total, 0.001)
}

func TestIntegration_G8_JoinedProjection(t *testing.T) {
	_, repo := setupG8(t)
	ctx := context.Background()

	type joinedDTO struct {
		Name   string  `db:"name"`
		Amount float64 `db:"amount"`
	}
	on := drel.QualifiedColRef("g8_users", "id").EqCol(drel.QualifiedColRef("g8_orders", "user_id"))
	qb := repo.
		InnerJoin("g8_users", on).
		Where(drel.NewOrderedCol[float64]("g8_orders.amount").GTE(20.0)).
		OrderBy(drel.NewOrderedCol[float64]("g8_orders.amount").Asc())

	out, err := drel.Select[joinedDTO](ctx, qb,
		drel.QualifiedColRef("g8_users", "name").Ref(),
		drel.QualifiedColRef("g8_orders", "amount").Ref(),
	)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "Alice", out[0].Name)
	assert.InDelta(t, 20.0, out[0].Amount, 0.001)
}
