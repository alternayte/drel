// Example: getting-started
//
// The "hello world" of drel — demonstrates the full codegen workflow
// using a simple Task model with exported fields:
//
//  1. Define a model with drel.Model[K] and exported fields + db tags
//  2. Run `drel generate` to produce scan/snapshot/diff/meta code
//  3. Use the generated DB struct with typed repositories for CRUD
//
// This example shows inserts via transaction, ordered queries using
// generated column refs, update with change tracking, filtered queries,
// and count operations.
//
// Usage:
//
//	cd examples/getting-started
//	go run ../../cmd/drel generate   # generates models/task_drel.go + db/drel_gen.go
//	export DATABASE_URL="postgres://localhost:5432/drelexample?sslmode=disable"
//	go run .
package main

//go:generate go run ../../cmd/drel generate

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/examples/getting-started/db"
	"github.com/alternayte/drel/examples/getting-started/models"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost:5432/drelexample?sslmode=disable"
	}

	ctx := context.Background()

	// Open uses the generated DB struct — typed repos are ready
	database, err := db.Open(dsn, drel.WithContext(ctx))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer database.Close()

	setup(ctx, database)
	defer teardown(ctx, database)

	// === INSERT via transaction ===
	fmt.Println("=== Insert tasks ===")
	// WithTx opens a transaction and puts it in the context. db.Tx(ctx) then
	// gives the tracked repositories of that transaction. The commit is
	// automatic when the function returns nil.
	err = database.WithTx(ctx, func(ctx context.Context) error {
		tasks := database.Tx(ctx).Tasks
		tasks.Add(models.NewTask("Build drel ORM", 1))
		tasks.Add(models.NewTask("Write documentation", 2))
		tasks.Add(models.NewTask("Add SQLite support", 3))
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// === QUERY using generated column refs ===
	fmt.Println("\n=== All tasks (ordered by priority) ===")
	tasks, err := database.Tasks.
		OrderBy(models.Tasks.Priority.Asc()).
		All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, t := range tasks {
		fmt.Printf("  [%d] %s (priority: %d, done: %v)\n", t.ID(), t.Title, t.Priority, t.Done)
	}

	// === UPDATE with change tracking ===
	fmt.Println("\n=== Mark first task done ===")
	// A tracked read plus a domain method is a partial UPDATE: only the
	// changed column reaches the database.
	err = database.WithTx(ctx, func(ctx context.Context) error {
		task, err := database.Tx(ctx).Tasks.Find(ctx, 1)
		if err != nil {
			return err
		}
		task.MarkDone() // domain method — only 'done' field changes
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	updated, _ := database.Tasks.Find(ctx, 1)
	fmt.Printf("  Task %d: done=%v\n", updated.ID(), updated.Done)

	// === Filtered query ===
	fmt.Println("\n=== Incomplete tasks ===")
	incomplete, err := database.Tasks.
		Where(models.Tasks.Done.IsFalse()).
		OrderBy(models.Tasks.Priority.Asc()).
		All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, t := range incomplete {
		fmt.Printf("  [%d] %s\n", t.ID(), t.Title)
	}

	// === Count ===
	count, _ := database.Tasks.Count(ctx)
	doneCount, _ := database.Tasks.Where(models.Tasks.Done.IsTrue()).Count(ctx)
	fmt.Printf("\n  Total: %d, Done: %d, Remaining: %d\n", count, doneCount, count-doneCount)
}

func setup(ctx context.Context, database *db.DB) {
	database.Exec(ctx, `DROP TABLE IF EXISTS tasks`)
	database.Exec(ctx, `
		CREATE TABLE tasks (
			id SERIAL PRIMARY KEY,
			title TEXT NOT NULL,
			done BOOLEAN NOT NULL DEFAULT false,
			priority INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
}

func teardown(ctx context.Context, database *db.DB) {
	database.Exec(ctx, `DROP TABLE IF EXISTS tasks`)
}
