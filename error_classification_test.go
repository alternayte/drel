package drel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupConstraintDB(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, age INTEGER CHECK(age >= 0))`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id))`,
		`INSERT INTO parent (id, name, age) VALUES (1, 'alice', 30)`,
	} {
		_, err := engine.Exec(ctx, ddl)
		require.NoError(t, err)
	}
	return engine
}

func TestErrorClassification_RawExec(t *testing.T) {
	engine := setupConstraintDB(t)
	ctx := context.Background()

	cases := []struct {
		name string
		sql  string
		want error
	}{
		{"unique", `INSERT INTO parent (id, name) VALUES (2, 'alice')`, drel.ErrUniqueViolation},
		{"primary key", `INSERT INTO parent (id, name) VALUES (1, 'bob')`, drel.ErrUniqueViolation},
		{"not null", `INSERT INTO parent (id, name) VALUES (3, NULL)`, drel.ErrNotNullViolation},
		{"check", `INSERT INTO parent (id, name, age) VALUES (4, 'carol', -5)`, drel.ErrCheckViolation},
		{"foreign key", `INSERT INTO child (id, parent_id) VALUES (1, 999)`, drel.ErrForeignKeyViolation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := engine.Exec(ctx, c.sql)
			require.Error(t, err)
			assert.True(t, errors.Is(err, c.want),
				"want errors.Is(_, %v), got %v", c.want, err)
		})
	}
}

func TestErrorClassification_NoFalsePositive(t *testing.T) {
	engine := setupConstraintDB(t)
	ctx := context.Background()
	// A valid insert returns no error and is obviously not classified.
	_, err := engine.Exec(ctx, `INSERT INTO parent (id, name) VALUES (5, 'dave')`)
	require.NoError(t, err)
	// A non-constraint error (syntax) is returned unclassified.
	_, err = engine.Exec(ctx, `INSERT INTO nope (x) VALUES (1)`)
	require.Error(t, err)
	assert.False(t, errors.Is(err, drel.ErrUniqueViolation))
	assert.False(t, errors.Is(err, drel.ErrForeignKeyViolation))
}

// uniqItem maps the parent table for the flush path.
type uniqItem struct {
	ID        int
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func TestErrorClassification_FlushPath(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()
	_, err = engine.Exec(ctx, `CREATE TABLE uniq_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	meta := drel.ModelMeta[uniqItem]{
		Table: "uniq_items", Columns: []string{"id", "name", "created_at", "updated_at"}, PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*uniqItem, error) {
			it := &uniqItem{}
			return it, r.Scan(&it.ID, &it.Name, &it.CreatedAt, &it.UpdatedAt)
		},
		PKValue:       func(it *uniqItem) any { return it.ID },
		InsertColumns: func(it *uniqItem) ([]string, []any) { return []string{"name"}, []any{it.Name} },
		ScanReturning: func(it *uniqItem, row drel.Row) error {
			return row.Scan(&it.ID, &it.CreatedAt, &it.UpdatedAt)
		},
	}

	// First insert succeeds.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		drel.NewTxRepository(drel.MustFromContext(ctx), meta).Add(&uniqItem{Name: "x"})
		return nil
	}))

	// A duplicate name through the flush must surface ErrUniqueViolation.
	err = engine.WithTx(ctx, func(ctx context.Context) error {
		drel.NewTxRepository(drel.MustFromContext(ctx), meta).Add(&uniqItem{Name: "x"})
		return nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, drel.ErrUniqueViolation), "got %v", err)
}
