package es_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/es"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Test events ─────────────────────────────────────────────────────────────

type OrderPlaced struct {
	Customer string `json:"customer"`
	Total    int    `json:"total"`
}

type OrderShipped struct {
	Carrier string `json:"carrier"`
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func newStore(t *testing.T) (*drel.Engine, *es.Store) {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })

	_, err = engine.Exec(context.Background(), es.Schema("drel_events", "sqlite"))
	require.NoError(t, err)
	return engine, es.NewStore(engine, "drel_events")
}

// drain collects every record of an iterator.
func drain(t *testing.T, it es.Iterator, err error) []es.Record {
	t.Helper()
	require.NoError(t, err)
	defer it.Close()

	var out []es.Record
	for it.Next() {
		out = append(out, it.Record())
	}
	require.NoError(t, it.Err())
	return out
}

// readStream collects one stream from a version.
func readStream(t *testing.T, s *es.Store, stream string, from int64) []es.Record {
	t.Helper()
	it, err := s.Read(context.Background(), stream, from)
	return drain(t, it, err)
}

// readLog collects the global log from a position.
func readLog(t *testing.T, s *es.Store, from es.Position, limit int) []es.Record {
	t.Helper()
	it, err := s.ReadAll(context.Background(), from, limit)
	return drain(t, it, err)
}

// appendEvents appends in its own transaction.
func appendEvents(t *testing.T, e *drel.Engine, s *es.Store, stream string, expected int64, events ...any) error {
	t.Helper()
	return e.WithTx(context.Background(), func(ctx context.Context) error {
		return s.Append(ctx, stream, expected, events)
	})
}

// ─── Schema ──────────────────────────────────────────────────────────────────

func TestSchema_EmitsBothDialects(t *testing.T) {
	for _, d := range []string{"postgres", "sqlite"} {
		ddl := es.Schema("drel_events", d)
		assert.Contains(t, ddl, `CREATE TABLE "drel_events"`)
		assert.Contains(t, ddl, `PRIMARY KEY ("stream_id", "version")`)
		assert.Contains(t, ddl, `"xact_id"`)
		assert.Contains(t, ddl, `"global_pos"`)
		assert.Contains(t, ddl, `CREATE UNIQUE INDEX "idx_drel_events_read" ON "drel_events" ("xact_id", "global_pos")`)
	}
	assert.Contains(t, es.Schema("drel_events", "postgres"), "pg_current_xact_id()")
}

func TestSchema_ExecutesAgainstSQLite(t *testing.T) {
	engine, _ := newStore(t)
	var n int
	require.NoError(t, engine.QueryRow(context.Background(),
		`SELECT count(*) FROM drel_events`).Scan(&n))
	assert.Equal(t, 0, n)
}

// ─── Append ──────────────────────────────────────────────────────────────────

func TestAppend_WritesEventsWithVersions(t *testing.T) {
	engine, store := newStore(t)

	require.NoError(t, appendEvents(t, engine, store, "order-1", 0,
		OrderPlaced{Customer: "Alice", Total: 4200},
		OrderShipped{Carrier: "UPS"},
	))

	got := readStream(t, store, "order-1", 0)
	require.Len(t, got, 2)

	assert.Equal(t, int64(1), got[0].Version)
	assert.Equal(t, int64(2), got[1].Version)
	assert.Equal(t, "order-1", got[0].StreamID)
	assert.Equal(t, int64(1), got[0].Position.GlobalPos)
	assert.Equal(t, int64(2), got[1].Position.GlobalPos)
	assert.False(t, got[0].RecordedAt.IsZero())
}

func TestAppend_UsesTheQualifiedTypeName(t *testing.T) {
	engine, store := newStore(t)
	require.NoError(t, appendEvents(t, engine, store, "order-1", 0, OrderPlaced{Customer: "Alice"}))

	got := readStream(t, store, "order-1", 0)
	require.Len(t, got, 1)

	want, err := drel.EventTypeName(OrderPlaced{})
	require.NoError(t, err)
	assert.Equal(t, want, got[0].Type)
	assert.Equal(t, "github.com/alternayte/drel/es_test.OrderPlaced", got[0].Type)
}

func TestAppend_StoresThePayload(t *testing.T) {
	engine, store := newStore(t)
	require.NoError(t, appendEvents(t, engine, store, "order-1", 0,
		OrderPlaced{Customer: "Alice", Total: 4200}))

	got := readStream(t, store, "order-1", 0)
	require.Len(t, got, 1)

	var back OrderPlaced
	require.NoError(t, json.Unmarshal(got[0].Payload, &back))
	assert.Equal(t, OrderPlaced{Customer: "Alice", Total: 4200}, back)
	assert.JSONEq(t, `{}`, string(got[0].Metadata))
}

func TestAppendWithMetadata_StoresMetadata(t *testing.T) {
	engine, store := newStore(t)

	require.NoError(t, engine.WithTx(context.Background(), func(ctx context.Context) error {
		return store.AppendWithMetadata(ctx, "order-1", 0,
			[]any{OrderPlaced{Customer: "Alice"}},
			map[string]any{"correlation_id": "abc", "user": "u1"})
	}))

	got := readStream(t, store, "order-1", 0)
	require.Len(t, got, 1)
	assert.JSONEq(t, `{"correlation_id":"abc","user":"u1"}`, string(got[0].Metadata))
}

func TestAppend_PanicsWithoutTransaction(t *testing.T) {
	_, store := newStore(t)

	assert.Panics(t, func() {
		_ = store.Append(context.Background(), "order-1", 0, []any{OrderPlaced{}})
	}, "an append outside the application transaction is a wiring fault")
}

func TestAppend_WrongExpectedVersionIsConcurrency(t *testing.T) {
	engine, store := newStore(t)
	require.NoError(t, appendEvents(t, engine, store, "order-1", 0, OrderPlaced{Customer: "Alice"}))

	// A second writer that read version 0 tries to append at version 1 again.
	err := appendEvents(t, engine, store, "order-1", 0, OrderShipped{Carrier: "UPS"})
	assert.ErrorIs(t, err, es.ErrConcurrency)

	// The stream is unchanged.
	got := readStream(t, store, "order-1", 0)
	assert.Len(t, got, 1)

	// The writer that reads again wins.
	require.NoError(t, appendEvents(t, engine, store, "order-1", 1, OrderShipped{Carrier: "UPS"}))
	got = readStream(t, store, "order-1", 0)
	assert.Len(t, got, 2)
}

func TestAppend_RollsBackWithTheTransaction(t *testing.T) {
	engine, store := newStore(t)
	boom := errors.New("the handler failed")

	err := engine.WithTx(context.Background(), func(ctx context.Context) error {
		if err := store.Append(ctx, "order-1", 0, []any{OrderPlaced{Customer: "Alice"}}); err != nil {
			return err
		}
		return boom
	})
	assert.ErrorIs(t, err, boom)

	got := readStream(t, store, "order-1", 0)
	assert.Empty(t, got, "the events must roll back with the transaction")
}

func TestAppend_EmptyIsNoOp(t *testing.T) {
	_, store := newStore(t)

	// An empty append needs no transaction, and it writes nothing.
	require.NoError(t, store.Append(context.Background(), "order-1", 0, nil))
	assert.Empty(t, readStream(t, store, "order-1", 0))
}

func TestAppend_ReadsItsOwnWrites(t *testing.T) {
	engine, store := newStore(t)

	require.NoError(t, engine.WithTx(context.Background(), func(ctx context.Context) error {
		if err := store.Append(ctx, "order-1", 0, []any{OrderPlaced{Customer: "Alice"}}); err != nil {
			return err
		}
		it, err := store.Read(ctx, "order-1", 0)
		got := drain(t, it, err)
		assert.Len(t, got, 1, "a read in the transaction must see the append")
		return nil
	}))
}

// ─── Read ────────────────────────────────────────────────────────────────────

func TestRead_FromVersionSkipsEarlier(t *testing.T) {
	engine, store := newStore(t)
	require.NoError(t, appendEvents(t, engine, store, "order-1", 0,
		OrderPlaced{Customer: "Alice"}, OrderShipped{Carrier: "UPS"}, OrderShipped{Carrier: "DHL"}))

	got := readStream(t, store, "order-1", 2)
	require.Len(t, got, 2)
	assert.Equal(t, int64(2), got[0].Version)
	assert.Equal(t, int64(3), got[1].Version)
}

func TestRead_OtherStreamsAreNotReturned(t *testing.T) {
	engine, store := newStore(t)
	require.NoError(t, appendEvents(t, engine, store, "order-1", 0, OrderPlaced{Customer: "Alice"}))
	require.NoError(t, appendEvents(t, engine, store, "order-2", 0, OrderPlaced{Customer: "Bob"}))

	got := readStream(t, store, "order-1", 0)
	require.Len(t, got, 1)
	assert.Equal(t, "order-1", got[0].StreamID)
}

func TestRead_UnknownStreamIsEmpty(t *testing.T) {
	_, store := newStore(t)
	assert.Empty(t, readStream(t, store, "nope", 0))
}

// ─── ReadAll ─────────────────────────────────────────────────────────────────

func TestReadAll_ReturnsEveryStreamInPositionOrder(t *testing.T) {
	engine, store := newStore(t)
	require.NoError(t, appendEvents(t, engine, store, "order-1", 0, OrderPlaced{Customer: "Alice"}))
	require.NoError(t, appendEvents(t, engine, store, "order-2", 0, OrderPlaced{Customer: "Bob"}))
	require.NoError(t, appendEvents(t, engine, store, "order-1", 1, OrderShipped{Carrier: "UPS"}))

	got := readLog(t, store, es.Position{}, 100)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"order-1", "order-2", "order-1"},
		[]string{got[0].StreamID, got[1].StreamID, got[2].StreamID})

	for i := 1; i < len(got); i++ {
		assert.True(t, got[i-1].Position.Before(got[i].Position),
			"the log must arrive in position order")
	}
}

func TestReadAll_ResumesFromAPosition(t *testing.T) {
	engine, store := newStore(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, appendEvents(t, engine, store, "order-1", int64(i), OrderShipped{Carrier: "UPS"}))
	}

	first := readLog(t, store, es.Position{}, 2)
	require.Len(t, first, 2)

	rest := readLog(t, store, first[1].Position, 100)
	require.Len(t, rest, 3)
	assert.True(t, first[1].Position.Before(rest[0].Position))
}

func TestReadAll_RespectsTheLimit(t *testing.T) {
	engine, store := newStore(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, appendEvents(t, engine, store, "order-1", int64(i), OrderShipped{Carrier: "UPS"}))
	}

	assert.Len(t, readLog(t, store, es.Position{}, 3), 3)
	assert.Len(t, readLog(t, store, es.Position{}, 1), 1)
}

func TestReadAll_EmptyLogIsEmpty(t *testing.T) {
	_, store := newStore(t)
	assert.Empty(t, readLog(t, store, es.Position{}, 10))
}

func TestPosition_Before(t *testing.T) {
	assert.True(t, es.Position{XactID: 1, GlobalPos: 9}.Before(es.Position{XactID: 2, GlobalPos: 1}),
		"the transaction ID orders first")
	assert.True(t, es.Position{XactID: 2, GlobalPos: 1}.Before(es.Position{XactID: 2, GlobalPos: 2}),
		"the global position orders second")
	assert.False(t, es.Position{XactID: 2, GlobalPos: 2}.Before(es.Position{XactID: 2, GlobalPos: 2}))
}
