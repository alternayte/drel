//go:build integration

package drel_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test models ---

type Author struct {
	ID        int
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Books     []*Book
	Profile   *AuthorProfile
	Tags      []*Tag
}

type Tag struct {
	ID        int
	Label     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Book struct {
	ID        int
	Title     string
	AuthorID  int
	CreatedAt time.Time
	UpdatedAt time.Time
	Author    *Author
}

type AuthorProfile struct {
	ID        int
	Bio       string
	AuthorID  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// --- ModelMeta definitions ---

var authorMeta = drel.ModelMeta[Author]{
	Table:     "authors",
	Columns:   []string{"id", "name", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*Author, error) {
		a := &Author{}
		err := row.Scan(&a.ID, &a.Name, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return nil, err
		}
		return a, nil
	},
	PKValue: func(a *Author) any {
		return a.ID
	},
	ColumnValue: func(a *Author, idx int) any {
		switch idx {
		case 0:
			return a.ID
		case 1:
			return a.Name
		case 2:
			return a.CreatedAt
		case 3:
			return a.UpdatedAt
		}
		return nil
	},
	Snapshot: func(a *Author) any {
		return a.Name
	},
	Diff: func(a *Author, snap any) []drel.FieldChange {
		if a.Name != snap.(string) {
			return []drel.FieldChange{{Column: "name", Value: a.Name}}
		}
		return nil
	},
	InsertColumns: func(a *Author) ([]string, []any) {
		return []string{"name"}, []any{a.Name}
	},
	ScanReturning: func(a *Author, row drel.Row) error {
		return row.Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
	},
}

var bookMeta = drel.ModelMeta[Book]{
	Table:     "books",
	Columns:   []string{"id", "title", "author_id", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*Book, error) {
		b := &Book{}
		err := row.Scan(&b.ID, &b.Title, &b.AuthorID, &b.CreatedAt, &b.UpdatedAt)
		if err != nil {
			return nil, err
		}
		return b, nil
	},
	PKValue: func(b *Book) any {
		return b.ID
	},
	ColumnValue: func(b *Book, idx int) any {
		switch idx {
		case 0:
			return b.ID
		case 1:
			return b.Title
		case 2:
			return b.AuthorID
		case 3:
			return b.CreatedAt
		case 4:
			return b.UpdatedAt
		}
		return nil
	},
	Snapshot: func(b *Book) any {
		return b.Title
	},
	Diff: func(b *Book, snap any) []drel.FieldChange {
		if b.Title != snap.(string) {
			return []drel.FieldChange{{Column: "title", Value: b.Title}}
		}
		return nil
	},
	InsertColumns: func(b *Book) ([]string, []any) {
		return []string{"title", "author_id"}, []any{b.Title, b.AuthorID}
	},
	ScanReturning: func(b *Book, row drel.Row) error {
		return row.Scan(&b.ID, &b.CreatedAt, &b.UpdatedAt)
	},
}

var profileMeta = drel.ModelMeta[AuthorProfile]{
	Table:     "author_profiles",
	Columns:   []string{"id", "bio", "author_id", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*AuthorProfile, error) {
		p := &AuthorProfile{}
		err := row.Scan(&p.ID, &p.Bio, &p.AuthorID, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		return p, nil
	},
	PKValue: func(p *AuthorProfile) any {
		return p.ID
	},
	ColumnValue: func(p *AuthorProfile, idx int) any {
		switch idx {
		case 0:
			return p.ID
		case 1:
			return p.Bio
		case 2:
			return p.AuthorID
		case 3:
			return p.CreatedAt
		case 4:
			return p.UpdatedAt
		}
		return nil
	},
	Snapshot: func(p *AuthorProfile) any {
		return p.Bio
	},
	Diff: func(p *AuthorProfile, snap any) []drel.FieldChange {
		if p.Bio != snap.(string) {
			return []drel.FieldChange{{Column: "bio", Value: p.Bio}}
		}
		return nil
	},
	InsertColumns: func(p *AuthorProfile) ([]string, []any) {
		return []string{"bio", "author_id"}, []any{p.Bio, p.AuthorID}
	},
	ScanReturning: func(p *AuthorProfile, row drel.Row) error {
		return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	},
}

var tagMeta = drel.ModelMeta[Tag]{
	Table:     "tags",
	Columns:   []string{"id", "label", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*Tag, error) {
		t := &Tag{}
		err := row.Scan(&t.ID, &t.Label, &t.CreatedAt, &t.UpdatedAt)
		return t, err
	},
	PKValue: func(t *Tag) any { return t.ID },
	ColumnValue: func(t *Tag, idx int) any {
		switch idx {
		case 0:
			return t.ID
		case 1:
			return t.Label
		case 2:
			return t.CreatedAt
		case 3:
			return t.UpdatedAt
		}
		return nil
	},
}

// --- Meta base conversions for RelationInfo ---

var bookMetaBase = drel.ToMetaBase(&bookMeta)
var profileMetaBase = drel.ToMetaBase(&profileMeta)
var authorMetaBase = drel.ToMetaBase(&authorMeta)
var tagMetaBase = drel.ToMetaBase(&tagMeta)

// --- RelationInfo definitions ---

var booksRelation = &drel.RelationInfo{
	Name:        "Books",
	Type:        drel.HasMany,
	FKColumn:    "author_id",
	RelatedMeta: bookMetaBase,
	FieldSetter: func(parent any, related any) {
		a := parent.(*Author)
		items := related.([]any)
		books := make([]*Book, len(items))
		for i, item := range items {
			books[i] = item.(*Book)
		}
		a.Books = books
	},
}

var profileRelation = &drel.RelationInfo{
	Name:        "Profile",
	Type:        drel.HasOne,
	FKColumn:    "author_id",
	RelatedMeta: profileMetaBase,
	FieldSetter: func(parent any, related any) {
		a := parent.(*Author)
		a.Profile = related.(*AuthorProfile)
	},
}

var authorRelation = &drel.RelationInfo{
	Name:        "Author",
	Type:        drel.BelongsTo,
	FKColumn:    "author_id",
	RelatedMeta: authorMetaBase,
	FieldSetter: func(parent any, related any) {
		b := parent.(*Book)
		b.Author = related.(*Author)
	},
}

var tagsRelation = &drel.RelationInfo{
	Name:        "Tags",
	Type:        drel.ManyToMany,
	FKColumn:    "author_id",
	JoinTable:   "author_tags",
	RefColumn:   "tag_id",
	RelatedMeta: tagMetaBase,
	FieldSetter: func(parent any, related any) {
		a := parent.(*Author)
		items := related.([]any)
		tags := make([]*Tag, len(items))
		for i, item := range items {
			tags[i] = item.(*Tag)
		}
		a.Tags = tags
	},
}

// --- Test helpers ---

func setupRelationDB(t *testing.T) *drel.Engine {
	t.Helper()
	engine := setupTestDB(t)
	ctx := context.Background()

	_, err := engine.Exec(ctx, `
		CREATE TABLE authors (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	require.NoError(t, err)

	_, err = engine.Exec(ctx, `
		CREATE TABLE books (
			id         SERIAL PRIMARY KEY,
			title      TEXT NOT NULL,
			author_id  INTEGER NOT NULL REFERENCES authors(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	require.NoError(t, err)

	_, err = engine.Exec(ctx, `
		CREATE TABLE author_profiles (
			id         SERIAL PRIMARY KEY,
			bio        TEXT NOT NULL,
			author_id  INTEGER NOT NULL UNIQUE REFERENCES authors(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	require.NoError(t, err)

	_, err = engine.Exec(ctx, `
		CREATE TABLE tags (
			id         SERIAL PRIMARY KEY,
			label      TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	require.NoError(t, err)

	_, err = engine.Exec(ctx, `
		CREATE TABLE author_tags (
			author_id INTEGER NOT NULL REFERENCES authors(id),
			tag_id    INTEGER NOT NULL REFERENCES tags(id),
			PRIMARY KEY (author_id, tag_id)
		)
	`)
	require.NoError(t, err)

	return engine
}

func seedRelationData(t *testing.T, engine *drel.Engine) {
	t.Helper()
	ctx := context.Background()

	// Authors
	_, err := engine.Exec(ctx, "INSERT INTO authors (id, name) VALUES (1, 'Alice'), (2, 'Bob')")
	require.NoError(t, err)

	// Books
	_, err = engine.Exec(ctx, `
		INSERT INTO books (title, author_id) VALUES
			('Book A1', 1),
			('Book A2', 1),
			('Book A3', 1),
			('Book B1', 2)
	`)
	require.NoError(t, err)

	// Profiles
	_, err = engine.Exec(ctx, `
		INSERT INTO author_profiles (bio, author_id) VALUES
			('Alice writes things', 1),
			('Bob also writes', 2)
	`)
	require.NoError(t, err)

	// Tags
	_, err = engine.Exec(ctx, `
		INSERT INTO tags (id, label) VALUES (1, 'fiction'), (2, 'tech'), (3, 'science')
	`)
	require.NoError(t, err)

	// Author-Tag associations: Alice=fiction+tech, Bob=tech+science
	_, err = engine.Exec(ctx, `
		INSERT INTO author_tags (author_id, tag_id) VALUES (1, 1), (1, 2), (2, 2), (2, 3)
	`)
	require.NoError(t, err)
}

// --- Tests ---

func TestIntegration_Include_HasMany(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	repo := drel.NewRepository(engine, authorMeta)
	ctx := context.Background()

	alice, err := repo.Include(drel.NewIncludeSpec(booksRelation)).Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Alice", alice.Name)
	require.Len(t, alice.Books, 3)

	titles := make([]string, len(alice.Books))
	for i, b := range alice.Books {
		titles[i] = b.Title
	}
	assert.ElementsMatch(t, []string{"Book A1", "Book A2", "Book A3"}, titles)
}

func TestIntegration_Include_HasMany_Empty(t *testing.T) {
	engine := setupRelationDB(t)
	ctx := context.Background()

	// Insert an author with no books.
	_, err := engine.Exec(ctx, "INSERT INTO authors (id, name) VALUES (1, 'Lonely')")
	require.NoError(t, err)

	repo := drel.NewRepository(engine, authorMeta)
	author, err := repo.Include(drel.NewIncludeSpec(booksRelation)).Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Lonely", author.Name)
	assert.NotNil(t, author.Books)
	assert.Len(t, author.Books, 0)
}

func TestIntegration_Include_HasOne(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	repo := drel.NewRepository(engine, authorMeta)
	ctx := context.Background()

	alice, err := repo.Include(drel.NewIncludeSpec(profileRelation)).Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Alice", alice.Name)
	require.NotNil(t, alice.Profile)
	assert.Equal(t, "Alice writes things", alice.Profile.Bio)
	assert.Equal(t, 1, alice.Profile.AuthorID)
}

func TestIntegration_Include_BelongsTo(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	bookRepo := drel.NewRepository(engine, bookMeta)
	ctx := context.Background()

	book, err := bookRepo.Include(drel.NewIncludeSpec(authorRelation)).Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Book A1", book.Title)
	require.NotNil(t, book.Author)
	assert.Equal(t, "Alice", book.Author.Name)
	assert.Equal(t, 1, book.Author.ID)
}

func TestIntegration_Include_MultipleParents(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	repo := drel.NewRepository(engine, authorMeta)
	ctx := context.Background()

	authors, err := repo.Include(drel.NewIncludeSpec(booksRelation)).All(ctx)
	require.NoError(t, err)
	require.Len(t, authors, 2)

	// Build a map for easier assertions (order may vary).
	booksByAuthor := make(map[string]int)
	for _, a := range authors {
		booksByAuthor[a.Name] = len(a.Books)
	}
	assert.Equal(t, 3, booksByAuthor["Alice"])
	assert.Equal(t, 1, booksByAuthor["Bob"])
}

func TestIntegration_Include_ManyToMany(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	repo := drel.NewRepository(engine, authorMeta)
	ctx := context.Background()

	alice, err := repo.Include(drel.NewIncludeSpec(tagsRelation)).Find(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Alice", alice.Name)
	require.Len(t, alice.Tags, 2)

	labels := make([]string, len(alice.Tags))
	for i, tag := range alice.Tags {
		labels[i] = tag.Label
	}
	assert.ElementsMatch(t, []string{"fiction", "tech"}, labels)
}

func TestIntegration_Include_ManyToMany_AllParents(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	repo := drel.NewRepository(engine, authorMeta)
	ctx := context.Background()

	authors, err := repo.Include(drel.NewIncludeSpec(tagsRelation)).All(ctx)
	require.NoError(t, err)
	require.Len(t, authors, 2)

	tagsByAuthor := make(map[string][]string)
	for _, a := range authors {
		labels := make([]string, len(a.Tags))
		for i, tag := range a.Tags {
			labels[i] = tag.Label
		}
		tagsByAuthor[a.Name] = labels
	}
	assert.ElementsMatch(t, []string{"fiction", "tech"}, tagsByAuthor["Alice"])
	assert.ElementsMatch(t, []string{"tech", "science"}, tagsByAuthor["Bob"])
}

func TestIntegration_Include_FilterAware_SoftDelete(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()

	// Create tables with soft delete on books.
	_, err := engine.Exec(ctx, `
		CREATE TABLE authors (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `
		CREATE TABLE books (
			id         SERIAL PRIMARY KEY,
			title      TEXT NOT NULL,
			author_id  INTEGER NOT NULL REFERENCES authors(id),
			deleted_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	require.NoError(t, err)

	// Seed: Alice with 3 books, one soft-deleted.
	_, err = engine.Exec(ctx, "INSERT INTO authors (id, name) VALUES (1, 'Alice')")
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO books (title, author_id) VALUES ('Active Book 1', 1), ('Active Book 2', 1)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO books (title, author_id, deleted_at) VALUES ('Deleted Book', 1, now())`)
	require.NoError(t, err)

	// Create a book meta WITH soft delete filter and deleted_at column.
	sdBookMeta := drel.ModelMeta[Book]{
		Table:     "books",
		Columns:   []string{"id", "title", "author_id", "deleted_at", "created_at", "updated_at"},
		PKColumns: []string{"id"},
		Scan: func(row drel.Row) (*Book, error) {
			b := &Book{}
			var deletedAt *time.Time
			err := row.Scan(&b.ID, &b.Title, &b.AuthorID, &deletedAt, &b.CreatedAt, &b.UpdatedAt)
			return b, err
		},
		PKValue:       func(b *Book) any { return b.ID },
		ColumnValue:   bookMeta.ColumnValue,
		HasSoftDelete: true,
		Filters:       []drel.NamedFilter{drel.SoftDeleteFilter},
	}
	sdBookMetaBase := drel.ToMetaBase(&sdBookMeta)

	sdBooksRelation := &drel.RelationInfo{
		Name:        "Books",
		Type:        drel.HasMany,
		FKColumn:    "author_id",
		RelatedMeta: sdBookMetaBase,
		FieldSetter: booksRelation.FieldSetter,
	}

	repo := drel.NewRepository(engine, authorMeta)

	// Default: soft-deleted books should be excluded.
	alice, err := repo.Include(drel.NewIncludeSpec(sdBooksRelation)).Find(ctx, 1)
	require.NoError(t, err)
	assert.Len(t, alice.Books, 2)

	// Unscoped: all books including soft-deleted.
	aliceAll, err := repo.Include(drel.NewIncludeSpec(sdBooksRelation).Unscoped()).Find(ctx, 1)
	require.NoError(t, err)
	assert.Len(t, aliceAll.Books, 3)
}

func TestIntegration_Include_Limit_PerParent(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	repo := drel.NewRepository(engine, authorMeta)
	ctx := context.Background()

	spec := drel.NewIncludeSpec(booksRelation).
		OrderBy(drel.NewStringCol("title").Asc()).
		Limit(2)

	authors, err := repo.Include(spec).OrderBy(drel.NewOrderedCol[int]("id").Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, authors, 2)

	// Alice has 3 books but Limit(2) per parent => exactly 2; Bob has 1.
	booksByAuthor := map[string][]string{}
	for _, a := range authors {
		titles := make([]string, len(a.Books))
		for i, b := range a.Books {
			titles[i] = b.Title
		}
		booksByAuthor[a.Name] = titles
	}
	require.Len(t, booksByAuthor["Alice"], 2, "per-parent limit must cap Alice at 2, not the batch at 2")
	require.Len(t, booksByAuthor["Bob"], 1)
	// Title ASC within Alice's partition.
	assert.Equal(t, []string{"Book A1", "Book A2"}, booksByAuthor["Alice"])
}

func TestIntegration_Include_ManyToMany_OrderBy(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	repo := drel.NewRepository(engine, authorMeta)
	ctx := context.Background()

	spec := drel.NewIncludeSpec(tagsRelation).OrderBy(drel.NewStringCol("label").Asc())
	alice, err := repo.Include(spec).Find(ctx, 1)
	require.NoError(t, err)
	require.Len(t, alice.Tags, 2)

	// label ASC => fiction before tech (regardless of pivot/id order).
	assert.Equal(t, []string{"fiction", "tech"}, []string{alice.Tags[0].Label, alice.Tags[1].Label})
}

func TestIntegration_Include_NestedManyToMany(t *testing.T) {
	engine := setupRelationDB(t)
	seedRelationData(t, engine)
	ctx := context.Background()

	// Add a books<->genres M2M reachable via Author -> Books -> Genres.
	_, err := engine.Exec(ctx, `CREATE TABLE genres (id SERIAL PRIMARY KEY, name TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `CREATE TABLE book_genres (book_id INTEGER NOT NULL REFERENCES books(id), genre_id INTEGER NOT NULL REFERENCES genres(id), PRIMARY KEY (book_id, genre_id))`)
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `INSERT INTO genres (id, name) VALUES (1,'epic'),(2,'noir')`)
	require.NoError(t, err)
	// Book 1 (Alice's Book A1) -> epic, noir.
	_, err = engine.Exec(ctx, `INSERT INTO book_genres (book_id, genre_id) VALUES (1,1),(1,2)`)
	require.NoError(t, err)

	type genre struct {
		ID   int
		Name string
	}
	genreMeta := drel.ModelMeta[genre]{
		Table:     "genres",
		Columns:   []string{"id", "name"},
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*genre, error) {
			g := &genre{}
			return g, r.Scan(&g.ID, &g.Name)
		},
		PKValue:     func(g *genre) any { return g.ID },
		ColumnValue: func(g *genre, i int) any { return [...]any{g.ID, g.Name}[i] },
	}
	// Extend Book with a Genres field via a local relation that stuffs into a map
	// we can assert on; FieldSetter records onto the book pointer's title->genres.
	collected := map[string][]string{}
	booksGenresRel := &drel.RelationInfo{
		Name:        "Genres",
		Type:        drel.ManyToMany,
		FKColumn:    "book_id",
		JoinTable:   "book_genres",
		RefColumn:   "genre_id",
		RelatedMeta: drel.ToMetaBase(&genreMeta),
		FieldSetter: func(parent any, related any) {
			b := parent.(*Book)
			for _, it := range related.([]any) {
				collected[b.Title] = append(collected[b.Title], it.(*genre).Name)
			}
		},
	}

	repo := drel.NewRepository(engine, authorMeta)
	nested := drel.NewIncludeSpec(booksRelation).
		Then(drel.NewIncludeSpec(booksGenresRel).OrderBy(drel.NewStringCol("name").Asc()))
	_, err = repo.Include(nested).All(ctx)
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"epic", "noir"}, collected["Book A1"])
}
