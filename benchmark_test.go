//go:build integration

package drel_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// benchTask is the model used by the benchmark suite.
type benchTask struct {
	ID        int
	Title     string
	Status    string
	Priority  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type benchTaskSnapshot struct {
	Title    string
	Status   string
	Priority int
}

var benchTaskMeta = drel.ModelMeta[benchTask]{
	Table:     "bench_tasks",
	Columns:   []string{"id", "title", "status", "priority", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*benchTask, error) {
		t := &benchTask{}
		err := row.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return nil, err
		}
		return t, nil
	},
	Snapshot: func(t *benchTask) any {
		return benchTaskSnapshot{Title: t.Title, Status: t.Status, Priority: t.Priority}
	},
	Diff: func(t *benchTask, snap any) []drel.FieldChange {
		s := snap.(benchTaskSnapshot)
		var changes []drel.FieldChange
		if t.Title != s.Title {
			changes = append(changes, drel.FieldChange{Column: "title", Value: t.Title})
		}
		if t.Status != s.Status {
			changes = append(changes, drel.FieldChange{Column: "status", Value: t.Status})
		}
		if t.Priority != s.Priority {
			changes = append(changes, drel.FieldChange{Column: "priority", Value: t.Priority})
		}
		return changes
	},
	PKValue: func(t *benchTask) any {
		return t.ID
	},
	InsertColumns: func(t *benchTask) ([]string, []any) {
		return []string{"title", "status", "priority"}, []any{t.Title, t.Status, t.Priority}
	},
	ScanReturning: func(t *benchTask, row drel.Row) error {
		return row.Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
	},
}

// benchTaskCols provides typed columns for predicates and ordering.
var benchTaskCols = struct {
	ID       drel.OrderedColumn[int]
	Title    drel.StringColumn
	Status   drel.StringColumn
	Priority drel.OrderedColumn[int]
}{
	ID:       drel.NewOrderedCol[int]("id"),
	Title:    drel.NewStringCol("title"),
	Status:   drel.NewStringCol("status"),
	Priority: drel.NewOrderedCol[int]("priority"),
}

// setupBenchDB starts a testcontainers Postgres instance, creates the
// bench_tasks table, seeds 1000 rows, and returns the drel Engine plus a raw
// pgxpool for baseline measurements. The B.Cleanup callback tears everything
// down automatically.
func setupBenchDB(b *testing.B) (*drel.Engine, *pgxpool.Pool) {
	b.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("drelbench"),
		tcpostgres.WithUsername("bench"),
		tcpostgres.WithPassword("bench"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		b.Fatalf("setupBenchDB: start container: %v", err)
	}

	b.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			b.Logf("setupBenchDB: terminate container: %v", err)
		}
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		b.Fatalf("setupBenchDB: connection string: %v", err)
	}

	engine, err := drel.NewEngine(connStr, drel.WithContext(ctx))
	if err != nil {
		b.Fatalf("setupBenchDB: new engine: %v", err)
	}
	b.Cleanup(func() { engine.Close() })

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		b.Fatalf("setupBenchDB: raw pool: %v", err)
	}
	b.Cleanup(func() { pool.Close() })

	_, err = engine.Exec(ctx, `
		CREATE TABLE bench_tasks (
			id          SERIAL PRIMARY KEY,
			title       TEXT NOT NULL,
			status      TEXT NOT NULL,
			priority    INTEGER NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		b.Fatalf("setupBenchDB: create table: %v", err)
	}

	// Seed 1000 rows in batches for speed.
	const seedCount = 1000
	for i := 0; i < seedCount; i++ {
		status := "open"
		if i%3 == 0 {
			status = "closed"
		}
		priority := (i % 5) + 1
		_, err := engine.Exec(ctx,
			"INSERT INTO bench_tasks (title, status, priority) VALUES ($1, $2, $3)",
			fmt.Sprintf("Task %d", i+1), status, priority,
		)
		if err != nil {
			b.Fatalf("setupBenchDB: seed row %d: %v", i, err)
		}
	}

	return engine, pool
}

// BenchmarkRawPgx_FindByID is the raw pgx baseline for single-row lookup.
func BenchmarkRawPgx_FindByID(b *testing.B) {
	b.ReportAllocs()
	_, pool := setupBenchDB(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := (i % 1000) + 1
		row := pool.QueryRow(ctx,
			"SELECT id, title, status, priority, created_at, updated_at FROM bench_tasks WHERE id = $1",
			id,
		)
		t := &benchTask{}
		if err := row.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt); err != nil {
			b.Fatalf("BenchmarkRawPgx_FindByID: scan: %v", err)
		}
	}
}

// BenchmarkDrel_FindByID measures the drel generated Find path.
func BenchmarkDrel_FindByID(b *testing.B) {
	b.ReportAllocs()
	engine, _ := setupBenchDB(b)
	ctx := context.Background()
	repo := drel.NewRepository(engine, benchTaskMeta)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := (i % 1000) + 1
		if _, err := repo.Find(ctx, id); err != nil {
			b.Fatalf("BenchmarkDrel_FindByID: %v", err)
		}
	}
}

// BenchmarkRawPgx_ScanN is the raw pgx baseline for scanning 100 rows.
func BenchmarkRawPgx_ScanN(b *testing.B) {
	b.ReportAllocs()
	_, pool := setupBenchDB(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := pool.Query(ctx,
			"SELECT id, title, status, priority, created_at, updated_at FROM bench_tasks LIMIT 100",
		)
		if err != nil {
			b.Fatalf("BenchmarkRawPgx_ScanN: query: %v", err)
		}
		for rows.Next() {
			t := &benchTask{}
			if err := rows.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt); err != nil {
				rows.Close()
				b.Fatalf("BenchmarkRawPgx_ScanN: scan: %v", err)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			b.Fatalf("BenchmarkRawPgx_ScanN: rows err: %v", err)
		}
	}
}

// BenchmarkDrel_ScanN measures the query builder plus generated scan for 100 rows.
func BenchmarkDrel_ScanN(b *testing.B) {
	b.ReportAllocs()
	engine, _ := setupBenchDB(b)
	ctx := context.Background()
	repo := drel.NewRepository(engine, benchTaskMeta)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tasks, err := repo.Limit(100).All(ctx)
		if err != nil {
			b.Fatalf("BenchmarkDrel_ScanN: %v", err)
		}
		_ = tasks
	}
}

// BenchmarkDrel_FilteredList measures the query builder with WHERE predicates.
func BenchmarkDrel_FilteredList(b *testing.B) {
	b.ReportAllocs()
	engine, _ := setupBenchDB(b)
	ctx := context.Background()
	repo := drel.NewRepository(engine, benchTaskMeta)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tasks, err := repo.
			Where(benchTaskCols.Status.Eq("open")).
			Where(benchTaskCols.Priority.GTE(3)).
			OrderBy(benchTaskCols.Priority.Desc()).
			Limit(50).
			All(ctx)
		if err != nil {
			b.Fatalf("BenchmarkDrel_FilteredList: %v", err)
		}
		_ = tasks
	}
}

// BenchmarkDrel_TrackedUpdate measures Find → mutate → SaveChanges inside a transaction.
func BenchmarkDrel_TrackedUpdate(b *testing.B) {
	b.ReportAllocs()
	engine, _ := setupBenchDB(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := (i % 1000) + 1
		err := engine.Transaction(ctx, func(tx *drel.Tx) error {
			repo := drel.NewTxRepository(tx, benchTaskMeta)
			task, err := repo.Find(ctx, id)
			if err != nil {
				return err
			}
			task.Status = "in_progress"
			task.Priority = (i % 5) + 1
			return tx.SaveChanges(ctx)
		})
		if err != nil {
			b.Fatalf("BenchmarkDrel_TrackedUpdate: %v", err)
		}
	}
}

// BenchmarkDrel_BulkInsert measures BulkInsert with 100 entities at a time.
func BenchmarkDrel_BulkInsert(b *testing.B) {
	b.ReportAllocs()
	engine, _ := setupBenchDB(b)
	ctx := context.Background()
	repo := drel.NewRepository(engine, benchTaskMeta)

	const batchSize = 100
	entities := make([]*benchTask, batchSize)
	for i := range entities {
		entities[i] = &benchTask{
			Title:    fmt.Sprintf("Bulk Task %d", i),
			Status:   "open",
			Priority: (i % 5) + 1,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-use the same slice (values are overwritten each round to avoid
		// accumulation in the DB affecting later iterations).
		for j := range entities {
			entities[j].Title = fmt.Sprintf("Bulk Task r%d-%d", i, j)
		}
		if _, err := repo.BulkInsert(ctx, entities); err != nil {
			b.Fatalf("BenchmarkDrel_BulkInsert: %v", err)
		}
	}
}
