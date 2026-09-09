// Package es holds an append-only event store. It shares the engine and the
// transaction of the application, so a stream and an aggregate commit together.
//
// The store is not a drel model. Its primary key is the pair of stream and
// version, and drel supports one surrogate key for each model. The statements
// are therefore hand-written SQL.
package es

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alternayte/drel"
)

// ErrConcurrency reports that the stream moved on since the caller read it. The
// expected version no longer matches the stream. Read the stream again, decide
// again, and append again.
var ErrConcurrency = errors.New("es: the stream is at another version")

// Position locates one event in the global log. It is a pair, because the read
// orders by the transaction ID first and the global position second. A single
// number cannot resume the read: a transaction that commits late holds a lower
// global position than one that already reached the reader.
type Position struct {
	XactID    int64
	GlobalPos int64
}

// Before reports whether p comes before other in the read order.
func (p Position) Before(other Position) bool {
	if p.XactID != other.XactID {
		return p.XactID < other.XactID
	}
	return p.GlobalPos < other.GlobalPos
}

// Record is one stored event.
type Record struct {
	StreamID   string
	Version    int64
	Position   Position
	Type       string
	Payload    []byte
	Metadata   []byte
	RecordedAt time.Time
}

// Store appends events to streams and reads them back.
type Store struct {
	engine *drel.Engine
	table  string
}

// NewStore creates a store on one table. The table must exist. Schema emits it.
func NewStore(e *drel.Engine, table string) *Store {
	return &Store{engine: e, table: table}
}

// Append writes the events to one stream inside the transaction that
// drel.WithTx put in the context. The events and the application write
// therefore commit together, or neither commits.
//
// expectedVersion is the version the caller believes the stream holds. A new
// stream holds 0. The first event of the append takes expectedVersion+1. A
// version that already exists returns ErrConcurrency, so two writers that read
// the same state cannot both win.
//
// An empty slice writes nothing and returns nil.
//
// Append panics when the context carries no transaction. Events outside the
// application transaction give no atomicity at all, so this is a wiring fault.
func (s *Store) Append(ctx context.Context, stream string, expectedVersion int64, events []any) error {
	return s.AppendWithMetadata(ctx, stream, expectedVersion, events, nil)
}

// AppendWithMetadata appends the events and stamps every one of them with the
// same metadata. Metadata carries the context of the append, for example the
// correlation ID, the causation ID and the acting user.
func (s *Store) AppendWithMetadata(ctx context.Context, stream string, expectedVersion int64, events []any, metadata map[string]any) error {
	if len(events) == 0 {
		return nil
	}
	tx := drel.MustFromContext(ctx)

	meta := []byte("{}")
	if len(metadata) > 0 {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("es: marshal the metadata for stream %q: %w", stream, err)
		}
		meta = encoded
	}

	for i, event := range events {
		name, err := drel.EventTypeName(event)
		if err != nil {
			return fmt.Errorf("es: name the event at index %d of stream %q: %w", i, stream, err)
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("es: marshal %s of stream %q: %w", name, stream, err)
		}

		version := expectedVersion + int64(i) + 1
		if _, err := tx.Exec(ctx, s.insertSQL(), stream, version, name, string(payload), string(meta)); err != nil {
			if errors.Is(err, drel.ErrUniqueViolation) {
				return fmt.Errorf("es: append %s to stream %q at version %d: %w",
					name, stream, version, ErrConcurrency)
			}
			return fmt.Errorf("es: append %s to stream %q: %w", name, stream, err)
		}
	}
	return nil
}

func (s *Store) quoted() string { return `"` + s.table + `"` }

// sqlite reports whether the engine speaks SQLite or LibSQL.
func (s *Store) sqlite() bool { return s.engine.DialectName() != "postgres" }

// insertSQL builds the append statement. Postgres fills global_pos from the
// sequence. SQLite has no sequence, so the statement reads the next value in
// the same statement. That is safe on SQLite, which serialises writers.
func (s *Store) insertSQL() string {
	if s.sqlite() {
		return fmt.Sprintf(`INSERT INTO %s ("stream_id", "version", "global_pos", "event_type", "payload", "metadata")
VALUES ($1, $2, (SELECT COALESCE(MAX("global_pos"), 0) + 1 FROM %s), $3, $4, $5)`, s.quoted(), s.quoted())
	}
	return fmt.Sprintf(
		`INSERT INTO %s ("stream_id", "version", "event_type", "payload", "metadata") VALUES ($1, $2, $3, $4, $5)`,
		s.quoted())
}

// Schema returns the CREATE TABLE and CREATE INDEX DDL for an event store for
// the given dialect ("postgres" or "sqlite").
//
// The primary key is the pair of stream and version. It gives optimistic
// concurrency at no extra cost: a duplicate version raises a unique violation,
// which Append maps to ErrConcurrency.
//
// On Postgres xact_id holds the transaction ID of the append. ReadAll returns
// only the rows below the transaction watermark, so no open transaction can
// insert an event behind the reader. On SQLite xact_id is always 0, because
// SQLite serialises writers and the insert order is already the commit order.
func Schema(table, dialect string) string {
	q := `"` + table + `"`
	idx := `"idx_` + table + `_read"`

	if dialect == "sqlite" {
		return fmt.Sprintf(`CREATE TABLE %s (
    "stream_id" TEXT NOT NULL,
    "version" INTEGER NOT NULL,
    "global_pos" INTEGER NOT NULL,
    "xact_id" INTEGER NOT NULL DEFAULT 0,
    "event_type" TEXT NOT NULL,
    "payload" TEXT NOT NULL,
    "metadata" TEXT NOT NULL DEFAULT '{}',
    "recorded_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY ("stream_id", "version")
);
CREATE UNIQUE INDEX %s ON %s ("xact_id", "global_pos");
`, q, idx, q)
	}

	return fmt.Sprintf(`CREATE TABLE %s (
    "stream_id" TEXT NOT NULL,
    "version" BIGINT NOT NULL,
    "global_pos" BIGSERIAL NOT NULL,
    "xact_id" BIGINT NOT NULL DEFAULT (pg_current_xact_id()::text::bigint),
    "event_type" TEXT NOT NULL,
    "payload" JSONB NOT NULL,
    "metadata" JSONB NOT NULL DEFAULT '{}'::jsonb,
    "recorded_at" TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY ("stream_id", "version")
);
CREATE UNIQUE INDEX %s ON %s ("xact_id", "global_pos");
`, q, idx, q)
}

// rowsIterator walks a result set of event rows.
type rowsIterator struct {
	rows    drel.Rows
	current Record
	err     error
	done    bool
}

func (it *rowsIterator) Next() bool {
	if it.done || it.err != nil {
		return false
	}
	if !it.rows.Next() {
		it.err = it.rows.Err()
		it.done = true
		return false
	}
	var (
		rec      Record
		payload  string
		metadata string
	)
	if err := it.rows.Scan(&rec.StreamID, &rec.Version, &rec.Position.XactID,
		&rec.Position.GlobalPos, &rec.Type, &payload, &metadata, &rec.RecordedAt); err != nil {
		it.err = fmt.Errorf("es: scan an event: %w", err)
		it.done = true
		return false
	}
	rec.Payload = []byte(payload)
	rec.Metadata = []byte(metadata)
	it.current = rec
	return true
}

// Record returns the event of the current step.
func (it *rowsIterator) Record() Record { return it.current }

// Err returns the first error the walk met.
func (it *rowsIterator) Err() error { return it.err }

// Close releases the result set. Call it when the walk ends.
func (it *rowsIterator) Close() { it.rows.Close() }

// Iterator walks a set of events. Close it when the walk ends.
type Iterator interface {
	Next() bool
	Record() Record
	Err() error
	Close()
}

// columns lists the read columns in scan order.
func (s *Store) columns() string {
	return `"stream_id", "version", "xact_id", "global_pos", "event_type", "payload", "metadata", "recorded_at"`
}

// query runs a read on the transaction in the context when one is present, so a
// caller reads its own writes. It runs on the engine otherwise.
func (s *Store) query(ctx context.Context, sqlText string, args ...any) (drel.Rows, error) {
	if tx, ok := drel.FromContext(ctx); ok {
		return tx.Query(ctx, sqlText, args...)
	}
	return s.engine.Query(ctx, sqlText, args...)
}

// Read returns the events of one stream from fromVersion, inclusive, in version
// order. Use it to rebuild one aggregate.
func (s *Store) Read(ctx context.Context, stream string, fromVersion int64) (Iterator, error) {
	sqlText := fmt.Sprintf(`SELECT %s FROM %s WHERE "stream_id" = $1 AND "version" >= $2 ORDER BY "version"`,
		s.columns(), s.quoted())

	rows, err := s.query(ctx, sqlText, stream, fromVersion)
	if err != nil {
		return nil, fmt.Errorf("es: read stream %q: %w", stream, err)
	}
	return &rowsIterator{rows: rows}, nil
}

// ReadAll returns the events after from, across every stream, in the read order
// of the transaction ID and then the global position. Pass the zero Position to
// start at the beginning, and pass the position of the last record to continue.
//
// On Postgres the read stops at the transaction watermark. An event of a
// transaction that is still open, and every event of a transaction that started
// later, stay invisible until that transaction ends. The reader therefore never
// passes an event that a later call would place behind it. A long write
// transaction delays the reader by that much.
//
// A projection stores the returned position with the read model it wrote, in
// one transaction, and passes it back on the next call.
func (s *Store) ReadAll(ctx context.Context, from Position, limit int) (Iterator, error) {
	if limit < 1 {
		limit = 1
	}

	watermark := ""
	if !s.sqlite() {
		watermark = `"xact_id" < (pg_snapshot_xmin(pg_current_snapshot())::text::bigint) AND `
	}

	sqlText := fmt.Sprintf(`SELECT %s FROM %s
WHERE %s("xact_id", "global_pos") > ($1, $2)
ORDER BY "xact_id", "global_pos"
LIMIT $3`, s.columns(), s.quoted(), watermark)

	rows, err := s.query(ctx, sqlText, from.XactID, from.GlobalPos, limit)
	if err != nil {
		return nil, fmt.Errorf("es: read the log: %w", err)
	}
	return &rowsIterator{rows: rows}, nil
}
