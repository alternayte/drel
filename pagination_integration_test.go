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

func seedManyProducts(t *testing.T, engine *drel.Engine, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		_, err := engine.Exec(ctx,
			"INSERT INTO products (name, price, in_stock) VALUES ($1, $2, $3)",
			fmt.Sprintf("p-%03d", i), (i%4)*100, true)
		require.NoError(t, err)
	}
}

func TestIntegration_PageOffset(t *testing.T) {
	engine := setupTestDB(t)
	seedManyProducts(t, engine, 25)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	page, err := repo.OrderBy(testmodels.Products.ID.Asc()).Skip(20).Take(10).PageOffset(ctx)
	require.NoError(t, err)
	assert.Equal(t, 25, page.Total)
	assert.Equal(t, 3, page.Page)
	assert.Equal(t, 3, page.TotalPages)
	assert.Len(t, page.Items, 5)
	assert.False(t, page.HasMore)
}

func TestIntegration_CursorPage_KeysetWithTiebreaker(t *testing.T) {
	engine := setupTestDB(t)
	seedManyProducts(t, engine, 25)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	// Order by a non-unique column (price) so the PK tiebreaker is exercised
	// against real Postgres, where the multi-clause keyset OR/AND with $N
	// placeholders and int binding must round-trip correctly.
	seen := make([]int, 0, 25)
	cursor := ""
	pages := 0
	for {
		q := repo.OrderBy(testmodels.Products.Price.Asc()).Take(6)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, pages, 10)
		for _, p := range page.Items {
			seen = append(seen, p.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}

	require.Len(t, seen, 25)
	dedup := map[int]bool{}
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice across pages", id)
		dedup[id] = true
	}
}

// nullRankRowPG mirrors the unit nullRankRow but is exercised against real
// Postgres, where NULL ordering and the keyset NULL branches are dialect-native.
type nullRankRowPG struct {
	ID   int
	Rank *int
}

var nullRankPGMeta = drel.ModelMeta[nullRankRowPG]{
	Table:     "null_rank_pg",
	Columns:   []string{"id", "rank"},
	PKColumns: []string{"id"},
	Scan: func(r drel.Row) (*nullRankRowPG, error) {
		x := &nullRankRowPG{}
		return x, r.Scan(&x.ID, &x.Rank)
	},
	PKValue: func(x *nullRankRowPG) any { return x.ID },
	ColumnValue: func(x *nullRankRowPG, i int) any {
		switch i {
		case 0:
			return x.ID
		case 1:
			if x.Rank == nil {
				return nil
			}
			return *x.Rank
		}
		return nil
	},
}

func TestIntegration_CursorPage_NullableColumn_WalksAllRows(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()
	_, err := engine.Exec(ctx, `CREATE TABLE null_rank_pg (id SERIAL PRIMARY KEY, rank INTEGER)`)
	require.NoError(t, err)
	for i := 1; i <= 10; i++ {
		if i%2 == 0 {
			_, err = engine.Exec(ctx, `INSERT INTO null_rank_pg (rank) VALUES ($1)`, i)
		} else {
			_, err = engine.Exec(ctx, `INSERT INTO null_rank_pg (rank) VALUES (NULL)`)
		}
		require.NoError(t, err)
	}
	repo := drel.NewRepository(engine, nullRankPGMeta)

	seen := make([]int, 0, 10)
	cursor := ""
	pages := 0
	for {
		q := repo.OrderBy(drel.NewOrderedCol[int]("rank").Asc().NullsLast()).Take(3)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, pages, 10)
		for _, it := range page.Items {
			seen = append(seen, it.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.Len(t, seen, 10, "all rows including 5 NULL-rank rows must be returned")
	dedup := map[int]bool{}
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice", id)
		dedup[id] = true
	}
}

func TestIntegration_CursorPage_MultiColumn_NoSkipsNoDupes(t *testing.T) {
	engine := setupTestDB(t)
	seedManyProducts(t, engine, 30)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	seen := make([]int, 0, 30)
	cursor := ""
	pages := 0
	for {
		// price repeats (i%4)*100 -> ties on the first user column; name + PK
		// resolve. 3 levels, against real pgx int binding.
		q := repo.OrderBy(testmodels.Products.Price.Asc(), testmodels.Products.Name.Desc()).Take(7)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, pages, 12)
		for _, p := range page.Items {
			seen = append(seen, p.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.Len(t, seen, 30)
	dedup := map[int]bool{}
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice", id)
		dedup[id] = true
	}
}

func TestIntegration_CursorPage_TimestampOrderKey_RoundTrips(t *testing.T) {
	engine := setupTestDB(t)
	seedManyProducts(t, engine, 20) // products has created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	// Many rows share created_at (inserted in the same instant) so the PK
	// tiebreaker is exercised through a gob-encoded time.Time boundary.
	seen := make([]int, 0, 20)
	cursor := ""
	pages := 0
	for {
		q := repo.OrderBy(testmodels.Products.CreatedAt.Asc()).Take(6)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, pages, 10)
		for _, p := range page.Items {
			seen = append(seen, p.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.Len(t, seen, 20)
	dedup := map[int]bool{}
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice at a timestamp boundary", id)
		dedup[id] = true
	}
}

func TestIntegration_CursorPage_Backward_RoundTrips(t *testing.T) {
	engine := setupTestDB(t)
	seedManyProducts(t, engine, 20)
	repo := drel.NewRepository(engine, testmodels.ProductMeta)
	ctx := context.Background()

	mk := func() *drel.QueryBuilder[testmodels.Product] {
		return repo.OrderBy(testmodels.Products.ID.Asc()).Take(5)
	}
	p1, err := mk().Page(ctx)
	require.NoError(t, err)
	p2, err := mk().After(p1.NextCursor).Page(ctx)
	require.NoError(t, err)
	require.True(t, p2.HasPrev)

	back, err := mk().Before(p2.PreviousCursor).Page(ctx)
	require.NoError(t, err)
	require.Len(t, back.Items, 5)
	// Backward page from p2 returns p1's rows in natural ascending order.
	assert.Equal(t, p1.Items[0].ID, back.Items[0].ID)
	assert.Equal(t, p1.Items[len(p1.Items)-1].ID, back.Items[len(back.Items)-1].ID)
}
