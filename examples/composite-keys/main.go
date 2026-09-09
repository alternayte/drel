// Example: composite-keys
//
// A composite primary key, declared as a comparable Go struct passed to
// drel.Model[K]. Each exported field of the key struct becomes one key
// column, named by its own db tag. A composite key is always
// application-assigned: there is no auto-increment for it, so the key is set
// with SetID before Add.
//
// Usage:
//
//	cd examples/composite-keys
//	go run ../../cmd/drel generate
//	go run .
package main

//go:generate go run ../../cmd/drel generate

import (
	"context"
	"fmt"
	"log"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/examples/composite-keys/db"
	"github.com/alternayte/drel/examples/composite-keys/orders"
)

func main() {
	ctx := context.Background()
	database, err := db.Open(":memory:")
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer database.Close()

	if _, err := database.Exec(ctx, `CREATE TABLE order_lines (
		order_id INTEGER NOT NULL,
		line_no INTEGER NOT NULL,
		qty INTEGER NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (order_id, line_no)
	)`); err != nil {
		log.Fatal(err)
	}

	key := orders.OrderLineKey{OrderID: 1001, LineNo: 1}

	err = database.WithTx(ctx, func(ctx context.Context) error {
		l := orders.NewOrderLine(key, 3)
		database.Tx(ctx).OrderLines.Add(l)
		return drel.MustFromContext(ctx).SaveChanges(ctx)
	})
	if err != nil {
		log.Fatal(err)
	}

	found, err := database.OrderLines.Find(ctx, key)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("found order %d line %d: qty %d\n", found.ID().OrderID, found.ID().LineNo, found.Qty)

	err = database.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		l, err := drel.NewTxRepository(tx, orders.OrderLineMeta).Find(ctx, key)
		if err != nil {
			return err
		}
		l.Qty = 7
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	updated, err := database.OrderLines.Find(ctx, key)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("updated qty: %d\n", updated.Qty)

	err = database.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		r := drel.NewTxRepository(tx, orders.OrderLineMeta)
		l, err := r.Find(ctx, key)
		if err != nil {
			return err
		}
		return r.Remove(l)
	})
	if err != nil {
		log.Fatal(err)
	}

	_, err = database.OrderLines.Find(ctx, key)
	fmt.Printf("after delete, find error: %v\n", err)
}
