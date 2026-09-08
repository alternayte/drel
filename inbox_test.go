package drel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inboxEngine returns an in-memory SQLite engine with an inbox and one table
// that stands for the application aggregate.
func inboxEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine, err := drel.NewEngine(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { engine.Close() })

	ctx := context.Background()
	_, err = engine.Exec(ctx, drel.InboxSchema("drel_inbox", "sqlite"))
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `CREATE TABLE aggregates (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`)
	require.NoError(t, err)
	return engine
}

func inboxCount(t *testing.T, e *drel.Engine, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, e.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func TestInboxSchema_ExecutesAgainstSQLite(t *testing.T) {
	engine := inboxEngine(t)
	ctx := context.Background()

	_, err := engine.Exec(ctx,
		`INSERT INTO drel_inbox (message_id, handler) VALUES ('m1', 'h1')`)
	require.NoError(t, err)

	// The pair is the primary key.
	_, err = engine.Exec(ctx,
		`INSERT INTO drel_inbox (message_id, handler) VALUES ('m1', 'h1')`)
	assert.Error(t, err, "the pair of message and handler must be unique")

	_, err = engine.Exec(ctx,
		`INSERT INTO drel_inbox (message_id, handler) VALUES ('m1', 'h2')`)
	assert.NoError(t, err, "a second handler must be able to record the same message")
}

func TestInboxSchema_EmitsBothDialects(t *testing.T) {
	for _, d := range []string{"postgres", "sqlite"} {
		ddl := drel.InboxSchema("drel_inbox", d)
		assert.Contains(t, ddl, `CREATE TABLE "drel_inbox"`)
		assert.Contains(t, ddl, `PRIMARY KEY ("message_id", "handler")`)
		for _, col := range []string{`"received_at"`, `"processed_at"`, `"attempts"`, `"last_error"`} {
			assert.Contains(t, ddl, col, "dialect %s", d)
		}
	}
}

func TestInbox_ClaimWritesRowInCallerTransaction(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		if err := inbox.Claim(ctx, "m1", "orders"); err != nil {
			return err
		}
		_, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO aggregates (name) VALUES (?)`, "from m1")
		return err
	}))

	assert.Equal(t, 1, inboxCount(t, engine,
		`SELECT count(*) FROM drel_inbox WHERE message_id = 'm1' AND handler = 'orders' AND processed_at IS NOT NULL`))
	assert.Equal(t, 1, inboxCount(t, engine, `SELECT count(*) FROM aggregates`))
	assert.Equal(t, 1, inboxCount(t, engine, `SELECT attempts FROM drel_inbox WHERE message_id = 'm1'`))
}

func TestInbox_ClaimRollsBackWithTheTransaction(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()
	boom := errors.New("handler failed")

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		if err := inbox.Claim(ctx, "m1", "orders"); err != nil {
			return err
		}
		_, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO aggregates (name) VALUES (?)`, "from m1")
		require.NoError(t, err)
		return boom
	})
	assert.ErrorIs(t, err, boom)

	assert.Equal(t, 0, inboxCount(t, engine, `SELECT count(*) FROM drel_inbox`),
		"the dedupe row must roll back with the handler")
	assert.Equal(t, 0, inboxCount(t, engine, `SELECT count(*) FROM aggregates`))
}

func TestInbox_SecondClaimReportsDuplicate(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return inbox.Claim(ctx, "m1", "orders")
	}))

	var second error
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		second = inbox.Claim(ctx, "m1", "orders")
		return nil
	}))
	assert.ErrorIs(t, second, drel.ErrDuplicateMessage)

	assert.Equal(t, 1, inboxCount(t, engine, `SELECT attempts FROM drel_inbox WHERE message_id = 'm1'`),
		"a duplicate claim must not change the row")
}

func TestInbox_DifferentHandlersBothClaim(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	for _, handler := range []string{"orders", "billing"} {
		require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
			return inbox.Claim(ctx, "m1", handler)
		}), "handler %s must claim the message", handler)
	}

	assert.Equal(t, 2, inboxCount(t, engine, `SELECT count(*) FROM drel_inbox WHERE message_id = 'm1'`))
}

func TestInbox_ClaimAfterFailureIsAllowed(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()
	boom := errors.New("transport error")

	// The handler failed, so the transaction rolled back and Fail recorded it.
	require.NoError(t, inbox.Fail(ctx, "m1", "orders", boom))
	assert.Equal(t, 1, inboxCount(t, engine, `SELECT attempts FROM drel_inbox WHERE message_id = 'm1'`))

	// The retry must be allowed.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return inbox.Claim(ctx, "m1", "orders")
	}))

	assert.Equal(t, 2, inboxCount(t, engine, `SELECT attempts FROM drel_inbox WHERE message_id = 'm1'`))
	assert.Equal(t, 1, inboxCount(t, engine,
		`SELECT count(*) FROM drel_inbox WHERE message_id = 'm1' AND processed_at IS NOT NULL AND last_error IS NULL`),
		"a successful claim must clear the earlier error")
}

func TestInbox_ClaimPanicsWithoutTransaction(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")

	assert.Panics(t, func() {
		_ = inbox.Claim(context.Background(), "m1", "orders")
	}, "a claim without a transaction is a wiring fault")
}

func TestInbox_FailRecordsErrorOutsideTheTransaction(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	require.NoError(t, inbox.Fail(ctx, "m1", "orders", errors.New("first")))
	require.NoError(t, inbox.Fail(ctx, "m1", "orders", errors.New("second")))

	var (
		attempts int
		lastErr  string
	)
	require.NoError(t, engine.QueryRow(ctx,
		`SELECT attempts, last_error FROM drel_inbox WHERE message_id = 'm1'`).Scan(&attempts, &lastErr))
	assert.Equal(t, 2, attempts)
	assert.Equal(t, "second", lastErr)
	assert.Equal(t, 1, inboxCount(t, engine,
		`SELECT count(*) FROM drel_inbox WHERE processed_at IS NULL`),
		"a failure record must not mark the message processed")
}

func TestInbox_FailAfterSuccessDoesNotReopen(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return inbox.Claim(ctx, "m1", "orders")
	}))
	require.NoError(t, inbox.Fail(ctx, "m1", "orders", errors.New("late error")))

	assert.Equal(t, 1, inboxCount(t, engine,
		`SELECT count(*) FROM drel_inbox WHERE processed_at IS NOT NULL`),
		"a late failure must not reopen a processed message")

	var second error
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		second = inbox.Claim(ctx, "m1", "orders")
		return nil
	}))
	assert.ErrorIs(t, second, drel.ErrDuplicateMessage)
}

func TestInbox_PurgeDeletesOldRows(t *testing.T) {
	engine := inboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	old := time.Now().Add(-48 * time.Hour)
	_, err := engine.Exec(ctx,
		`INSERT INTO drel_inbox (message_id, handler, received_at) VALUES ('old', 'h', ?)`, old)
	require.NoError(t, err)
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		return inbox.Claim(ctx, "new", "h")
	}))

	n, err := inbox.Purge(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	assert.Equal(t, 1, inboxCount(t, engine, `SELECT count(*) FROM drel_inbox`))
	assert.Equal(t, 1, inboxCount(t, engine, `SELECT count(*) FROM drel_inbox WHERE message_id = 'new'`))
}
