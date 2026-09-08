export const meta = {
  name: 'prod-readiness-auto',
  description: 'Sequentially execute the remaining production-readiness TDD plans (W1-G2 onward), with adversarial review and a build-green gate between plans; stop on first hard failure.',
  whenToUse: 'Auto-drive the remaining Drel production-readiness plans in roadmap order on an isolated branch.',
  phases: [
    { title: 'W1', detail: 'Correctness & data integrity (g2-g8)' },
    { title: 'W2', detail: 'Feature completeness (g1-g9)' },
    { title: 'W3', detail: 'Production hardening (g1-g9)' },
    { title: 'W-CLI', detail: 'Developer experience (g1-g4)' },
  ],
}

// Ordered list of remaining production-readiness plans (roadmap order).
// Embedded here (not via args) so delivery can't drop them. Stop-on-fail, sequential.
const PLAN_DIR = '/Users/nathananderson-tennant/Development/drel-go/docs/superpowers/plans/2026-06-14-'
const PLANS = [
  // w1-g2 (pagination) and w1-g3 (change-tracking) are COMPLETE and merged on this branch.
  ['w1-g4', 'W1', 'identity-map-by-pk', 'Identity map by (table, PK) — one instance per row; no silent lost update'],
  ['w1-g5', 'W1', 'marker-mutation-correctness', 'Versioned-on-delete; fix Attach-on-Audit dup column; uint-PK schema'],
  ['w1-g6', 'W1', 'query-builder-predicate-correctness', 'Empty In/NotIn/And/Or valid SQL; Raw no-panic; WhereIf'],
  ['w1-g7', 'W1', 'bulk-safety', 'Bulk full-table guards; honor app-key/audit/version; correct counts on rollback'],
  ['w1-g8', 'W1', 'relationship-loading-correctness', 'Include Limit(n) per-parent (window fn); M2M UUID keys; preserve M2M OrderBy'],
  ['w2-g1', 'W2', 'multicolumn-value-objects', 'MultiColumnMapper (Money->amount+currency) end-to-end through codegen'],
  ['w2-g2', 'W2', 'single-column-vo-contract', 'Single-col VO: lock sql.Scanner/driver.Valuer contract; infer SQL type'],
  ['w2-g3', 'W2', 'enum-completeness', 'Int enums valid DDL; enum-value-growth diff; declaration order; default='],
  ['w2-g4', 'W2', 'range-operators', 'time.Time/uuid/VO range ops (GT/GTE/LT/LTE/Between, Before/After)'],
  ['w2-g5', 'W2', 'copy-bulk-insert', 'Postgres COPY (pgx.CopyFrom) + fallback; ON CONFLICT DO NOTHING'],
  ['w2-g6', 'W2', 'json-array-columns', 'JSON/jsonb + array/slice/map columns (DDL + scan/value + diff)'],
  ['w2-g7', 'W2', 'advisory-locks', 'Tx.AdvisoryLock/TryAdvisoryLock (PG advisory locks; SQLite fallback)'],
  ['w2-g8', 'W2', 'projection-aggregate-gaps', 'DISTINCT/COUNT(DISTINCT)/COUNT(*)/JOINs in projections'],
  ['w2-g9', 'W2', 'db-tag-default-parsing', 'Comma-safe check=; working default=; fail-loud unknown tag options'],
  ['w3-g1', 'W3', 'timeouts-health-pool', 'Query timeout; Ping/HealthCheck/Stats; PgBouncer exec mode; raw-API consistency'],
  ['w3-g2', 'W3', 'retry-error-classification', 'WithRetry/TransactionWithRetry; classify commit + pipeline + SQLITE_BUSY'],
  ['w3-g3', 'W3', 'replica-routing-failover', 'Replica failover to primary; consistent routing for Include/Select/Primary()'],
  ['w3-g4', 'W3', 'tracing-batching-devmode', 'Spans/hooks on tx/bulk/pipeline; batching error model; dev-mode safety'],
  ['w3-g5', 'W3', 'tx-uow-parity', 'Bulk*/Include/Select/Aggregate/GroupBy/Batch on Tx & UoW'],
  ['w3-g6', 'W3', 'migration-robustness', 'libsql connect; drift/verify; first-down pivots+enums; migration lock; lifecycle'],
  ['w3-g7', 'W3', 'outbox-event-dispatch', 'Outbox index; after-commit ctx + panic recovery'],
  ['w3-g8', 'W3', 'transaction-lifecycle-safety', 'Detached-context rollback; savepoint-release safety; read-only tx'],
  ['w3-g9', 'W3', 'dialect-parity-hardening', 'ws:// time corruption guard; SQLite RETURNING; DSN/FK ON DELETE/ON UPDATE'],
  ['wcli-g1', 'W-CLI', 'cli-flags-config-tests', 'Dialect validation; --config=value; migrate new arg fix; --help/version; CLI tests'],
  ['wcli-g2', 'W-CLI', 'go-generate-watch', '//go:generate support + generate --watch'],
  ['wcli-g3', 'W-CLI', 'atomic-codegen-failloud', 'Atomic codegen; fail-loud on unscanned relations / unsupported types / dup model names'],
  ['wcli-g4', 'W-CLI', 'test-helpers', 'dreltest/pgtest: real WithSeed, CreateSchema/WithMigrations, dialect guards'],
]
// Optional: args may be an array of plan IDs to restrict/resume the run (e.g. ["w1-g2","w1-g3"]).
let only = null
if (Array.isArray(args)) only = new Set(args)
else if (typeof args === 'string' && args.trim().startsWith('[')) { try { only = new Set(JSON.parse(args)) } catch {} }
const plans = PLANS
  .map(([id, ws, slug, goal]) => ({ id, ws, path: `${PLAN_DIR}${id}-${slug}.md`, goal }))
  .filter(p => only ? only.has(p.id) : p.id === 'wcli-g4') // g2..wcli-g3 already complete on this branch
if (plans.length === 0) {
  log('No plans selected; nothing to do.')
  return { error: 'no-plans' }
}
log(`Executing ${plans.length} plan(s): ${plans.map(p => p.id).join(', ')}`)

const IMPL_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['status', 'startSha', 'endSha', 'tasksCompleted', 'totalTasks', 'buildGreen', 'notes'],
  properties: {
    status: { type: 'string', enum: ['DONE', 'PARTIAL', 'BLOCKED'] },
    startSha: { type: 'string' },
    endSha: { type: 'string' },
    tasksCompleted: { type: 'integer' },
    totalTasks: { type: 'integer' },
    commits: { type: 'array', items: { type: 'string' } },
    buildGreen: { type: 'boolean' },
    integrationRun: { type: 'boolean' },
    notes: { type: 'string' },
  },
}

const REVIEW_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['verdict', 'summary'],
  properties: {
    verdict: { type: 'string', enum: ['APPROVED', 'CHANGES_REQUESTED'] },
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
        },
      },
    },
    summary: { type: 'string' },
  },
}

const VERIFY_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['green', 'build', 'vet', 'unitTests', 'integrationTests', 'details'],
  properties: {
    green: { type: 'boolean' },
    build: { type: 'boolean' },
    vet: { type: 'boolean' },
    unitTests: { type: 'boolean' },
    integrationTests: { type: 'string', enum: ['pass', 'fail', 'flaky-ignored', 'infra-unavailable'] },
    details: { type: 'string' },
  },
}

const REPO = '/Users/nathananderson-tennant/Development/drel-go'

function implementerPrompt(p) {
  return `You are executing ONE implementation plan end-to-end for the Drel Go ORM, on the current git branch (prod-readiness-auto). Work from ${REPO}.

PLAN FILE: ${p.path}
PLAN ID: ${p.id}
PLAN GOAL: ${p.goal}

## How to execute
1. FIRST run \`git rev-parse HEAD\` and record it as startSha.
2. Read the ENTIRE plan file. It is a self-contained TDD plan with numbered "### Task N" sections; each has steps (write failing test → verify fail → minimal impl → verify pass → conventional-commit). The plan often contains EXACT code to paste.
3. Execute every task IN ORDER, following its steps exactly. Use strict TDD. Commit after each task with the conventional-commit message the plan specifies (feat:/fix:/test:/docs:/chore:).
4. After EACH task, the build must stay green: \`go build ./...\` and \`go vet ./...\` exit 0, and \`go test ./...\` passes. If a task is a new query path, run its integration test (\`go test -tags integration ./...\` for the touched package; Docker IS available). If integration infra flakes on an UNRELATED test, note it but don't treat it as your failure.
5. CODEGEN/EMITTER changes: if this plan changes \`internal/codegen\` or any dialect emitter, regenerate ALL committed example files and confirm they still build (project convention: generated examples/**/*_drel.go ARE committed).

## CRITICAL — do not hack around plan defects
If the plan's own test assertions contradict its implementation, or following a step would break the build or introduce a silent bug, STOP. Do NOT "make the test pass" by corrupting the design. Report status BLOCKED with the specific contradiction and where you stopped. (A real example from a prior plan: a test asserted a struct-field index while the implementation and doc comment specified a within-slice position — following the test blindly would have panicked downstream.) Fail loud; never ship detected-but-unimplemented behavior or silent skips.

## Finish
When all tasks are done (or you stop), run \`git rev-parse HEAD\` and record endSha. Run \`go build ./... && go vet ./... && go test ./...\` once more and record whether the build is green.

Return ONLY the structured report:
- status: DONE if every task completed with a green build; PARTIAL if some tasks done but not all; BLOCKED if you hit a plan defect or an unrecoverable failure.
- startSha, endSha (short or full), tasksCompleted, totalTasks, commits (subject lines), buildGreen, integrationRun, notes (anything the reviewer must know: deviations, flaky tests, plan defects).`
}

function reviewerPrompt(p, impl) {
  return `You are an adversarial reviewer for a just-implemented plan in the Drel Go ORM (${REPO}, branch prod-readiness-auto). Do NOT trust the implementer's report — read the actual diff and code.

PLAN FILE: ${p.path}
PLAN GOAL: ${p.goal}
DIFF RANGE: ${impl.startSha}..${impl.endSha}  (run \`git diff ${impl.startSha}..${impl.endSha}\` and \`git log --oneline ${impl.startSha}..${impl.endSha}\`)
Implementer notes: ${impl.notes}

## Review for (in priority order)
1. CORRECTNESS / data-integrity: does the change actually achieve the plan GOAL, not merely pass its own tests? Hunt for the failure patterns this program cares about: silent data loss, positional/order corruption, wrong null handling, off-by-one, lost-update, retry/commit-ordering bugs, count-on-rollback errors. Trace the real code paths.
2. SPEC compliance: implemented what the GOAL claims — nothing silently skipped, nothing detected-but-unimplemented. No untyped/string-only escape hatches where a typed API was promised. No unproven claims.
3. PARAMETERIZATION/safety preserved: still parameterized (no SQL injection), no client-side eval introduced, no N+1 added.
4. Tests genuinely prove behavior (adversarial cases, not tautologies); integration coverage exists for new query paths.
5. Quality: idiomatic, clear, no dead code, follows existing patterns.

Read the dialect emitters / codegen if the change touches them; verify emit-order/scan-order invariants hold.

Return ONLY the structured verdict: APPROVED only if you are confident the change is correct and complete; otherwise CHANGES_REQUESTED with specific issues (severity, file, summary).`
}

function fixerPrompt(p, review) {
  const issues = (review.issues || []).map((i, n) => `  ${n + 1}. [${i.severity}] ${i.file || ''} — ${i.summary}`).join('\n')
  return `You are fixing reviewer-identified issues in the Drel Go ORM (${REPO}, branch prod-readiness-auto) for plan ${p.id} (${p.goal}).

The reviewer returned CHANGES_REQUESTED:
${review.summary}

Issues:
${issues}

Fix each issue properly (TDD where it's behavioral: add/adjust a failing test first, then fix). Keep the build green: \`go build ./... && go vet ./... && go test ./...\` must pass. Commit your fixes with conventional-commit messages. If an issue reflects a genuine plan defect you cannot resolve without a design decision, STOP and report it rather than hacking around it.

Report briefly: what you fixed, what you couldn't, and whether the build is green.`
}

function verifierPrompt(p) {
  return `You are the build-green gate for the Drel Go ORM (${REPO}, branch prod-readiness-auto), after plan ${p.id}. Docker IS available (testcontainers Postgres).

Run, in ${REPO}, in order:
1. \`go build ./...\`  → build pass/fail
2. \`go vet ./...\`    → vet pass/fail
3. \`go test ./...\`   → unit pass/fail
4. \`go test -tags integration ./...\` (timeout 600s) → integration. This is the REAL backstop: a prior plan's change broke soft-delete integration while unit tests stayed green, so DO run it.

FLAKE HANDLING for step 4: if the integration run reports failures, RE-RUN each distinct failing test in isolation once (\`go test -tags integration . -run '<TestName>' -count=1 -v\`). If it then PASSES in isolation, it was an environmental flake (testcontainers resource contention) — record it as integrationTests="flaky-ignored" and name it in details, do NOT count it against green. If it FAILS again in isolation, it is a REAL failure → integrationTests="fail". If Docker/testcontainers is genuinely unavailable, integrationTests="infra-unavailable".

Return ONLY the structured result:
- green = true ONLY if build, vet, unitTests all pass AND integrationTests is "pass", "flaky-ignored", or "infra-unavailable". green = false if any of build/vet/unit fail OR integrationTests="fail".
- details: paste the first real failing package/test and its error; for flaky-ignored, name the flaked test and note it passed in isolation; otherwise "all green".`
}

const results = []
let stopped = null

for (const p of plans) {
  phase(p.ws)
  log(`▶ ${p.id}: ${p.goal}`)

  const impl = await agent(implementerPrompt(p), {
    label: `impl:${p.id}`, phase: p.ws, model: 'sonnet', schema: IMPL_SCHEMA,
  })

  if (!impl) {
    stopped = { plan: p.id, stage: 'implement', reason: 'implementer agent returned null (skipped/died)' }
    results.push({ id: p.id, outcome: 'ABORTED', detail: stopped.reason })
    log(`✖ ${p.id}: implementer died — stopping.`)
    break
  }

  if (impl.status !== 'DONE' || !impl.buildGreen) {
    stopped = { plan: p.id, stage: 'implement', reason: `status=${impl.status} buildGreen=${impl.buildGreen} (${impl.tasksCompleted}/${impl.totalTasks} tasks): ${impl.notes}` }
    results.push({ id: p.id, outcome: impl.status, detail: stopped.reason, range: `${impl.startSha}..${impl.endSha}` })
    log(`✖ ${p.id}: ${impl.status}, build green=${impl.buildGreen} — stopping. ${impl.notes}`)
    break
  }

  let review = await agent(reviewerPrompt(p, impl), {
    label: `review:${p.id}`, phase: p.ws, model: 'opus', schema: REVIEW_SCHEMA,
  })

  if (review && review.verdict === 'CHANGES_REQUESTED') {
    const hasBlocking = (review.issues || []).some(i => i.severity === 'critical' || i.severity === 'important')
    if (hasBlocking) {
      log(`… ${p.id}: review requested changes — dispatching fixer.`)
      await agent(fixerPrompt(p, review), { label: `fix:${p.id}`, phase: p.ws, model: 'sonnet' })
    } else {
      log(`… ${p.id}: review noted only minor issues — proceeding.`)
    }
  }

  const verify = await agent(verifierPrompt(p), {
    label: `verify:${p.id}`, phase: p.ws, model: 'sonnet', schema: VERIFY_SCHEMA,
  })

  if (!verify || !verify.green) {
    stopped = { plan: p.id, stage: 'verify', reason: verify ? verify.details : 'verifier returned null' }
    results.push({ id: p.id, outcome: 'BUILD_BROKEN', detail: stopped.reason, range: `${impl.startSha}..${impl.endSha}` })
    log(`✖ ${p.id}: build gate FAILED after review — stopping. ${stopped.reason}`)
    break
  }

  results.push({
    id: p.id, outcome: 'COMPLETE',
    range: `${impl.startSha}..${impl.endSha}`,
    review: review ? review.verdict : 'n/a',
    detail: impl.notes,
  })
  log(`✓ ${p.id}: complete & green (review ${review ? review.verdict : 'n/a'}).`)
}

const completed = results.filter(r => r.outcome === 'COMPLETE').map(r => r.id)
log(`Done. Completed ${completed.length}/${plans.length} plans. ${stopped ? `Stopped at ${stopped.plan} (${stopped.stage}).` : 'All attempted plans complete.'}`)

return { completed, results, stopped }
