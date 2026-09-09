// Example: model-features
//
// Demonstrates drel's built-in model traits: soft delete, optimistic
// concurrency (versioning), and audit columns. An Article model embeds all
// three traits and uses exported fields (Title, Body).
//
// Key concepts shown:
//   - SoftDelete: Remove sets deleted_at instead of deleting the row.
//     Normal queries auto-filter deleted rows; Unscoped bypasses the filter.
//     HardRemove permanently deletes the row.
//   - Versioned: Each update increments a version column. Concurrent edits
//     are detected via optimistic concurrency (WHERE version = N).
//   - Audit: created_by/updated_by are populated from context via WithActor.
//
// Usage:
//
//	cd examples/model-features
//	go run ../../cmd/drel generate   # generates articles/article_drel.go + db/drel_gen.go
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
	"github.com/alternayte/drel/examples/model-features/articles"
	"github.com/alternayte/drel/examples/model-features/db"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost:5432/drelexample?sslmode=disable"
	}

	ctx := context.Background()

	database, err := db.Open(dsn, drel.WithContext(ctx))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer database.Close()

	setup(ctx, database)
	defer teardown(ctx, database)

	demoSoftDelete(ctx, database)
	demoVersioning(ctx, database)
	demoAudit(ctx, database)
}

// ---------------------------------------------------------------------------
// Soft Delete
// ---------------------------------------------------------------------------

func demoSoftDelete(ctx context.Context, database *db.DB) {
	fmt.Println("=== Soft Delete ===")

	// Create an article
	var articleID int
	err := database.WithTx(ctx, func(ctx context.Context) error {
		repo := database.Tx(ctx).Articles
		a := &articles.Article{Title: "Soft Delete Demo", Body: "This article will be soft-deleted."}
		repo.Add(a)
		if err := drel.MustFromContext(ctx).SaveChanges(ctx); err != nil {
			return err
		}
		articleID = a.ID()
		fmt.Printf("  Created article %d: %q\n", a.ID(), a.Title)

		// Soft-delete it
		return repo.Remove(a)
	})
	if err != nil {
		log.Fatal(err)
	}

	// Verify deleted_at is set via raw SQL
	row := database.QueryRow(ctx,
		"SELECT deleted_at IS NOT NULL FROM articles WHERE id = $1", articleID)
	var hasDeletedAt bool
	if err := row.Scan(&hasDeletedAt); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  deleted_at set? %v\n", hasDeletedAt)

	// Normal query: soft-deleted article is auto-filtered
	all, err := database.Articles.All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Articles.All() returned %d articles (filtered)\n", len(all))

	// Unscoped query: includes soft-deleted articles
	unscoped, err := database.Articles.Unscoped().All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Articles.Unscoped().All() returned %d articles (unfiltered)\n", len(unscoped))

	// Hard delete: permanently removes the row
	fmt.Println("\n  --- HardRemove ---")

	var hardID int
	err = database.WithTx(ctx, func(ctx context.Context) error {
		repo := database.Tx(ctx).Articles
		a := &articles.Article{Title: "Hard Delete Demo", Body: "This article will be permanently deleted."}
		repo.Add(a)
		if err := drel.MustFromContext(ctx).SaveChanges(ctx); err != nil {
			return err
		}
		hardID = a.ID()
		fmt.Printf("  Created article %d: %q\n", a.ID(), a.Title)

		return drel.MustFromContext(ctx).HardRemove(a)
	})
	if err != nil {
		log.Fatal(err)
	}

	row = database.QueryRow(ctx,
		"SELECT COUNT(*) FROM articles WHERE id = $1", hardID)
	var count int
	if err := row.Scan(&count); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Row count after HardRemove: %d (completely gone)\n\n", count)
}

// ---------------------------------------------------------------------------
// Versioning (Optimistic Concurrency)
// ---------------------------------------------------------------------------

func demoVersioning(ctx context.Context, database *db.DB) {
	fmt.Println("=== Versioning ===")

	// Create an article — version starts at 1
	article := &articles.Article{Title: "Version Demo", Body: "Original body."}
	err := database.WithTx(ctx, func(ctx context.Context) error {
		repo := database.Tx(ctx).Articles
		repo.Add(article)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Created article %d: version=%d\n", article.ID(), article.Version())

	// Update the article — version becomes 2
	err = database.WithTx(ctx, func(ctx context.Context) error {
		repo := database.Tx(ctx).Articles
		a, err := repo.Find(ctx, article.ID())
		if err != nil {
			return err
		}
		a.Title = "Version Demo (updated)"
		a.Body = "Updated body."
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// Verify version in the database
	row := database.QueryRow(ctx,
		"SELECT version, title FROM articles WHERE id = $1", article.ID())
	var dbVersion int
	var dbTitle string
	if err := row.Scan(&dbVersion, &dbTitle); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  After update: version=%d, title=%q\n\n", dbVersion, dbTitle)
}

// ---------------------------------------------------------------------------
// Audit Columns
// ---------------------------------------------------------------------------

func demoAudit(ctx context.Context, database *db.DB) {
	fmt.Println("=== Audit ===")

	// Create with actor "admin"
	adminCtx := drel.WithActor(ctx, "admin")
	var articleID int
	err := database.Transaction(adminCtx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, articles.ArticleMeta)
		a := &articles.Article{Title: "Audit Demo", Body: "Created by admin."}
		repo.Add(a)
		if err := tx.SaveChanges(adminCtx); err != nil {
			return err
		}
		articleID = a.ID()
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// Check created_by / updated_by
	row := database.QueryRow(ctx,
		"SELECT created_by, updated_by FROM articles WHERE id = $1", articleID)
	var createdBy, updatedBy string
	if err := row.Scan(&createdBy, &updatedBy); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  After create: created_by=%q, updated_by=%q\n", createdBy, updatedBy)

	// Update with a different actor "editor"
	editorCtx := drel.WithActor(ctx, "editor")
	err = database.Transaction(editorCtx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, articles.ArticleMeta)
		a, err := repo.Find(editorCtx, articleID)
		if err != nil {
			return err
		}
		a.Body = "Updated by editor."
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// Verify created_by unchanged, updated_by changed
	row = database.QueryRow(ctx,
		"SELECT created_by, updated_by FROM articles WHERE id = $1", articleID)
	if err := row.Scan(&createdBy, &updatedBy); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  After update: created_by=%q, updated_by=%q\n", createdBy, updatedBy)
}

// ---------------------------------------------------------------------------
// Setup / Teardown
// ---------------------------------------------------------------------------

func setup(ctx context.Context, database *db.DB) {
	database.Exec(ctx, `DROP TABLE IF EXISTS articles`)
	database.Exec(ctx, `
		CREATE TABLE articles (
			id         SERIAL PRIMARY KEY,
			title      TEXT NOT NULL,
			body       TEXT NOT NULL,
			tags       JSONB NOT NULL DEFAULT '[]'::jsonb,
			metadata   JSONB NOT NULL DEFAULT '{}'::jsonb,
			deleted_at TIMESTAMPTZ,
			version    INTEGER NOT NULL DEFAULT 1,
			created_by TEXT NOT NULL DEFAULT '',
			updated_by TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
}

func teardown(ctx context.Context, database *db.DB) {
	database.Exec(ctx, `DROP TABLE IF EXISTS articles`)
}
