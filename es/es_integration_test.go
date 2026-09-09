//go:build integration

package es_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/dreltest/pgtest"
	"github.com/alternayte/drel/es"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPGStore(t *testing.T) (*drel.Engine, *es.Store) {
	t.Helper()
	engine := pgtest.NewPostgres(t)
	ctx := context.Background()

	_, err := engine.Exec(ctx, es.Schema("drel_events", "postgres"))
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `CREATE TABLE read_model (stream_id TEXT PRIMARY KEY, customer TEXT NOT NULL)`)
	require.NoError(t, err)
	return engine, es.NewStore(engine, "drel_events")
}

func TestIntegration_ES_AppendAndReadAll(t *testing.T) {
	engine, store := newPGStore(t)
	ctx := context.Background()

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-1", 0, []any{
			OrderPlaced{Customer: "Alice", Total: 4200},
			OrderShipped{Carrier: "UPS"},
		})
	}))
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-2", 0, []any{OrderPlaced{Customer: "Bob", Total: 1599}})
	}))

	got := readLog(t, store, es.Position{}, 100)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"order-1", "order-1", "order-2"},
		[]string{got[0].StreamID, got[1].StreamID, got[2].StreamID})
	for i := 1; i < len(got); i++ {
		assert.True(t, got[i-1].Position.Before(got[i].Position))
	}

	stream := readStream(t, store, "order-1", 0)
	require.Len(t, stream, 2)
	assert.Equal(t, int64(1), stream[0].Version)
	assert.Equal(t, int64(2), stream[1].Version)
}

// TestIntegration_ES_EventsCommitWithTheAggregate proves the property the host
// needs: the read model row and the events are one commit.
func TestIntegration_ES_EventsCommitWithTheAggregate(t *testing.T) {
	engine, store := newPGStore(t)
	ctx := context.Background()
	boom := errors.New("the handler failed")

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		if err := store.Append(ctx, "order-1", 0, []any{OrderPlaced{Customer: "Alice"}}); err != nil {
			return err
		}
		if _, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO read_model (stream_id, customer) VALUES ($1, $2)`, "order-1", "Alice"); err != nil {
			return err
		}
		return boom
	})
	assert.ErrorIs(t, err, boom)

	assert.Empty(t, readLog(t, store, es.Position{}, 10))
	var n int
	require.NoError(t, engine.QueryRow(ctx, `SELECT count(*) FROM read_model`).Scan(&n))
	assert.Equal(t, 0, n)
}

// TestIntegration_ES_ConcurrentAppendersOneWins proves the optimistic
// concurrency of the primary key. Eight writers that all read version 0 try to
// append version 1, and exactly one succeeds.
func TestIntegration_ES_ConcurrentAppendersOneWins(t *testing.T) {
	engine, store := newPGStore(t)
	ctx := context.Background()

	const writers = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		conflicts int
		others    []error
	)
	start := make(chan struct{})

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := engine.WithTx(ctx, func(ctx context.Context) error {
				return store.Append(ctx, "order-1", 0, []any{OrderPlaced{Customer: "Alice"}})
			})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, es.ErrConcurrency):
				conflicts++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Empty(t, others, "no writer may fail for another reason")
	assert.Equal(t, 1, succeeded, "exactly one writer may take version 1")
	assert.Equal(t, writers-1, conflicts)
	assert.Len(t, readStream(t, store, "order-1", 0), 1)
}

// TestIntegration_ES_OpenTransactionHoldsTheWatermark proves the decision this
// whole design rests on.
//
// Transaction A appends first and stays open. Transaction B appends after and
// commits. B therefore holds a higher global position but reaches the database
// first. A reader that ordered by global_pos alone would return B, advance its
// checkpoint past A, and lose A for good.
//
// The watermark read must return nothing while A is open, and then both events
// in order once A commits.
func TestIntegration_ES_OpenTransactionHoldsTheWatermark(t *testing.T) {
	engine, store := newPGStore(t)
	ctx := context.Background()

	appended := make(chan struct{})
	commitA := make(chan struct{})
	doneA := make(chan error, 1)

	go func() {
		doneA <- engine.WithTx(ctx, func(ctx context.Context) error {
			if err := store.Append(ctx, "order-A", 0, []any{OrderPlaced{Customer: "Alice"}}); err != nil {
				return err
			}
			close(appended)
			<-commitA // hold the transaction open
			return nil
		})
	}()

	<-appended

	// B starts later, appends later, and commits first.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return store.Append(ctx, "order-B", 0, []any{OrderPlaced{Customer: "Bob"}})
	}))

	// B is committed and A is open. The watermark must hide both.
	assert.Empty(t, readLog(t, store, es.Position{}, 100),
		"the reader must not pass an event that an open transaction still precedes")

	close(commitA)
	require.NoError(t, <-doneA)

	// Both are visible now, and A comes first.
	require.Eventually(t, func() bool {
		return len(readLog(t, store, es.Position{}, 100)) == 2
	}, 5*time.Second, 50*time.Millisecond)

	got := readLog(t, store, es.Position{}, 100)
	require.Len(t, got, 2)
	assert.Equal(t, "order-A", got[0].StreamID, "the earlier transaction must arrive first")
	assert.Equal(t, "order-B", got[1].StreamID)
	assert.True(t, got[0].Position.Before(got[1].Position))
}

// TestIntegration_ES_ProjectionNeverSkips walks the log the way a projection
// does, with a checkpoint, while a slow writer holds a transaction open.
func TestIntegration_ES_ProjectionNeverSkips(t *testing.T) {
	engine, store := newPGStore(t)
	ctx := context.Background()

	slowAppended := make(chan struct{})
	releaseSlow := make(chan struct{})
	slowDone := make(chan error, 1)

	go func() {
		slowDone <- engine.WithTx(ctx, func(ctx context.Context) error {
			if err := store.Append(ctx, "slow", 0, []any{OrderPlaced{Customer: "Slow"}}); err != nil {
				return err
			}
			close(slowAppended)
			<-releaseSlow
			return nil
		})
	}()
	<-slowAppended

	for i := 0; i < 5; i++ {
		require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
			return store.Append(ctx, "fast", int64(i), []any{OrderShipped{Carrier: "UPS"}})
		}))
	}

	// The projection walks with a checkpoint while the slow writer holds on.
	var (
		seen       []string
		checkpoint es.Position
	)
	walk := func() {
		for {
			batch := readLog(t, store, checkpoint, 2)
			if len(batch) == 0 {
				return
			}
			for _, rec := range batch {
				seen = append(seen, rec.StreamID)
				checkpoint = rec.Position
			}
		}
	}

	walk()
	assert.Empty(t, seen, "nothing may pass the open transaction")

	close(releaseSlow)
	require.NoError(t, <-slowDone)

	require.Eventually(t, func() bool {
		walk()
		return len(seen) == 6
	}, 5*time.Second, 50*time.Millisecond)

	assert.Equal(t, "slow", seen[0], "the held event must not be skipped")
	assert.Len(t, seen, 6)
}
