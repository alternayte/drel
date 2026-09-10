package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Three-level model tree: Author → Books → Reviews ────────────────────────

type niAuthor struct {
	ID    int
	Name  string
	Books []*niBook
}

type niBook struct {
	ID       int
	AuthorID int
	Title    string
	Reviews  []*niReview
}

type niReview struct {
	ID     int
	BookID int
	Stars  int
}

func niAuthorMeta() drel.ModelMeta[niAuthor] {
	return drel.ModelMeta[niAuthor]{
		Table:     "ni_authors",
		Columns:   []string{"id", "name"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*niAuthor, error) {
			a := &niAuthor{}
			return a, r.Scan(&a.ID, &a.Name)
		},
		PKValue:     func(a *niAuthor) any { return a.ID },
		ColumnValue: func(a *niAuthor, i int) any { return [...]any{a.ID, a.Name}[i] },
	}
}

func niBookMeta() drel.ModelMeta[niBook] {
	return drel.ModelMeta[niBook]{
		Table:     "ni_books",
		Columns:   []string{"id", "author_id", "title"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*niBook, error) {
			b := &niBook{}
			return b, r.Scan(&b.ID, &b.AuthorID, &b.Title)
		},
		PKValue:     func(b *niBook) any { return b.ID },
		ColumnValue: func(b *niBook, i int) any { return [...]any{b.ID, b.AuthorID, b.Title}[i] },
	}
}

func niReviewMeta() drel.ModelMeta[niReview] {
	return drel.ModelMeta[niReview]{
		Table:     "ni_reviews",
		Columns:   []string{"id", "book_id", "stars"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*niReview, error) {
			rv := &niReview{}
			return rv, r.Scan(&rv.ID, &rv.BookID, &rv.Stars)
		},
		PKValue:     func(rv *niReview) any { return rv.ID },
		ColumnValue: func(rv *niReview, i int) any { return [...]any{rv.ID, rv.BookID, rv.Stars}[i] },
	}
}

func setupNestedDB(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE TABLE ni_authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE ni_books (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL)`,
		`CREATE TABLE ni_reviews (id INTEGER PRIMARY KEY, book_id INTEGER NOT NULL, stars INTEGER NOT NULL)`,
		`INSERT INTO ni_authors (id, name) VALUES (1,'Ann'),(2,'Bob')`,
		`INSERT INTO ni_books (id, author_id, title) VALUES (10,1,'A1'),(11,1,'A2'),(12,2,'B1')`,
		`INSERT INTO ni_reviews (id, book_id, stars) VALUES (100,10,5),(101,10,4),(102,11,3),(103,12,2)`,
	} {
		_, err := engine.Exec(ctx, ddl)
		require.NoError(t, err)
	}
	return engine
}

func TestInclude_Limit_PerParent_SQLite(t *testing.T) {
	engine := setupNestedDB(t)
	ctx := context.Background()

	// setupNestedDB seeds: author 1 has books 10,11; author 2 has book 12.
	// Add more books so each author has > the per-parent limit.
	for _, ddl := range []string{
		`INSERT INTO ni_books (id, author_id, title) VALUES (13,1,'A3'),(14,1,'A4'),(15,2,'B2'),(16,2,'B3')`,
	} {
		_, err := engine.Exec(ctx, ddl)
		require.NoError(t, err)
	}

	bookMeta := niBookMeta()
	authorMeta := niAuthorMeta()

	authorBooksRel := drel.RelationInfo{
		Name:        "Books",
		Type:        drel.HasMany,
		FKColumn:    "author_id",
		RelatedMeta: drel.ToMetaBase(&bookMeta),
		FieldSetter: func(parent any, related any) {
			a := parent.(*niAuthor)
			for _, it := range related.([]any) {
				a.Books = append(a.Books, it.(*niBook))
			}
		},
	}

	repo := drel.NewRepository(engine, authorMeta)
	spec := drel.NewIncludeSpec(&authorBooksRel).
		OrderBy(drel.NewOrderedCol[int]("id").Asc()).
		Limit(2)

	authors, err := repo.Include(spec).OrderBy(drel.NewOrderedCol[int]("id").Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, authors, 2)

	// Each parent must get its own 2 books — not 2 total.
	require.Len(t, authors[0].Books, 2, "author 1 should get its own 2 books")
	require.Len(t, authors[1].Books, 2, "author 2 should get its own 2 books")

	// OrderBy id ASC, Limit 2 => author 1 gets books 10,11; author 2 gets 12,15.
	assert.Equal(t, 10, authors[0].Books[0].ID)
	assert.Equal(t, 11, authors[0].Books[1].ID)
	assert.Equal(t, 12, authors[1].Books[0].ID)
	assert.Equal(t, 15, authors[1].Books[1].ID)
}

type miAuthor struct {
	ID   int
	Name string
	Tags []*miTag
}

type miTag struct {
	ID    int
	Label string
}

func miAuthorMeta() drel.ModelMeta[miAuthor] {
	return drel.ModelMeta[miAuthor]{
		Table:     "mi_authors",
		Columns:   []string{"id", "name"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*miAuthor, error) {
			a := &miAuthor{}
			return a, r.Scan(&a.ID, &a.Name)
		},
		PKValue:     func(a *miAuthor) any { return a.ID },
		ColumnValue: func(a *miAuthor, i int) any { return [...]any{a.ID, a.Name}[i] },
	}
}

func miTagMeta() drel.ModelMeta[miTag] {
	return drel.ModelMeta[miTag]{
		Table:     "mi_tags",
		Columns:   []string{"id", "label"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*miTag, error) {
			tg := &miTag{}
			return tg, r.Scan(&tg.ID, &tg.Label)
		},
		PKValue:     func(tg *miTag) any { return tg.ID },
		ColumnValue: func(tg *miTag, i int) any { return [...]any{tg.ID, tg.Label}[i] },
	}
}

func TestInclude_ManyToMany_OrderBy_SQLite(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()

	for _, ddl := range []string{
		`CREATE TABLE mi_authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE mi_tags (id INTEGER PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE mi_author_tags (author_id INTEGER NOT NULL, tag_id INTEGER NOT NULL, PRIMARY KEY (author_id, tag_id))`,
		`INSERT INTO mi_authors (id, name) VALUES (1,'Ann')`,
		// Insert tags so that label order (alpha) differs from id/pivot order.
		`INSERT INTO mi_tags (id, label) VALUES (1,'zeta'),(2,'alpha'),(3,'mid')`,
		// Pivot inserted in id order; without OrderBy the slice would be zeta,alpha,mid.
		`INSERT INTO mi_author_tags (author_id, tag_id) VALUES (1,1),(1,2),(1,3)`,
	} {
		_, err := engine.Exec(ctx, ddl)
		require.NoError(t, err)
	}

	authorMeta := miAuthorMeta()
	tagMeta := miTagMeta()
	tagsRel := drel.RelationInfo{
		Name:        "Tags",
		Type:        drel.ManyToMany,
		FKColumn:    "author_id",
		JoinTable:   "mi_author_tags",
		RefColumn:   "tag_id",
		RelatedMeta: drel.ToMetaBase(&tagMeta),
		FieldSetter: func(parent any, related any) {
			a := parent.(*miAuthor)
			for _, it := range related.([]any) {
				a.Tags = append(a.Tags, it.(*miTag))
			}
		},
	}

	repo := drel.NewRepository(engine, authorMeta)
	spec := drel.NewIncludeSpec(&tagsRel).OrderBy(drel.NewStringCol("label").Asc())
	ann, err := repo.Include(spec).Find(ctx, 1)
	require.NoError(t, err)
	require.Len(t, ann.Tags, 3)

	// Must follow the requested label ASC order, not pivot/id order.
	labels := []string{ann.Tags[0].Label, ann.Tags[1].Label, ann.Tags[2].Label}
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, labels)
}

func TestNestedInclude_ThreeLevels(t *testing.T) {
	engine := setupNestedDB(t)
	ctx := context.Background()

	authorMeta := niAuthorMeta()
	bookMeta := niBookMeta()
	reviewMeta := niReviewMeta()

	bookReviewsRel := drel.RelationInfo{
		Name:        "Reviews",
		Type:        drel.HasMany,
		FKColumn:    "book_id",
		RelatedMeta: drel.ToMetaBase(&reviewMeta),
		FieldSetter: func(parent any, related any) {
			b := parent.(*niBook)
			for _, it := range related.([]any) {
				b.Reviews = append(b.Reviews, it.(*niReview))
			}
		},
	}
	authorBooksRel := drel.RelationInfo{
		Name:        "Books",
		Type:        drel.HasMany,
		FKColumn:    "author_id",
		RelatedMeta: drel.ToMetaBase(&bookMeta),
		FieldSetter: func(parent any, related any) {
			a := parent.(*niAuthor)
			for _, it := range related.([]any) {
				a.Books = append(a.Books, it.(*niBook))
			}
		},
	}

	repo := drel.NewRepository(engine, authorMeta)
	nested := drel.NewIncludeSpec(&authorBooksRel).Then(drel.NewIncludeSpec(&bookReviewsRel))

	authors, err := repo.Include(nested).OrderBy(drel.NewOrderedCol[int]("id").Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, authors, 2)

	// Ann → 2 books; A1 has 2 reviews, A2 has 1 review.
	ann := authors[0]
	require.Equal(t, "Ann", ann.Name)
	require.Len(t, ann.Books, 2)

	reviewsByTitle := map[string]int{}
	for _, b := range ann.Books {
		reviewsByTitle[b.Title] = len(b.Reviews)
	}
	assert.Equal(t, 2, reviewsByTitle["A1"])
	assert.Equal(t, 1, reviewsByTitle["A2"])

	// Bob → 1 book with 1 review.
	bob := authors[1]
	require.Len(t, bob.Books, 1)
	assert.Len(t, bob.Books[0].Reviews, 1)
	assert.Equal(t, 2, bob.Books[0].Reviews[0].Stars)
}
