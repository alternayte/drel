package drel

import (
	"context"
	"strings"
	"testing"
)

// shapeRow returns a variable column list depending on Variant, simulating a
// custom ModelMeta whose InsertColumns is not fixed-shape.
type shapeRow struct {
	ID      int
	Name    string
	Extra   string
	Variant int
}

func shapeRowMeta() ModelMeta[shapeRow] {
	return ModelMeta[shapeRow]{
		Table:     "shape",
		Columns:   []string{"id", "name", "extra"},
		PKColumns: []string{"id"},
		Scan: func(r Row) (*shapeRow, error) {
			s := &shapeRow{}
			return s, r.Scan(&s.ID, &s.Name, &s.Extra)
		},
		PKValue: func(s *shapeRow) any { return s.ID },
		InsertColumns: func(s *shapeRow) ([]string, []any) {
			if s.Variant == 1 {
				// Different shape: drops "extra".
				return []string{"name"}, []any{s.Name}
			}
			return []string{"name", "extra"}, []any{s.Name, s.Extra}
		},
	}
}

func TestBulkInsert_NonUniformShape_ReturnsError(t *testing.T) {
	eng, err := NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	ctx := context.Background()
	if _, err := eng.Exec(ctx, `CREATE TABLE shape (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		extra TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(eng, shapeRowMeta())

	rows := []*shapeRow{
		{Name: "a", Extra: "x", Variant: 0},
		{Name: "b", Variant: 1}, // different column shape
	}
	n, err := repo.BulkInsert(ctx, rows)
	if err == nil {
		t.Fatal("expected an error for non-uniform bulk row shape, got nil")
	}
	if !strings.Contains(err.Error(), "column shape") {
		t.Fatalf("expected a column-shape error, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 on shape error, got %d", n)
	}
	cnt, err := repo.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("expected nothing persisted on shape error, got %d", cnt)
	}
}

func TestBulkInsert_UniformShape_Succeeds(t *testing.T) {
	eng, err := NewEngine(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	ctx := context.Background()
	if _, err := eng.Exec(ctx, `CREATE TABLE shape (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		extra TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(eng, shapeRowMeta())

	rows := []*shapeRow{
		{Name: "a", Extra: "x"},
		{Name: "b", Extra: "y"},
	}
	n, err := repo.BulkInsert(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 inserted, got %d", n)
	}
}
