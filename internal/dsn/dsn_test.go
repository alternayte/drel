package dsn_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel/internal/dsn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectDialect(t *testing.T) {
	cases := map[string]string{
		"libsql://mydb.turso.io":    "libsql",
		"wss://mydb.turso.io":       "libsql",
		"ws://localhost:8080":       "libsql",
		"http://localhost:8080":     "libsql",
		"https://mydb.turso.io":     "libsql",
		"file:app.db":               "sqlite",
		"sqlite://app.db":           "sqlite",
		":memory:":                  "sqlite",
		"data.db":                   "sqlite",
		"postgres://u:p@host/db":    "postgres",
		"postgresql://u:p@host/db":  "postgres",
		"host=localhost user=admin": "postgres",
	}
	for in, want := range cases {
		assert.Equal(t, want, dsn.DetectDialect(in), "dsn=%s", in)
	}
}

func TestApplyAuthToken(t *testing.T) {
	assert.Equal(t, "libsql://db.turso.io?authToken=tok",
		dsn.ApplyAuthToken("libsql://db.turso.io", "tok"))
	assert.Equal(t, "libsql://db.turso.io?x=1&authToken=tok",
		dsn.ApplyAuthToken("libsql://db.turso.io?x=1", "tok"))
	// Empty token is a no-op.
	assert.Equal(t, "libsql://db.turso.io",
		dsn.ApplyAuthToken("libsql://db.turso.io", ""))
	// Already-present token is preserved.
	assert.Equal(t, "libsql://db.turso.io?authToken=a",
		dsn.ApplyAuthToken("libsql://db.turso.io?authToken=a", "b"))
}

func TestDetectDialect_LibpqKeyValueStaysPostgres(t *testing.T) {
	// A bare libpq key=value DSN must not be misread as sqlite/libsql.
	assert.Equal(t, "postgres", dsn.DetectDialect("host=localhost port=5432 dbname=app"))
}

func TestOpenDriver_SQLite(t *testing.T) {
	drv, err := dsn.OpenDriver(context.Background(), ":memory:", "")
	require.NoError(t, err)
	defer drv.Close()
	_, err = drv.Exec(context.Background(), "CREATE TABLE t (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
}
