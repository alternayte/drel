package es

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alternayte/drel"
)

// Checkpoints records how far each projection has read the log.
//
// A projection writes its read model and saves its checkpoint in one
// transaction. It can therefore never record progress that it did not make: a
// failed handler rolls both back, and the next pass reads the same events.
//
// The position is the pair of transaction ID and global position, because that
// is the read order of ReadAll. One number cannot resume the read.
type Checkpoints struct {
	engine *drel.Engine
	table  string
}

// NewCheckpoints creates a checkpoint store on one table. The table must exist.
// CheckpointSchema emits it.
func NewCheckpoints(e *drel.Engine, table string) *Checkpoints {
	return &Checkpoints{engine: e, table: table}
}

// Load returns the position one projection reached. An unknown projection
// returns the zero position, which starts the read at the beginning of the log.
//
// Load uses the transaction in the context when one is present, so it reads its
// own writes. It uses the engine otherwise.
func (c *Checkpoints) Load(ctx context.Context, projection string) (Position, error) {
	sqlText := fmt.Sprintf(`SELECT "xact_id", "global_pos" FROM %s WHERE "projection" = $1`, c.quoted())

	var pos Position
	err := c.queryRow(ctx, sqlText, projection).Scan(&pos.XactID, &pos.GlobalPos)
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, drel.ErrNotFound):
		return Position{}, nil
	case err != nil:
		return Position{}, fmt.Errorf("es: load the checkpoint of %q: %w", projection, err)
	}
	return pos, nil
}

// Save stores the position of one projection inside the transaction that
// drel.WithTx put in the context. Call it in the same transaction as the read
// model write, so the two commit together.
//
// Save panics when the context carries no transaction. A checkpoint outside the
// read model transaction can record progress that the read model never made, so
// this is a wiring fault.
func (c *Checkpoints) Save(ctx context.Context, projection string, pos Position) error {
	tx := drel.MustFromContext(ctx)

	sqlText := fmt.Sprintf(`INSERT INTO %s ("projection", "xact_id", "global_pos", "updated_at")
VALUES ($1, $2, $3, $4)
ON CONFLICT ("projection") DO UPDATE
SET "xact_id" = $5, "global_pos" = $6, "updated_at" = $7`, c.quoted())

	now := time.Now()
	if _, err := tx.Exec(ctx, sqlText, projection, pos.XactID, pos.GlobalPos, now,
		pos.XactID, pos.GlobalPos, now); err != nil {
		return fmt.Errorf("es: save the checkpoint of %q: %w", projection, err)
	}
	return nil
}

// Reset returns one projection to the beginning of the log, for a replay. Clear
// the read model in the same transaction, so the replay starts from an empty
// read model and an empty checkpoint together.
//
// Reset uses the transaction in the context when one is present, and the engine
// otherwise.
func (c *Checkpoints) Reset(ctx context.Context, projection string) error {
	sqlText := fmt.Sprintf(`INSERT INTO %s ("projection", "xact_id", "global_pos", "updated_at")
VALUES ($1, 0, 0, $2)
ON CONFLICT ("projection") DO UPDATE
SET "xact_id" = 0, "global_pos" = 0, "updated_at" = $3`, c.quoted())

	now := time.Now()
	if _, err := c.exec(ctx, sqlText, projection, now, now); err != nil {
		return fmt.Errorf("es: reset the checkpoint of %q: %w", projection, err)
	}
	return nil
}

func (c *Checkpoints) quoted() string { return `"` + c.table + `"` }

// queryRow reads on the transaction in the context when one is present.
func (c *Checkpoints) queryRow(ctx context.Context, sqlText string, args ...any) drel.Row {
	if tx, ok := drel.FromContext(ctx); ok {
		return tx.QueryRow(ctx, sqlText, args...)
	}
	return c.engine.QueryRow(ctx, sqlText, args...)
}

// exec writes on the transaction in the context when one is present.
func (c *Checkpoints) exec(ctx context.Context, sqlText string, args ...any) (int64, error) {
	if tx, ok := drel.FromContext(ctx); ok {
		return tx.Exec(ctx, sqlText, args...)
	}
	return c.engine.Exec(ctx, sqlText, args...)
}

// CheckpointSchema returns the CREATE TABLE DDL for a checkpoint table for the
// given dialect ("postgres" or "sqlite").
//
// The table holds the transaction ID and the global position, because that pair
// is the read order of ReadAll. The design document named one position column,
// which cannot resume the read.
func CheckpointSchema(table, dialect string) string {
	q := `"` + table + `"`
	if dialect == "sqlite" {
		return fmt.Sprintf(`CREATE TABLE %s (
    "projection" TEXT NOT NULL PRIMARY KEY,
    "xact_id" INTEGER NOT NULL DEFAULT 0,
    "global_pos" INTEGER NOT NULL DEFAULT 0,
    "updated_at" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`, q)
	}
	return fmt.Sprintf(`CREATE TABLE %s (
    "projection" TEXT NOT NULL PRIMARY KEY,
    "xact_id" BIGINT NOT NULL DEFAULT 0,
    "global_pos" BIGINT NOT NULL DEFAULT 0,
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT now()
);
`, q)
}
