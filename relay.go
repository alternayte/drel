package drel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// ClaimedMessage is one outbox row that a Relay holds a lease on.
type ClaimedMessage struct {
	ID           int64
	Type         string
	Payload      []byte
	PartitionKey string
	// Attempts counts the claims of this message, this one included. The first
	// delivery carries 1.
	Attempts int
}

// Publisher hands a claimed message to the transport. The host implements it.
// A nil error marks the message processed. An error records the failure and
// leaves the message for another attempt.
type Publisher interface {
	Publish(ctx context.Context, msg ClaimedMessage) error
}

// PublisherFunc adapts a function to the Publisher interface.
type PublisherFunc func(ctx context.Context, msg ClaimedMessage) error

// Publish calls f.
func (f PublisherFunc) Publish(ctx context.Context, msg ClaimedMessage) error {
	return f(ctx, msg)
}

type relayConfig struct {
	batchSize   int
	lease       time.Duration
	poll        time.Duration
	maxAttempts int
	workerID    string
	concurrency int
	onDead      func(ctx context.Context, msg ClaimedMessage, err error)
	onError     func(ctx context.Context, err error)
	now         func() time.Time
}

// RelayOption configures a Relay.
type RelayOption func(*relayConfig)

// WithRelayBatchSize sets how many messages one claim takes. The default is 100.
func WithRelayBatchSize(n int) RelayOption {
	return func(c *relayConfig) { c.batchSize = n }
}

// WithRelayLease sets how long a claim holds a message. Another replica can
// claim the message again after the lease ends. Set it well above the time one
// publish takes. The default is 30 seconds.
func WithRelayLease(d time.Duration) RelayOption {
	return func(c *relayConfig) { c.lease = d }
}

// WithRelayPollInterval sets the wait between two empty polls. The default is
// one second. Run polls again at once after a full batch.
func WithRelayPollInterval(d time.Duration) RelayOption {
	return func(c *relayConfig) { c.poll = d }
}

// WithRelayMaxAttempts sets how many claims a message gets before the relay
// marks it dead. The default is 5. A value below 1 disables the dead-letter
// path, and the relay retries forever.
func WithRelayMaxAttempts(n int) RelayOption {
	return func(c *relayConfig) { c.maxAttempts = n }
}

// WithRelayWorkerID names this replica in the claimed_by column. The default is
// the host name and the process ID.
func WithRelayWorkerID(id string) RelayOption {
	return func(c *relayConfig) { c.workerID = id }
}

// WithRelayConcurrency limits how many partitions publish at the same time. The
// default is 8.
func WithRelayConcurrency(n int) RelayOption {
	return func(c *relayConfig) { c.concurrency = n }
}

// WithRelayOnDead registers a callback that runs one time for each message that
// reaches the attempt limit. Use it to alert a person. The callback must not
// block for long.
func WithRelayOnDead(fn func(ctx context.Context, msg ClaimedMessage, err error)) RelayOption {
	return func(c *relayConfig) { c.onDead = fn }
}

// WithRelayOnError registers a callback for an error that Run cannot return,
// because Run only ends when the context ends. Without this option Run logs the
// error through the engine logger at error level. Register it in production, so
// that a claim failure or a publish failure reaches your alerting.
func WithRelayOnError(fn func(ctx context.Context, err error)) RelayOption {
	return func(c *relayConfig) { c.onError = fn }
}

// withRelayClock replaces the clock. Tests use it to expire a lease.
func withRelayClock(fn func() time.Time) RelayOption {
	return func(c *relayConfig) { c.now = fn }
}

// Relay publishes outbox messages to a transport and marks them processed.
//
// More than one replica can run a Relay against one table. A claim takes a
// lease on a batch of messages, so a second replica skips the messages that the
// first one holds. Postgres claims with FOR UPDATE SKIP LOCKED. SQLite and
// LibSQL claim with a conditional UPDATE, which is safe because SQLite
// serialises writers.
//
// Delivery is at-least-once, not exactly-once. A publish that outlives its
// lease can run a second time on another replica. Make the transport or the
// consumer idempotent.
//
// Every replica must run a synchronised clock. The lease compares the times
// that the replicas write.
type Relay struct {
	engine *Engine
	table  string
	pub    Publisher
	cfg    relayConfig
}

// NewRelay creates a relay for one outbox table. The table must carry the lease
// columns that OutboxSchema emits.
func NewRelay(e *Engine, table string, pub Publisher, opts ...RelayOption) *Relay {
	cfg := relayConfig{
		batchSize:   100,
		lease:       30 * time.Second,
		poll:        time.Second,
		maxAttempts: 5,
		concurrency: 8,
		workerID:    defaultWorkerID(),
		now:         time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.batchSize < 1 {
		cfg.batchSize = 1
	}
	if cfg.concurrency < 1 {
		cfg.concurrency = 1
	}
	return &Relay{engine: e, table: table, pub: pub, cfg: cfg}
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return host + "-" + strconv.Itoa(os.Getpid())
}

// Run polls the outbox until ctx ends. It returns the context error.
//
// Run polls again at once after a full batch, and it waits for the poll
// interval after a short batch. A claim or publish error does not stop the
// loop. Run reports the error through the engine's error hooks and continues.
func (r *Relay) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.RunOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			r.reportError(ctx, err)
		case n >= r.cfg.batchSize:
			continue // a full batch means more work waits
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.cfg.poll):
		}
	}
}

// reportError hands a batch error to the OnError callback, or to the engine
// logger when no callback is registered. Run cannot return the error, so it
// must never disappear.
func (r *Relay) reportError(ctx context.Context, err error) {
	if r.cfg.onError != nil {
		defer func() { _ = recover() }()
		r.cfg.onError(ctx, err)
		return
	}
	logger := r.engine.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.ErrorContext(ctx, "drel relay: batch failed", "table", r.table, "error", err)
}

// RunOnce claims one batch, publishes it, and returns how many messages it
// published. A message that fails to publish is not counted.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	batch, err := r.claim(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		for _, key := range batch.keys {
			if relErr := r.releasePartition(ctx, key); relErr != nil {
				r.reportError(ctx, relErr)
			}
		}
	}()
	if batch.count == 0 {
		return 0, nil
	}
	return r.publishBatch(ctx, batch.groups)
}

// claimedBatch is one claim: the publish groups and the partition leases that
// the relay must release when the batch ends.
type claimedBatch struct {
	groups [][]ClaimedMessage
	keys   []string
	count  int
}

// claim takes the next batch of work. It leases whole partitions first, so that
// two replicas never publish one partition at the same time and the order
// inside a partition holds across replicas. Messages with no partition key need
// no order, so the relay claims them row by row.
func (r *Relay) claim(ctx context.Context) (claimedBatch, error) {
	var batch claimedBatch

	keys, err := r.candidatePartitions(ctx)
	if err != nil {
		return batch, err
	}

	for _, key := range keys {
		if batch.count >= r.cfg.batchSize {
			break
		}
		ok, err := r.leasePartition(ctx, key)
		if err != nil {
			return batch, err
		}
		if !ok {
			continue // another replica owns this partition
		}

		msgs, err := r.claimRows(ctx, key, r.cfg.batchSize-batch.count)
		if err != nil {
			_ = r.releasePartition(ctx, key)
			return batch, err
		}
		if len(msgs) == 0 {
			// The partition emptied between the two statements.
			if relErr := r.releasePartition(ctx, key); relErr != nil {
				return batch, relErr
			}
			continue
		}
		batch.keys = append(batch.keys, key)
		batch.groups = append(batch.groups, msgs)
		batch.count += len(msgs)
	}

	if batch.count < r.cfg.batchSize {
		loose, err := r.claimRows(ctx, "", r.cfg.batchSize-batch.count)
		if err != nil {
			return batch, err
		}
		for _, m := range loose {
			batch.groups = append(batch.groups, []ClaimedMessage{m})
			batch.count++
		}
	}
	return batch, nil
}

// candidatePartitions lists the partition keys that hold open work, in the order
// of their oldest message. A partition whose rows all carry a live lease is
// skipped.
func (r *Relay) candidatePartitions(ctx context.Context) ([]string, error) {
	sqlText := fmt.Sprintf(`SELECT "partition_key", MIN("id") AS head FROM %s
WHERE "processed_at" IS NULL AND "dead_at" IS NULL AND "partition_key" IS NOT NULL
  AND ("claimed_until" IS NULL OR "claimed_until" < $1)
GROUP BY "partition_key"
ORDER BY head
LIMIT $2`, r.quoted(r.table))

	rows, err := r.engine.Query(ctx, r.bind(sqlText), r.cfg.now(), r.cfg.batchSize)
	if err != nil {
		return nil, fmt.Errorf("drel: relay list partitions of %s: %w", r.table, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var (
			key  string
			head int64
		)
		if err := rows.Scan(&key, &head); err != nil {
			return nil, fmt.Errorf("drel: relay list partitions of %s: %w", r.table, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drel: relay list partitions of %s: %w", r.table, err)
	}
	return keys, nil
}

// leasePartition takes the lease on one partition. It reports false when
// another replica holds a live lease. The upsert is atomic, so two replicas
// cannot both win.
func (r *Relay) leasePartition(ctx context.Context, key string) (bool, error) {
	now := r.cfg.now()
	sqlText := fmt.Sprintf(`INSERT INTO %s ("partition_key", "claimed_by", "claimed_until")
VALUES ($1, $2, $3)
ON CONFLICT ("partition_key") DO UPDATE SET "claimed_by" = $4, "claimed_until" = $5
WHERE %s."claimed_until" < $6
RETURNING "partition_key"`, r.quoted(r.partitionTable()), r.quoted(r.partitionTable()))

	rows, err := r.engine.Query(ctx, r.bind(sqlText),
		key, r.cfg.workerID, now.Add(r.cfg.lease),
		r.cfg.workerID, now.Add(r.cfg.lease), now)
	if err != nil {
		return false, fmt.Errorf("drel: relay lease partition %q: %w", key, err)
	}
	defer rows.Close()

	won := rows.Next()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("drel: relay lease partition %q: %w", key, err)
	}
	return won, nil
}

// releasePartition gives the partition back. Only the holder can release it.
func (r *Relay) releasePartition(ctx context.Context, key string) error {
	sqlText := fmt.Sprintf(`DELETE FROM %s WHERE "partition_key" = $1 AND "claimed_by" = $2`,
		r.quoted(r.partitionTable()))
	if _, err := r.engine.Exec(ctx, r.bind(sqlText), key, r.cfg.workerID); err != nil {
		return fmt.Errorf("drel: relay release partition %q: %w", key, err)
	}
	return nil
}

// claimRows takes a lease on open messages and increments their attempt
// counters. An empty key claims the messages that carry no partition key.
func (r *Relay) claimRows(ctx context.Context, key string, limit int) ([]ClaimedMessage, error) {
	if limit < 1 {
		return nil, nil
	}
	now := r.cfg.now()
	args := []any{r.cfg.workerID, now.Add(r.cfg.lease), now}
	if key != "" {
		args = append(args, key)
	}
	args = append(args, limit)

	rows, err := r.engine.Query(ctx, r.claimSQL(key != ""), args...)
	if err != nil {
		return nil, fmt.Errorf("drel: relay claim on %s: %w", r.table, err)
	}
	defer rows.Close()

	var msgs []ClaimedMessage
	for rows.Next() {
		var (
			m       ClaimedMessage
			payload string
			pk      sql.NullString
		)
		if err := rows.Scan(&m.ID, &m.Type, &payload, &pk, &m.Attempts); err != nil {
			return nil, fmt.Errorf("drel: relay claim scan on %s: %w", r.table, err)
		}
		m.Payload = []byte(payload)
		m.PartitionKey = pk.String
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drel: relay claim on %s: %w", r.table, err)
	}

	// The RETURNING order is not defined. A partition publishes in id order.
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	return msgs, nil
}

// claimSQL builds the claim statement for the engine's dialect. Postgres skips
// the rows another replica holds with FOR UPDATE SKIP LOCKED. SQLite serialises
// writers, so the conditional UPDATE is enough on its own.
func (r *Relay) claimSQL(keyed bool) string {
	t := r.quoted(r.table)

	filter := `"partition_key" IS NULL`
	if keyed {
		filter = `"partition_key" = $4`
	}

	skipLocked := "\n        FOR UPDATE SKIP LOCKED"
	if r.engine.dialect().UsesQuestionPlaceholders() {
		skipLocked = ""
	}

	limitArg := "$4"
	if keyed {
		limitArg = "$5"
	}

	sqlText := fmt.Sprintf(`UPDATE %s SET "claimed_by" = $1, "claimed_until" = $2, "attempts" = "attempts" + 1
WHERE "id" IN (
    SELECT "id" FROM %s
    WHERE "processed_at" IS NULL AND "dead_at" IS NULL
      AND ("claimed_until" IS NULL OR "claimed_until" < $3)
      AND %s
    ORDER BY "id"
    LIMIT %s%s
)
RETURNING "id", "type", "payload", "partition_key", "attempts"`, t, t, filter, limitArg, skipLocked)

	return r.bind(sqlText)
}

// partitionTable names the partition lease table of this outbox.
func (r *Relay) partitionTable() string { return r.table + "_partitions" }

// quoted wraps an identifier in double quotes.
func (r *Relay) quoted(name string) string { return `"` + name + `"` }

// publishBatch groups the messages by partition key and publishes each group.
// One group publishes in id order and stops at its first failure, so the order
// inside a partition holds. Groups run in parallel. A message with no key is
// its own group and needs no order.
func (r *Relay) publishBatch(ctx context.Context, groups [][]ClaimedMessage) (int, error) {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		published int
		errs      []error
	)
	sem := make(chan struct{}, r.cfg.concurrency)

	for _, group := range groups {
		wg.Add(1)
		go func(group []ClaimedMessage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			for i, msg := range group {
				err := r.publishOne(ctx, msg)
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
				} else {
					published++
				}
				mu.Unlock()
				if err != nil {
					// Hold the order inside this partition. The rest of the
					// group was claimed but never tried, so give the lease and
					// the attempt back.
					if relErr := r.release(ctx, group[i+1:]); relErr != nil {
						mu.Lock()
						errs = append(errs, relErr)
						mu.Unlock()
					}
					return
				}
			}
		}(group)
	}
	wg.Wait()

	return published, errors.Join(errs...)
}

// release gives back the lease and the attempt of messages that the relay
// claimed but never tried, because an earlier message of their partition
// failed. Without this the messages wait for the lease to expire, and they
// reach the dead-letter limit without one publish attempt.
func (r *Relay) release(ctx context.Context, msgs []ClaimedMessage) error {
	var errs []error
	for _, msg := range msgs {
		sqlText := fmt.Sprintf(
			`UPDATE "%s" SET "claimed_by" = NULL, "claimed_until" = NULL, "attempts" = "attempts" - 1 WHERE "id" = $1`,
			r.table)
		if _, err := r.engine.Exec(ctx, r.bind(sqlText), msg.ID); err != nil {
			errs = append(errs, fmt.Errorf("drel: relay release message %d: %w", msg.ID, err))
		}
	}
	return errors.Join(errs...)
}

// publishOne publishes one message and records the outcome.
func (r *Relay) publishOne(ctx context.Context, msg ClaimedMessage) error {
	if err := r.pub.Publish(ctx, msg); err != nil {
		if markErr := r.fail(ctx, msg, err); markErr != nil {
			return errors.Join(err, markErr)
		}
		return fmt.Errorf("drel: relay publish message %d: %w", msg.ID, err)
	}
	return r.ack(ctx, msg)
}

// ack marks the message processed and releases the lease.
func (r *Relay) ack(ctx context.Context, msg ClaimedMessage) error {
	sqlText := fmt.Sprintf(
		`UPDATE "%s" SET "processed_at" = $1, "claimed_by" = NULL, "claimed_until" = NULL WHERE "id" = $2`,
		r.table)
	if _, err := r.engine.Exec(ctx, r.bind(sqlText), r.cfg.now(), msg.ID); err != nil {
		return fmt.Errorf("drel: relay ack message %d: %w", msg.ID, err)
	}
	return nil
}

// fail records the publish error and releases the lease. A message that reached
// the attempt limit is marked dead, and the OnDead callback runs one time.
func (r *Relay) fail(ctx context.Context, msg ClaimedMessage, cause error) error {
	dead := r.cfg.maxAttempts >= 1 && msg.Attempts >= r.cfg.maxAttempts

	sqlText := fmt.Sprintf(
		`UPDATE "%s" SET "last_error" = $1, "claimed_by" = NULL, "claimed_until" = NULL WHERE "id" = $2`,
		r.table)
	args := []any{cause.Error(), msg.ID}
	if dead {
		// The placeholder rewrite for SQLite binds by textual order, so the
		// numbers must run in the order they appear.
		sqlText = fmt.Sprintf(
			`UPDATE "%s" SET "last_error" = $1, "claimed_by" = NULL, "claimed_until" = NULL, "dead_at" = $2 WHERE "id" = $3`,
			r.table)
		args = []any{cause.Error(), r.cfg.now(), msg.ID}
	}
	if _, err := r.engine.Exec(ctx, r.bind(sqlText), args...); err != nil {
		return fmt.Errorf("drel: relay record failure for message %d: %w", msg.ID, err)
	}

	if dead && r.cfg.onDead != nil {
		r.cfg.onDead(ctx, msg, cause)
	}
	return nil
}

// bind rewrites $N placeholders to ? for the dialects that need it.
func (r *Relay) bind(sqlText string) string {
	if r.engine.dialect().UsesQuestionPlaceholders() {
		return rewritePlaceholders(sqlText)
	}
	return sqlText
}
