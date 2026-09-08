package drel

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrDuplicateMessage reports that the handler already processed the message.
// The caller must acknowledge the message to the broker and do no further work.
var ErrDuplicateMessage = errors.New("drel: the handler already processed this message")

// Inbox suppresses a duplicate delivery. A message broker delivers at least
// once, so one message can reach a handler more than one time. Claim writes a
// dedupe row inside the caller's transaction, next to the application write.
// The two therefore commit together, or neither commits.
//
// The key is the pair of message ID and handler name. Two handlers can process
// one message. One handler cannot process one message two times.
type Inbox struct {
	engine *Engine
	table  string
}

// NewInbox creates an inbox on one table. The table must exist. InboxSchema
// emits it.
func NewInbox(e *Engine, table string) *Inbox {
	return &Inbox{engine: e, table: table}
}

// Claim writes the dedupe row for one message and one handler inside the
// transaction that WithTx put in ctx. Call it first in the handler, before the
// application write.
//
// It returns ErrDuplicateMessage when the handler already processed the
// message. Acknowledge the message and stop.
//
// It panics when ctx carries no transaction. A dedupe row outside the
// application transaction gives no protection at all, so this is a wiring fault
// and it must fail at once.
//
// A handler that fails must roll the transaction back. The dedupe row then
// disappears with it, and the broker can deliver the message again. Record the
// failure with Fail after the rollback.
func (i *Inbox) Claim(ctx context.Context, messageID, handler string) error {
	tx := MustFromContext(ctx)

	sqlText := fmt.Sprintf(`INSERT INTO %s ("message_id", "handler", "received_at", "processed_at", "attempts", "last_error")
VALUES ($1, $2, $3, $4, 1, NULL)
ON CONFLICT ("message_id", "handler") DO UPDATE
SET "attempts" = %s."attempts" + 1, "processed_at" = $5, "last_error" = NULL
WHERE %s."processed_at" IS NULL
RETURNING "message_id"`, i.quoted(), i.quoted(), i.quoted())

	now := time.Now()
	rows, err := tx.Query(ctx, i.bind(sqlText), messageID, handler, now, now, now)
	if err != nil {
		return fmt.Errorf("drel: inbox claim %q for %q: %w", messageID, handler, err)
	}
	defer rows.Close()

	claimed := rows.Next()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("drel: inbox claim %q for %q: %w", messageID, handler, err)
	}
	if !claimed {
		return fmt.Errorf("drel: inbox claim %q for %q: %w", messageID, handler, ErrDuplicateMessage)
	}
	return nil
}

// Fail records a failed attempt. Call it after the handler's transaction rolled
// back, so it runs on the engine and not on that transaction. The row it writes
// counts the attempt and holds the error. It does not block a later claim.
//
// A message that the handler already processed is left alone, so a late error
// cannot reopen it.
func (i *Inbox) Fail(ctx context.Context, messageID, handler string, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}

	sqlText := fmt.Sprintf(`INSERT INTO %s ("message_id", "handler", "received_at", "processed_at", "attempts", "last_error")
VALUES ($1, $2, $3, NULL, 1, $4)
ON CONFLICT ("message_id", "handler") DO UPDATE
SET "attempts" = %s."attempts" + 1, "last_error" = $5
WHERE %s."processed_at" IS NULL`, i.quoted(), i.quoted(), i.quoted())

	if _, err := i.engine.Exec(ctx, i.bind(sqlText), messageID, handler, time.Now(), msg, msg); err != nil {
		return fmt.Errorf("drel: inbox record failure for %q and %q: %w", messageID, handler, err)
	}
	return nil
}

// Purge deletes the rows received before a time and returns the count. Run it
// on a schedule to hold the table small.
//
// Purge only beyond the retention of the message broker. A purged row no longer
// suppresses its message, so an older delivery would run a second time.
func (i *Inbox) Purge(ctx context.Context, before time.Time) (int64, error) {
	sqlText := fmt.Sprintf(`DELETE FROM %s WHERE "received_at" < $1`, i.quoted())
	n, err := i.engine.Exec(ctx, i.bind(sqlText), before)
	if err != nil {
		return 0, fmt.Errorf("drel: inbox purge: %w", err)
	}
	return n, nil
}

func (i *Inbox) quoted() string { return `"` + i.table + `"` }

func (i *Inbox) bind(sqlText string) string {
	if i.engine.dialect().UsesQuestionPlaceholders() {
		return rewritePlaceholders(sqlText)
	}
	return sqlText
}

// InboxSchema returns the CREATE TABLE DDL for an inbox table for the given
// dialect ("postgres" or "sqlite").
//
// The primary key is the pair of message ID and handler name. A row with
// processed_at set is a committed success, and it blocks a new claim. A row
// with processed_at null only counts a failed attempt, and it does not block.
func InboxSchema(table, dialect string) string {
	q := `"` + table + `"`
	if dialect == "sqlite" {
		return fmt.Sprintf(`CREATE TABLE %s (
    "message_id" TEXT NOT NULL,
    "handler" TEXT NOT NULL,
    "received_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "processed_at" DATETIME,
    "attempts" INTEGER NOT NULL DEFAULT 0,
    "last_error" TEXT,
    PRIMARY KEY ("message_id", "handler")
);
`, q)
	}
	return fmt.Sprintf(`CREATE TABLE %s (
    "message_id" TEXT NOT NULL,
    "handler" TEXT NOT NULL,
    "received_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    "processed_at" TIMESTAMPTZ,
    "attempts" INTEGER NOT NULL DEFAULT 0,
    "last_error" TEXT,
    PRIMARY KEY ("message_id", "handler")
);
`, q)
}
