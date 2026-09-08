export const meta = {
  name: 'prod-readiness-review',
  description: 'Adversarially re-review the CHANGES_REQUESTED plans and two flagged concerns on prod-readiness-auto, then fix any confirmed critical/important findings.',
  phases: [
    { title: 'Review', detail: '13 parallel adversarial reviewers' },
    { title: 'Fix', detail: 'sequential fixers for confirmed findings' },
  ],
}

const REPO = '/Users/nathananderson-tennant/Development/drel-go'
const PLAN_DIR = `${REPO}/docs/superpowers/plans/2026-06-14-`

// [id, slug, range, goal, focus] — the 11 CHANGES_REQUESTED plans + 2 concern probes.
const ITEMS = [
  ['w1-g7', 'bulk-safety', '3b6ec8b..95c3b55', 'Bulk full-table guards; honor app-key/audit/version; correct counts on rollback',
    'Verify the full-table-guard actually BLOCKS an unguarded BulkDelete/BulkUpdate (no WHERE) and the multi-batch count-on-rollback is correct (batch1 ok, batch2 fails → returned count). AllRows() was added to Repository[T]. Confirm app-key/audit/version honored in bulk paths.'],
  ['w2-g2', 'single-column-vo-contract', '34b3aee..012ca86', 'Single-col VO: lock sql.Scanner/driver.Valuer contract; infer SQL type',
    'The implementer said EmitModelFileChecked does NOT reject multi-col VOs and that a "phantom single-col ColumnMapper/DrelValue/DrelScan API does not exist". VERIFY no claimed PRD feature was silently dropped — is the single-col VO contract (sql.Scanner/driver.Valuer) actually enforced/tested? Confirm voBaseType single-field-struct handling is correct.'],
  ['w2-g4', 'range-operators', 'f241fc0..9dace33', 'time.Time/uuid/VO range ops; SQLite UTC normalize',
    'CRITICAL focus: the SQLite UTC time normalizeArg() fix in internal/dialect/sqlite — verify it is correct AND complete across Eq/In/NotIn/Between/GT/GTE/LT/LTE, and does not double-convert or break Postgres. Also conditional `time` import in emitter; generate_test.go updated to ComparableColumn. Confirm value-objects drel_gen.go restoration did not mask a real codegen bug.'],
  ['w2-g6', 'json-array-columns', '1faf2ba..f1d93e9', 'JSON/jsonb + array/slice/map columns (DDL + scan/value + diff)',
    'Verify JSON/array columns: DDL type, scan, value (driver), AND diff all handle them; aliases threaded through emitScanFunc/emitDiff/emitColumnValue/emitInsertColumns. Confirm the value-objects db/drel_gen.go revert was correct, not hiding a regression.'],
  ['w2-g9', 'db-tag-default-parsing', 'f796ef5..6dcbf90', 'Comma-safe check=; working default=; fail-loud unknown tag options',
    'Verify comma-safe check= parsing (default=ARRAY[...] with brackets, check= with commas), default= actually emitted, and unknown tag options FAIL LOUD. Confirm the multi-col VO firstDBTagSegment() guard does not suppress real unknown-option errors on normal fields.'],
  ['w3-g1', 'timeouts-health-pool', '91127d6..d45ef10', 'Query timeout; Ping/HealthCheck/Stats; PgBouncer exec mode; raw-API consistency',
    'Verify the query timeout is ACTUALLY enforced (the test was changed to drain rows + check rows.Err — confirm a real timeout triggers it, not a vacuous pass). Confirm Ping/HealthCheck/Stats and PgBouncer simple-exec mode work; raw-API consistency.'],
  ['w3-g2', 'retry-error-classification', '76119df..ad3b0e5', 'WithRetry/TransactionWithRetry; classify commit + pipeline + SQLITE_BUSY',
    'Verify retry classification is correct: serialization/40001 at COMMIT, pipeline errors, SQLITE_BUSY are classified as retryable; non-retryable errors are NOT retried. Confirm WithRetry/TransactionWithRetry semantics (no double-execute side effects).'],
  ['w3-g4', 'tracing-batching-devmode', '98cbc34..a6a9e96', 'Spans/hooks on tx/bulk/pipeline; batching error model; dev-mode safety',
    'Verify spans actually emitted on tx/bulk/pipeline (tests were flagged as false-positive-passing). CRITICAL: the ErrBatchPartial error model — batchPartialError.Unwrap() must make BOTH errors.Is(err, ErrBatchPartial) and errors.Is(err, dberr.ErrUniqueViolation) true. Confirm BulkInsert returns 0 (not total) on error per existing rollback test.'],
  ['w3-g6', 'migration-robustness', '98cbc34..2e12805', 'libsql connect; drift/verify; first-down pivots+enums; migration lock; lifecycle',
    'HIGHEST PRIORITY: the implementer warned "CHECK no longer emitted on the Postgres path — only SQLite emits that warning." Determine if Postgres enum CHECK constraints are GENUINELY DROPPED (a regression / silently lost feature) or correctly replaced by a named CHECK constraint in migrations. Read the postgres dialect CREATE TABLE + migration generation for enums. Also verify drift/verify, migration lock, first-down pivot+enum ordering.'],
  ['wcli-g1', 'cli-flags-config-tests', '3bd2174..7c72a53', 'Dialect validation; --config=value; migrate new arg fix; --help/version; CLI tests',
    'Verify --config=value parsing, dialect validation rejects bad dialects, migrate-new name validation/error message, --help/version. Confirm the error-wrapping for migrate-new flag does not mask other flag errors.'],
  ['wcli-g2', 'go-generate-watch', '034caa7..6c24f8c', '//go:generate support + generate --watch',
    'Verify //go:generate works and `generate --watch` detects changes (baseline captured BEFORE Generate to avoid the race). Investigate the implementer-flagged "ResolveModuleRoot pre-existing bug: generate from examples/*/ subdir looks at repo root" — is that a real latent bug worth a finding?'],
  // Two dedicated concern probes (not tied to one diff):
  ['CONCERN-vo', null, null, 'Value-objects example codegen coherence',
    `Investigate ${REPO}/examples/value-objects end-to-end. History: multiple W2 plans hit "two packages both define Account → duplicate field in db/drel_gen.go" and restored db/drel_gen.go from git; wcli-g3 then renamed Account→UserAccount (table accounts→user_accounts) to satisfy a new duplicate-model-name fail-loud. QUESTIONS: (1) Is the example now coherent — does \`go run ./cmd/drel generate\` (or the example's generate) run cleanly without duplicate-field corruption? (2) Are the committed *_drel.go and db/drel_gen.go consistent with the source models (no drift)? (3) Was renaming the domain model Account→UserAccount the right resolution, or is there a latent codegen bug where two packages with same-named models corrupt the aggregated DB struct? Build the example. Report findings + suggested fix. Do NOT edit; review only.`],
  ['CONCERN-check', null, null, 'Postgres enum CHECK constraint emission',
    `Determine whether enum CHECK constraints are still emitted for POSTGRES (in both CREATE TABLE codegen and Atlas/migration generation). Read internal/dialect/postgres and internal/codegen schema/DDL paths and internal/migrate. Compare to SQLite. The w3-g6 work reportedly moved/removed the inline CHECK on the Postgres path. Confirm: are Postgres enum values still constrained at the DB (named CHECK constraint or otherwise), or is the constraint genuinely lost (a real regression that would let invalid enum values into Postgres)? This overlaps w2-g3 (enum-completeness) and w3-g6. Report a clear verdict + suggested fix if it is lost. Review only.`],
]

const REVIEW_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['plan', 'verdict', 'summary'],
  properties: {
    plan: { type: 'string' },
    verdict: { type: 'string', enum: ['CLEAN', 'ISSUES'] },
    issues: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['severity', 'summary'],
        properties: {
          severity: { type: 'string', enum: ['critical', 'important', 'minor'] },
          file: { type: 'string' },
          summary: { type: 'string' },
          suggestedFix: { type: 'string' },
        },
      },
    },
    summary: { type: 'string' },
  },
}

function reviewPrompt(it) {
  const [id, slug, range, goal, focus] = it
  const planLine = slug ? `PLAN FILE: ${PLAN_DIR}${id.replace('CONCERN-', '')}-${slug}.md\nGOAL: ${goal}\nDIFF RANGE: ${range} (run \`git log --oneline ${range}\`; if the range looks wrong/empty, find this plan's commits with \`git log --oneline --grep\` or by scanning git log around them)` : `INVESTIGATION (not a single diff): ${goal}`
  return `You are an adversarial reviewer for the Drel Go ORM (${REPO}, branch prod-readiness-auto). This change ALREADY passed a build+integration gate; your job is to find CORRECTNESS or SILENTLY-DROPPED-FEATURE problems the gate cannot catch. Read real code and RUN things (Docker available for integration). Do NOT trust prior reports.

${planLine}

FOCUS: ${focus}

Look specifically for the project's known failure patterns: silent data loss, detected-but-unimplemented or silently-dropped features, untyped/string-only escape hatches where a typed API was promised, wrong null/UTC/ordering handling, retry/commit-ordering bugs, unproven claims, and tests that pass vacuously (assert nothing real). Verify any feature the plan GOAL promises is actually implemented and tested — not just that the build is green.

Run whatever helps: \`git show\`/\`git diff\` the range, read the current code, \`go test\` specific tests, \`go test -tags integration . -run <T>\`.

Return ONLY the structured verdict for plan="${id}":
- verdict=CLEAN if, after adversarial inspection, the change correctly achieves its goal with no real (critical/important) problem. Minor nits may be listed but still CLEAN.
- verdict=ISSUES if you find a real critical/important problem. List each with severity, file, a precise summary, and a concrete suggestedFix.`
}

const FIX_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['plan', 'status', 'summary'],
  properties: {
    plan: { type: 'string' },
    status: { type: 'string', enum: ['FIXED', 'PARTIAL', 'COULD_NOT_FIX', 'NOT_A_REAL_ISSUE'] },
    buildGreen: { type: 'boolean' },
    commit: { type: 'string' },
    summary: { type: 'string' },
  },
}

function fixPrompt(plan, issues) {
  const list = issues.map((i, n) => `  ${n + 1}. [${i.severity}] ${i.file || ''} — ${i.summary}\n     suggestedFix: ${i.suggestedFix || '(none given)'}`).join('\n')
  return `You are fixing confirmed review findings on the Drel Go ORM (${REPO}, branch prod-readiness-auto), for ${plan}. Work from ${REPO}.

Confirmed critical/important findings to address:
${list}

For each: first CONFIRM it is real by reading the code (if you conclude a finding is NOT a real issue after inspection, say so and skip it — status NOT_A_REAL_ISSUE for that one, do not invent a change). Fix the real ones properly, TDD where behavioral (add/adjust a failing test first). Keep the build green: \`go build ./... && go vet ./... && go test ./...\` must pass; run \`go test -tags integration . -run <relevant>\` for touched query/migration paths (Docker available). If a codegen/emitter file changes, regenerate ALL examples and confirm they build. Commit with a conventional-commit message.

Return the structured result (status FIXED only if all real findings fixed and build green; buildGreen reflects the final go build+vet+unit state; commit = the SHA if you committed).`
}

phase('Review')
log(`Reviewing ${ITEMS.length} items (11 CHANGES_REQUESTED plans + 2 concerns) in parallel...`)
const reviews = (await parallel(ITEMS.map(it => () =>
  agent(reviewPrompt(it), { label: `review:${it[0]}`, phase: 'Review', model: 'opus', schema: REVIEW_SCHEMA })
))).filter(Boolean)

const withIssues = reviews.filter(r => r.verdict === 'ISSUES' &&
  (r.issues || []).some(i => i.severity === 'critical' || i.severity === 'important'))

log(`Review done. ${reviews.filter(r => r.verdict === 'CLEAN').length} CLEAN, ${withIssues.length} with critical/important issues.`)

phase('Fix')
const fixes = []
for (const r of withIssues) {
  const blocking = r.issues.filter(i => i.severity === 'critical' || i.severity === 'important')
  log(`Fixing ${r.plan}: ${blocking.length} blocking finding(s)`)
  const fx = await agent(fixPrompt(r.plan, blocking), { label: `fix:${r.plan}`, phase: 'Fix', model: 'sonnet', schema: FIX_SCHEMA })
  if (fx) fixes.push(fx)
}

return {
  reviews: reviews.map(r => ({ plan: r.plan, verdict: r.verdict, summary: r.summary, issues: r.issues || [] })),
  fixes,
  clean: reviews.filter(r => r.verdict === 'CLEAN').map(r => r.plan),
  needsAttention: withIssues.map(r => r.plan),
}
