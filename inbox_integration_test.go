//go:build integration

package drel_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/alternayte/drel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pgInboxEngine(t *testing.T) *drel.Engine {
	t.Helper()
	engine := setupTestDB(t)
	ctx := context.Background()
	_, err := engine.Exec(ctx, drel.InboxSchema("drel_inbox", "postgres"))
	require.NoError(t, err)
	_, err = engine.Exec(ctx, `CREATE TABLE aggregates (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL)`)
	require.NoError(t, err)
	return engine
}

func pgCount(t *testing.T, e *drel.Engine, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, e.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

// TestIntegration_Inbox_AggregateAndDedupeCommitTogether proves the property the
// host needs: the read model write and the dedupe row are one commit.
func TestIntegration_Inbox_AggregateAndDedupeCommitTogether(t *testing.T) {
	engine := pgInboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	handle := func(ctx context.Context, messageID string) error {
		return engine.WithTx(ctx, func(ctx context.Context) error {
			if err := inbox.Claim(ctx, messageID, "orders"); err != nil {
				return err
			}
			_, err := drel.MustFromContext(ctx).Exec(ctx,
				`INSERT INTO aggregates (name) VALUES ($1)`, messageID)
			return err
		})
	}

	require.NoError(t, handle(ctx, "m1"))

	// The broker delivers the same message again.
	err := handle(ctx, "m1")
	assert.ErrorIs(t, err, drel.ErrDuplicateMessage)

	assert.Equal(t, 1, pgCount(t, engine, `SELECT count(*) FROM aggregates`),
		"a duplicate delivery must not write the aggregate a second time")
	assert.Equal(t, 1, pgCount(t, engine, `SELECT count(*) FROM drel_inbox`))
}

// TestIntegration_Inbox_HandlerFailureLeavesNoRow proves a failed handler leaves
// the message claimable.
func TestIntegration_Inbox_HandlerFailureLeavesNoRow(t *testing.T) {
	engine := pgInboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()
	boom := errors.New("downstream down")

	err := engine.WithTx(ctx, func(ctx context.Context) error {
		if err := inbox.Claim(ctx, "m1", "orders"); err != nil {
			return err
		}
		_, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO aggregates (name) VALUES ($1)`, "m1")
		require.NoError(t, err)
		return boom
	})
	assert.ErrorIs(t, err, boom)

	require.NoError(t, inbox.Fail(ctx, "m1", "orders", boom))
	assert.Equal(t, 0, pgCount(t, engine, `SELECT count(*) FROM aggregates`))
	assert.Equal(t, 1, pgCount(t, engine, `SELECT attempts FROM drel_inbox WHERE message_id = 'm1'`))

	// The retry succeeds.
	require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
		if err := inbox.Claim(ctx, "m1", "orders"); err != nil {
			return err
		}
		_, err := drel.MustFromContext(ctx).Exec(ctx,
			`INSERT INTO aggregates (name) VALUES ($1)`, "m1")
		return err
	}))
	assert.Equal(t, 1, pgCount(t, engine, `SELECT count(*) FROM aggregates`))
	assert.Equal(t, 2, pgCount(t, engine, `SELECT attempts FROM drel_inbox WHERE message_id = 'm1'`))
}

// TestIntegration_Inbox_ConcurrentHandlersClaimOnce proves that many replicas
// that receive one message at the same time write the aggregate one time.
func TestIntegration_Inbox_ConcurrentHandlersClaimOnce(t *testing.T) {
	engine := pgInboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	const workers = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		duplicate int
		others    []error
	)
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := engine.WithTx(ctx, func(ctx context.Context) error {
				if err := inbox.Claim(ctx, "m1", "orders"); err != nil {
					return err
				}
				_, err := drel.MustFromContext(ctx).Exec(ctx,
					`INSERT INTO aggregates (name) VALUES ($1)`, "m1")
				return err
			})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, drel.ErrDuplicateMessage):
				duplicate++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Empty(t, others, "no worker may fail for another reason")
	assert.Equal(t, 1, succeeded, "exactly one worker must process the message")
	assert.Equal(t, workers-1, duplicate)
	assert.Equal(t, 1, pgCount(t, engine, `SELECT count(*) FROM aggregates`))
}

// TestIntegration_Inbox_TwoHandlersOneMessage proves the pair key: two handlers
// each process the same message one time.
func TestIntegration_Inbox_TwoHandlersOneMessage(t *testing.T) {
	engine := pgInboxEngine(t)
	inbox := drel.NewInbox(engine, "drel_inbox")
	ctx := context.Background()

	for _, handler := range []string{"orders", "billing"} {
		require.NoError(t, engine.WithTx(ctx, func(ctx context.Context) error {
			if err := inbox.Claim(ctx, "m1", handler); err != nil {
				return err
			}
			_, err := drel.MustFromContext(ctx).Exec(ctx,
				`INSERT INTO aggregates (name) VALUES ($1)`, handler)
			return err
		}))
	}

	assert.Equal(t, 2, pgCount(t, engine, `SELECT count(*) FROM aggregates`))
	assert.Equal(t, 2, pgCount(t, engine, `SELECT count(*) FROM drel_inbox WHERE message_id = 'm1'`))
}
