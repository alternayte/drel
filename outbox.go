package drel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// OutboxMessage is a row written to the transactional outbox. Type identifies
// the event kind; Payload is JSON-serialized into the outbox table's payload
// column. Messages are written within the same transaction as the changes that
// produced the events, giving exactly-once hand-off to an external relay
// (Debezium CDC, a polling worker, etc.).
// PartitionKey orders the relay publish. Messages that carry the same key are
// published in id order. Messages with different keys can go in parallel. Leave
// it empty when the message needs no order; the column then stays NULL.
type OutboxMessage struct {
	Type         string
	Payload      any
	PartitionKey string
}

type outboxConfig struct {
	table  string
	mapper func(event any) (OutboxMessage, bool)
}

// OutboxOption configures UseOutbox.
type OutboxOption func(*outboxConfig)

// WithOutboxMapper customizes how domain events are mapped to outbox messages.
// Return ok=false to skip an event. The default maps every event to a message
// whose Type is the event's package-qualified Go type name (PkgPath + "." +
// Name, e.g. "github.com/acme/orders.OrderPlaced") and whose Payload is the
// event itself. The qualified name avoids cross-package collisions in the
// feature-slice layout where two packages may each define an "OrderPlaced".
func WithOutboxMapper(fn func(event any) (OutboxMessage, bool)) OutboxOption {
	return func(c *outboxConfig) { c.mapper = fn }
}

// UseOutbox writes domain events to a transactional outbox table on every
// SaveChanges, within the same transaction as the changes. The table must exist
// (see OutboxSchema) with at least (type, payload) columns; payload is stored as
// JSON text. Events are dispatched to after-commit handlers as usual in addition
// to being persisted.
func (e *Engine) UseOutbox(table string, opts ...OutboxOption) {
	cfg := &outboxConfig{table: table, mapper: defaultOutboxMapper}
	for _, o := range opts {
		o(cfg)
	}
	e.addEventSink(func(ctx context.Context, tx *Tx, events []any) error {
		for _, ev := range events {
			msg, ok := cfg.mapper(ev)
			if !ok {
				continue
			}
			if msg.Type == "" {
				return fmt.Errorf("drel: outbox: %w", ErrOutboxAnonymousEvent)
			}
			payload, err := json.Marshal(msg.Payload)
			if err != nil {
				return fmt.Errorf("drel: outbox marshal %s: %w", msg.Type, err)
			}
			cols := []string{"type", "payload"}
			vals := []any{msg.Type, string(payload)}
			if msg.PartitionKey != "" {
				cols = append(cols, "partition_key")
				vals = append(vals, msg.PartitionKey)
			}
			res := e.dia.BuildInsert(cfg.table, cols, vals, nil)
			if _, err := tx.Exec(ctx, res.SQL, res.Args...); err != nil {
				return fmt.Errorf("drel: outbox insert into %s: %w", cfg.table, err)
			}
		}
		return nil
	})
}

func defaultOutboxMapper(ev any) (OutboxMessage, bool) {
	name, err := eventTypeName(ev)
	if err != nil {
		// Surface as a message whose Type is empty so UseOutbox can detect and
		// fail loudly; the mapper signature cannot return an error directly.
		return OutboxMessage{Type: "", Payload: ev}, true
	}
	return OutboxMessage{Type: name, Payload: ev}, true
}

// ErrOutboxAnonymousEvent is returned by the default outbox mapping path when an
// event value has no resolvable type name (nil, or an anonymous type). Such
// events would otherwise be written with an empty type, which a relay cannot
// route. Provide a WithOutboxMapper to map anonymous events explicitly.
var ErrOutboxAnonymousEvent = errors.New("drel: outbox event has no qualified type name (nil or anonymous type)")

// eventTypeName returns the package-qualified Go type name of an event value
// (PkgPath + "." + Name), unwrapping a pointer. It returns ErrOutboxAnonymousEvent
// for nil or anonymous types (which have an empty Name).
func eventTypeName(v any) (string, error) {
	t := reflect.TypeOf(v)
	if t == nil {
		return "", ErrOutboxAnonymousEvent
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Name() == "" {
		return "", ErrOutboxAnonymousEvent
	}
	if pkg := t.PkgPath(); pkg != "" {
		return pkg + "." + t.Name(), nil
	}
	// Builtin/unnamed-package types (rare for events) fall back to bare name.
	return t.Name(), nil
}

// OutboxSchema returns CREATE TABLE (and a partial index) DDL for an outbox
// table for the given dialect ("postgres" or "sqlite"). The payload column is
// TEXT for portability (JSON is stored as a string); processed_at is left for a
// relay to stamp.
//
// The lease columns (claimed_by, claimed_until, attempts, last_error, dead_at)
// let more than one Relay replica poll one table without publishing a message
// two times. partition_key orders the publish inside one aggregate.
//
// A partial index on open rows is emitted so the relay claim
//
//	SELECT id FROM <table> WHERE processed_at IS NULL AND dead_at IS NULL ORDER BY id;
//
// uses an index seek rather than a sequential scan that grows with total outbox
// size (processed rows are typically retained for audit).
func OutboxSchema(table, dialect string) string {
	q := `"` + table + `"`
	idx := `"idx_` + table + `_unprocessed"`
	index := fmt.Sprintf(
		`CREATE INDEX %s ON %s ("id") WHERE "processed_at" IS NULL AND "dead_at" IS NULL;`+"\n", idx, q)
	if dialect == "sqlite" {
		return fmt.Sprintf(`CREATE TABLE %s (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT,
    "type" TEXT NOT NULL,
    "payload" TEXT NOT NULL,
    "partition_key" TEXT,
    "created_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "processed_at" DATETIME,
    "claimed_by" TEXT,
    "claimed_until" DATETIME,
    "attempts" INTEGER NOT NULL DEFAULT 0,
    "last_error" TEXT,
    "dead_at" DATETIME
);
%s%s`, q, index, partitionSchema(table, "sqlite"))
	}
	return fmt.Sprintf(`CREATE TABLE %s (
    "id" BIGSERIAL PRIMARY KEY,
    "type" TEXT NOT NULL,
    "payload" TEXT NOT NULL,
    "partition_key" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    "processed_at" TIMESTAMPTZ,
    "claimed_by" TEXT,
    "claimed_until" TIMESTAMPTZ,
    "attempts" INTEGER NOT NULL DEFAULT 0,
    "last_error" TEXT,
    "dead_at" TIMESTAMPTZ
);
%s%s`, q, index, partitionSchema(table, "postgres"))
}

// partitionSchema emits the partition lease table of an outbox. A Relay leases a
// partition key here before it claims the messages of that partition, so two
// replicas never publish one partition at the same time. The order inside a
// partition therefore holds across replicas.
func partitionSchema(table, dialect string) string {
	q := `"` + table + `_partitions"`
	if dialect == "sqlite" {
		return fmt.Sprintf(`CREATE TABLE %s (
    "partition_key" TEXT PRIMARY KEY,
    "claimed_by" TEXT NOT NULL,
    "claimed_until" DATETIME NOT NULL
);
`, q)
	}
	return fmt.Sprintf(`CREATE TABLE %s (
    "partition_key" TEXT PRIMARY KEY,
    "claimed_by" TEXT NOT NULL,
    "claimed_until" TIMESTAMPTZ NOT NULL
);
`, q)
}
