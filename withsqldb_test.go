package drel

import (
	"context"
	"database/sql"
	"testing"
)

// WithSQLDB lets an application supply a driver drel does not depend on. The
// test uses the SQLite driver drel already registers, because the mechanism —
// not the driver — is what must hold.
func TestWithSQLDB_UsesTheSuppliedHandle(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1) // :memory: lives in one connection

	engine, err := NewEngine("", WithSQLDB(sqlDB, "sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	if got := engine.DialectName(); got != "sqlite" {
		t.Fatalf("DialectName() = %q, want %q", got, "sqlite")
	}

	ctx := context.Background()
	if _, err := engine.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Exec(ctx, "INSERT INTO t (id, name) VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := engine.QueryRow(ctx, "SELECT name FROM t WHERE id = 1").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "a" {
		t.Fatalf("name = %q, want %q", name, "a")
	}
}
