// Package introspect reads the live schema of a database into the same
// structures codegen builds from the Go models, so the two can be compared.
//
// drel's migration differ works from a snapshot of what drel itself generated.
// A constraint or an index written by hand is absent from that snapshot, so
// every later migration is planned as though it did not exist. Introspection
// makes such an object visible: `drel migrate verify` reports it.
package introspect

import (
	"context"
	"fmt"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/alternayte/drel/internal/driver"
)

// Querier is the part of driver.Driver introspection needs.
type Querier interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
}

// Schema reads the live schema for the given dialect.
func Schema(ctx context.Context, q Querier, dialect string) (codegen.Schema, error) {
	switch dialect {
	case "sqlite", "libsql":
		return sqliteSchema(ctx, q)
	case "postgres":
		return postgresSchema(ctx, q)
	default:
		return codegen.Schema{}, fmt.Errorf("introspect: unknown dialect %q", dialect)
	}
}

// collectRows runs a query and calls scan for each row.
func collectRows(ctx context.Context, q Querier, query string, scan func(driver.Rows) error) error {
	rows, err := q.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// table returns the named table of s, adding it when absent.
func table(s *codegen.Schema, name string) *codegen.Table {
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	s.Tables = append(s.Tables, codegen.Table{Name: name})
	return &s.Tables[len(s.Tables)-1]
}

// column returns the named column of t, or nil.
func column(t *codegen.Table, name string) *codegen.Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}
