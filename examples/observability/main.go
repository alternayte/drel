// Example: observability
//
// Demonstrates drel's production observability surface — all opt-in via Options
// passed to Open, plus the OnQuery hook:
//
//   - WithLogger + WithQueryLog: structured slog line for every query.
//   - WithSlowQueryThreshold: queries slower than the threshold are flagged.
//   - WithTracer: one span per executed query (OpenTelemetry-style). This
//     example plugs in a tiny in-memory tracer that counts spans.
//   - WithDevMode: development-time diagnostics — here it warns about an
//     unbounded SELECT (no LIMIT).
//   - OnQuery: a raw hook receiving SQL, args, duration, and error for custom
//     metrics/audit.
//
// Runs against in-memory SQLite (pure-Go modernc.org/sqlite, no CGO).
//
// Usage:
//
//	cd examples/observability
//	go run ../../cmd/drel generate   # generates catalog/product_drel.go + db/drel_gen.go
//	go run .
package main

//go:generate go run ../../cmd/drel generate

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/examples/observability/catalog"
	"github.com/alternayte/drel/examples/observability/db"
)

// countingTracer is a minimal drel.Tracer. A real one would create OpenTelemetry
// spans; here we just count them and print the span name.
type countingTracer struct{ spans int64 }

func (t *countingTracer) Start(ctx context.Context, name string) (context.Context, drel.Span) {
	atomic.AddInt64(&t.spans, 1)
	fmt.Printf("  [trace] start span %q\n", name)
	return ctx, spanStub{}
}

type spanStub struct{}

func (spanStub) End()                {}
func (spanStub) RecordError(e error) {}

func main() {
	ctx := context.Background()

	// Structured logger to stdout. We drop the time attribute so the example's
	// output is stable run-to-run; a real app would keep it.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))

	tracer := &countingTracer{}

	database, err := db.Open(":memory:",
		drel.WithLogger(logger),
		drel.WithQueryLog(true),                          // log every query
		drel.WithSlowQueryThreshold(50*time.Millisecond), // flag slow ones
		drel.WithTracer(tracer),                          // a span per query
		drel.WithDevMode(),                               // dev diagnostics
	)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer database.Close()

	// A raw query hook for custom metrics/audit, independent of the logger.
	var queryCount int64
	database.OnQuery(func(ctx context.Context, ev drel.QueryEvent) {
		atomic.AddInt64(&queryCount, 1)
		_ = ev // ev.SQL, ev.Args, ev.Duration, ev.Err available here
	})

	if _, err := database.Exec(ctx, `CREATE TABLE products (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT    NOT NULL,
		price      INTEGER NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		log.Fatal(err)
	}

	fmt.Println("=== Insert (watch the query log + spans) ===")
	err = database.WithTx(ctx, func(ctx context.Context) error {
		products := database.Tx(ctx).Products
		products.Add(catalog.NewProduct("Keyboard", 7999))
		products.Add(catalog.NewProduct("Mouse", 2999))
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n=== Bounded query (Where + Limit) ===")
	cheap, err := database.Products.
		Where(catalog.Products.Price.LT(5000)).
		OrderBy(catalog.Products.Price.Asc()).
		Take(10).
		All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  found %d product(s) under 5000\n", len(cheap))

	fmt.Println("\n=== Unbounded query (dev-mode should warn) ===")
	all, err := database.Products.All(ctx) // no LIMIT — dev-mode flags this
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  loaded all %d product(s)\n", len(all))

	fmt.Printf("\n=== Totals: %d queries observed, %d trace spans ===\n",
		atomic.LoadInt64(&queryCount), atomic.LoadInt64(&tracer.spans))
}
