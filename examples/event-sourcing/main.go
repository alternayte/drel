// Example: event-sourcing
//
// Stores the history of an aggregate as events, then builds a read model from
// that history with a projection.
//
// The event store shares the engine and the transaction of the application, so
// the events and any other write commit together. The projection advances its
// checkpoint in the same transaction as the read model, so it can never record
// progress that it did not make.
//
// Key concepts shown:
//   - Append: events are written at expectedVersion+1 and up.
//   - Optimistic concurrency: a stale expected version returns ErrConcurrency.
//   - Read: rebuild one aggregate from its own stream.
//   - ReadAll + checkpoints: a projection walks the log in batches and resumes
//     exactly where it stopped.
//   - Failure: a handler that fails leaves the checkpoint where it was.
//   - Reset: clear the read model and the checkpoint to replay the history.
//
// The read of the whole log stops at a transaction watermark on Postgres, so a
// transaction that commits late can never slip behind the reader. SQLite
// serialises writers, so the insert order is already the commit order.
//
// Runs against in-memory SQLite. No external database is needed.
//
// Usage:
//
//	go run ./examples/event-sourcing/
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/es"
)

// ─── Domain events ───────────────────────────────────────────────────────────

// OrderPlaced records a new order.
type OrderPlaced struct {
	Customer string `json:"customer"`
	Total    int    `json:"total"`
}

// OrderShipped records a dispatch.
type OrderShipped struct {
	Carrier string `json:"carrier"`
}

// Order is the aggregate. It is rebuilt from its own stream.
type Order struct {
	Customer string
	Total    int
	Shipped  bool
	Version  int64
}

// apply folds one event into the aggregate.
func (o *Order) apply(rec es.Record) error {
	switch rec.Type {
	case typeName(OrderPlaced{}):
		var ev OrderPlaced
		if err := json.Unmarshal(rec.Payload, &ev); err != nil {
			return err
		}
		o.Customer, o.Total = ev.Customer, ev.Total
	case typeName(OrderShipped{}):
		o.Shipped = true
	}
	o.Version = rec.Version
	return nil
}

func main() {
	ctx := context.Background()

	engine, err := drel.NewEngine(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()

	setup(ctx, engine)
	store := es.NewStore(engine, "drel_events")
	checkpoints := es.NewCheckpoints(engine, "drel_checkpoints")

	// ─── Append ──────────────────────────────────────────────────────────────
	fmt.Println("=== Append events to a stream ===")
	// A new stream is at version 0. The first event takes version 1.
	if err := engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-1", 0, []any{
			OrderPlaced{Customer: "Alice", Total: 4200},
			OrderShipped{Carrier: "UPS"},
		})
	}); err != nil {
		log.Fatal(err)
	}
	if err := engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-2", 0, []any{OrderPlaced{Customer: "Bob", Total: 1599}})
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println("  wrote 3 events across 2 streams")

	// ─── Rebuild one aggregate ───────────────────────────────────────────────
	fmt.Println("\n=== Rebuild an aggregate from its stream ===")
	order, err := loadOrder(ctx, store, "order-1")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  order-1: customer=%s total=%d shipped=%v version=%d\n",
		order.Customer, order.Total, order.Shipped, order.Version)

	// ─── Optimistic concurrency ──────────────────────────────────────────────
	fmt.Println("\n=== Two writers, one winner ===")
	// This writer read the stream at version 1 and did not see the shipment.
	err = engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-1", 1, []any{OrderShipped{Carrier: "DHL"}})
	})
	fmt.Printf("  a stale expected version conflicts: %v\n", errors.Is(err, es.ErrConcurrency))

	// The writer reads again and appends at the version it actually found.
	if err := engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-1", order.Version, []any{OrderShipped{Carrier: "DHL"}})
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println("  after reading again, the append succeeds")

	// ─── Project ─────────────────────────────────────────────────────────────
	fmt.Println("\n=== Project the log into a read model ===")
	n, err := project(ctx, engine, store, checkpoints, "")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  handled %d events\n", n)
	showCustomers(ctx, engine)

	// A second pass finds nothing: the checkpoint holds.
	n, err = project(ctx, engine, store, checkpoints, "")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  a second pass handles %d events\n", n)

	// ─── A failing handler does not advance the checkpoint ───────────────────
	fmt.Println("\n=== A handler that fails keeps its place ===")
	if err := engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-3", 0, []any{OrderPlaced{Customer: "Mallory", Total: 5}})
	}); err != nil {
		log.Fatal(err)
	}

	before, _ := checkpoints.Load(ctx, "customers")
	_, err = project(ctx, engine, store, checkpoints, "Mallory") // the handler rejects Mallory
	fmt.Printf("  the projection failed: %v\n", err != nil)
	after, _ := checkpoints.Load(ctx, "customers")
	fmt.Printf("  the checkpoint moved: %v\n", before != after)

	// The handler is fixed, and the event is handled exactly one time.
	if _, err := project(ctx, engine, store, checkpoints, ""); err != nil {
		log.Fatal(err)
	}
	showCustomers(ctx, engine)

	// ─── Replay ──────────────────────────────────────────────────────────────
	fmt.Println("\n=== Replay the history into an empty read model ===")
	// Clear the read model and the checkpoint together, so the replay starts
	// from a consistent point.
	if err := engine.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx, `DELETE FROM customers`); err != nil {
			return err
		}
		return checkpoints.Reset(ctx, "customers")
	}); err != nil {
		log.Fatal(err)
	}

	n, err = project(ctx, engine, store, checkpoints, "")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  replayed %d events\n", n)
	showCustomers(ctx, engine)
}

// loadOrder rebuilds one aggregate from its stream.
func loadOrder(ctx context.Context, store *es.Store, stream string) (*Order, error) {
	it, err := store.Read(ctx, stream, 0)
	if err != nil {
		return nil, err
	}
	defer it.Close()

	order := &Order{}
	for it.Next() {
		if err := order.apply(it.Record()); err != nil {
			return nil, err
		}
	}
	return order, it.Err()
}

// project reads the log in batches. Each batch writes the read model and saves
// the checkpoint in one transaction, so the two can never disagree.
//
// rejectCustomer makes the handler fail on one customer, to show that a failure
// does not advance the checkpoint.
func project(ctx context.Context, engine *drel.Engine, store *es.Store, checkpoints *es.Checkpoints, rejectCustomer string) (int, error) {
	handled := 0

	for {
		from, err := checkpoints.Load(ctx, "customers")
		if err != nil {
			return handled, err
		}

		it, err := store.ReadAll(ctx, from, 2)
		if err != nil {
			return handled, err
		}
		var batch []es.Record
		for it.Next() {
			batch = append(batch, it.Record())
		}
		err = it.Err()
		it.Close()
		if err != nil {
			return handled, err
		}
		if len(batch) == 0 {
			return handled, nil
		}

		err = engine.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			for _, rec := range batch {
				if rec.Type == typeName(OrderPlaced{}) {
					var ev OrderPlaced
					if err := json.Unmarshal(rec.Payload, &ev); err != nil {
						return err
					}
					if ev.Customer == rejectCustomer {
						return fmt.Errorf("the handler rejected %s", ev.Customer)
					}
					if _, err := tx.Exec(ctx,
						`INSERT INTO customers (name, spent) VALUES (?, ?)
						 ON CONFLICT (name) DO UPDATE SET spent = customers.spent + ?`,
						ev.Customer, ev.Total, ev.Total); err != nil {
						return err
					}
				}
				// The checkpoint advances with the read model, in this same
				// transaction.
				if err := checkpoints.Save(ctx, "customers", rec.Position); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return handled, err
		}
		handled += len(batch)
	}
}

func showCustomers(ctx context.Context, engine *drel.Engine) {
	rows, err := engine.Query(ctx, `SELECT name, spent FROM customers ORDER BY name`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			name  string
			spent int
		)
		if err := rows.Scan(&name, &spent); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  read model: %-8s spent=%d\n", name, spent)
	}
}

// typeName is the name the store gives an event: the package-qualified Go type.
func typeName(v any) string {
	name, err := drel.EventTypeName(v)
	if err != nil {
		log.Fatal(err)
	}
	return name
}

func setup(ctx context.Context, engine *drel.Engine) {
	for _, ddl := range []string{
		es.Schema("drel_events", "sqlite"),
		es.CheckpointSchema("drel_checkpoints", "sqlite"),
		`CREATE TABLE customers (name TEXT PRIMARY KEY, spent INTEGER NOT NULL)`,
	} {
		if _, err := engine.Exec(ctx, ddl); err != nil {
			log.Fatal(err)
		}
	}
}
