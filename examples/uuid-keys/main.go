// Example: uuid-keys
//
// Application-assigned UUIDv7 primary keys: drel stamps a time-ordered v7 UUID
// at Add() time, so the id exists before the row is flushed — no readback, no
// mid-transaction SaveChanges to "get the id".
//
// Usage:
//
//	cd examples/uuid-keys
//	go run ../../cmd/drel generate
//	go run .
package main

//go:generate go run ../../cmd/drel generate

import (
	"context"
	"fmt"
	"log"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/examples/uuid-keys/db"
	"github.com/alternayte/drel/examples/uuid-keys/orders"
)

func main() {
	ctx := context.Background()
	database, err := db.Open(":memory:")
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer database.Close()

	if _, err := database.Exec(ctx, `CREATE TABLE orders (
		id TEXT PRIMARY KEY,
		customer TEXT NOT NULL,
		total INTEGER NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		log.Fatal(err)
	}

	err = database.WithTx(ctx, func(ctx context.Context) error {
		o := orders.NewOrder("Alice", 4200)
		database.Tx(ctx).Orders.Add(o)
		fmt.Printf("id available immediately after Add: %s (v7)\n", o.ID())
		// SaveChanges flushes early, so the id is in the database before the
		// commit. WithTx flushes on its own when the function returns.
		return drel.MustFromContext(ctx).SaveChanges(ctx)
	})
	if err != nil {
		log.Fatal(err)
	}

	all, err := database.Orders.OrderBy(orders.Orders.ID.Asc()).All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, o := range all {
		fmt.Printf("order %s: %s spent %d\n", o.ID(), o.Customer, o.Total)
	}
}
