// Example: sqlite-todo
//
// Demonstrates drel against SQLite (pure-Go modernc.org/sqlite, no CGO):
//
//  1. Define a model with db-tag index options (unique + plain index)
//  2. Run `drel generate` to produce scan/snapshot/diff/meta code
//  3. Open an in-memory SQLite database (auto-detected from the DSN)
//  4. CRUD with change tracking + keyset (cursor) pagination
//
// Usage:
//
//	cd examples/sqlite-todo
//	go run ../../cmd/drel generate
//	go run .
package main

//go:generate go run ../../cmd/drel generate

import (
	"context"
	"fmt"
	"log"

	"github.com/alternayte/drel/examples/sqlite-todo/db"
	"github.com/alternayte/drel/examples/sqlite-todo/models"
)

func main() {
	ctx := context.Background()

	// ":memory:" is auto-detected as SQLite. No DATABASE_URL needed.
	database, err := db.Open(":memory:")
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer database.Close()

	setup(ctx, database)

	// === Insert inside a context transaction (change tracking) ===
	if err := database.WithTx(ctx, func(ctx context.Context) error {
		notes := database.Tx(ctx).Notes
		for i := 1; i <= 12; i++ {
			notes.Add(models.NewNote(
				fmt.Sprintf("note-%02d", i),
				fmt.Sprintf("Note %d", i),
				[]string{"work", "home", "ideas"}[i%3],
			))
		}
		return nil
	}); err != nil {
		log.Fatal(err)
	}

	// === Unique index enforcement ===
	err = database.WithTx(ctx, func(ctx context.Context) error {
		database.Tx(ctx).Notes.Add(models.NewNote("note-01", "dup", "work"))
		return nil
	})
	fmt.Printf("=== Duplicate slug rejected by unique index: %v\n", err != nil)

	// === Filtered query using generated column refs ===
	work, _ := database.Notes.Where(models.Notes.Category.Eq("work")).All(ctx)
	fmt.Printf("\n=== %d notes in category \"work\"\n", len(work))

	// === Cursor (keyset) pagination ===
	fmt.Println("\n=== Paginating 12 notes, 5 per page ===")
	cursor := ""
	page := 1
	for {
		q := database.Notes.OrderBy(models.Notes.ID.Asc()).Take(5)
		if cursor != "" {
			q = q.After(cursor)
		}
		result, err := q.Page(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  page %d: %d notes (hasMore=%v)\n", page, len(result.Items), result.HasMore)
		if !result.HasMore {
			break
		}
		cursor = result.NextCursor
		page++
	}

	// === Update with change tracking inside a context transaction ===
	if err := database.WithTx(ctx, func(ctx context.Context) error {
		n, err := database.Tx(ctx).Notes.Find(ctx, 1) // tracked
		if err != nil {
			return err
		}
		n.Pin() // only `pinned` is included in the UPDATE
		return nil
	}); err != nil {
		log.Fatal(err)
	}

	pinned, _ := database.Notes.Where(models.Notes.Pinned.IsTrue()).Count(ctx)
	total, _ := database.Notes.Count(ctx)
	fmt.Printf("\n=== %d/%d notes pinned\n", pinned, total)
}

func setup(ctx context.Context, database *db.DB) {
	mustExec(ctx, database, `CREATE TABLE notes (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		slug       TEXT NOT NULL,
		title      TEXT NOT NULL,
		category   TEXT NOT NULL,
		pinned     INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	mustExec(ctx, database, `CREATE UNIQUE INDEX uq_notes_slug ON notes (slug)`)
	mustExec(ctx, database, `CREATE INDEX idx_notes_category ON notes (category)`)
}

func mustExec(ctx context.Context, database *db.DB, sql string) {
	if _, err := database.Exec(ctx, sql); err != nil {
		log.Fatalf("setup: %v", err)
	}
}
