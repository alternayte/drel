//go:build integration

package drel_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgRelayEngine returns a Postgres engine with an outbox table.
func pgRelayEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine := setupTestDB(t)
	_, err := engine.Exec(context.Background(), drel.OutboxSchema("outbox", "postgres"))
	require.NoError(t, err)
	return engine
}

func pgSeedOutbox(t *testing.T, e *drel.Engine, typ, payload, key string) int64 {
	t.Helper()
	var id int64
	if key == "" {
		require.NoError(t, e.QueryRow(context.Background(),
			`INSERT INTO outbox (type, payload) VALUES ($1, $2) RETURNING id`, typ, payload).Scan(&id))
		return id
	}
	require.NoError(t, e.QueryRow(context.Background(),
		`INSERT INTO outbox (type, payload, partition_key) VALUES ($1, $2, $3) RETURNING id`,
		typ, payload, key).Scan(&id))
	return id
}

// countingPublisher records every message it sees, with its worker name.
type countingPublisher struct {
	mu     sync.Mutex
	worker string
	seen   map[int64][]string
	delay  time.Duration
}

func (p *countingPublisher) Publish(ctx context.Context, msg drel.ClaimedMessage) error {
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen[msg.ID] = append(p.seen[msg.ID], p.worker)
	return nil
}

// TestIntegration_Relay_TwoWorkersPublishEachMessageOnce is the property the
// host depends on. Two replicas poll one table and no message goes out twice.
func TestIntegration_Relay_TwoWorkersPublishEachMessageOnce(t *testing.T) {
	engine := pgRelayEngine(t)
	ctx := context.Background()

	const total = 60
	for i := 0; i < total; i++ {
		pgSeedOutbox(t, engine, "t", fmt.Sprintf(`{"i":%d}`, i), "")
	}

	shared := &sync.Mutex{}
	seen := make(map[int64][]string)

	newRelay := func(name string) *drel.Relay {
		pub := drel.PublisherFunc(func(ctx context.Context, msg drel.ClaimedMessage) error {
			time.Sleep(2 * time.Millisecond) // widen the window for a double claim
			shared.Lock()
			defer shared.Unlock()
			seen[msg.ID] = append(seen[msg.ID], name)
			return nil
		})
		return drel.NewRelay(engine, "outbox", pub,
			drel.WithRelayWorkerID(name),
			drel.WithRelayBatchSize(7),
			drel.WithRelayLease(time.Minute))
	}

	var wg sync.WaitGroup
	for _, name := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			relay := newRelay(name)
			for {
				n, err := relay.RunOnce(ctx)
				require.NoError(t, err)
				if n == 0 {
					return
				}
			}
		}(name)
	}
	wg.Wait()

	shared.Lock()
	defer shared.Unlock()
	assert.Len(t, seen, total, "every message must be published")
	for id, workers := range seen {
		assert.Len(t, workers, 1, "message %d was published by %v", id, workers)
	}

	var unprocessed int
	require.NoError(t, engine.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE processed_at IS NULL`).Scan(&unprocessed))
	assert.Equal(t, 0, unprocessed)
}

// TestIntegration_Relay_SkipLockedDoesNotBlock proves the second worker returns
// at once while the first one holds a batch, instead of waiting on the lock.
func TestIntegration_Relay_SkipLockedDoesNotBlock(t *testing.T) {
	engine := pgRelayEngine(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		pgSeedOutbox(t, engine, "t", "{}", "")
	}

	release := make(chan struct{})
	slow := drel.PublisherFunc(func(context.Context, drel.ClaimedMessage) error {
		<-release
		return nil
	})
	first := drel.NewRelay(engine, "outbox", slow,
		drel.WithRelayWorkerID("first"), drel.WithRelayLease(time.Minute))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = first.RunOnce(ctx)
	}()

	require.Eventually(t, func() bool {
		var n int
		require.NoError(t, engine.QueryRow(ctx,
			`SELECT count(*) FROM outbox WHERE claimed_until IS NOT NULL`).Scan(&n))
		return n == 5
	}, 5*time.Second, 10*time.Millisecond)

	second := drel.NewRelay(engine, "outbox", &countingPublisher{worker: "second", seen: map[int64][]string{}},
		drel.WithRelayWorkerID("second"))

	start := time.Now()
	n, err := second.RunOnce(ctx)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Less(t, elapsed, 2*time.Second, "the claim must skip locked rows, not wait for them")

	close(release)
	<-done
}

// TestIntegration_Relay_PartitionOrderAcrossWorkers proves one aggregate keeps
// its order even while two workers run.
func TestIntegration_Relay_PartitionOrderAcrossWorkers(t *testing.T) {
	engine := pgRelayEngine(t)
	ctx := context.Background()

	var want []int64
	for i := 0; i < 20; i++ {
		want = append(want, pgSeedOutbox(t, engine, "t", "{}", "order-1"))
	}

	var mu sync.Mutex
	var got []int64
	newRelay := func(name string) *drel.Relay {
		pub := drel.PublisherFunc(func(ctx context.Context, msg drel.ClaimedMessage) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, msg.ID)
			return nil
		})
		return drel.NewRelay(engine, "outbox", pub,
			drel.WithRelayWorkerID(name), drel.WithRelayBatchSize(6), drel.WithRelayLease(time.Minute))
	}

	var wg sync.WaitGroup
	for _, name := range []string{"a", "b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			relay := newRelay(name)
			for {
				n, err := relay.RunOnce(ctx)
				require.NoError(t, err)
				if n == 0 {
					return
				}
			}
		}(name)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, want, got, "one partition must publish in id order across workers")
}

// TestIntegration_Relay_DeadLetterStopsClaiming proves a dead message stays put
// and is never claimed again.
func TestIntegration_Relay_DeadLetterStopsClaiming(t *testing.T) {
	engine := pgRelayEngine(t)
	ctx := context.Background()
	id := pgSeedOutbox(t, engine, "t", "{}", "")

	boom := errors.New("always fails")
	pub := drel.PublisherFunc(func(context.Context, drel.ClaimedMessage) error { return boom })
	relay := drel.NewRelay(engine, "outbox", pub, drel.WithRelayMaxAttempts(3))

	for i := 0; i < 3; i++ {
		_, err := relay.RunOnce(ctx)
		require.Error(t, err)
	}

	var attempts int
	var deadAt *time.Time
	require.NoError(t, engine.QueryRow(ctx,
		`SELECT attempts, dead_at FROM outbox WHERE id = $1`, id).Scan(&attempts, &deadAt))
	assert.Equal(t, 3, attempts)
	require.NotNil(t, deadAt)

	n, err := relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
