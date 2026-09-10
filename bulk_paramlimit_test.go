package drel_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wideRow has many insertable columns, so a fixed 1000-row batch would exceed
// the per-statement parameter limit. drel must size batches by column count.
type wideRow struct {
	ID   int
	Vals [70]int
}

const wideCols = 70

func wideRowMeta() drel.ModelMeta[wideRow] {
	cols := make([]string, 0, wideCols+1)
	cols = append(cols, "id")
	for i := 0; i < wideCols; i++ {
		cols = append(cols, fmt.Sprintf("c%d", i))
	}
	insertCols := cols[1:]
	return drel.ModelMeta[wideRow]{
		Table:     "wide",
		Columns:   cols,
		PKColumns: []string{"id"},
		Scan: func(r drel.Row) (*wideRow, error) {
			w := &wideRow{}
			dest := make([]any, 0, wideCols+1)
			dest = append(dest, &w.ID)
			for i := range w.Vals {
				dest = append(dest, &w.Vals[i])
			}
			return w, r.Scan(dest...)
		},
		PKValue: func(w *wideRow) any { return w.ID },
		InsertColumns: func(w *wideRow) ([]string, []any) {
			vals := make([]any, wideCols)
			for i := range w.Vals {
				vals[i] = w.Vals[i]
			}
			return insertCols, vals
		},
	}
}

func TestBulkInsert_WideTableRespectsParamLimit(t *testing.T) {
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	defer engine.Close()
	ctx := context.Background()

	// Build the 71-column table.
	defs := []string{"id INTEGER PRIMARY KEY AUTOINCREMENT"}
	for i := 0; i < wideCols; i++ {
		defs = append(defs, fmt.Sprintf("c%d INTEGER NOT NULL", i))
	}
	_, err = engine.Exec(ctx, "CREATE TABLE wide ("+strings.Join(defs, ", ")+")")
	require.NoError(t, err)

	repo := drel.NewRepository(engine, wideRowMeta())

	// 600 rows * 70 cols = 42000 params — over SQLite's 32766 limit if done in
	// one batch, so this only succeeds because drel sizes batches by column count.
	rows := make([]*wideRow, 600)
	for i := range rows {
		rows[i] = &wideRow{}
		rows[i].Vals[0] = i
	}
	n, err := repo.BulkInsert(ctx, rows)
	require.NoError(t, err)
	assert.Equal(t, 600, n)

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 600, count)
}
