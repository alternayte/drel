// Package libsqldriver implements driver.Driver for libSQL / Turso using the
// database/sql libsql driver.
//
// The upstream client (github.com/tursodatabase/libsql-client-go) carries a
// deprecation notice on its repository, while Turso's own Go SDK reference
// still names it the package for remote Turso Cloud access. It stays here
// because it is the only pure-Go option: go-libsql needs CGO, and the tursogo
// packages target the newer engine and have no tagged release. A caller who
// wants either one supplies it through drel.WithSQLDB, so drel does not take
// the dependency.
package libsqldriver

import (
	"database/sql"
	"fmt"

	"github.com/alternayte/drel/internal/driver"
	"github.com/alternayte/drel/internal/driver/sqldriver"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

// New opens a libSQL database at the given DSN (e.g. "libsql://name.turso.io?authToken=...").
func New(dsn string, pc ...driver.PoolConfig) (*sqldriver.Driver, error) {
	db, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("libsqldriver: open: %w", err)
	}
	sqldriver.ApplyPoolConfig(db, pc...)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("libsqldriver: open: %w", err)
	}
	return sqldriver.New(db), nil
}
