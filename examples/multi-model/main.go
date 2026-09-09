// Example: multi-model
//
// Demonstrates multi-table transactions, domain events, and transaction hooks
// using DDD style (unexported fields + domain methods). This is the only
// example that uses this encapsulation pattern — all other examples use
// exported fields.
//
// Key concepts shown:
//   - Domain model with Transfer method that modifies two entities atomically
//   - RecordEvent captures domain events during business logic
//   - OnBeforeCommit hook writes events to an audit_log table within the
//     same transaction (rolled back if commit fails)
//   - OnAfterCommit hook fires only after successful commit (e.g. logging)
//
// Usage:
//
//	cd examples/multi-model
//	go run ../../cmd/drel generate   # generates users/user_drel.go + db/drel_gen.go
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
	"github.com/alternayte/drel/examples/multi-model/db"
	"github.com/alternayte/drel/examples/multi-model/users"
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

	// === Seed users ===
	fmt.Println("=== Seed users ===")
	err = database.WithTx(ctx, func(ctx context.Context) error {
		repo := database.Tx(ctx).Users
		repo.Add(users.NewUser("Alice", 1000))
		repo.Add(users.NewUser("Bob", 500))
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	all, _ := database.Users.All(ctx)
	for _, u := range all {
		fmt.Printf("  %s: balance=%d\n", u.Name(), u.Balance())
	}

	// === Register hooks ===

	// OnBeforeCommit: write domain events to audit_log within the same transaction.
	// If the transaction rolls back, the audit_log insert is rolled back too.
	database.OnBeforeCommit(func(ctx context.Context, tx *drel.Tx, events []any) error {
		for _, e := range events {
			_, err := tx.Exec(ctx,
				"INSERT INTO audit_log (event_type, payload) VALUES ($1, $2)",
				fmt.Sprintf("%T", e), fmt.Sprintf("%+v", e))
			if err != nil {
				return err
			}
		}
		return nil
	})

	// OnAfterCommit: fires only after successful commit — safe for side-effects.
	database.OnAfterCommit(func(ctx context.Context, events []any) {
		for _, e := range events {
			fmt.Printf("  [after-commit] %T: %+v\n", e, e)
		}
	})

	// === Transfer ===
	fmt.Println("\n=== Transfer 200 from Alice to Bob ===")
	err = database.WithTx(ctx, func(ctx context.Context) error {
		repo := database.Tx(ctx).Users

		alice, err := repo.Find(ctx, 1)
		if err != nil {
			return err
		}
		bob, err := repo.Find(ctx, 2)
		if err != nil {
			return err
		}

		// Domain method modifies both entities and records a BalanceTransferred event.
		if err := alice.Transfer(200, bob); err != nil {
			return err
		}

		return nil
		// Auto-flush: both UPDATE statements + before/after commit hooks fire here.
	})
	if err != nil {
		log.Fatal(err)
	}

	// === Results ===
	fmt.Println("\n=== Updated balances ===")
	all, _ = database.Users.OrderBy(users.Users.ID.Asc()).All(ctx)
	for _, u := range all {
		fmt.Printf("  %s: balance=%d\n", u.Name(), u.Balance())
	}

	// Verify audit log
	fmt.Println("\n=== Audit log ===")
	row := database.QueryRow(ctx,
		"SELECT event_type, payload FROM audit_log ORDER BY id LIMIT 1")
	var eventType, payload string
	if err := row.Scan(&eventType, &payload); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  event_type=%s payload=%s\n", eventType, payload)
}

func setup(ctx context.Context, database *db.DB) {
	database.Exec(ctx, `DROP TABLE IF EXISTS audit_log`)
	database.Exec(ctx, `DROP TABLE IF EXISTS users`)
	database.Exec(ctx, `
		CREATE TABLE users (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL,
			balance    INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	database.Exec(ctx, `
		CREATE TABLE audit_log (
			id         SERIAL PRIMARY KEY,
			event_type TEXT NOT NULL,
			payload    TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
}

func teardown(ctx context.Context, database *db.DB) {
	database.Exec(ctx, `DROP TABLE IF EXISTS audit_log`)
	database.Exec(ctx, `DROP TABLE IF EXISTS users`)
}
