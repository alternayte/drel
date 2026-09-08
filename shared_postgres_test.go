//go:build integration

package drel_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The integration tests share one Postgres container. Each test gets its own
// empty database on that container, so the tests stay isolated and every
// sequence starts at 1. One container for each test cost about two seconds
// each, and the suite passed the ten minute limit of the testing package.
//
// The container starts on the first test that needs it, and TestMain stops it
// when the binary ends. A test binary that runs no database test starts no
// container.
var (
	pgOnce      sync.Once
	pgContainer testcontainers.Container
	pgAdmin     *drel.Engine
	pgBaseDSN   string
	pgStartErr  error
	pgDBCounter atomic.Int64
)

// TestMain stops the shared container after the tests.
func TestMain(m *testing.M) {
	code := m.Run()
	if pgAdmin != nil {
		pgAdmin.Close()
	}
	if pgContainer != nil {
		_ = pgContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

// startSharedPostgres starts the container and opens the admin engine. It runs
// one time for each test binary.
func startSharedPostgres() {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("dreltest"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		pgStartErr = fmt.Errorf("start the shared Postgres container: %w", err)
		return
	}
	pgContainer = container

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		pgStartErr = fmt.Errorf("read the container connection string: %w", err)
		return
	}
	pgBaseDSN = dsn

	// The admin engine runs CREATE DATABASE. A database cannot be created
	// inside a transaction, and Engine.Exec runs outside one.
	admin, err := drel.NewEngine(dsn, drel.WithContext(ctx))
	if err != nil {
		pgStartErr = fmt.Errorf("open the admin engine: %w", err)
		return
	}
	pgAdmin = admin
}

// newTestDatabase creates an empty database on the shared container and returns
// its DSN. Each test therefore starts from an empty schema and from sequence 1.
func newTestDatabase(t *testing.T) string {
	t.Helper()
	pgOnce.Do(startSharedPostgres)
	require.NoError(t, pgStartErr)

	name := fmt.Sprintf("drel_test_%d", pgDBCounter.Add(1))
	_, err := pgAdmin.Exec(context.Background(), `CREATE DATABASE "`+name+`"`)
	require.NoError(t, err, "create the test database")

	u, err := url.Parse(pgBaseDSN)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// newTestEngine opens an engine on a fresh database of the shared container.
func newTestEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(newTestDatabase(t), drel.WithContext(context.Background()))
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	return engine
}
