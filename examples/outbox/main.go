// Example: outbox
//
// Demonstrates the transactional outbox pattern with drel.Engine.UseOutbox.
// Domain events recorded during a business transaction are written to an outbox
// table *in the same transaction* as the data changes that produced them. This
// gives a reliable, exactly-once hand-off to external systems (a CDC pipeline
// like Debezium, a message bus, or a polling relay) without dual-write races:
// either the order AND its event commit together, or neither does.
//
// Key concepts shown:
//   - UseOutbox: register an outbox table; every SaveChanges persists the
//     pending domain events into it within the same transaction.
//   - Atomicity: when a transaction rolls back, no outbox row is left behind —
//     you never publish an event for work that didn't commit.
//   - Relay: drel.NewRelay claims a batch under a lease, publishes it, and
//     stamps processed_at. A second poll returns nothing. The lease lets more
//     than one replica poll one table, and partition_key keeps the events of
//     one order in order.
//
// Runs against in-memory SQLite (pure-Go modernc.org/sqlite, no CGO and no
// external database needed).
//
// Usage:
//
//	cd examples/outbox
//	go run ../../cmd/drel generate   # generates orders/order_drel.go + db/drel_gen.go
//	go run .
package main

//go:generate go run ../../cmd/drel generate

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/examples/outbox/db"
	"github.com/alternayte/drel/examples/outbox/orders"
	"github.com/google/uuid"
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

	// Register the outbox. From now on, every committed transaction also writes
	// its domain events into the "outbox" table, transactionally. The mapper
	// sets the partition key to the order id, so the relay publishes the events
	// of one order in order, even when more than one replica runs.
	database.UseOutbox("outbox", drel.WithOutboxMapper(func(ev any) (drel.OutboxMessage, bool) {
		switch e := ev.(type) {
		case orders.OrderPlaced:
			return drel.OutboxMessage{Type: "order.placed", Payload: e, PartitionKey: e.OrderID.String()}, true
		case orders.OrderShipped:
			return drel.OutboxMessage{Type: "order.shipped", Payload: e, PartitionKey: e.OrderID.String()}, true
		default:
			return drel.OutboxMessage{}, false
		}
	}))

	placeOrders(ctx, database)
	showOutbox(ctx, database, "after placing & shipping orders")

	demoRollbackIsAtomic(ctx, database)
	showOutbox(ctx, database, "after a rolled-back transaction (unchanged)")

	relay(ctx, database)
	relay(ctx, database) // second pass: nothing left — exactly-once hand-off
}

// ---------------------------------------------------------------------------
// Place & ship orders — events land in the outbox atomically
// ---------------------------------------------------------------------------

func placeOrders(ctx context.Context, database *db.DB) {
	fmt.Println("=== Place orders ===")

	type spec struct {
		customer string
		total    int
	}
	var placedIDs []uuid.UUID
	for _, s := range []spec{{"Alice", 4200}, {"Bob", 1599}} {
		err := database.WithTx(ctx, func(ctx context.Context) error {
			o := orders.NewOrder(s.customer, s.total)
			// Add() stamps a UUIDv7 id immediately — o.ID() is valid right away,
			// no mid-transaction flush needed to "get the id".
			database.Tx(ctx).Orders.Add(o)
			o.Place()
			placedIDs = append(placedIDs, o.ID())
			fmt.Printf("  placed order %s for %s (%d cents)\n", o.ID(), o.Customer, o.Total)
			return nil
			// Auto-flush at tx end: the INSERT and the OrderPlaced outbox row are
			// written within this same transaction, then commit.
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	// Ship the first placed order — records OrderShipped, also via the outbox.
	err := database.WithTx(ctx, func(ctx context.Context) error {
		o, err := database.Tx(ctx).Orders.Find(ctx, placedIDs[0])
		if err != nil {
			return err
		}
		o.Ship("UPS")
		fmt.Printf("  shipped order %s via %s\n", o.ID(), "UPS")
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Atomicity: a rolled-back transaction leaves no outbox row
// ---------------------------------------------------------------------------

func demoRollbackIsAtomic(ctx context.Context, database *db.DB) {
	fmt.Println("\n=== Rollback is atomic ===")

	ordersBefore := count(ctx, database, "SELECT COUNT(*) FROM orders")
	outboxBefore := count(ctx, database, "SELECT COUNT(*) FROM outbox")

	// Place an order and record its event, then fail the transaction. Because
	// the outbox write happens inside the same transaction, the rollback drops
	// the order AND its event together — no orphaned message escapes.
	errBoom := errors.New("payment declined")
	err := database.WithTx(ctx, func(ctx context.Context) error {
		o := orders.NewOrder("Mallory", 999999)
		database.Tx(ctx).Orders.Add(o)
		o.Place()
		return errBoom // force rollback
	})
	fmt.Printf("  transaction failed as expected: %v\n", errors.Is(err, errBoom))

	ordersAfter := count(ctx, database, "SELECT COUNT(*) FROM orders")
	outboxAfter := count(ctx, database, "SELECT COUNT(*) FROM outbox")
	fmt.Printf("  orders: %d -> %d, outbox: %d -> %d (both unchanged)\n",
		ordersBefore, ordersAfter, outboxBefore, outboxAfter)
}

// ---------------------------------------------------------------------------
// Relay: drel claims a batch under a lease, publishes it, and marks it processed
// ---------------------------------------------------------------------------

func relay(ctx context.Context, database *db.DB) {
	fmt.Println("\n=== Relay poll ===")

	published := 0
	pub := drel.PublisherFunc(func(ctx context.Context, msg drel.ClaimedMessage) error {
		// A real relay would publish to Kafka, NATS or an HTTP endpoint here.
		fmt.Printf("  publish #%d %-12s %s\n", msg.ID, msg.Type, string(msg.Payload))
		published++
		return nil
	})

	// The lease lets more than one replica poll one table. partition_key keeps
	// the messages of one aggregate in order.
	r := drel.NewRelay(database.Engine, "outbox", pub, drel.WithRelayBatchSize(50))

	if _, err := r.RunOnce(ctx); err != nil {
		log.Fatal(err)
	}
	if published == 0 {
		fmt.Println("  nothing to publish — outbox drained")
		return
	}
	fmt.Printf("  published %d message(s)\n", published)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func showOutbox(ctx context.Context, database *db.DB, when string) {
	fmt.Printf("\n=== Outbox contents (%s) ===\n", when)
	rows, err := database.Query(ctx,
		`SELECT id, type, payload, processed_at IS NOT NULL FROM outbox ORDER BY id`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var (
			id        int
			typ       string
			payload   string
			processed bool
		)
		if err := rows.Scan(&id, &typ, &payload, &processed); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  #%d %-12s processed=%-5v %s\n", id, typ, processed, payload)
		n++
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
	if n == 0 {
		fmt.Println("  (empty)")
	}
}

func count(ctx context.Context, database *db.DB, sql string) int {
	var n int
	if err := database.QueryRow(ctx, sql).Scan(&n); err != nil {
		log.Fatal(err)
	}
	return n
}

func setup(ctx context.Context, database *db.DB) {
	mustExec(ctx, database, `CREATE TABLE orders (
		id         TEXT    PRIMARY KEY,
		customer   TEXT    NOT NULL,
		total      INTEGER NOT NULL,
		status     TEXT    NOT NULL DEFAULT 'placed',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	mustExec(ctx, database, `CREATE INDEX idx_orders_status ON orders (status)`)

	// drel.OutboxSchema returns dialect-appropriate DDL for the outbox table,
	// including a partial index on unprocessed rows
	// (CREATE INDEX ... WHERE processed_at IS NULL) so the relay poll below
	// performs an index seek instead of a sequential scan as the table grows.
	mustExec(ctx, database, drel.OutboxSchema("outbox", "sqlite"))
}

func mustExec(ctx context.Context, database *db.DB, sql string) {
	if _, err := database.Exec(ctx, sql); err != nil {
		log.Fatalf("setup: %v", err)
	}
}
