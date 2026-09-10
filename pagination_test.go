package drel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Pagination test model ───────────────────────────────────────────────────

type pageRow struct {
	ID   int
	Name string
	Rank int
}

var pageRowMeta = drel.ModelMeta[pageRow]{
	Table:     "page_rows",
	Columns:   []string{"id", "name", "rank"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*pageRow, error) {
		p := &pageRow{}
		err := row.Scan(&p.ID, &p.Name, &p.Rank)
		return p, err
	},
	PKValue: func(p *pageRow) any { return p.ID },
	ColumnValue: func(p *pageRow, idx int) any {
		switch idx {
		case 0:
			return p.ID
		case 1:
			return p.Name
		case 2:
			return p.Rank
		}
		return nil
	},
	InsertColumns: func(p *pageRow) ([]string, []any) {
		return []string{"name", "rank"}, []any{p.Name, p.Rank}
	},
}

func setupPageRows(t *testing.T, n int) (*drel.Engine, *drel.Repository[pageRow]) {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })

	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE page_rows (
		id   INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		rank INTEGER NOT NULL
	)`)
	require.NoError(t, err)

	for i := 1; i <= n; i++ {
		// rank deliberately repeats so keyset pagination must rely on the PK tiebreaker.
		_, err = engine.Exec(ctx, `INSERT INTO page_rows (name, rank) VALUES (?, ?)`,
			fmt.Sprintf("row-%02d", i), i%3)
		require.NoError(t, err)
	}
	return engine, drel.NewRepository(engine, pageRowMeta)
}

// ─── Offset pagination ───────────────────────────────────────────────────────

func TestPageOffset_Math(t *testing.T) {
	engine, repo := setupPageRows(t, 25)
	_ = engine
	ctx := context.Background()

	page, err := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Skip(20).Take(10).PageOffset(ctx)
	require.NoError(t, err)
	assert.Equal(t, 25, page.Total)
	assert.Equal(t, 10, page.PageSize)
	assert.Equal(t, 3, page.Page)       // offset 20 / size 10 + 1
	assert.Equal(t, 3, page.TotalPages) // ceil(25/10)
	assert.Len(t, page.Items, 5)        // only 5 rows left after skipping 20
	assert.False(t, page.HasMore)       // 20 + 5 == 25
	assert.Equal(t, 21, page.Items[0].ID)
}

func TestPageOffset_FirstPageHasMore(t *testing.T) {
	_, repo := setupPageRows(t, 25)
	ctx := context.Background()

	page, err := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Take(10).PageOffset(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Len(t, page.Items, 10)
	assert.True(t, page.HasMore)
}

func TestPageOffset_RequiresLimit(t *testing.T) {
	_, repo := setupPageRows(t, 3)
	ctx := context.Background()
	_, err := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).PageOffset(ctx)
	assert.ErrorIs(t, err, drel.ErrPaginationNeedsLimit)
}

// ─── Cursor pagination ───────────────────────────────────────────────────────

func TestCursorPage_WalksAllRowsAscending(t *testing.T) {
	_, repo := setupPageRows(t, 25)
	ctx := context.Background()

	seen := make([]int, 0, 25)
	cursor := ""
	pages := 0
	for {
		q := repo.OrderBy(drel.NewOrderedCol[int]("rank").Asc()).Take(7)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, pages, 10, "too many pages — cursor not advancing")
		for _, it := range page.Items {
			seen = append(seen, it.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
		require.NotEmpty(t, cursor)
	}

	// Every row exactly once, no duplicates, no skips.
	require.Len(t, seen, 25)
	dedup := make(map[int]bool)
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice", id)
		dedup[id] = true
	}
}

func TestCursorPage_Descending(t *testing.T) {
	_, repo := setupPageRows(t, 12)
	ctx := context.Background()

	seen := make([]int, 0, 12)
	cursor := ""
	for {
		q := repo.OrderBy(drel.NewOrderedCol[int]("id").Desc()).Take(5)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		for _, it := range page.Items {
			seen = append(seen, it.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.Len(t, seen, 12)
	// Descending: first id is 12, last is 1, strictly decreasing.
	for i := 1; i < len(seen); i++ {
		assert.Greater(t, seen[i-1], seen[i])
	}
	assert.Equal(t, 12, seen[0])
	assert.Equal(t, 1, seen[len(seen)-1])
}

func TestCursorPage_RequiresOrderBy(t *testing.T) {
	_, repo := setupPageRows(t, 3)
	ctx := context.Background()
	_, err := repo.Take(2).Page(ctx)
	assert.ErrorIs(t, err, drel.ErrCursorPaginationNeedsOrderBy)
}

// catKind is a named string type used to exercise cursor encoding of a
// non-builtin (enum-like / uuid-like) order-key type.
type catKind string

type catRow struct {
	ID   int
	Kind catKind
}

var catRowMeta = drel.ModelMeta[catRow]{
	Table:     "cat_rows",
	Columns:   []string{"id", "kind"},
	PKColumns: []string{"id"},
	Scan: func(r drel.Row) (*catRow, error) {
		c := &catRow{}
		return c, r.Scan(&c.ID, &c.Kind)
	},
	PKValue: func(c *catRow) any { return c.ID },
	ColumnValue: func(c *catRow, i int) any {
		if i == 0 {
			return c.ID
		}
		return c.Kind
	},
}

func TestCursorPage_NamedTypeOrderKey(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE cat_rows (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL)`)
	require.NoError(t, err)
	for i := 1; i <= 9; i++ {
		_, err = engine.Exec(ctx, `INSERT INTO cat_rows (kind) VALUES (?)`,
			[]string{"a", "b", "c"}[i%3])
		require.NoError(t, err)
	}
	repo := drel.NewRepository(engine, catRowMeta)

	// Ordering by the named-type column must encode/decode cursors without error.
	seen := 0
	cursor := ""
	for {
		q := repo.OrderBy(drel.NewOrderedCol[catKind]("kind").Asc()).Take(4)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err) // would error if gob couldn't encode catKind
		seen += len(page.Items)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
		require.NotEmpty(t, cursor)
	}
	assert.Equal(t, 9, seen)
}

func TestCursorPage_InvalidCursor(t *testing.T) {
	_, repo := setupPageRows(t, 3)
	ctx := context.Background()
	_, err := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Take(2).After("not-a-valid-cursor!!!").Page(ctx)
	assert.ErrorIs(t, err, drel.ErrInvalidCursor)
}

func TestPageOffset_RejectsZeroAndNegativeTake(t *testing.T) {
	_, repo := setupPageRows(t, 5)
	ctx := context.Background()

	_, err := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Take(0).PageOffset(ctx)
	assert.ErrorIs(t, err, drel.ErrInvalidPageSize)

	_, err = repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Take(-3).PageOffset(ctx)
	assert.ErrorIs(t, err, drel.ErrInvalidPageSize)
}

func TestCursorPage_RejectsZeroAndNegativeTake(t *testing.T) {
	_, repo := setupPageRows(t, 5)
	ctx := context.Background()

	_, err := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Take(0).Page(ctx)
	assert.ErrorIs(t, err, drel.ErrInvalidPageSize)

	_, err = repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Take(-1).Page(ctx)
	assert.ErrorIs(t, err, drel.ErrInvalidPageSize)
}

func TestCursorPage_IgnoresSkipOnFirstPage(t *testing.T) {
	_, repo := setupPageRows(t, 25)
	ctx := context.Background()

	// Skip(5) must be ignored by keyset Page; the first page must start at id 1.
	page, err := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Skip(5).Take(3).Page(ctx)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	assert.Equal(t, 1, page.Items[0].ID)
	assert.Equal(t, 2, page.Items[1].ID)
	assert.Equal(t, 3, page.Items[2].ID)
}

func TestCursorPage_IgnoresSkipAcrossWalk(t *testing.T) {
	_, repo := setupPageRows(t, 25)
	ctx := context.Background()

	// A Skip set on the builder must not drop rows across the whole cursor walk.
	seen := make([]int, 0, 25)
	cursor := ""
	for {
		q := repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Skip(5).Take(7)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		for _, it := range page.Items {
			seen = append(seen, it.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.Len(t, seen, 25)
	assert.Equal(t, 1, seen[0])
	assert.Equal(t, 25, seen[len(seen)-1])
}

func TestCursorPage_MultiColumnKeyset_NoSkipsNoDupes(t *testing.T) {
	_, repo := setupPageRows(t, 30)
	ctx := context.Background()

	// Two distinct user columns (rank asc, name desc) + implicit PK = 3 levels.
	// rank repeats (i%3) forcing the name + PK tiebreaks to be exercised.
	seen := make([]int, 0, 30)
	cursor := ""
	pages := 0
	for {
		q := repo.OrderBy(
			drel.NewOrderedCol[int]("rank").Asc(),
			drel.NewStringCol("name").Desc(),
		).Take(4)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, pages, 12, "cursor not advancing on 3-level keyset")
		for _, it := range page.Items {
			seen = append(seen, it.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}

	require.Len(t, seen, 30)
	dedup := map[int]bool{}
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice across 3-level keyset walk", id)
		dedup[id] = true
	}
}

// nullRankRow has a nullable rank column to exercise null-aware keyset paging.
type nullRankRow struct {
	ID   int
	Rank *int
}

var nullRankMeta = drel.ModelMeta[nullRankRow]{
	Table:     "null_rank_rows",
	Columns:   []string{"id", "rank"},
	PKColumns: []string{"id"},
	Scan: func(r drel.Row) (*nullRankRow, error) {
		x := &nullRankRow{}
		return x, r.Scan(&x.ID, &x.Rank)
	},
	PKValue: func(x *nullRankRow) any { return x.ID },
	ColumnValue: func(x *nullRankRow, i int) any {
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

func setupNullRankRows(t *testing.T) (*drel.Engine, *drel.Repository[nullRankRow]) {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE null_rank_rows (id INTEGER PRIMARY KEY AUTOINCREMENT, rank INTEGER)`)
	require.NoError(t, err)
	// 10 rows: ids 1..10; even ids get a rank, odd ids get NULL.
	for i := 1; i <= 10; i++ {
		if i%2 == 0 {
			_, err = engine.Exec(ctx, `INSERT INTO null_rank_rows (rank) VALUES (?)`, i)
		} else {
			_, err = engine.Exec(ctx, `INSERT INTO null_rank_rows (rank) VALUES (NULL)`)
		}
		require.NoError(t, err)
	}
	return engine, drel.NewRepository(engine, nullRankMeta)
}

func TestCursorPage_NullableColumn_NullsLast_WalksAllRows(t *testing.T) {
	_, repo := setupNullRankRows(t)
	ctx := context.Background()

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
		require.LessOrEqual(t, pages, 10, "cursor not advancing")
		for _, it := range page.Items {
			seen = append(seen, it.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}

	// All 10 rows, exactly once — including the 5 NULL-rank rows.
	require.Len(t, seen, 10)
	dedup := map[int]bool{}
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice", id)
		dedup[id] = true
	}
}

func TestCursorPage_BackwardWalk_MatchesForward(t *testing.T) {
	_, repo := setupPageRows(t, 20)
	ctx := context.Background()

	// Forward to the 3rd page to obtain a cursor positioned mid-set.
	col := func() *drel.QueryBuilder[pageRow] {
		return repo.OrderBy(drel.NewOrderedCol[int]("id").Asc()).Take(5)
	}
	p1, err := col().Page(ctx)
	require.NoError(t, err)
	require.True(t, p1.HasMore)
	p2, err := col().After(p1.NextCursor).Page(ctx)
	require.NoError(t, err)
	require.True(t, p2.HasMore)
	// p2 items should be ids 6..10.
	assert.Equal(t, 6, p2.Items[0].ID)
	assert.Equal(t, 10, p2.Items[len(p2.Items)-1].ID)

	// p2 must report a previous page exists, with a usable PreviousCursor.
	assert.True(t, p2.HasPrev)
	require.NotEmpty(t, p2.PreviousCursor)

	// Walk backward from p2 using Before -> must return p1's rows (ids 1..5) in
	// natural ascending order.
	back, err := col().Before(p2.PreviousCursor).Page(ctx)
	require.NoError(t, err)
	gotIDs := make([]int, len(back.Items))
	for i, it := range back.Items {
		gotIDs[i] = it.ID
	}
	assert.Equal(t, []int{1, 2, 3, 4, 5}, gotIDs)
}

func TestCursorPage_NullableColumn_DefaultNulls_Errors(t *testing.T) {
	_, repo := setupNullRankRows(t)
	ctx := context.Background()

	// Walk forward until the cursor lands on a NULL rank value, then the next
	// page must surface ErrCursorColumnNullable because NULL placement is unpinned.
	cursor := ""
	var lastErr error
	for i := 0; i < 12; i++ {
		q := repo.OrderBy(drel.NewOrderedCol[int]("rank").Asc()).Take(3)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		if err != nil {
			lastErr = err
			break
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	assert.ErrorIs(t, lastErr, drel.ErrCursorColumnNullable)
}

type floatRow struct {
	ID    int
	Score float64
}

var floatRowMeta = drel.ModelMeta[floatRow]{
	Table:     "float_rows",
	Columns:   []string{"id", "score"},
	PKColumns: []string{"id"},
	Scan: func(r drel.Row) (*floatRow, error) {
		x := &floatRow{}
		return x, r.Scan(&x.ID, &x.Score)
	},
	PKValue: func(x *floatRow) any { return x.ID },
	ColumnValue: func(x *floatRow, i int) any {
		if i == 0 {
			return x.ID
		}
		return x.Score
	},
}

func TestCursorPage_FloatOrderKey_RoundTrips(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE float_rows (id INTEGER PRIMARY KEY AUTOINCREMENT, score REAL NOT NULL)`)
	require.NoError(t, err)
	// Duplicate scores force the PK tiebreaker through a gob-encoded float cursor.
	for i := 1; i <= 12; i++ {
		_, err = engine.Exec(ctx, `INSERT INTO float_rows (score) VALUES (?)`, float64(i%3)+0.5)
		require.NoError(t, err)
	}
	repo := drel.NewRepository(engine, floatRowMeta)

	seen := make([]int, 0, 12)
	cursor := ""
	for {
		q := repo.OrderBy(drel.NewOrderedCol[float64]("score").Asc()).Take(5)
		if cursor != "" {
			q = q.After(cursor)
		}
		page, err := q.Page(ctx)
		require.NoError(t, err)
		for _, it := range page.Items {
			seen = append(seen, it.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.Len(t, seen, 12)
	dedup := map[int]bool{}
	for _, id := range seen {
		require.False(t, dedup[id], "row %d returned twice", id)
		dedup[id] = true
	}
}
