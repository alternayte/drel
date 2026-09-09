package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alternayte/drel/internal/driver"
)

// Migration represents a single versioned migration with up and down SQL.
type Migration struct {
	Version string
	Name    string
	UpSQL   string
	DownSQL string
}

// ParseMigrationDir reads a directory of migration files and returns them
// sorted by version. Files must follow the naming convention:
//
//	<version>_<name>.up.sql
//	<version>_<name>.down.sql
//
// If the directory does not exist, it returns nil with no error.
func ParseMigrationDir(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("migrate: read dir: %w", err)
	}

	upFiles := map[string]string{}
	downFiles := map[string]string{}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".up.sql") {
			key := strings.TrimSuffix(name, ".up.sql")
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			upFiles[key] = string(content)
		} else if strings.HasSuffix(name, ".down.sql") {
			key := strings.TrimSuffix(name, ".down.sql")
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			downFiles[key] = string(content)
		}
	}

	var keys []string
	for k := range upFiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var migrations []Migration
	for _, key := range keys {
		parts := strings.SplitN(key, "_", 2)
		version := parts[0]
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}
		migrations = append(migrations, Migration{
			Version: version,
			Name:    name,
			UpSQL:   upFiles[key],
			DownSQL: downFiles[key],
		})
	}

	return migrations, nil
}

// WriteMigration creates a new migration file pair in the given directory.
// It returns the generated version string (timestamp in YYYYMMDDHHmmss format).
func WriteMigration(dir, name, upSQL, downSQL string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("migrate: create dir: %w", err)
	}

	version := nextVersion(dir)
	slug := strings.ReplaceAll(strings.ToLower(name), " ", "_")

	upPath := filepath.Join(dir, fmt.Sprintf("%s_%s.up.sql", version, slug))
	downPath := filepath.Join(dir, fmt.Sprintf("%s_%s.down.sql", version, slug))

	if err := os.WriteFile(upPath, []byte(upSQL), 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(downPath, []byte(downSQL), 0644); err != nil {
		return "", err
	}

	return version, nil
}

// nextVersion returns a unique, monotonically increasing 14-digit version based
// on the current time. If a migration with the candidate version already exists
// (e.g. two migrations created within the same second), the version is bumped
// by one until free, guaranteeing deterministic ordering.
func nextVersion(dir string) string {
	v := time.Now().Format("20060102150405")
	for versionExists(dir, v) {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			break
		}
		v = fmt.Sprintf("%014d", n+1)
	}
	return v
}

func versionExists(dir, version string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, version+"_*.up.sql"))
	return len(matches) > 0
}

// ChecksumContent returns the SHA-256 hex digest of the given content string.
func ChecksumContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// Runner executes migrations against a database using the driver interface.
type Runner struct {
	drv     driver.Driver
	dir     string
	dialect string
}

// NewRunner creates a Runner that reads migration files from dir and executes
// them against the provided driver. dialect ("postgres", "sqlite", or "libsql")
// selects the migration-lock strategy used by Up/Down.
func NewRunner(drv driver.Driver, dir, dialect string) *Runner {
	return &Runner{drv: drv, dir: dir, dialect: dialect}
}

// Dialect returns the dialect string the Runner was constructed with.
func (r *Runner) Dialect() string { return r.dialect }

// migrationLockID is a fixed key for the Postgres session advisory lock that
// serialises migration runs. The value is arbitrary but must be stable.
const migrationLockID int64 = 728341

// ensureLockTable creates the SQLite/libSQL sentinel lock table if absent.
func (r *Runner) ensureLockTable(ctx context.Context) error {
	_, err := r.drv.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS drel_migration_lock (
			id         INTEGER PRIMARY KEY CHECK (id = 1),
			locked_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("migrate: create lock table: %w", err)
	}
	return nil
}

// lock acquires the migration lock. It returns a clear error if another process
// already holds it. The returned func releases the lock and must be deferred.
func (r *Runner) lock(ctx context.Context) (func(), error) {
	if r.dialect == "postgres" {
		// pg_advisory_xact_lock is transaction-scoped: it is automatically
		// released when the transaction ends (commit or rollback), making it
		// safe on pooled connections. Session-scoped pg_try_advisory_lock /
		// pg_advisory_unlock can run on different pooled connections,
		// potentially leaking the lock indefinitely.
		//
		// We start a dedicated "lock transaction" that holds this pg xact lock
		// for the entire duration of Up/Down. The tx is never committed — we
		// always roll it back at the end, which also releases the lock.
		// This tx makes no schema changes; all migration work still happens in
		// separate per-migration transactions.
		lockTx, err := r.drv.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("migrate: begin lock transaction: %w", err)
		}
		if _, err := lockTx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
			_ = lockTx.Rollback(ctx)
			return nil, fmt.Errorf("migrate: acquire advisory lock: %w", err)
		}
		return func() {
			_ = lockTx.Rollback(ctx) // releases the pg_advisory_xact_lock
		}, nil
	}

	// SQLite / libSQL: a single sentinel row guards the run. INSERT of id=1 fails
	// (PK violation) when the lock is already held.
	if err := r.ensureLockTable(ctx); err != nil {
		return nil, err
	}
	if _, err := r.drv.Exec(ctx, "INSERT INTO drel_migration_lock (id) VALUES (1)"); err != nil {
		return nil, fmt.Errorf("migrate: migrations are locked by another process (delete the drel_migration_lock row to clear a stale lock): %w", err)
	}
	return func() {
		_, _ = r.drv.Exec(ctx, "DELETE FROM drel_migration_lock WHERE id = 1")
	}, nil
}

// ForceLockForTest acquires the SQLite/libSQL sentinel lock out of band. Test-only.
func ForceLockForTest(ctx context.Context, drv driver.Driver) error {
	r := &Runner{drv: drv, dialect: "sqlite"}
	if err := r.ensureLockTable(ctx); err != nil {
		return err
	}
	_, err := drv.Exec(ctx, "INSERT INTO drel_migration_lock (id) VALUES (1)")
	return err
}

// ForceUnlockForTest releases the SQLite/libSQL sentinel lock. Test-only.
func ForceUnlockForTest(ctx context.Context, drv driver.Driver) error {
	_, err := drv.Exec(ctx, "DELETE FROM drel_migration_lock WHERE id = 1")
	return err
}

func (r *Runner) ensureTable(ctx context.Context) error {
	// Portable across Postgres and SQLite: TIMESTAMP + CURRENT_TIMESTAMP are
	// understood by both (SQLite is dynamically typed; Postgres maps TIMESTAMP).
	_, err := r.drv.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS drel_migrations (
			version    VARCHAR(14) PRIMARY KEY,
			name       TEXT NOT NULL,
			checksum   TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err == nil {
		return nil
	}

	// CREATE TABLE IF NOT EXISTS is not race-safe on Postgres. Two sessions can
	// both pass the existence check, and the loser then fails on a catalog
	// index. Up and Down create the table under the migration lock, so they
	// never meet this. The read-only paths cannot take the lock, so they
	// confirm the table is there and carry on.
	if exists, checkErr := r.tableExists(ctx, "drel_migrations"); checkErr == nil && exists {
		return nil
	}
	return fmt.Errorf("migrate: create tracking table: %w", err)
}

// tableExists reports whether a table is present. It is used to tell a lost
// race to create a table from a real failure.
func (r *Runner) tableExists(ctx context.Context, name string) (bool, error) {
	query := "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	if r.dialect == "postgres" {
		query = "SELECT count(*) FROM information_schema.tables WHERE table_name = $1"
	}
	var n int
	if err := r.drv.QueryRow(ctx, query, name).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *Runner) appliedVersions(ctx context.Context) (map[string]string, error) {
	rows, err := r.drv.Query(ctx, "SELECT version, checksum FROM drel_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("migrate: query applied: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var v, cs string
		if err := rows.Scan(&v, &cs); err != nil {
			return nil, err
		}
		applied[v] = cs
	}
	return applied, rows.Err()
}

// Up applies every pending migration in order and returns how many it applied.
//
// Up takes the migration lock first, so several replicas that boot at one time
// serialise: one applies the migrations and the others wait and then find
// nothing to do.
//
// Do not call Up from two goroutines of one process that share one connection
// pool. Each waiting call holds a pool connection while it waits for the lock,
// so a pool smaller than the count of callers starves the winner. Separate
// processes hold separate pools and are not affected.
func (r *Runner) Up(ctx context.Context) (int, error) {
	// Take the lock before creating the tracking table. Several replicas that
	// boot at one time would otherwise race on CREATE TABLE IF NOT EXISTS,
	// which Postgres does not serialise, and the losing replica would fail to
	// start.
	unlock, err := r.lock(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()

	if err := r.ensureTable(ctx); err != nil {
		return 0, err
	}

	migrations, err := ParseMigrationDir(r.dir)
	if err != nil {
		return 0, err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return 0, err
	}

	// Verify checksums of already-applied migrations before applying new ones.
	for _, m := range migrations {
		storedChecksum, ok := applied[m.Version]
		if !ok {
			continue
		}
		currentChecksum := ChecksumContent(m.UpSQL)
		if currentChecksum != storedChecksum {
			return 0, fmt.Errorf("migration %s has been modified after being applied (expected checksum %s, got %s)",
				m.Version, storedChecksum, currentChecksum)
		}
	}

	count := 0
	for _, m := range migrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}

		tx, err := r.drv.Begin(ctx)
		if err != nil {
			return count, fmt.Errorf("migrate: begin: %w", err)
		}

		if _, err := tx.Exec(ctx, m.UpSQL); err != nil {
			_ = tx.Rollback(ctx)
			return count, fmt.Errorf("migrate: apply %s_%s: %w", m.Version, m.Name, err)
		}

		checksum := ChecksumContent(m.UpSQL)
		if _, err := tx.Exec(ctx,
			"INSERT INTO drel_migrations (version, name, checksum) VALUES ($1, $2, $3)",
			m.Version, m.Name, checksum); err != nil {
			_ = tx.Rollback(ctx)
			return count, fmt.Errorf("migrate: record %s: %w", m.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return count, fmt.Errorf("migrate: commit %s: %w", m.Version, err)
		}
		count++
	}

	return count, nil
}

// Down rolls back the most recently applied migration.
func (r *Runner) Down(ctx context.Context) error {
	// Take the lock before creating the tracking table, as Up does.
	unlock, err := r.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	if err := r.ensureTable(ctx); err != nil {
		return err
	}

	migrations, err := ParseMigrationDir(r.dir)
	if err != nil {
		return err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return err
	}

	var last *Migration
	for i := len(migrations) - 1; i >= 0; i-- {
		if _, ok := applied[migrations[i].Version]; ok {
			last = &migrations[i]
			break
		}
	}

	if last == nil {
		return fmt.Errorf("migrate: no migrations to roll back")
	}

	if strings.TrimSpace(last.DownSQL) == "" {
		return fmt.Errorf("migrate: migration %s_%s has no down migration (file is empty or missing)", last.Version, last.Name)
	}

	tx, err := r.drv.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin: %w", err)
	}

	if _, err := tx.Exec(ctx, last.DownSQL); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("migrate: rollback %s_%s: %w", last.Version, last.Name, err)
	}

	if _, err := tx.Exec(ctx,
		"DELETE FROM drel_migrations WHERE version = $1",
		last.Version); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("migrate: unrecord %s: %w", last.Version, err)
	}

	return tx.Commit(ctx)
}

// MigrationState indicates the lifecycle state of a migration.
type MigrationState string

const (
	// StateApplied means the migration is in the directory and has been applied
	// with a matching checksum.
	StateApplied MigrationState = "applied"
	// StatePending means the migration is in the directory but has not been applied.
	StatePending MigrationState = "pending"
	// StateModified means the migration has been applied but the file content
	// no longer matches the stored checksum.
	StateModified MigrationState = "modified"
	// StateMissing means the migration was applied but the file no longer exists
	// in the migration directory.
	StateMissing MigrationState = "missing"
)

// MigrationStatus represents the state of a single migration.
type MigrationStatus struct {
	Version string
	Name    string
	State   MigrationState
	// Applied is true when the migration has been applied to the database,
	// regardless of whether the file still matches. Kept for backward compatibility.
	Applied bool
}

// appliedNames returns a map of version → name for applied migrations.
func (r *Runner) appliedNames(ctx context.Context) (map[string]string, error) {
	rows, err := r.drv.Query(ctx, "SELECT version, name FROM drel_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("migrate: query applied names: %w", err)
	}
	defer rows.Close()

	names := make(map[string]string)
	for rows.Next() {
		var v, n string
		if err := rows.Scan(&v, &n); err != nil {
			return nil, err
		}
		names[v] = n
	}
	return names, rows.Err()
}

// Status returns the status of all migrations, including those in the directory
// and those applied to the database. Each migration is classified as one of:
// applied, pending, modified, or missing.
func (r *Runner) Status(ctx context.Context) ([]MigrationStatus, error) {
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}

	migrations, err := ParseMigrationDir(r.dir)
	if err != nil {
		return nil, err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	appliedN, err := r.appliedNames(ctx)
	if err != nil {
		return nil, err
	}

	// Track which applied versions we've seen in the directory.
	seen := make(map[string]bool)

	var statuses []MigrationStatus
	for _, m := range migrations {
		storedChecksum, isApplied := applied[m.Version]
		seen[m.Version] = true

		var state MigrationState
		if !isApplied {
			state = StatePending
		} else if ChecksumContent(m.UpSQL) != storedChecksum {
			state = StateModified
		} else {
			state = StateApplied
		}

		statuses = append(statuses, MigrationStatus{
			Version: m.Version,
			Name:    m.Name,
			State:   state,
			Applied: isApplied,
		})
	}

	// Append migrations that are applied but no longer exist in the directory.
	for version, checksum := range applied {
		if seen[version] {
			continue
		}
		_ = checksum // checksum not needed for missing entries
		name := appliedN[version]
		statuses = append(statuses, MigrationStatus{
			Version: version,
			Name:    name,
			State:   StateMissing,
			Applied: true,
		})
	}

	// Sort by version for deterministic output.
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Version < statuses[j].Version
	})

	return statuses, nil
}

// LintResult reports a migration file whose content has changed after it was
// applied to the database.
type LintResult struct {
	Version  string
	Name     string
	Expected string
	Actual   string
}

// Lint checks all applied migrations for checksum mismatches, detecting files
// that were modified after being applied.
func (r *Runner) Lint(ctx context.Context) ([]LintResult, error) {
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}

	migrations, err := ParseMigrationDir(r.dir)
	if err != nil {
		return nil, err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	var issues []LintResult
	for _, m := range migrations {
		storedChecksum, ok := applied[m.Version]
		if !ok {
			continue
		}
		currentChecksum := ChecksumContent(m.UpSQL)
		if currentChecksum != storedChecksum {
			issues = append(issues, LintResult{
				Version:  m.Version,
				Name:     m.Name,
				Expected: storedChecksum,
				Actual:   currentChecksum,
			})
		}
	}

	return issues, nil
}

// Pending returns the migrations present in the directory that have not yet been
// applied to the database, in version order. It is the basis for `migrate check`:
// generating a new migration while unapplied files exist compounds snapshot
// drift, so callers should surface a clear error.
func (r *Runner) Pending(ctx context.Context) ([]Migration, error) {
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}
	migrations, err := ParseMigrationDir(r.dir)
	if err != nil {
		return nil, err
	}
	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	var pending []Migration
	for _, m := range migrations {
		if _, ok := applied[m.Version]; !ok {
			pending = append(pending, m)
		}
	}
	return pending, nil
}
