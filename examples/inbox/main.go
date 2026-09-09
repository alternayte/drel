// Example: inbox
//
// A message broker delivers at least once. One message can therefore reach a
// handler more than one time, and a handler that is not idempotent then charges
// a card two times or sends two emails.
//
// drel.Inbox suppresses the duplicate. Claim writes a dedupe row inside the
// caller's transaction, next to the application write, so the two commit
// together or neither commits.
//
// Key concepts shown:
//   - Claim: the first delivery proceeds, and the second returns
//     ErrDuplicateMessage.
//   - Atomicity: a handler that fails rolls the dedupe row back with its work,
//     so the broker can deliver the message again.
//   - Fail: records the failed attempt outside the rolled-back transaction.
//   - Two handlers: the key is the pair of message and handler, so a second
//     handler processes the same message one time of its own.
//   - Purge: deletes the old rows.
//
// Runs against in-memory SQLite. No external database is needed.
//
// Usage:
//
//	go run ./examples/inbox/
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/alternayte/drel"
)

// message is one delivery from the broker.
type message struct {
	ID     string
	Amount int
}

func main() {
	ctx := context.Background()

	engine, err := drel.NewEngine(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()

	setup(ctx, engine)
	inbox := drel.NewInbox(engine, "drel_inbox")

	// ─── A duplicate delivery is suppressed ──────────────────────────────────
	fmt.Println("=== The broker delivers the same message two times ===")
	msg := message{ID: "msg-1", Amount: 4200}

	for attempt := 1; attempt <= 2; attempt++ {
		err := handlePayment(ctx, engine, inbox, msg)
		switch {
		case errors.Is(err, drel.ErrDuplicateMessage):
			fmt.Printf("  delivery %d: already processed, acknowledge and stop\n", attempt)
		case err != nil:
			log.Fatal(err)
		default:
			fmt.Printf("  delivery %d: processed, charged %d cents\n", attempt, msg.Amount)
		}
	}
	fmt.Printf("  payments recorded: %d (one, not two)\n", count(ctx, engine, "SELECT count(*) FROM payments"))

	// ─── A failed handler leaves the message claimable ───────────────────────
	fmt.Println("\n=== A handler that fails must run again ===")
	broken := message{ID: "msg-2", Amount: 999}

	err = engine.WithTx(ctx, func(ctx context.Context) error {
		if err := inbox.Claim(ctx, broken.ID, "payments"); err != nil {
			return err
		}
		return errors.New("the payment gateway timed out")
	})
	fmt.Printf("  first attempt failed: %v\n", err != nil)

	// The transaction rolled back, so the dedupe row went with it. Record the
	// attempt outside the transaction.
	if err := inbox.Fail(ctx, broken.ID, "payments", err); err != nil {
		log.Fatal(err)
	}

	if err := handlePayment(ctx, engine, inbox, broken); err != nil {
		log.Fatal(err)
	}
	fmt.Println("  retry succeeded")
	fmt.Printf("  attempts recorded for msg-2: %d\n",
		count(ctx, engine, "SELECT attempts FROM drel_inbox WHERE message_id = 'msg-2'"))

	// ─── Two handlers, one message ───────────────────────────────────────────
	fmt.Println("\n=== A second handler processes the same message ===")
	err = engine.WithTx(ctx, func(ctx context.Context) error {
		if err := inbox.Claim(ctx, msg.ID, "receipts"); err != nil {
			return err
		}
		_, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO receipts (message_id) VALUES (?)`, msg.ID)
		return err
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  the receipts handler processed msg-1: %d receipt(s)\n",
		count(ctx, engine, "SELECT count(*) FROM receipts"))

	// ─── Purge ───────────────────────────────────────────────────────────────
	fmt.Println("\n=== Purge the old rows ===")
	fmt.Printf("  rows before: %d\n", count(ctx, engine, "SELECT count(*) FROM drel_inbox"))
	// Purge only beyond the retention of the broker. A purged row no longer
	// suppresses its message.
	removed, err := inbox.Purge(ctx, time.Now().Add(time.Second))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  purged: %d\n", removed)
}

// handlePayment claims the message and writes the payment in one transaction.
// The dedupe row and the payment therefore commit together.
func handlePayment(ctx context.Context, engine *drel.Engine, inbox *drel.Inbox, msg message) error {
	return engine.WithTx(ctx, func(ctx context.Context) error {
		if err := inbox.Claim(ctx, msg.ID, "payments"); err != nil {
			return err // ErrDuplicateMessage on a repeat delivery
		}
		_, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO payments (message_id, amount) VALUES (?, ?)`, msg.ID, msg.Amount)
		return err
	})
}

func setup(ctx context.Context, engine *drel.Engine) {
	for _, ddl := range []string{
		drel.InboxSchema("drel_inbox", "sqlite"),
		`CREATE TABLE payments (message_id TEXT PRIMARY KEY, amount INTEGER NOT NULL)`,
		`CREATE TABLE receipts (message_id TEXT PRIMARY KEY)`,
	} {
		if _, err := engine.Exec(ctx, ddl); err != nil {
			log.Fatal(err)
		}
	}
}

func count(ctx context.Context, engine *drel.Engine, query string) int {
	var n int
	if err := engine.QueryRow(ctx, query).Scan(&n); err != nil {
		log.Fatal(err)
	}
	return n
}
