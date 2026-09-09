package drel

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alternayte/drel/internal/dberr"
	"github.com/alternayte/drel/internal/dialect"
	"github.com/alternayte/drel/internal/dialect/postgres"
	dialectsqlite "github.com/alternayte/drel/internal/dialect/sqlite"
	"github.com/alternayte/drel/internal/driver"
	"github.com/alternayte/drel/internal/driver/pgxdriver"
	"github.com/alternayte/drel/internal/driver/sqlitedriver"
	"github.com/alternayte/drel/internal/dsn"
	"github.com/alternayte/drel/internal/migrate"
)

// replicaCooldown is how long a read replica is skipped after a failed read
// before it is tried again.
const replicaCooldown = 5 * time.Second

// Engine holds a database driver and dialect for executing queries.
type Engine struct {
	drv               driver.Driver
	dia               dialect.Dialect
	hookMu            sync.RWMutex
	beforeCommitHooks []BeforeCommitHook
	afterCommitHooks  []AfterCommitHook
	eventSinks        []func(ctx context.Context, tx *Tx, events []any) error
	queryHooks        []QueryHook

	afterCommitSink func(ctx context.Context, err error)

	logger        *slog.Logger
	tracer        Tracer
	devMode       bool
	slowThreshold time.Duration
	queryTimeout  time.Duration
	n1            *n1Detector

	replicas  []driver.Driver
	rrCounter uint64

	// replicaFailed maps a replica index → the UnixNano time it last failed a
	// read. A replica within replicaCooldown of its last failure is skipped.
	// A zero-value Engine (test literals) is safe: a missing key means "healthy".
	replicaFailed sync.Map // map[int]int64
}

// Option configures Engine creation.
type Option func(*engineConfig)

type engineConfig struct {
	ctx context.Context
	drv driver.Driver
	dia dialect.Dialect

	afterCommitSink func(ctx context.Context, err error)

	logger        *slog.Logger
	queryLog      bool
	slowThreshold time.Duration
	tracer        Tracer
	devMode       bool
	queryTimeout  time.Duration

	replicaDSNs []string
	replicaDrvs []driver.Driver

	authToken  string
	poolConfig driver.PoolConfig
}

// detectDialect inspects the DSN and returns "libsql", "sqlite", or "postgres".
// Delegates to internal/dsn so the engine and CLI cannot drift apart.
func detectDialect(d string) string {
	return dsn.DetectDialect(d)
}

// NewEngine creates a new Engine connected to the given DSN.
// The dialect and driver are auto-detected from the DSN unless overridden
// with WithDriver or WithDialect.
//
// libSQL/Turso caveat: the websocket transport (ws:// or wss://) returns DATETIME
// columns as strings and corrupts time.Time scans (including created_at/updated_at).
// Prefer a libsql:// or https:// DSN for models with time.Time fields; NewEngine
// logs a warning when a ws://-scheme DSN is opened.
func NewEngine(dsn string, opts ...Option) (*Engine, error) {
	cfg := &engineConfig{
		ctx: context.Background(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	drv := cfg.drv
	dia := cfg.dia

	if drv == nil || dia == nil {
		detected := detectDialect(dsn)
		switch detected {
		case "libsql":
			// libSQL is SQLite-compatible: reuse the SQLite dialect.
			if drv == nil {
				d, err := newLibSQLDriver(applyAuthToken(dsn, cfg.authToken), cfg.poolConfig)
				if err != nil {
					return nil, fmt.Errorf("drel: open: %w", err)
				}
				drv = d
			}
			if dia == nil {
				dia = dialectsqlite.New()
			}
		case "sqlite":
			if drv == nil {
				d, err := sqlitedriver.New(dsn, cfg.poolConfig)
				if err != nil {
					return nil, fmt.Errorf("drel: open: %w", err)
				}
				drv = d
			}
			if dia == nil {
				dia = dialectsqlite.New()
			}
		default: // "postgres"
			if drv == nil {
				d, err := pgxdriver.New(cfg.ctx, dsn, cfg.poolConfig)
				if err != nil {
					return nil, fmt.Errorf("drel: open: %w", err)
				}
				drv = d
			}
			if dia == nil {
				dia = postgres.New()
			}
		}
	}

	// Open read replicas (same dialect as the primary). Reads round-robin across
	// them; writes and transactions always use the primary. Replicas are opened
	// lazily (no startup ping) so an unreachable replica does not prevent engine
	// startup — the failover loop surfaces the error at query time instead.
	var replicas []driver.Driver
	for _, rdsn := range cfg.replicaDSNs {
		rd, err := openReplicaDriverForDSN(cfg.ctx, rdsn, cfg.poolConfig)
		if err != nil {
			for _, r := range replicas {
				r.Close()
			}
			return nil, fmt.Errorf("drel: open read replica: %w", err)
		}
		replicas = append(replicas, rd)
	}
	replicas = append(replicas, cfg.replicaDrvs...)

	e := &Engine{
		drv:          drv,
		dia:          dia,
		replicas:     replicas,
		queryTimeout: cfg.queryTimeout,
	}
	e.installObservability(cfg)
	e.warnWSTransport(cfg.ctx, dsn)
	return e, nil
}

// openDriverForDSN opens a driver for a DSN using the same dialect detection as
// the primary connection.
func openDriverForDSN(ctx context.Context, dsn string, pc driver.PoolConfig) (driver.Driver, error) {
	switch detectDialect(dsn) {
	case "libsql":
		return newLibSQLDriver(dsn, pc)
	case "sqlite":
		return sqlitedriver.New(dsn, pc)
	default:
		return pgxdriver.New(ctx, dsn, pc)
	}
}

// openReplicaDriverForDSN opens a read-replica driver without an eager ping.
// Connection failures surface at query time so a transient or permanently-down
// replica does not prevent engine startup; the failover logic handles it.
func openReplicaDriverForDSN(ctx context.Context, dsn string, pc driver.PoolConfig) (driver.Driver, error) {
	switch detectDialect(dsn) {
	case "libsql":
		return newLibSQLDriver(dsn, pc)
	case "sqlite":
		return sqlitedriver.New(dsn, pc)
	default:
		return pgxdriver.NewLazy(ctx, dsn, pc)
	}
}

// WithMaxConns sets the maximum number of open database connections in the pool.
func WithMaxConns(n int) Option {
	return func(cfg *engineConfig) { cfg.poolConfig.MaxConns = n }
}

// WithConnMaxLifetime sets the maximum lifetime of a pooled connection before it
// is recycled (useful behind connection poolers / for failover).
func WithConnMaxLifetime(d time.Duration) Option {
	return func(cfg *engineConfig) { cfg.poolConfig.ConnMaxLifetime = d }
}

// WithConnMaxIdleTime sets how long a connection may sit idle before being closed.
func WithConnMaxIdleTime(d time.Duration) Option {
	return func(cfg *engineConfig) { cfg.poolConfig.ConnMaxIdleTime = d }
}

// WithSimpleProtocol makes the Postgres driver use pgx's simple query protocol
// (no server-side prepared statements), required for PgBouncer transaction or
// statement pooling. It is a no-op for SQLite and libSQL/Turso. The equivalent
// DSN escape hatch is "?default_query_exec_mode=simple_protocol".
func WithSimpleProtocol() Option {
	return func(cfg *engineConfig) { cfg.poolConfig.SimpleProtocol = true }
}

// WithQueryTimeout sets a default deadline applied to every engine-level query
// and exec that does not already carry a shorter deadline. Zero (the default)
// means no default timeout. It composes with caller-supplied deadlines — the
// shorter one wins. Per-builder .Timeout(d) overrides this default for that
// query. The default is NOT auto-applied to queries issued inside an explicit
// Tx, because a timeout firing mid-transaction aborts the whole transaction;
// use .Timeout(d) explicitly there if you accept that semantics.
func WithQueryTimeout(d time.Duration) Option {
	return func(cfg *engineConfig) { cfg.queryTimeout = d }
}

// WithAuthToken sets the authentication token for a libSQL/Turso connection.
// It is appended to the DSN as the authToken query parameter.
//
// Prefer a libsql:// or https:// DSN over ws://wss:// — the websocket transport
// corrupts time.Time columns (see NewEngine).
func WithAuthToken(token string) Option {
	return func(cfg *engineConfig) { cfg.authToken = token }
}

// applyAuthToken appends an authToken query parameter to a libSQL DSN if a token
// is provided and the DSN does not already carry one.
// Delegates to internal/dsn so the engine and CLI cannot drift apart.
func applyAuthToken(d, token string) string {
	return dsn.ApplyAuthToken(d, token)
}

// warnWSTransport logs a warning when a libSQL DSN uses the websocket transport
// (ws:// or wss://). The websocket transport returns DATETIME columns as strings
// rather than time values, silently corrupting time.Time scans (including the
// always-present created_at/updated_at). The HTTP transport (https://) does not
// have this defect. Operators should prefer libsql:// or https:// for models with
// time.Time fields. See docs/prd.md.
func (e *Engine) warnWSTransport(ctx context.Context, d string) {
	if !strings.HasPrefix(d, "ws://") && !strings.HasPrefix(d, "wss://") {
		return
	}
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"drel: libSQL websocket transport returns DATETIME as strings and corrupts time.Time columns (e.g. created_at/updated_at); use a libsql:// or https:// DSN for models with time fields",
		"dsn_scheme", dsnScheme(d))
}

// dsnScheme returns the URL scheme of a DSN (the part before "://"), or "" if the
// DSN carries no scheme. Used for diagnostics without logging credentials.
func dsnScheme(d string) string {
	if i := strings.Index(d, "://"); i >= 0 {
		return d[:i]
	}
	return ""
}

// WithReadReplica registers a read replica. Read queries issued through
// repositories (not within a transaction, and not forced to Primary) are routed
// round-robin across all registered replicas; writes and transactions always go
// to the primary.
func WithReadReplica(dsn string) Option {
	return func(cfg *engineConfig) {
		cfg.replicaDSNs = append(cfg.replicaDSNs, dsn)
	}
}

// WithContext sets the context used during engine creation.
func WithContext(ctx context.Context) Option {
	return func(cfg *engineConfig) {
		cfg.ctx = ctx
	}
}

// WithDriver overrides the driver used by the engine.
// When set, auto-detection is skipped for the driver.
func WithDriver(drv driver.Driver) Option {
	return func(cfg *engineConfig) {
		cfg.drv = drv
	}
}

// WithDialect overrides the SQL dialect used by the engine.
// When set, auto-detection is skipped for the dialect.
func WithDialect(dia dialect.Dialect) Option {
	return func(cfg *engineConfig) {
		cfg.dia = dia
	}
}

// Close shuts down the underlying database connection and any read replicas.
func (e *Engine) Close() {
	e.drv.Close()
	for _, r := range e.replicas {
		r.Close()
	}
}

// readDriver selects the driver for a read query: the primary when primary is
// true or no replicas are registered, otherwise a replica chosen round-robin.
func (e *Engine) readDriver(primary bool) driver.Driver {
	if primary || len(e.replicas) == 0 {
		return e.drv
	}
	n := atomic.AddUint64(&e.rrCounter, 1)
	return e.replicas[(n-1)%uint64(len(e.replicas))]
}

// replicaCooling reports whether replica idx failed a read within the cooldown.
func (e *Engine) replicaCooling(idx int, cutoff int64) bool {
	v, ok := e.replicaFailed.Load(idx)
	return ok && v.(int64) > cutoff
}

// markReplicaFailed records that replica idx failed a read at now (UnixNano).
func (e *Engine) markReplicaFailed(idx int, now int64) {
	e.replicaFailed.Store(idx, now)
}

// readWithFailover runs do against read replicas (skipping ones that recently
// failed), falling back to the primary. It returns the first success or, if
// every target fails, the last error. When primary is true or no replicas are
// registered it runs against the primary directly.
func (e *Engine) readWithFailover(ctx context.Context, primary bool, do func(d driver.Driver) (driver.Rows, error)) (driver.Rows, error) {
	if primary || len(e.replicas) == 0 {
		return do(e.drv)
	}
	now := time.Now().UnixNano()
	cutoff := now - int64(replicaCooldown)
	start := int(atomic.AddUint64(&e.rrCounter, 1) - 1)
	n := len(e.replicas)

	var lastErr error
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if e.replicaCooling(idx, cutoff) {
			continue // still cooling down from a recent failure
		}
		rows, err := do(e.replicas[idx])
		if err == nil {
			return rows, nil
		}
		e.markReplicaFailed(idx, now)
		lastErr = err
		if e.logger != nil {
			e.logger.WarnContext(ctx, "drel: read replica failed, trying next target",
				"replica", idx, "err", err)
		}
	}
	if lastErr == nil && e.logger != nil {
		// Every replica was within its cooldown window; go straight to primary.
		e.logger.WarnContext(ctx, "drel: all read replicas recently failed, using primary")
	}
	return do(e.drv)
}

// rowDriver picks a read driver for the single-row path (and the batch path),
// skipping replicas that recently failed a read. Unlike readWithFailover it
// cannot retry after the fact, so it only avoids known-bad replicas and
// otherwise round-robins.
func (e *Engine) rowDriver(ctx context.Context, primary bool) driver.Driver {
	if primary || len(e.replicas) == 0 {
		return e.drv
	}
	cutoff := time.Now().UnixNano() - int64(replicaCooldown)
	start := int(atomic.AddUint64(&e.rrCounter, 1) - 1)
	n := len(e.replicas)
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if !e.replicaCooling(idx, cutoff) {
			return e.replicas[idx]
		}
	}
	if e.logger != nil {
		e.logger.WarnContext(ctx, "drel: all read replicas recently failed, using primary for row read")
	}
	return e.drv
}

// Exec executes a raw SQL statement and returns the number of rows affected.
// $N placeholders are rewritten to ? on dialects that use ? (SQLite/libSQL).
func (e *Engine) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	if needsPlaceholderRewrite(e) {
		sql = rewritePlaceholders(sql)
	}
	return e.execInternal(ctx, sql, args...)
}

// Query executes a raw SQL query and returns the result rows.
// The caller must close the returned Rows when done. $N placeholders are
// rewritten to ? on dialects that use ? (SQLite/libSQL).
func (e *Engine) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	if needsPlaceholderRewrite(e) {
		sql = rewritePlaceholders(sql)
	}
	return e.queryInternal(ctx, sql, args...)
}

// QueryRow executes a raw SQL query that is expected to return at most one row.
// $N placeholders are rewritten to ? on dialects that use ? (SQLite/libSQL).
func (e *Engine) QueryRow(ctx context.Context, sql string, args ...any) Row {
	if needsPlaceholderRewrite(e) {
		sql = rewritePlaceholders(sql)
	}
	return e.queryRowInternal(ctx, sql, args...)
}

func (e *Engine) driver() driver.Driver {
	return e.drv
}

func (e *Engine) dialect() dialect.Dialect {
	return e.dia
}

// DialectName reports the engine's dialect: "postgres" or "sqlite".
// libSQL/Turso reuse the SQLite dialect, so they also report "sqlite".
func (e *Engine) DialectName() string {
	return e.dia.Name()
}

// ApplyMigrations runs all pending up-migrations from dir against the engine's
// own connection pool, returning the number applied. It reuses the standard
// migration runner (tracking table + checksums), so it is safe to call against
// an in-memory SQLite engine — the migrations land in the same database the
// engine queries. The migration files must be written for the engine's dialect
// (see Engine.DialectName); this is the caller's responsibility.
func (e *Engine) ApplyMigrations(ctx context.Context, dir string) (int, error) {
	return migrate.NewRunner(e.drv, dir, e.DialectName()).Up(ctx)
}

// ApplyMigrationsFS applies the pending migrations of one or more embedded
// migration sets and returns how many it applied. Each feature slice embeds its
// own migrations with //go:embed, and drel merges the sets in version order, so
// the migrations run in the order they were written and not in the order the
// arguments appear.
//
//	//go:embed *.sql
//	var FS embed.FS            // generated in each module
//
//	n, err := db.ApplyMigrationsFS(ctx, posts.FS, users.FS, billing.FS)
//
// A version that appears in two modules is an error that names both files. Two
// slices generated in the same second, or on two branches, produce the same
// version, and there is no correct order between them.
//
// The migration files must be written for the engine's dialect.
func (e *Engine) ApplyMigrationsFS(ctx context.Context, fsys ...fs.FS) (int, error) {
	return migrate.NewRunnerFS(e.drv, e.DialectName(), fsys...).Up(ctx)
}

// includeDialect exposes the dialect for the include reader interface.
func (e *Engine) includeDialect() dialect.Dialect { return e.dia }

// startSpan begins a tracing span for a query if a tracer is configured.
// It returns the (possibly augmented) context and a no-op-safe end function.
func (e *Engine) startSpan(ctx context.Context, name string) (context.Context, func(err error)) {
	if e.tracer == nil {
		return ctx, func(error) {}
	}
	spanCtx, span := e.tracer.Start(ctx, name)
	return spanCtx, func(err error) {
		span.RecordError(err)
		span.End()
	}
}

// withTimeout derives a child context bounded by the engine's default query
// timeout. It returns the original context (and a no-op cancel) when no default
// is configured or when the caller's existing deadline is already at or under
// the default. The returned cancel must always be deferred by the caller.
func (e *Engine) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return applyTimeout(ctx, e.queryTimeout)
}

// applyTimeout bounds ctx by d, returning ctx unchanged when d <= 0 or the
// existing caller deadline is already at or under d (shorter wins).
func applyTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= d {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func (e *Engine) execInternal(ctx context.Context, sql string, args ...any) (int64, error) {
	ctx, cancel := e.withTimeout(ctx)
	defer cancel()
	ctx, endSpan := e.startSpan(ctx, "drel.exec")
	start := time.Now()
	n, err := e.drv.Exec(ctx, sql, args...)
	endSpan(err)
	e.notifyQueryHooks(ctx, sql, args, time.Since(start), err)
	return n, dberr.Classify(err)
}

func (e *Engine) queryInternal(ctx context.Context, sql string, args ...any) (Rows, error) {
	return e.queryRouted(ctx, false, sql, args...)
}

// queryRouted executes a read query against a replica (primary=false) or the
// primary (primary=true), applying the engine default query timeout.
func (e *Engine) queryRouted(ctx context.Context, primary bool, sql string, args ...any) (Rows, error) {
	return e.queryRoutedTimeout(ctx, primary, e.queryTimeout, sql, args...)
}

// queryRoutedTimeout is queryRouted with an explicit timeout override (0 = use
// the engine default; a positive value overrides it for this call). The shorter
// of the override and any caller deadline wins.
func (e *Engine) queryRoutedTimeout(ctx context.Context, primary bool, timeout time.Duration, sql string, args ...any) (Rows, error) {
	if timeout <= 0 {
		timeout = e.queryTimeout
	}
	// A multi-row Rows handle is drained by the caller after this function
	// returns, so we must NOT defer cancel() here — doing so would cancel the
	// context the instant we return the Rows, aborting iteration. Instead we
	// wrap the Rows in a timeoutRows that fires cancel on Close(), mirroring
	// the timeoutRow pattern used on the single-row path.
	rowCtx, cancel := applyTimeout(ctx, timeout)
	rowCtx, endSpan := e.startSpan(rowCtx, "drel.query")
	start := time.Now()
	rows, err := e.readWithFailover(rowCtx, primary, func(d driver.Driver) (driver.Rows, error) {
		return d.Query(rowCtx, sql, args...)
	})
	endSpan(err)
	e.notifyQueryHooks(rowCtx, sql, args, time.Since(start), err)
	if err != nil {
		cancel() // query failed — release the context now
		return nil, dberr.Classify(err)
	}
	return timeoutRows{rows: rows, cancel: cancel}, nil
}

func (e *Engine) queryRowInternal(ctx context.Context, sql string, args ...any) Row {
	return e.queryRowRouted(ctx, false, sql, args...)
}

func (e *Engine) queryRowRouted(ctx context.Context, primary bool, sql string, args ...any) Row {
	return e.queryRowRoutedTimeout(ctx, primary, e.queryTimeout, sql, args...)
}

func (e *Engine) queryRowRoutedTimeout(ctx context.Context, primary bool, timeout time.Duration, sql string, args ...any) Row {
	if timeout <= 0 {
		timeout = e.queryTimeout
	}
	// A QueryRow defers its error to Scan, so the derived context must outlive
	// this function; cancel is attached to the returned row's lifecycle via a
	// timeoutRow wrapper that cancels on Scan.
	rowCtx, cancel := applyTimeout(ctx, timeout)
	rowCtx, endSpan := e.startSpan(rowCtx, "drel.queryRow")
	start := time.Now()
	row := e.rowDriver(rowCtx, primary).QueryRow(rowCtx, sql, args...)
	endSpan(nil)
	e.notifyQueryHooks(rowCtx, sql, args, time.Since(start), nil)
	return classifyRow{row: timeoutRow{row: row, cancel: cancel}}
}

// queryOn runs a read query against a specific driver (chosen by the batch's
// single-target routing) with tracing, query-hook notification, and error
// classification — the same plumbing as queryRoutedTimeout, but without
// re-choosing the driver or applying a per-query timeout.
func (e *Engine) queryOn(ctx context.Context, d driver.Driver, sql string, args ...any) (Rows, error) {
	ctx, endSpan := e.startSpan(ctx, "drel.query")
	start := time.Now()
	rows, err := d.Query(ctx, sql, args...)
	endSpan(err)
	e.notifyQueryHooks(ctx, sql, args, time.Since(start), err)
	if err != nil {
		return nil, dberr.Classify(err)
	}
	return rows, nil
}

func (e *Engine) OnBeforeCommit(hook BeforeCommitHook) {
	e.hookMu.Lock()
	e.beforeCommitHooks = append(e.beforeCommitHooks, hook)
	e.hookMu.Unlock()
}

func (e *Engine) OnAfterCommit(hook AfterCommitHook) {
	e.hookMu.Lock()
	e.afterCommitHooks = append(e.afterCommitHooks, hook)
	e.hookMu.Unlock()
}

// WithAfterCommitErrorSink registers a callback that receives errors and
// recovered panics from after-commit hooks. After-commit hooks run after the
// commit has already succeeded, so their failures cannot roll back the
// transaction — durable side-effects belong in the outbox. The sink is the only
// signal for such failures; if unset they are dropped. The sink itself is
// recovered, so a panicking sink cannot crash the caller.
func WithAfterCommitErrorSink(fn func(ctx context.Context, err error)) Option {
	return func(cfg *engineConfig) { cfg.afterCommitSink = fn }
}

// dispatchAfterCommit runs every registered after-commit hook with a context
// detached from cancellation (so a cancelled request ctx does not silently drop
// already-committed side-effects) while preserving its values. Each hook is run
// under recover so one panicking or slow handler does not abort the rest or
// crash the caller; recovered panics are reported to the configured
// after-commit error sink (WithAfterCommitErrorSink) when set. After-commit
// failures cannot roll back — durable side-effects belong in the outbox.
func (e *Engine) dispatchAfterCommit(ctx context.Context, events []any) {
	hooks := e.snapshotAfterCommitHooks()
	if len(hooks) == 0 {
		return
	}
	dctx := context.WithoutCancel(ctx)
	for _, hook := range hooks {
		e.runAfterCommitHook(dctx, hook, events)
	}
}

// runAfterCommitHook invokes a single after-commit hook under recover, routing
// any panic to the error sink.
func (e *Engine) runAfterCommitHook(ctx context.Context, hook AfterCommitHook, events []any) {
	defer func() {
		if p := recover(); p != nil {
			e.reportAfterCommit(ctx, fmt.Errorf("drel: after-commit hook panicked: %v", p))
		}
	}()
	hook(ctx, events)
}

// reportAfterCommit forwards an after-commit failure to the configured sink, if
// any. The sink is itself recovered so a faulty sink cannot crash the caller.
func (e *Engine) reportAfterCommit(ctx context.Context, err error) {
	if e.afterCommitSink == nil {
		return
	}
	defer func() { _ = recover() }()
	e.afterCommitSink(ctx, err)
}

// addEventSink registers a function that receives all committed events (including
// those from entities staged by before-commit hooks) after the hook-flush step.
// This is the registration point used by UseOutbox.
func (e *Engine) addEventSink(fn func(ctx context.Context, tx *Tx, events []any) error) {
	e.eventSinks = append(e.eventSinks, fn)
}

// timeoutRow defers a context cancel until Scan runs, so a row whose query used
// a derived (timeout) context does not leak the cancel func. The cancel is
// idempotent.
type timeoutRow struct {
	row    driver.Row
	cancel context.CancelFunc
}

func (r timeoutRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	r.cancel()
	return err
}

// timeoutRows wraps a multi-row Rows result and defers the context cancel until
// Close is called. This is the multi-row equivalent of timeoutRow: because the
// caller drains rows after queryRoutedTimeout returns, we cannot cancel the
// context in that function — we must hold the cancel alive until the caller
// signals it is done by calling Close. The cancel is idempotent.
type timeoutRows struct {
	rows   Rows
	cancel context.CancelFunc
}

func (r timeoutRows) Next() bool {
	if r.rows == nil {
		return false
	}
	return r.rows.Next()
}
func (r timeoutRows) Scan(dest ...any) error {
	if r.rows == nil {
		return nil
	}
	return r.rows.Scan(dest...)
}
func (r timeoutRows) Err() error {
	if r.rows == nil {
		return nil
	}
	return r.rows.Err()
}
func (r timeoutRows) Close() {
	if r.rows != nil {
		r.rows.Close()
	}
	r.cancel()
}
