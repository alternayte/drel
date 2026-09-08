package dreltest

import (
	"context"
	"errors"

	"github.com/alternayte/drel"
)

// testingTB is the subset of *testing.T the dreltest helpers use. It lets the
// helpers be exercised by a fake in dreltest's own tests; production callers
// always pass *testing.T.
type testingTB interface {
	Helper()
	Cleanup(func())
	Fatalf(format string, args ...any)
}

// Option configures NewSQLite.
type Option func(*config)

type config struct {
	migrationsDir string
	schemaDDL     []string
	seedFn        func(*drel.Engine) error
}

// WithSeed runs a seed function after the database is created and after any
// schema/migrations have been applied. A non-nil error fails the test.
func WithSeed(fn func(*drel.Engine) error) Option {
	return func(c *config) { c.seedFn = fn }
}

// WithSchema applies raw DDL statements (e.g. CREATE TABLE) after the database
// is created, each in order. A failing statement fails the test.
func WithSchema(ddl ...string) Option {
	return func(c *config) { c.schemaDDL = append(c.schemaDDL, ddl...) }
}

// WithMigrations applies all pending up-migrations from dir against the test
// engine. The migration files must be written for SQLite (the test engine's
// dialect); see CreateSchema for raw-DDL setup.
func WithMigrations(dir string) Option {
	return func(c *config) { c.migrationsDir = dir }
}

// NewSQLite creates an in-memory SQLite engine for testing, applying any options
// (WithMigrations, WithSchema, WithSeed) in that order. The engine is
// automatically closed when the test completes.
func NewSQLite(t testingTB, opts ...Option) *drel.Engine {
	t.Helper()
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	engine, err := drel.NewEngine(":memory:")
	if err != nil {
		t.Fatalf("dreltest.NewSQLite: %v", err)
	}
	t.Cleanup(func() { engine.Close() })

	ctx := context.Background()
	if cfg.migrationsDir != "" {
		if _, err := engine.ApplyMigrations(ctx, cfg.migrationsDir); err != nil {
			t.Fatalf("dreltest.NewSQLite: migrations: %v", err)
		}
	}
	for _, ddl := range cfg.schemaDDL {
		if _, err := engine.Exec(ctx, ddl); err != nil {
			t.Fatalf("dreltest.NewSQLite: schema %q: %v", ddl, err)
		}
	}
	if cfg.seedFn != nil {
		if err := cfg.seedFn(engine); err != nil {
			t.Fatalf("dreltest.NewSQLite: seed: %v", err)
		}
	}
	return engine
}

// CreateSchema executes each DDL statement against the engine in order, failing
// the test on the first error. Convenience for tests that already hold an engine
// (it is the helper the PRD's hand-waved createSchema(t, engine) refers to).
func CreateSchema(t testingTB, engine *drel.Engine, ddl ...string) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range ddl {
		if _, err := engine.Exec(ctx, stmt); err != nil {
			t.Fatalf("dreltest.CreateSchema: %q: %v", stmt, err)
		}
	}
}

// Begin opens a transaction-scoped sandbox against a SQLite (or libSQL) engine:
// every write made through the returned *drel.Tx is rolled back when the test
// completes, giving per-test isolation without recreating the schema. Unlike a
// raw SAVEPOINT through the connection pool, this drives the engine's own
// single-connection transaction, so isolation holds for file-backed SQLite too
// (not just in-memory). Begin fails the test on a non-SQLite engine.
func Begin(t testingTB, engine *drel.Engine) *drel.Tx {
	t.Helper()
	if engine.DialectName() != "sqlite" {
		t.Fatalf("dreltest.Begin: requires a SQLite/libSQL engine, got dialect %q", engine.DialectName())
		return nil
	}
	tx, err := park(t, engine)
	if err != nil {
		t.Fatalf("dreltest.Begin: %v", err)
		return nil
	}
	return tx
}

// WithRollback runs fn inside a transaction and rolls that transaction back.
// The transaction travels in the context, so code that calls Engine.WithTx
// joins it through a savepoint instead of opening its own transaction. Nothing
// fn writes reaches the next test.
//
// Use it for every test that touches the database. On Postgres the test
// functions can then run in parallel against one database, and no test needs to
// recreate the schema.
//
//	func TestPlaceOrder(t *testing.T) {
//	    dreltest.WithRollback(t, engine, func(ctx context.Context) {
//	        err := service.PlaceOrder(ctx, order) // calls db.WithTx inside
//	        require.NoError(t, err)
//	    })
//	}
//
// The rollback runs at test cleanup, so it also runs after t.Fatal. A failed
// test does not leak an open transaction.
//
// Three limits hold.
//
// A test that needs a second connection to read fn's writes cannot use
// WithRollback, because nothing is committed.
//
// On Postgres the transaction holds one pooled connection for the whole test,
// so the pool must be larger than the count of parallel tests.
//
// A SQLite engine drives one connection. Two parked transactions therefore
// cannot exist at one time, and two parallel WithRollback calls on one SQLite
// engine deadlock. Run the SQLite tests one after another, or give each test
// its own engine with NewSQLite.
func WithRollback(t testingTB, engine *drel.Engine, fn func(ctx context.Context)) {
	t.Helper()
	tx, err := park(t, engine)
	if err != nil {
		t.Fatalf("dreltest.WithRollback: %v", err)
		return
	}
	fn(drel.ContextWithTx(context.Background(), tx))
}

// park opens a transaction and holds it open in its own goroutine. The
// transaction rolls back at test cleanup, which the testing package runs even
// after t.Fatal unwinds the test goroutine.
func park(t testingTB, engine *drel.Engine) (*drel.Tx, error) {
	type ready struct {
		tx  *drel.Tx
		err error
	}
	readyCh := make(chan ready, 1)
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		// errRelease unwinds fn so Transaction rolls back the single driver.Tx.
		err := engine.Transaction(context.Background(), func(tx *drel.Tx) error {
			readyCh <- ready{tx: tx}
			<-release
			return errRelease
		})
		// errRelease is the expected rollback signal; anything else is a real
		// failure to surface. We cannot call t.Fatalf from this goroutine after
		// the test returns, so record via readyCh only on the pre-ready path.
		if err != nil && err != errRelease {
			select {
			case readyCh <- ready{err: err}:
			default:
			}
		}
	}()

	r := <-readyCh
	if r.err != nil {
		return nil, r.err
	}

	t.Cleanup(func() {
		close(release)
		<-done // ensure rollback completed before the next test reads
	})
	return r.tx, nil
}

// errRelease is returned from the Begin transaction's fn to trigger a rollback
// of the sandbox at test cleanup.
var errRelease = errors.New("dreltest: begin sandbox released")
