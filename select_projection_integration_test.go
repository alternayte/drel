//go:build integration

package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/dreltest/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgProjRow is the tracked model backing the projection integration test.
type pgProjRow struct {
	ID       int
	Name     string
	Category string
	Price    float64
}

var pgProjMeta = drel.ModelMeta[pgProjRow]{
	Table:     "proj_products",
	Columns:   []string{"id", "name", "category", "price"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*pgProjRow, error) {
		p := &pgProjRow{}
		err := row.Scan(&p.ID, &p.Name, &p.Category, &p.Price)
		return p, err
	},
	Snapshot: func(p *pgProjRow) any { return *p },
	Diff:     func(p *pgProjRow, snap any) []drel.FieldChange { return nil },
	PKValue:  func(p *pgProjRow) any { return p.ID },
	InsertColumns: func(p *pgProjRow) ([]string, []any) {
		return []string{"name", "category", "price"},
			[]any{p.Name, p.Category, p.Price}
	},
	ScanReturning: func(p *pgProjRow, row drel.Row) error {
		return row.Scan(&p.ID)
	},
}

// pgPriceNameDTO declares fields in the reverse of the projection order.
type pgPriceNameDTO struct {
	Price float64 `db:"price"`
	Name  string  `db:"name"`
}

func TestSelect_NameBinding_Postgres(t *testing.T) {
	engine := pgtest.NewPostgres(t)
	ctx := context.Background()

	_, err := engine.Exec(ctx, `
		CREATE TABLE proj_products (
			id       SERIAL PRIMARY KEY,
			name     TEXT NOT NULL,
			category TEXT NOT NULL,
			price    DOUBLE PRECISION NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = engine.Exec(ctx,
		"INSERT INTO proj_products (name, category, price) VALUES ($1,$2,$3),($4,$5,$6)",
		"Widget A", "electronics", 9.99,
		"Widget B", "electronics", 19.99)
	require.NoError(t, err)

	repo := drel.NewRepository(engine, pgProjMeta)
	priceCol := drel.NewOrderedCol[float64]("price")
	qb := repo.OrderBy(priceCol.Asc())

	// Projection order (name, price) differs from DTO struct order (Price, Name).
	results, err := drel.Select[pgPriceNameDTO](ctx, qb,
		drel.ColRef("name"), drel.ColRef("price"))
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "Widget A", results[0].Name)
	assert.InDelta(t, 9.99, results[0].Price, 0.001)
	assert.Equal(t, "Widget B", results[1].Name)
	assert.InDelta(t, 19.99, results[1].Price, 0.001)
}

func TestSelect_UnknownColumn_Postgres(t *testing.T) {
	engine := pgtest.NewPostgres(t)
	ctx := context.Background()

	_, err := engine.Exec(ctx, `
		CREATE TABLE proj_products (
			id       SERIAL PRIMARY KEY,
			name     TEXT NOT NULL,
			category TEXT NOT NULL,
			price    DOUBLE PRECISION NOT NULL
		)
	`)
	require.NoError(t, err)

	repo := drel.NewRepository(engine, pgProjMeta)
	qb := repo.OrderBy(drel.NewStringCol("name").Asc())

	type pgNameOnly struct {
		Name string `db:"name"`
	}
	_, err = drel.Select[pgNameOnly](ctx, qb,
		drel.ColRef("name"), drel.ColRef("price"))
	require.Error(t, err)
	assert.ErrorIs(t, err, drel.ErrUnknownProjectionColumn)
}
