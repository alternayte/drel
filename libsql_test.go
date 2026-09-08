package drel

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestDetectDialect_LibSQL(t *testing.T) {
	cases := map[string]string{
		"libsql://db.turso.io":    "libsql",
		"wss://db.turso.io":       "libsql",
		"ws://localhost:8080":     "libsql",
		"http://localhost:8080":   "libsql",
		"https://db.turso.io":     "libsql",
		"file:app.db":             "sqlite",
		":memory:":                "sqlite",
		"postgres://localhost/x":  "postgres",
		"postgresql://localhost/": "postgres",
	}
	for dsn, want := range cases {
		if got := detectDialect(dsn); got != want {
			t.Errorf("detectDialect(%q) = %q, want %q", dsn, got, want)
		}
	}
}

func TestDetectDialect_SQLiteFilePaths(t *testing.T) {
	cases := map[string]string{
		// extension-based SQLite detection
		"app.sqlite":          "sqlite",
		"data.sqlite3":        "sqlite",
		"/var/lib/app.sqlite": "sqlite",
		"./db/app.sqlite3":    "sqlite",
		"app.db":              "sqlite", // existing behavior preserved
		"file:app.sqlite":     "sqlite",
		// bare path (no scheme, looks like a file) -> SQLite
		"./var/app":            "sqlite",
		"../data/store":        "sqlite",
		"data/app.bin":         "sqlite",
		"/absolute/path/store": "sqlite",
		// Postgres shapes preserved
		"postgres://localhost/x":    "postgres",
		"postgresql://localhost/":   "postgres",
		"localhost:5432":            "postgres",
		"db.internal:5432":          "postgres",
		"user@dbhost":               "postgres",
		"host=localhost dbname=app": "postgres",
	}
	for dsn, want := range cases {
		if got := detectDialect(dsn); got != want {
			t.Errorf("detectDialect(%q) = %q, want %q", dsn, got, want)
		}
	}
}

func TestWarnWSTransport_LogsForWSScheme(t *testing.T) {
	cases := map[string]bool{
		"ws://localhost:8080":  true,
		"wss://db.turso.io":    true,
		"libsql://db.turso.io": false,
		"https://db.turso.io":  false,
		"file:app.db":          false,
		":memory:":             false,
	}
	for dsn, wantWarn := range cases {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		e := &Engine{logger: logger}
		e.warnWSTransport(context.Background(), dsn)
		got := strings.Contains(buf.String(), "websocket")
		if got != wantWarn {
			t.Errorf("warnWSTransport(%q): logged-websocket-warning=%v, want %v (output=%q)", dsn, got, wantWarn, buf.String())
		}
	}
}

func TestApplyAuthToken(t *testing.T) {
	if got := applyAuthToken("libsql://db.turso.io", "tok"); got != "libsql://db.turso.io?authToken=tok" {
		t.Errorf("got %q", got)
	}
	if got := applyAuthToken("libsql://db.turso.io?x=1", "tok"); got != "libsql://db.turso.io?x=1&authToken=tok" {
		t.Errorf("got %q", got)
	}
	// No token → unchanged.
	if got := applyAuthToken("libsql://db.turso.io", ""); got != "libsql://db.turso.io" {
		t.Errorf("got %q", got)
	}
	// Existing token → unchanged.
	if got := applyAuthToken("libsql://db.turso.io?authToken=abc", "tok"); got != "libsql://db.turso.io?authToken=abc" {
		t.Errorf("got %q", got)
	}
}
