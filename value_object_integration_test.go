//go:build integration

package drel_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/dreltest"
	vomodels "github.com/alternayte/drel/examples/value-objects/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nullableEmailMeta adapts AccountMeta to the nullable-email table
// (same schema but email column allows NULL).
// We reuse AccountMeta directly — the table name is overridden per-query via a
// custom repo — but to keep things simple we use raw engine queries for the
// zero->NULL verification and only rely on repo.Add / tx.SaveChanges.

// voZeroNullRoundTrip inserts an account with a zero Email (no address) and
// verifies: (a) the row is written as SQL NULL in the email column, and (b)
// scanning the row back produces a zero Email (IsZero() == true).
func voZeroNullRoundTrip(t *testing.T, engine *drel.Engine, dialect string, nullEmailDDL string) {
	t.Helper()
	ctx := context.Background()

	// Create a separate table that allows NULL for email.
	_, err := engine.Exec(ctx, nullEmailDDL)
	require.NoError(t, err, "create nullable-email accounts table")

	// Build a meta that targets the nullable table.
	nullMeta := vomodels.UserAccountMeta
	nullMeta.Table = "accounts_nullable"

	// Insert an account with a zero Email (zero value, not constructed via NewEmail).
	var id int
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, nullMeta)
		a := vomodels.NewUserAccount(vomodels.Email{}, vomodels.NewCents(42))
		repo.Add(a)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		id = a.ID()
		return nil
	})
	require.NoError(t, err, "insert zero-email account")

	// The email column must be NULL in the database.
	row := engine.QueryRow(ctx,
		"SELECT email IS NULL FROM accounts_nullable WHERE id = "+voPlaceholder(dialect, 1), id)
	var isNull bool
	require.NoError(t, row.Scan(&isNull))
	assert.True(t, isNull, "zero Email must be stored as SQL NULL (Value() returns nil for IsZero)")

	// Round-trip: scan the row back and verify email is zero.
	repo := drel.NewRepository(engine, nullMeta)
	got, err := repo.Find(ctx, id)
	require.NoError(t, err)
	assert.True(t, got.Email().IsZero(), "scanned Email from NULL must be zero")
	assert.Equal(t, int64(42), got.Balance().Int64())
}

func voRoundTrip(t *testing.T, engine *drel.Engine, dialect string) {
	ctx := context.Background()

	email, err := vomodels.NewEmail("Alice@Example.com")
	require.NoError(t, err)

	// Insert.
	var id int
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		repo := drel.NewTxRepository(tx, vomodels.UserAccountMeta)
		a := vomodels.NewUserAccount(email, vomodels.NewCents(1000))
		repo.Add(a)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		id = a.ID()
		return nil
	})
	require.NoError(t, err)

	// Prove the int64-backed VO stored as an integer, not text: arithmetic in SQL
	// must work (it would error/compare lexically on a TEXT column).
	row := engine.QueryRow(ctx,
		"SELECT balance + 1 FROM user_accounts WHERE id = "+voPlaceholder(dialect, 1), id)
	var balPlus int64
	require.NoError(t, row.Scan(&balPlus))
	assert.Equal(t, int64(1001), balPlus, "balance must be stored as an integer column")

	// Query by VO equality (Where(Accounts.Email.Eq(vo))).
	repo := drel.NewRepository(engine, vomodels.UserAccountMeta)
	got, err := repo.Where(vomodels.UserAccounts.Email.Eq(email)).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", got.Email().String())
	assert.Equal(t, int64(1000), got.Balance().Int64())

	// Mutate via a tracked unit of work and SaveChanges; diff must fire.
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		trepo := drel.NewTxRepository(tx, vomodels.UserAccountMeta)
		acc, err := trepo.Find(ctx, id)
		if err != nil {
			return err
		}
		acc.SetBalance(vomodels.NewCents(2500))
		return tx.SaveChanges(ctx)
	})
	require.NoError(t, err)

	after, err := drel.NewRepository(engine, vomodels.UserAccountMeta).Find(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2500), after.Balance().Int64())
}

// voPlaceholder returns the dialect-correct positional placeholder for a raw query.
func voPlaceholder(dialect string, n int) string {
	if dialect == "sqlite" {
		return "?"
	}
	return "$1"
}

func TestIntegration_ValueObject_NullableVO_ZeroToNull_Postgres(t *testing.T) {
	engine := setupTestDB(t)
	voZeroNullRoundTrip(t, engine, "postgres", `CREATE TABLE accounts_nullable (
		id         SERIAL PRIMARY KEY,
		email      text,
		balance    bigint NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now()
	)`)
}

func TestIntegration_ValueObject_NullableVO_ZeroToNull_SQLite(t *testing.T) {
	engine := dreltest.NewSQLite(t)
	voZeroNullRoundTrip(t, engine, "sqlite", `CREATE TABLE accounts_nullable (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		email      TEXT,
		balance    INTEGER NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
}

func TestIntegration_ValueObject_RoundTrip_Postgres(t *testing.T) {
	engine := setupTestDB(t)
	ctx := context.Background()
	// email is nullable: Email has IsZero() -> Value() returns nil for zero ->
	// codegen now emits email without NOT NULL. A non-zero email still stores fine.
	_, err := engine.Exec(ctx, `CREATE TABLE user_accounts (
		id         SERIAL PRIMARY KEY,
		email      text,
		balance    bigint NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		UNIQUE(email)
	)`)
	require.NoError(t, err)
	voRoundTrip(t, engine, "postgres")
}

func TestIntegration_ValueObject_RoundTrip_SQLite(t *testing.T) {
	engine := dreltest.NewSQLite(t)
	ctx := context.Background()
	// email is nullable to match codegen output (HasIsZero -> no NOT NULL).
	_, err := engine.Exec(ctx, `CREATE TABLE user_accounts (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		email      TEXT,
		balance    INTEGER NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(email)
	)`)
	require.NoError(t, err)
	voRoundTrip(t, engine, "sqlite")
}
