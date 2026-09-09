// Example: feature-slices
//
// A vertical slice owns its models and its migrations. Deleting
// features/posts removes the model, the generated code and the migrations of
// that feature together, and nothing else changes.
//
// Key concepts shown:
//   - modules in drel.yaml: each slice names its packages.
//   - Per-slice migrations: `drel migrate new --module posts <name>` writes into
//     features/posts/migrations and diffs that slice's own snapshot.
//   - Embedded sets: generation writes a migrations_drel.go with //go:embed, so
//     each slice exposes its migrations as an fs.FS.
//   - ApplyMigrationsFS: drel merges the sets in version order, so the
//     migrations run in the order they were written, not in the order the
//     arguments appear.
//   - db.Modules.<Slice>: a slice reaches only its own repositories.
//   - db.Tx(ctx).Modules.<Slice>: the same, bound to one shared transaction.
//
// Regenerate with:
//
//	go run ../../cmd/drel generate
//	go run ../../cmd/drel migrate new --module users <name>
//
// Runs against in-memory SQLite. No external database is needed.
//
// Usage:
//
//	go run ./examples/feature-slices/
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/alternayte/drel/examples/feature-slices/db"
	"github.com/alternayte/drel/examples/feature-slices/features/posts"
	postsmigrations "github.com/alternayte/drel/examples/feature-slices/features/posts/migrations"
	"github.com/alternayte/drel/examples/feature-slices/features/users"
	usersmigrations "github.com/alternayte/drel/examples/feature-slices/features/users/migrations"
)

func main() {
	ctx := context.Background()

	database, err := db.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	// ─── Migrations from every slice, merged ─────────────────────────────────
	// The posts set is passed first on purpose. drel merges by version, so the
	// users migration still runs first.
	applied, err := database.ApplyMigrationsFS(ctx, postsmigrations.FS, usersmigrations.FS)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("=== Applied %d migration(s) from 2 slices ===\n", applied)

	// ─── Write through one slice ─────────────────────────────────────────────
	// db.Tx(ctx).Modules.Users reaches only the users slice, and the
	// transaction stays shared with every other slice.
	err = database.WithTx(ctx, func(ctx context.Context) error {
		slice := database.Tx(ctx).Modules.Users
		slice.Users.Add(&users.User{Name: "Alice", Email: "alice@example.com"})
		slice.Users.Add(&users.User{Name: "Bob", Email: "bob@example.com"})
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// ─── Two slices, one transaction ─────────────────────────────────────────
	// A post is written next to a user, and both commit together.
	err = database.WithTx(ctx, func(ctx context.Context) error {
		tx := database.Tx(ctx)

		author, err := tx.Modules.Users.Users.Find(ctx, 1)
		if err != nil {
			return err
		}
		tx.Modules.Posts.Posts.Add(&posts.Post{Title: "Hello from a slice", AuthorID: author.ID()})
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n=== A user and a post committed in one transaction ===")

	// ─── Read through the slice-scoped sets ──────────────────────────────────
	allUsers, err := database.Modules.Users.Users.All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	allPosts, err := database.Modules.Posts.Posts.All(ctx)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n=== users slice: %d user(s) ===\n", len(allUsers))
	for _, u := range allUsers {
		fmt.Printf("  %d %-6s %s\n", u.ID(), u.Name, u.Email)
	}
	fmt.Printf("\n=== posts slice: %d post(s) ===\n", len(allPosts))
	for _, p := range allPosts {
		fmt.Printf("  %d %q by user %d\n", p.ID(), p.Title, p.AuthorID)
	}

	// The flat fields still reach every model, for code that is not
	// slice-scoped.
	total, err := database.Users.Count(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n=== the flat db.Users field still works: %d ===\n", total)
}
