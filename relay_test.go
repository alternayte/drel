package drel_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relayEngine returns an in-memory SQLite engine with an outbox table.
func relayEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })
	_, err = engine.Exec(context.Background(), drel.OutboxSchema("outbox", "sqlite"))
	require.NoError(t, err)
	return engine
}

// seedOutbox inserts one message and returns its id.
func seedOutbox(t *testing.T, e *drel.Engine, typ, payload, key string) int64 {
	t.Helper()
	ctx := context.Background()
	var (
		err error
		id  int64
	)
	if key == "" {
		_, err = e.Exec(ctx, `INSERT INTO outbox (type, payload) VALUES (?, ?)`, typ, payload)
	} else {
		_, err = e.Exec(ctx, `INSERT INTO outbox (type, payload, partition_key) VALUES (?, ?, ?)`, typ, payload, key)
	}
	require.NoError(t, err)
	require.NoError(t, e.QueryRow(ctx, `SELECT max(id) FROM outbox`).Scan(&id))
	return id
}

// recorder collects the messages a relay publishes.
type recorder struct {
	mu   sync.Mutex
	got  []drel.ClaimedMessage
	fail func(msg drel.ClaimedMessage) error
}

func (r *recorder) Publish(ctx context.Context, msg drel.ClaimedMessage) error {
	if r.fail != nil {
		if err := r.fail(msg); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, msg)
	return nil
}

func (r *recorder) ids() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, len(r.got))
	for i, m := range r.got {
		out[i] = m.ID
	}
	return out
}

func outboxScalar(t *testing.T, e *drel.Engine, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, e.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func TestRelay_PublishesAndMarksProcessed(t *testing.T) {
	engine := relayEngine(t)
	id := seedOutbox(t, engine, "item.created", `{"a":1}`, "")

	rec := &recorder{}
	relay := drel.NewRelay(engine, "outbox", rec)

	n, err := relay.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	require.Len(t, rec.got, 1)
	assert.Equal(t, id, rec.got[0].ID)
	assert.Equal(t, "item.created", rec.got[0].Type)
	assert.Equal(t, `{"a":1}`, string(rec.got[0].Payload))
	assert.Equal(t, 1, rec.got[0].Attempts, "the first delivery must report one attempt")

	assert.Equal(t, 1, outboxScalar(t, engine, `SELECT count(*) FROM outbox WHERE processed_at IS NOT NULL`))
	assert.Equal(t, 1, outboxScalar(t, engine, `SELECT count(*) FROM outbox WHERE claimed_by IS NULL AND claimed_until IS NULL`))
}

func TestRelay_SecondPassFindsNothing(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "")

	rec := &recorder{}
	relay := drel.NewRelay(engine, "outbox", rec)
	ctx := context.Background()

	first, err := relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, first)

	second, err := relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, second, "a processed message must not be claimed again")
	assert.Len(t, rec.got, 1)
}

func TestRelay_FailureRecordsErrorAndRetries(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "")
	ctx := context.Background()

	boom := errors.New("transport down")
	var calls int
	rec := &recorder{fail: func(drel.ClaimedMessage) error {
		calls++
		if calls == 1 {
			return boom
		}
		return nil
	}}
	relay := drel.NewRelay(engine, "outbox", rec)

	n, err := relay.RunOnce(ctx)
	assert.Equal(t, 0, n)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)

	var lastErr string
	require.NoError(t, engine.QueryRow(ctx, `SELECT last_error FROM outbox`).Scan(&lastErr))
	assert.Equal(t, "transport down", lastErr)
	assert.Equal(t, 1, outboxScalar(t, engine, `SELECT count(*) FROM outbox WHERE claimed_until IS NULL`),
		"a failed publish must release the lease at once")

	// The retry succeeds and reports the second attempt.
	n, err = relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, rec.got, 1)
	assert.Equal(t, 2, rec.got[0].Attempts)
}

func TestRelay_DeadLettersAfterMaxAttempts(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "")
	ctx := context.Background()

	boom := errors.New("always fails")
	rec := &recorder{fail: func(drel.ClaimedMessage) error { return boom }}

	var dead []drel.ClaimedMessage
	relay := drel.NewRelay(engine, "outbox", rec,
		drel.WithRelayMaxAttempts(2),
		drel.WithRelayOnDead(func(ctx context.Context, msg drel.ClaimedMessage, err error) {
			dead = append(dead, msg)
		}))

	_, err := relay.RunOnce(ctx) // attempt 1
	require.Error(t, err)
	assert.Empty(t, dead)
	assert.Equal(t, 0, outboxScalar(t, engine, `SELECT count(*) FROM outbox WHERE dead_at IS NOT NULL`))

	_, err = relay.RunOnce(ctx) // attempt 2 reaches the limit
	require.Error(t, err)

	require.Len(t, dead, 1, "OnDead must run one time")
	assert.Equal(t, 2, dead[0].Attempts)
	assert.Equal(t, 1, outboxScalar(t, engine, `SELECT count(*) FROM outbox WHERE dead_at IS NOT NULL`))

	// A dead message is never claimed again.
	n, err := relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Len(t, dead, 1)
}

func TestRelay_ClaimedRowIsNotClaimedTwice(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "")
	ctx := context.Background()

	// A publisher that blocks lets the first claim hold its lease.
	release := make(chan struct{})
	slow := drel.PublisherFunc(func(ctx context.Context, msg drel.ClaimedMessage) error {
		<-release
		return nil
	})
	first := drel.NewRelay(engine, "outbox", slow, drel.WithRelayLease(time.Minute))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = first.RunOnce(ctx)
	}()

	// Wait until the claim landed.
	require.Eventually(t, func() bool {
		return outboxScalar(t, engine, `SELECT count(*) FROM outbox WHERE claimed_until IS NOT NULL`) == 1
	}, 2*time.Second, 5*time.Millisecond)

	second := drel.NewRelay(engine, "outbox", &recorder{}, drel.WithRelayWorkerID("second"))
	n, err := second.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a leased message must not be claimed by a second relay")

	close(release)
	<-done
}

func TestRelay_ExpiredLeaseIsReclaimed(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "")
	ctx := context.Background()

	// Claim with a lease that has already ended.
	stuck := drel.PublisherFunc(func(context.Context, drel.ClaimedMessage) error {
		return errors.New("worker died")
	})
	dead := drel.NewRelay(engine, "outbox", stuck, drel.WithRelayLease(-time.Minute))
	_, err := dead.RunOnce(ctx)
	require.Error(t, err)

	rec := &recorder{}
	live := drel.NewRelay(engine, "outbox", rec, drel.WithRelayWorkerID("live"))
	n, err := live.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "an expired lease must be reclaimed")
	assert.Equal(t, 2, rec.got[0].Attempts)
}

func TestRelay_PartitionOrderIsKept(t *testing.T) {
	engine := relayEngine(t)
	var want []int64
	for i := 0; i < 5; i++ {
		want = append(want, seedOutbox(t, engine, "t", "{}", "order-1"))
	}

	rec := &recorder{}
	relay := drel.NewRelay(engine, "outbox", rec)

	n, err := relay.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, want, rec.ids(), "one partition must publish in id order")
}

func TestRelay_PartitionFailureStopsOnlyThatKey(t *testing.T) {
	engine := relayEngine(t)
	badFirst := seedOutbox(t, engine, "t", "{}", "bad")
	goodA := seedOutbox(t, engine, "t", "{}", "good")
	badSecond := seedOutbox(t, engine, "t", "{}", "bad")
	goodB := seedOutbox(t, engine, "t", "{}", "good")

	rec := &recorder{fail: func(msg drel.ClaimedMessage) error {
		if msg.PartitionKey == "bad" {
			return errors.New("bad partition")
		}
		return nil
	}}
	relay := drel.NewRelay(engine, "outbox", rec)

	n, err := relay.RunOnce(context.Background())
	require.Error(t, err)
	assert.Equal(t, 2, n)
	assert.ElementsMatch(t, []int64{goodA, goodB}, rec.ids())

	// The second message of the failed partition was claimed but never tried.
	// It must get its lease and its attempt back, so it is claimable at once
	// and it does not reach the dead-letter limit without one attempt.
	assert.Equal(t, 1, outboxScalar(t, engine,
		`SELECT count(*) FROM outbox WHERE id = ? AND attempts = 0 AND claimed_until IS NULL`, badSecond))
	assert.Equal(t, 1, outboxScalar(t, engine,
		`SELECT count(*) FROM outbox WHERE id = ? AND last_error IS NOT NULL AND attempts = 1`, badFirst))

	// The next pass retries the whole failed partition in order.
	rec.fail = nil
	n, err = relay.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []int64{goodA, goodB, badFirst, badSecond}, rec.ids())
}

func TestRelay_LeasedPartitionIsSkippedBySecondWorker(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "order-1")
	seedOutbox(t, engine, "t", "{}", "order-1")
	ctx := context.Background()

	release := make(chan struct{})
	slow := drel.PublisherFunc(func(context.Context, drel.ClaimedMessage) error {
		<-release
		return nil
	})
	first := drel.NewRelay(engine, "outbox", slow,
		drel.WithRelayWorkerID("first"), drel.WithRelayBatchSize(1), drel.WithRelayLease(time.Minute))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = first.RunOnce(ctx)
	}()

	require.Eventually(t, func() bool {
		return outboxScalar(t, engine, `SELECT count(*) FROM outbox_partitions`) == 1
	}, 2*time.Second, 5*time.Millisecond)

	// The second message of that partition is still open, but the partition
	// carries a lease, so a second worker must not take it.
	second := drel.NewRelay(engine, "outbox", &recorder{}, drel.WithRelayWorkerID("second"))
	n, err := second.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a leased partition must not be claimed by a second relay")

	close(release)
	<-done

	// The lease is released when the batch ends.
	assert.Equal(t, 0, outboxScalar(t, engine, `SELECT count(*) FROM outbox_partitions`))
}

func TestRelay_Run_StopsOnContextCancel(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "")

	rec := &recorder{}
	relay := drel.NewRelay(engine, "outbox", rec, drel.WithRelayPollInterval(5*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- relay.Run(ctx) }()

	require.Eventually(t, func() bool { return len(rec.ids()) == 1 }, 2*time.Second, 5*time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when the context ended")
	}
}

func TestRelay_OnErrorReceivesBatchFailure(t *testing.T) {
	engine := relayEngine(t)
	seedOutbox(t, engine, "t", "{}", "")

	boom := errors.New("transport down")
	rec := &recorder{fail: func(drel.ClaimedMessage) error { return boom }}

	var mu sync.Mutex
	var seen []error
	relay := drel.NewRelay(engine, "outbox", rec,
		drel.WithRelayPollInterval(5*time.Millisecond),
		drel.WithRelayOnError(func(ctx context.Context, err error) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, err)
		}))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- relay.Run(ctx) }()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0
	}, 2*time.Second, 5*time.Millisecond)
	cancel()
	<-errCh

	mu.Lock()
	defer mu.Unlock()
	assert.ErrorIs(t, seen[0], boom)
}
