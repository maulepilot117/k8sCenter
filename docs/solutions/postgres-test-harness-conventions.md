---
title: PostgreSQL test harness conventions for backend/internal/store
date: 2026-09-12
category: docs/solutions
module: backend/internal/store
problem_type: convention
component: testing_framework
severity: medium
related_components: [database, development_workflow]
tags: [database-testing, test-harness, postgresql, test-isolation, env-gated-tests, migrations]
applies_when:
  - "Adding tests to backend/internal/store or another PostgreSQL-backed package"
  - "Deciding whether a new test harness should use a go:build tag or an env-var skip"
  - "Writing a new table migration and evaluating whether it needs a unique-id isolation exception"
  - "Reviewing whether a CI check silently skips database coverage"
---

# PostgreSQL Test Harness Conventions — `backend/internal/store`

**Status:** active convention. Established by PR #414, corrected by PR #419 (see *History*).
Read this before writing any PostgreSQL-backed test in the Go backend, before adding a
table to `backend/internal/store/migrations/`, and before adding a new environment-gated
test category anywhere in the repo.

The harness is one file: `backend/internal/store/testdb_test.go`. Its file-top comment
(`testdb_test.go:3-61`) is the normative contract; this document is the reasoning behind
it, which a comment cannot carry without becoming a design doc.

---

## Context

`backend/internal/store/` is the package every persistence feature funnels through — it
owns the pool, the embedded migrations, and the `golang-migrate` runner
(`store.go:16-17`, `migrate.go:13-36`). Until PR #414 it contained **zero test files**;
`git ls-tree -r 6dfa7ffe^ -- backend/internal/store | grep _test` returns nothing. Schema
changes were verified by deploying them.

That was survivable while the schema grew one table at a time. It stops being survivable
now: the current schema is **15 tables**, and the queued release plans
(`docs/plans/2026-09-10-release-{a,b,c,d,e,f}-*-impl.md`) introduce **twelve more** —
`user_preferences` (A), five `incident*` tables (D), two `change_receipt*` tables (E), and
four `backup_assurance_*` tables (F). Releases B and C add none: Release C introduces no
table at all, and the `eso_sync_history` DDL in Release B is the *existing* 000011 table
quoted as "Current ... verbatim", not a new one. Every one of them needs tests that do a **real migration
round trip**: a fake or an in-memory shim proves nothing about a partial unique index, an
`ON CONFLICT` clause, or a `CHECK` constraint, which is exactly where these designs put
their correctness (e.g. `docs/plans/2026-09-10-release-f-backup-assurance-impl.md:243-248`
leans on a partial unique index to make exception re-opening idempotent).

> **How the table count was derived.** `grep -c 'CREATE TABLE IF NOT EXISTS'` over
> `backend/internal/store/migrations/*.up.sql` returns **14** and is wrong:
> `000004_create_local_users.up.sql:1` says `CREATE TABLE local_users (` with no
> `IF NOT EXISTS`. The count above comes from a case-insensitive match on `CREATE TABLE`
> with the `IF NOT EXISTS` clause optional, deduplicated by table name. Count the thing,
> not one spelling of it.

The harness therefore had to answer five questions before any suite could be written, and
each answer is a convention that later suites inherit whether or not they think about it.

---

## Guidance

### 1. Env-gated, never build-tagged

The harness gates on a single environment variable and skips when it is absent:

```go
// testdb_test.go:82
const testDatabaseURLEnv = "KUBECENTER_TEST_DATABASE_URL"
```

```go
// testdb_test.go:144-150
connString := testDatabaseURL(os.LookupEnv)
if connString == "" {
    if testDatabaseRequired(os.LookupEnv) { /* Fatalf — see convention 2 */ }
    t.Skipf("%s is not set; skipping PostgreSQL-backed test", testDatabaseURLEnv)
}
```

There is **deliberately no `//go:build` tag on the file**, and `testdb_test.go:16-18`
records why:

> "There is deliberately NO //go:build tag on this file. The repo's canonical check is
> repo-wide `go test ./...` (CLAUDE.md Agent Directive 4); a build tag would exclude these
> tests from that command and they would never run."

`CLAUDE.md:17-22` is the rule being honoured — Agent Directive 4 prescribes
`cd backend && go vet ./... && go test ./...` as the repo-canonical check, adding
"Scoped checks (single file or directory) MISS pre-existing issues in sibling files that
CI will flag." A build tag is a scoping mechanism: it removes the file from the default
build graph, so the canonical command compiles it never, runs it never, and reports
success. Env-gating keeps the file in the default graph — it always compiles, its
hermetic tests always run — and moves the decision to runtime.

**The alternative was proposed and rejected, not merely unconsidered** (session history).
During planning, two independent release-track planners proposed conflicting designs for
this same need: one an env-gated skip, the other a build tag (`//go:build pgintegration`).
The conflict was resolved toward env-gating on exactly the reasoning above — a
build-tag-gated file is invisible to the canonical `go test ./...`, and the repo has no
separate tagged invocation that would pick it up. The rejected option is worth recording
because the final tree shows only the winner: nothing in the code says a build tag was
ever on the table, so the next person to propose one will have to re-derive why it loses.

Two corollaries that are part of the convention:

- **The harness reads only the test variable.** It never falls back to
  `KUBECENTER_DATABASE_URL`, the runtime variable (`testdb_test.go:22-26`), and there is a
  hermetic test asserting that fallback does not exist (`testdb_test.go:325-333`,
  "runtime KUBECENTER_DATABASE_URL is ignored"). A developer shell with the deployment URL
  exported cannot migrate production by running `go test`.
- **The gating decision is a pure function.** `testDatabaseURL(lookup func(string) (string, bool))`
  (`testdb_test.go:125-131`) takes the lookup injected, so the gating rule itself is
  unit-testable with no database (`TestTestDatabaseURL_Gating`, `testdb_test.go:291`).

### 2. In CI, a skip is a hard failure

`go test` prints `ok <pkg>` for a package whose every test skipped, byte-identical to a
package that passed. A gate that can be silently disabled is not a gate, so the harness
ships a second variable that inverts the skip:

```go
// testdb_test.go:95
const testDatabaseRequiredEnv = "KUBECENTER_TEST_REQUIRE_DATABASE"
```

```go
// testdb_test.go:145-148
if testDatabaseRequired(os.LookupEnv) {
    t.Fatalf("%s is set but %s is empty; the PostgreSQL harness was required to run and would otherwise have skipped silently",
        testDatabaseRequiredEnv, testDatabaseURLEnv)
}
```

CI sets both (`.github/workflows/ci.yml:95` and `:100`, on the `Test` step at `:90`),
against a throwaway `postgres:17-alpine` service defined at `ci.yml:50-63`. Developers set
neither and keep the skip — `go test ./...` on a laptop with no database stays green.

`testDatabaseRequired` (`testdb_test.go:99-110`) is deliberately lenient about truthiness:
unset, empty, `0`, `false`, `no` (case- and whitespace-insensitive) mean not required;
**anything else means required**. The asymmetry is intentional — a typo'd value fails
loudly rather than quietly reverting to skip-mode.

### 3. Migrations run through the real runner, once per process

```go
// testdb_test.go:152-168
migrateOnce.Do(func() {
    ctx, cancel := context.WithTimeout(context.Background(), testDBConnectTimeout)
    defer cancel()
    db, err := New(ctx, connString, 0, 0, testLogger())
    ...
    db.Close()
})
```

`New` is the production entry point: it parses the URL, builds the pool, pings with retry,
and calls `db.migrate(connString)` (`store.go:29-94`, migration call at `store.go:86-89`),
which runs `golang-migrate` over the `//go:embed migrations/*.sql` FS
(`migrate.go:14-27`). The test schema is therefore produced by **exactly the code path the
binary uses at boot** — a hand-rolled test migrator could drift from boot behaviour, and
the drift would show up in production rather than in CI.

Three details that are load-bearing:

- **`testDBConnectTimeout = 30 * time.Second`** (`testdb_test.go:87`) bounds the pass.
  `New`'s retry loop is 10 attempts with a `(attempt+1) * 2s` delay (`store.go:49-81`) —
  roughly 90 seconds against a dead host, which is right for a pod waiting on a starting
  database and wrong for a test run. The shorter ceiling turns a typo'd URL into a fast
  failure.
- **`migrateOnce` / `migrateErr`** (`testdb_test.go:112-119`) make the pass once per test
  *binary*, not once per test. The migrating pool is closed immediately; each test then
  gets its own small pool (`MaxConns = 4`, `MinConns = 0`, `testdb_test.go:180-181`) so a
  test that poisons a connection cannot affect siblings, and parallel suites stay well
  under PostgreSQL's default `max_connections`.
- **Schema-ahead detection.** `latestEmbeddedMigrationVersion` (`testdb_test.go:252-279`)
  scans the embedded directory for the highest `NNNNNN` with an `.up.sql`, and the gated
  smoke test compares it against `schema_migrations`, failing *distinctly* when the
  database is **ahead** of the branch (`testdb_test.go:509-515`) — the symptom of reusing a
  test database last migrated by a branch with later migrations. The remedy is named in the
  error text: drop and recreate it.

### 4. Isolate by unique identifier — never truncate, never tear down

```go
// testdb_test.go:28-33
// Isolation contract: the harness never drops the schema and never truncates
// shared tables — later units may run their suites in parallel against the
// same database, and teardown-based isolation would race.
```

The only cleanup the harness registers is `t.Cleanup(pool.Close)` (`testdb_test.go:187`).
No `TRUNCATE`, no `DROP SCHEMA`, no transaction-rollback wrapper. Scoping is the test's job:

```go
// testdb_test.go:200-208
func testOwnerID(t *testing.T) string {
    var suffix [8]byte
    if _, err := rand.Read(suffix[:]); err != nil { ... }
    return ownerIDFor(t.Name(), hex.EncodeToString(suffix[:]))
}
```

`ownerIDFor` (`testdb_test.go:222-246`) lowercases the test name, replaces every character
outside `[a-z0-9]` with `-`, collapses runs, truncates the readable part to 64 chars
(`ownerIDMaxNameLen`, `:217`), and joins it to the fixed `test` prefix (`:212`) and the hex
suffix — yielding e.g. `test-testpreferencestore-create-9f2c1ab70e4d5566`. The name segment
makes stray rows attributable by eye in a shared database; the random suffix is what
guarantees uniqueness across parallel tests, `-count=N` reruns, and reruns after a failed
cleanup.

**Every suite MUST scope the rows it writes by this id (or an equally unique key) and MUST
filter by it on every read** (`testdb_test.go:198-199`).

### 5. The singleton exception: `app_settings`

Convention 4 rests on a property that has to be checked, not assumed: residue is harmless
only for a table whose *every* uniqueness constraint includes a column the test controls.
An audit of the current schema plus the tables the five queued persistence releases add
found exactly one table where that fails.

```sql
-- backend/internal/store/migrations/000002_create_settings.up.sql:2, :19
id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
...
INSERT INTO app_settings (id) VALUES (1) ON CONFLICT DO NOTHING;
```

One row can ever exist, and it is seeded at migrate time. There is no scoping key, so two
suites that write settings collide unconditionally and the residue is visible to every
later test (`testdb_test.go:50-54`). **A suite that touches `app_settings` must serialize
against other such suites and restore what it changed**, via a session-scoped advisory lock
released in `t.Cleanup`:

```go
// prescribed at testdb_test.go:56-61 — no consumer exists yet, so no helper exists yet
SELECT pg_advisory_lock(hashtext('app_settings'))
```

Everything else is covered by one of three test-controlled keys (`testdb_test.go:36-48`):
an **owner column** (`audit_logs."user"`, `nc_reads.user_id`, `mobile_push_devices.user_id`,
Release A's `user_preferences.owner_id`); a **cluster id** (`clusters`,
`cluster_monitoring`, `compliance_snapshots (cluster_id, snapshot_date)`,
`eso_bulk_refresh_jobs`, Release F's `backup_assurance_collector_lease`); or a **natural key
the test picks** (`eso_sync_history (uid, attempt_at)`, `git_commit_cache (canonical_url, sha)`,
and the `TEXT`/`UUID` primary keys on `local_users`, `auth_providers`, `nc_channels`,
`nc_notifications`, `nc_rules`). That accounts for all 15 current tables: 3 owner-column,
4 cluster-id, 7 natural-key, plus `app_settings`.

The twelve queued tables fall in the same scheme, with one addition. `incidents` and
`change_receipts` carry `owner_id TEXT NOT NULL`; `incident_evidence` adds a `cluster_id`;
`incident_notes` has a UUID primary key. The remaining three — `incident_note_revisions`
(`PRIMARY KEY (note_id, revision)`), `incident_grants` (`(incident_id, grantee_id)`) and
`change_receipt_grants` (`(receipt_id, grantee_id)`) — are **derived-key children**: they
key on a parent row the test itself created, so they inherit that parent's uniqueness and
need no scoping column of their own. None of the twelve is a singleton.

Two of these were initially miscategorised in review and are worth stating explicitly,
because both *look* like exceptions and neither is:

- **`eso_bulk_refresh_jobs` is not an exception.** Its partial unique index is
  `ON eso_bulk_refresh_jobs (cluster_id, action, scope_target) WHERE completed_at IS NULL`
  (`backend/internal/store/migrations/000014_unique_active_bulk_jobs.up.sql:9-11`). `cluster_id` leads the index,
  so a unique test cluster id satisfies it — two suites collide only if they *choose* the
  same cluster id.
- **`backup_assurance_collector_lease` is not an exception.** It reads like a singleton —
  one lease, one holder — but it is keyed `cluster_id TEXT PRIMARY KEY`
  (`docs/plans/2026-09-10-release-f-backup-assurance-impl.md:273-280`). Per-cluster, not
  global; a unique test cluster id isolates it like any other cluster-keyed table.

`backend/internal/store/migrations/NOTES.txt:86-115` carries the same rule from the migrations side, ending with
the forward-looking instruction: *"If you add a new table, prefer giving it an owner or
cluster column so it falls under the normal unique-id isolation rule rather than becoming a
second exception."*

---

## Why This Matters

**A build tag would have made the tests unrunnable by the repo's own canonical command.**
Agent Directive 4 (`CLAUDE.md:17-22`) exists because scoped checks miss things; a build tag
scopes at the file level, invisibly. The first sign that DB tests had never run would have
been a migration failing in a cluster. That is the precise failure mode the harness was
built to prevent, so building it behind a build tag would have been self-defeating.

**A silent skip is worse than no test.** `go test` gives a skipped package and a passing
package the same `ok <pkg>` line. Without `KUBECENTER_TEST_REQUIRE_DATABASE`, a dropped
service block, a renamed env var, a copy-pasted job definition, or a YAML indentation slip
in `ci.yml` would disable **every** PostgreSQL-backed test in the repo behind a green check,
and nothing in the output would say so. The gate has to be able to observe its own
disablement; `testdb_test.go:89-94` says exactly this, and `ci.yml:96-99` repeats it at the
call site so the next person editing the workflow sees it without opening Go code.

**Teardown-based isolation races.** `TRUNCATE` and `DROP SCHEMA` are global operations on a
shared resource. The moment a second suite runs — `go test ./...` runs packages in parallel
by default, and `t.Parallel()` is available within a package — one suite's cleanup deletes
another suite's live rows, and the resulting failure is intermittent, ordering-dependent,
and attributed to the wrong test. Scoping by unique id has no such window: a suite reads
only what it wrote, and abandoned rows from a crashed run are inert. The cost is that the
test database accumulates rows; that is the intended trade, and the `test-` prefix on every
generated id (`testdb_test.go:212`) is what makes the residue explicable when someone
inspects the database later.

**The singleton is where the trade breaks, and the only defence is an audit.** The original
#414 comment claimed residue was universally harmless "because no other test will ever look
them up by the same identifier." That is false for `app_settings`, and it would have been
discovered as a flake — a settings test passing alone and failing in a full run — long after
the suites were written. Conventions of this shape (isolation-by-key, tenant-scoped queries,
idempotency-by-natural-key) are only as true as the schema audit behind them, and the audit
has to be redone whenever a table lands.

**A schema enforcement beats a comment.** Release A's `user_preferences.owner_id TEXT NOT NULL`
(`docs/plans/2026-09-10-release-a-saved-workspaces-impl.md:154`) means that suite
*physically cannot* write an unscoped row — an `INSERT` without an owner is a constraint
violation, not a convention violation. The unique index at `:178-179` is
`(owner_id, kind, cluster_id, dedup_key)`, owner-leading, so even the dedup path is
test-isolable. That is the standard to design new tables against.

---

## When to Apply

Apply these conventions when you are:

- **Writing any PostgreSQL-backed test in this backend.** Obtain the pool from `testDB(t)`
  — never construct your own `pgxpool` from an env var, or you re-introduce the runtime-URL
  fallback the harness exists to block. Note `testDB` currently lives in `package store`;
  a suite in another package needs an equivalent gated on the *same* variable, with the same
  skip/require semantics.
- **Adding a table to `backend/internal/store/migrations/`.** Check whether every uniqueness
  constraint on the new table includes a column a test controls. If not, you are creating a
  second `app_settings` — prefer an owner or cluster column (`backend/internal/store/migrations/NOTES.txt:113-115`). If a true
  singleton is genuinely required, add a `NOTES.txt` section saying so and land the
  advisory-lock helper with it.
- **Adding a unique or partial-unique index to an existing table.** A partial index added
  later can silently remove a table from the isolable set. Verify the leading column is
  test-controlled, as `eso_bulk_refresh_jobs` is via `cluster_id`.
- **Adding a new category of gated test anywhere in the repo** (a test needing a live
  cluster, an object store, a real IdP). The pattern transfers whole: env-gate rather than
  build-tag so the file stays in the default build graph; add a `*_REQUIRE_*` companion so
  CI cannot silently lose the category; keep the gating predicate a pure function of an
  injected lookup so it is itself testable without the dependency.
- **Editing `.github/workflows/ci.yml`'s backend job.** Both env vars on the `Test` step
  (`:95`, `:100`) are load-bearing. Removing the second one does not fail anything — that is
  exactly why it must not be removed.

Do **not** apply the isolation convention by reflex to a database you exclusively own (a
disposable container started and destroyed by one test binary). The convention exists
because the CI database is shared across every package in one `go test ./...` invocation.

---

## Examples

### A future suite, end to end

```go
func TestPreferenceStore_CreateAndList(t *testing.T) {
    pool := testDB(t)             // skips locally, Fatalfs in CI if the URL went missing
    owner := testOwnerID(t)       // "test-testpreferencestore-createandlist-<16 hex>"
    ctx := t.Context()

    s := NewPreferenceStore(pool)
    if _, err := s.Create(ctx, owner, PreferenceInput{Kind: "view", Name: "prod pods"}); err != nil {
        t.Fatalf("Create: %v", err)
    }

    // Filter by owner on EVERY read. A bare `SELECT ... FROM user_preferences`
    // sees rows from every other suite and every previous run.
    got, err := s.List(ctx, owner, "view")
    if err != nil {
        t.Fatalf("List: %v", err)
    }
    if len(got) != 1 {
        t.Fatalf("List returned %d rows; want 1", len(got))
    }
    // No cleanup. The rows stay; nothing else can see them.
}
```

Points to copy: the pool comes from `testDB(t)`; the owner comes from `testOwnerID(t)` and
is passed to both the write and the read; there is no `TRUNCATE` and no `t.Cleanup` beyond
what the harness registers; and — because `user_preferences.owner_id` is `TEXT NOT NULL` —
forgetting the owner is a constraint error, not a silent cross-test leak.

### A table with no owner column

Scope by whatever the table's uniqueness actually keys on. For a cluster-keyed table,
generate the cluster id the same way:

```go
cluster := testOwnerID(t) // reused as a unique cluster_id; same uniqueness guarantee
_, err := pool.Exec(ctx,
    `INSERT INTO eso_bulk_refresh_jobs (id, cluster_id, action, scope_target) VALUES ($1,$2,$3,$4)`,
    uuid.New(), cluster, "refresh", "ns/my-es")
```

The `000014` partial unique index is `(cluster_id, action, scope_target) WHERE completed_at IS NULL`
(`backend/internal/store/migrations/000014_unique_active_bulk_jobs.up.sql:9-11`), so a unique `cluster_id` makes the
duplicate-job assertion a *property of this test* rather than a race with whatever else is
running. Do not hard-code `"test-cluster"`.

### Touching `app_settings`

There is no helper yet, because no suite writes settings. Write it when you become the first
consumer, and follow `testdb_test.go:56-61` rather than widening the comment again:

```go
func testDBExclusiveSettings(t *testing.T, pool *pgxpool.Pool) {
    t.Helper()
    conn, err := pool.Acquire(t.Context())   // session-scoped: the lock lives on THIS conn
    if err != nil {
        t.Fatalf("acquire: %v", err)
    }
    if _, err := conn.Exec(t.Context(),
        `SELECT pg_advisory_lock(hashtext('app_settings'))`); err != nil {
        conn.Release()
        t.Fatalf("advisory lock: %v", err)
    }
    t.Cleanup(func() {
        _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('app_settings'))`)
        conn.Release()
    })
    // ... and snapshot the row here, restoring it in a second t.Cleanup.
}
```

Reach for `TRUNCATE app_settings` instead and you have re-introduced the race the whole
isolation contract is built to avoid — and, since `000002` seeds the singleton row at
migrate time, you have also deleted a row nothing will recreate until the database is
rebuilt.

### What the harness proves about itself

Hermetic tests run everywhere, with no database: the gating predicate, including
"runtime `KUBECENTER_DATABASE_URL` is ignored" (`testdb_test.go:291-342`); the require-flag
truthiness table (`:344-377`); owner-id sanitisation, covering subtest names, unicode, and
the length cap (`:379-407`); owner-id uniqueness over 256 draws plus the parent/subtest
prefix split (`:409-446`); and the up/down migration pairing check inside
`TestLatestEmbeddedMigrationVersion` (`:460-477`). Two gated tests run only with a database:
`TestDBHarness_MigrationsApplied` (`:484`) asserts `schema_migrations` is clean and exactly
at the latest embedded version, and `TestDBHarness_PerTestPoolsAreIndependent` (`:531`)
asserts a nested `testDB` call gets a private pool whose `Close` does not break the parent's.
Keep that split when you extend the harness — the gate's own logic should never be one of the
things the gate can skip.

---

## See Also

- `docs/solutions/backend-resilience-conventions.md` — different subject (goroutine panic
  safety, native fuzzing), but its `-list` drift guard is the same family of fix as
  convention 2 here: a CI mechanism that can report success while verifying nothing, closed
  with a hard-failing assertion. Note that doc's **Hermetic** rule ("Fuzz targets must not
  touch the network, PostgreSQL, or a real cluster") is scoped to *fuzz targets* and does
  not govern this harness — the gated tests here deliberately touch a real database, as a
  separate and sanctioned test category.
- `docs/solutions/yaml-rate-limiter-e2e-flake.md` — same CI-signal-integrity family, seen
  from the failure side: a green signal that proved nothing for two weeks.

## History

| PR | What |
|----|------|
| #414 | `backend/internal/store/testdb_test.go` — env-gated harness (`testDB`, `testOwnerID`, once-per-process migration via `store.New`), hermetic + gated test split, and the `postgres:17-alpine` service plus both env vars in `.github/workflows/ci.yml`. First test file in the package. |
| #419 | Corrected the isolation contract after review of #414: residue is harmless only where every uniqueness constraint includes a test-controlled column. Audited the current schema and the five queued persistence releases; `app_settings` is the sole exception. Recorded the rule in `testdb_test.go:36-61` and `backend/internal/store/migrations/NOTES.txt:86-115`. The review's second cited example (`eso_bulk_refresh_jobs`) did not hold. No code change — the advisory-lock helper is specified, not built, because it would have had zero consumers. |
