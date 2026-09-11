---
title: "Release F — Backup Assurance and Recovery Readiness — Implementation Plan"
parent_plan: docs/plans/2026-09-10-1315-feat-feature-expansion-plan.md
release: F
units: U32–U36 (+ U37 docs-only, Q3-gated)
migration_sequence: 000022
date: 2026-09-10
---

# Release F — Backup Assurance and Recovery Readiness — Implementation Plan

**Scope.** Requirements R3, R22, R23 (shippable) and R24, R25 (documentation only).
KTD11 (durable backup-exception state machine) is implemented. KTD12 (rehearsal) is
specified but **not** implemented. Acceptance example **AE8** is the release gate.
**AE9** is deliberately out of reach in this release — nothing here can create a Restore.

**Hard boundary.** Every unit below is *observation only*. No unit adds, extends, or
wires any code path that creates a `velero.io/v1 Restore`, a `DeleteBackupRequest`, or
any other mutating Velero object. The existing `HandleCreateRestore` in
`backend/internal/velero/handler.go` is untouched by this release.

**Units after splitting.** The master plan lists five shippable units (U32–U36). Three
of them exceed the five-file cap or fuse independently reviewable risk, so this plan
ships **nine** units plus the documentation unit: U32, U32b, U33, U34a, U34b, U34c,
U35, U36, U36b, U37. Splits and their reasons are stated at each unit.

---

## Codebase Findings

Every row below is a file read in full or in the cited line range during planning.

| File (absolute-relative to repo root) | What was read | What it constrains |
|---|---|---|
| `backend/internal/velero/types.go` | Whole file (169 LOC) | The complete existing Velero data model. See "Existing Velero model" below. |
| `backend/internal/velero/discovery.go` | Whole file | The discovery/degradation pattern the assurance collector must inherit. |
| `backend/internal/velero/handler.go` | L1–200, L985–1140, L1211–1419 (of 1419) | `Handler` struct + `NewHandler` DI path, `fetchAll`/`doFetchAll` cache + errgroup fan-out, all five CRD parsers, `computeNextRun`, `canAccess`, `auditLog`, `getImpersonatingClient`. |
| `backend/internal/velero/parsers_fuzz_test.go` | L1–40 | The `FuzzVeleroParsers` seam and `unstructuredFromFuzz` helper the new evaluator's fuzz target reuses. |
| `backend/internal/velero/discovery_test.go`, absence of `handler_test.go` | `ls backend/internal/velero/*_test.go` | The package has **no HTTP handler test today**. U35 introduces the first. |
| `backend/internal/notifications/types.go` | Whole file | `SourceVelero` exists (exact name confirmed), `Source.Valid()` allow-list, `Notification.SuppressResourceFields` (`json:"-"`), `Severity`, `ListOpts.Namespaces`. |
| `backend/internal/notifications/service.go` | L20–270, L550–600, L630–652 | `dedupWindow = 15 * time.Minute`, `Emit` lifecycle, `persistAndBroadcast`, `runDispatcher`, `dispatchToChannels`, `queueSize = 1000` drop-on-full, `suppressResourceFieldsBySource`, `sanitizeForEmailDigest`, `runRetention` (the one `recoverutil.Tick` site in the file). |
| `backend/internal/notifications/store.go` | Function list + `DedupExists` (L49–66), `ListNotifications` (L68–110), `RecentBySource` (L234–268) | The **exact** dedup key and the feed's namespace filter. |
| `backend/internal/notifications/handler.go` | `accessibleNamespaces` (L540–558) | The read-time RBAC filter idiom U35 must copy verbatim. |
| `backend/internal/externalsecrets/poller.go` | L1–230, L380–570 | **The poller being copied.** Interval constant, `Start`, `runTickWithRecover`, `tick`, `emit`, `dispatchEmits`, `fetchExternalSecrets`. |
| `backend/internal/externalsecrets/persist.go` | L60–175 | `seedFromNotifications` / `bucketFromNotification` — the *lossy* restart-recovery hack that U32/U34 replace with real durable state. |
| `backend/internal/certmanager/poller.go` | L195–235 | Second poller precedent: identical `Start` + `runTickWithRecover` shape, 60 s ticker. |
| `backend/internal/recoverutil/recoverutil.go` | Whole file | `Go` / `Tick` / `Safe` contracts and the nil-logger fallback. |
| `docs/solutions/backend-resilience-conventions.md` | L30–105 | Wrapper-selection table, the channel-send/cleanup hazard, the `wg.Done()`-outside rule, the sanctioned-exceptions list, the new-goroutine checklist. |
| `backend/internal/store/migrations/000011_create_eso_sync_history.up.sql` | Whole file | DDL house style: `CREATE TABLE IF NOT EXISTS`, `TEXT NOT NULL DEFAULT ''`, `CHECK (… IN (…))`, `TIMESTAMPTZ`, partial indexes, `COMMENT ON TABLE`. |
| `backend/internal/store/migrations/000013_…up.sql`, `000014_…up.sql` + both `.down.sql` | Whole files | **The coordination precedent** — a partial `UNIQUE` index enforcing at-most-one-active row, and the 000014 comment explaining it closes a FindActive→Insert TOCTOU. |
| `backend/internal/store/eso_bulk_jobs.go` | L1–140 + function list | Store-package idiom: `pgxpool.Pool`, caller-generated UUID, `pgconn.PgError` code `23505` → sentinel `ErrBulkJobActiveExists`, `CompleteOrphans`, `Cleanup(retentionDays)`. |
| `backend/internal/store/eso_history.go` | Function list | Second store idiom sample (`Insert`, `QueryByUID`, `LatestByUID`, `Cleanup`). |
| `backend/internal/store/migrate.go` | Whole file | `iofs` + `golang-migrate` embed; migrations run automatically at boot; dirty-state guard. |
| `backend/internal/store/migrations/NOTES.txt` | Whole file | Operator-note convention: one section per migration needing a heads-up, incl. binary-rollback constraints. |
| `backend/internal/server/routes.go` | L185–215, L648–681 | `registerVeleroRoutes` (the whole `/velero` group), the `if s.XHandler != nil` gate pattern, `middleware.RateLimit(yamlRL)`, `resources.ValidateURLParams`, `middleware.RequireAdmin` usage sites. |
| `backend/internal/server/server.go` | `Server` struct (L60–110), `Deps`, L285–305 | `VeleroHandler` already lives in both `Server` and `Deps`; assignment is `if deps.X != nil { s.X = deps.X }`. |
| `backend/cmd/kubecenter/main.go` | L143, L690–830, L875–965 | `signal.NotifyContext` root ctx; Velero DI at L705–707; post-construction field assignment `veleroHandler.NotifService = notifService` at L760; `go cmPoller.Start(ctx)` at L771; `go esoPoller.Start(ctx)` at L799; `server.Deps{…}` literal at L878–922; shutdown sequence at L935–965. |
| `backend/internal/k8s/client.go` | L100–135 | `BaseDynamicClient()` = platform ServiceAccount, `DynamicClientForUser` = impersonated. The service-identity split. |
| `backend/internal/auth/provider.go` | `User` struct + `IsAdmin` | `User.ID` is provider-qualified; `IsAdmin` reads `Roles`. |
| `backend/internal/config/config.go` | L26, L187 | `Config.ClusterID` (koanf `clusterid`) — the local cluster id already threaded into the ESO poller. |
| `backend/internal/httputil/*.go` | Function list | `WriteData`, `WriteError`, `WriteErrorWithReason`, `RequireUser`. |
| `helm/kubecenter/templates/clusterrole.yaml` | L185–203, L295–310 | ServiceAccount already holds `list, watch` on `velero.io` backups/restores/schedules/BSLs/VSLs. |
| `helm/kubecenter/values.yaml` | L4 | `replicaCount: 1  # Safe to increase with PostgreSQL (no single-writer constraint)`. |
| `frontend/islands/VeleroDashboard.tsx` | L1–200 + grep sweep (738 LOC) | Data-fetch shape, `Tab` union, `titles`/`subtitles`/`createLabels` maps, 33 inline `style={{}}` blocks vs 3 `class=` usages. |
| `frontend/routes/backup/index.tsx`, `schedules.tsx` | Whole files | The route convention: a 5-line `define.page` that renders `<VeleroDashboard initialTab=… />`. |
| `frontend/lib/constants.ts` | L465–480 | The `backup` nav section — the only place a `/backup/*` page becomes reachable. |
| `frontend/lib/api.ts` | Export list (302 LOC) | `apiGet`/`apiPost`/`apiPut`/`apiDelete` + the `notifApi`/`limitsApi` named-namespace pattern. |
| `frontend/lib/velero-types.ts` | L61 | Mirrors `Schedule.lastBackupPhase`. |
| `frontend/deno.json` | L3–17 | `check` = `deno fmt --check . && deno lint . && deno check`; `test` = `deno test -A`; `build` = `vite build`. |
| `frontend/assets/styles.css` | L26–170 | `--glass-*` tokens and the `.glass-bar` primitive. |
| `e2e/playwright.config.ts` | Whole file | `testDir: "./tests"`, project matrix (`setup` → `chromium` → `route-contract`), `webServer` env. |
| `e2e/tests/certificates.spec.ts` | Whole file | The CRD-feature spec template: probe `/api/v1/<feature>/status`, `test.skip` when not detected. |
| `e2e/tests/api-routes.spec.ts` | L1–50 | Auto-discovers every literal `/v1/...` string in `frontend/{islands,lib,routes,components}` and asserts non-404. |
| `e2e/helpers.ts` | L1–60 | `getAuthHeaders`, `e2eName`, `waitForTableLoaded`. |
| `.github/workflows/fuzz.yml` | L35 | The existing matrix row `{ pkg: ./internal/velero/, target: FuzzVeleroParsers }`. |

### (a) The exact existing Velero model vs what assurance needs

`backend/internal/velero/types.go` models **exactly this** and nothing more:

- `Backup`: `Name, Namespace, Phase, IncludedNamespaces, ExcludedNamespaces, StorageLocation, TTL, StartTime, CompletionTime, Expiration, ItemsBackedUp, TotalItems, Warnings, Errors, ScheduleName, SnapshotVolumes, Labels`.
- `Schedule`: `Name, Namespace, Phase, Schedule, Paused, LastBackup, NextRunTime, IncludedNamespaces, TTL, StorageLocation, LastBackupPhase, ValidationErrors`.
- `BackupStorageLocation`: `Name, Namespace, Provider, Bucket, Prefix, Phase, Default, LastSyncedTime, Message`.
- Phase helpers `IsFailedPhase` / `IsWarningPhase` / `IsSuccessPhase` / `IsProgressPhase`, written **for UI badge colouring** (the comment says so).

What `parseBackup` / `parseSchedule` / `parseBSL` actually populate (handler.go L1211–1349):

- `Backup.Phase` ← `status.phase`; `CompletionTime` ← `status.completionTimestamp` via `getTime`, which returns `nil` on any parse failure or missing key; `ScheduleName` ← label `velero.io/schedule-name`.
- `Schedule.Paused` ← `spec.paused`; `LastBackup` ← `status.lastBackup`; `NextRunTime` ← `computeNextRun` **only when** `Schedule != "" && !Paused && Phase == "Enabled"`.
- `BSL.Phase` ← `status.phase`; `Message` ← `status.message` (free-form controller text, may contain bucket names / ARNs / paths).

**Gaps assurance must close, all in U33:**

1. **No UID anywhere.** `grep -rn "GetUID" backend/internal/velero/` returns nothing. R1 and KTD3 require a UID on every reference so a deleted-and-recreated schedule does not inherit the prior object's evidence. U33 adds `UID string \`json:"uid"\`` to `Backup` and `Schedule`, populated from `obj.GetUID()`. Additive wire change; mobile-safe (R4).
2. **`Schedule.LastBackupPhase` is dead.** Declared in `types.go` L97 and mirrored in `frontend/lib/velero-types.ts` L61; `grep` finds **no writer anywhere in the repo**. Every response has omitted it since the field was introduced (`omitempty`). Removing it is a wire no-op. This is the Agent-Directive-1 "Step 0" item for U33 (Go side) and U36b (TS side).
3. **The phase helpers cannot be reused for backup outcome classification.** `IsSuccessPhase` returns `true` for `"Available"` and `"Enabled"` — those are *BackupStorageLocation* and *Schedule* phases, never Backup phases. `IsProgressPhase` folds `FinalizingPartiallyFailed` and `WaitingForPluginOperationsPartiallyFailed` into "progress", which is correct for a badge but wrong for a freshness clock. U33 introduces its own `BackupOutcomeFor(phase string) BackupOutcome`; the badge helpers stay untouched.
4. **`computeNextRun` can never reveal a missed run.** handler.go L1394–1419: after choosing `from = *lastRun`, it does `if from.Before(time.Now()) { from = time.Now() }`. The returned `NextRunTime` is therefore always in the future regardless of how many runs were missed. It is a display helper. U33 must not reuse it and instead needs `ExpectedRunsSince` (below).
5. **`fetchAll` is a whole-cluster ServiceAccount read with a 30 s TTL and singleflight** (handler.go L985–1104). Assurance reuses it verbatim — no second client, no second cache, no new goroutines.

### (b) The named existing poller being copied

**`backend/internal/externalsecrets/poller.go`** — specifically its `Start` (L425–447), `runTickWithRecover` (L455–457), and `tick` (L464–…) trio, cross-checked against the identical shape in **`backend/internal/certmanager/poller.go`** L206–233.

The idiom, exactly:

```go
const pollerInterval = 60 * time.Second           // eso; certmanager hardcodes 60s inline

func (p *Poller) Start(ctx context.Context) {
	p.runTickWithRecover(ctx)                     // fire immediately
	ticker := time.NewTicker(pollerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runTickWithRecover(ctx)
		}
	}
}

func (p *Poller) runTickWithRecover(ctx context.Context) {
	recoverutil.Tick(ctx, p.logger, "externalsecrets poller tick", p.tick)
}
```

- **Started from `main.go`** as a bare `go <poller>.Start(ctx)` — `go cmPoller.Start(ctx)` at L771, `go esoPoller.Start(ctx)` at L799. `ctx` is the root `signal.NotifyContext(context.Background(), SIGTERM, SIGINT)` from L143, so cancellation is the shutdown signal.
- **Interval:** 60 s for both. The ESO comment justifies it against the handler's 30 s cache TTL. Assurance uses the same 60 s for the same reason.
- **Bounded work:** ESO's `tick` early-returns when `p.disc.IsAvailable(ctx)` is false, early-returns without pruning on a failed list, and caps dispatch fan-out at `emitConcurrency = 10`.
- **`recoverutil` usage:** `recoverutil.Tick` wraps the whole tick body. The one deviation is `dispatchEmits`, which hand-rolls `defer func(){ _ = recover() }()` per emit goroutine — this is a **sanctioned exception** named explicitly in `docs/solutions/backend-resilience-conventions.md` L82–87 ("deliberately-silent per-item sibling isolation"). Assurance avoids needing the exception at all (see U34b).
- **Shutdown:** no `Stop()` method, no `WaitGroup`; the goroutine returns on `ctx.Done()` and `main.go`'s shutdown block (L947–964) does not wait for it. Assurance adds one thing the precedents lack: a best-effort lease release on the way out, on its own short-lived context because `ctx` is already cancelled.
- **Service identity:** `NewPoller(cf *k8s.ClientFactory, …)` and the comment "wires the poller against the platform service-account dynamic client … Local cluster only". No user, no impersonation, no retained token.

### (c) The named existing lease / coordination precedent

**There is no time-based lease with renewal anywhere in this repository.** The single coordination precedent is:

**`backend/internal/store/migrations/000013_create_eso_bulk_refresh_jobs.up.sql` + `000014_unique_active_bulk_jobs.up.sql`**, consumed by `backend/internal/store/eso_bulk_jobs.go`.

The pattern is a **partial `UNIQUE` index over the active subset**:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_eso_bulk_refresh_jobs_active_unique
    ON eso_bulk_refresh_jobs (cluster_id, action, scope_target)
    WHERE completed_at IS NULL;
```

with the application converting `pgconn.PgError.Code == "23505"` into the sentinel
`ErrBulkJobActiveExists` (`eso_bulk_jobs.go` L86–97), and a startup reconciler
`CompleteOrphans` (L232) that reaps rows abandoned by a crashed process.

Release F **reuses this exact idiom** for exception identity (the correctness
guarantee) and adds a lease table only as a *work-suppression* layer. This distinction
is load-bearing and is stated again in Design Decisions: correctness never depends on
the lease.

Related finding: `main.go` L820–823 comments that `CompleteOrphans` "is single-replica
safe only … The Helm chart pins this deployment to one replica", while
`helm/kubecenter/values.yaml` L4 says `replicaCount: 1  # Safe to increase with
PostgreSQL (no single-writer constraint)`. Those two statements contradict each other
today. Release F does not fix the ESO reaper, but it must not add a second
single-replica-only assumption — hence the DB-enforced identity plus lease.

### Where the master plan is wrong about this codebase

| # | Master-plan claim | Reality | Consequence |
|---|---|---|---|
| C1 | KTD11: "Reuse `notifications.SourceVelero`" | **Correct.** `SourceVelero Source = "velero"` exists in `notifications/types.go` L17 and is in `Source.Valid()`. Already emitted by `velero.Handler.InvalidateCache` (handler.go L81–97). | No change needed; but see C5. |
| C2 | KTD11: "so a 15-minute notification dedup window is not the only protection" | The window **is** 15 minutes (`dedupWindow = 15 * time.Minute`, service.go L26) — but the real defect is the **key**, not the duration. `Store.DedupExists` matches on `(source, resource_kind, resource_ns, resource_name, title)` with **no `cluster_id` and no UID**. Two clusters with an identically named schedule, or a delete-and-recreate of the same name, collide. | The plan understates the problem. Durable state is required for *identity*, not just for *duration*. Written up in Design Decisions §3. |
| C3 | U33 files: `assurance.go`, `assurance_test.go`, `types.go` — "Evaluate actual backup outcomes" | `types.go` models no UID; the phase helpers are documented as UI-badge helpers and misclassify BSL/Schedule phases as backup success; `computeNextRun` clamps to `now` and cannot express a missed run; `Schedule.LastBackupPhase` is dead. | U33 is not a thin extension. It needs its own outcome classifier and its own expected-run walker, plus a Step-0 dead-field removal and a fuzz target. Files corrected below. |
| C4 | U35 files include `backend/internal/server/server.go`; API family is `/backup/assurance/*` | `VeleroHandler` is **already** in `Server` (server.go L77) and `Deps` (L124) and assigned at L294–296 — no `server.go` edit is needed. And the live route group is `ar.Route("/velero", …)` at routes.go L650; a new top-level `/backup` group would split one feature across two prefixes and need a second nil-gate. | U35 drops `server.go`, adds routes under `/velero/assurance/*`. The **frontend page** stays at `/backup/assurance` — page path ≠ API path is already the norm (`/backup/backups` calls `/v1/velero/backups`). |
| C5 | U34 assigns the `main.go` edit and the notification-service edit to one unit; U35 owns `velero/handler.go` | The repo's DI idiom is post-construction field assignment (`veleroHandler.NotifService = notifService`, main.go L760). Wiring the assurance service in `main.go` therefore requires the `velero.Handler` field to already exist. | The handler field moves into the wiring unit (U34c) so U34c owns both `main.go` and `velero/handler.go`, and U35 needs neither. |
| C6 | U36 files: `VeleroDashboard.tsx`, `BackupAssurance.tsx`, `backup-assurance-types.ts`, `routes/backup/assurance.tsx`, e2e spec | Omits `frontend/lib/constants.ts` — the `backup` nav group at L465–479 is the only place a `/backup/*` page becomes reachable; without it the page exists but nothing links to it. Also omits any `deno task test` unit-test file, though the Verification Contract requires `deno task test` to cover "new helpers". | U36 swaps `VeleroDashboard.tsx` for `constants.ts` (keeping 5 files) and U36b carries the dashboard cross-link plus the Deno unit test. |
| C7 | Verification Contract: `npm test` from `e2e/` — "Configured Playwright projects collect and pass new specs" | `playwright.config.ts` sets `testDir: "./tests"`, but `e2e/velero.spec.ts`, `e2e/namespace-limits.spec.ts` and `e2e/flux-notifications.spec.ts` sit at the `e2e/` root and are **never collected**. | The new spec must go in `e2e/tests/` (the master plan's path is right). Flagged as a pre-existing repo defect in Risks; not fixed here. |
| C8 | Delivery Order row F entry condition: "Explicit backup freshness policies" | There is no policy concept in the codebase at all — no table, no type, no endpoint. | Policies are created from zero in U32/U35, with an explicit "no policies configured" empty state rather than an implied default that would silently start alerting on install. |

---

## Design Decisions

### 1. Migration `000022` — exact DDL

One migration creates all four tables so the schema is atomic; the Go accessors are
split across U32 and U32b. **Sequence `000022` is reserved for this track; 000018–000021
belong to other tracks and must not be used here.**

`backend/internal/store/migrations/000022_create_backup_assurance.up.sql`:

```sql
-- Release F (KTD11). Durable backup-exception state machine. Four tables:
--   policies    — operator-declared freshness expectations (admin-managed)
--   exceptions  — the state machine; at most one OPEN row per condition identity
--   deliveries  — durable notification intents; at most one per (exception, transition)
--   lease       — collector work-suppression across replicas (NOT a correctness guard)
-- Style follows 000011/000013: IF NOT EXISTS, TEXT NOT NULL DEFAULT '', CHECK enums,
-- TIMESTAMPTZ, partial indexes, COMMENT ON TABLE.

CREATE TABLE IF NOT EXISTS backup_assurance_policies (
    id                UUID PRIMARY KEY,
    cluster_id        TEXT NOT NULL DEFAULT 'local',
    scope_kind        TEXT NOT NULL CHECK (scope_kind IN ('schedule', 'namespace', 'cluster')),
    scope_namespace   TEXT NOT NULL DEFAULT '',
    scope_name        TEXT NOT NULL DEFAULT '',
    max_age_seconds   INTEGER NOT NULL CHECK (max_age_seconds >= 300),
    grace_seconds     INTEGER NOT NULL DEFAULT 3600 CHECK (grace_seconds >= 0),
    treat_partial_as  TEXT NOT NULL DEFAULT 'failure' CHECK (treat_partial_as IN ('success', 'failure')),
    alert_on_paused   BOOLEAN NOT NULL DEFAULT TRUE,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_by        TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by        TEXT NOT NULL DEFAULT '',
    updated_at        TIMESTAMPTZ,
    revision          BIGINT NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_backup_assurance_policies_scope
    ON backup_assurance_policies (cluster_id, scope_kind, scope_namespace, scope_name);

CREATE TABLE IF NOT EXISTS backup_assurance_exceptions (
    id                 UUID PRIMARY KEY,
    cluster_id         TEXT NOT NULL DEFAULT 'local',
    policy_id          UUID NOT NULL REFERENCES backup_assurance_policies(id) ON DELETE CASCADE,
    subject_kind       TEXT NOT NULL CHECK (subject_kind IN ('schedule', 'namespace', 'cluster')),
    subject_namespace  TEXT NOT NULL DEFAULT '',
    subject_name       TEXT NOT NULL DEFAULT '',
    subject_uid        TEXT NOT NULL DEFAULT '',
    condition          TEXT NOT NULL CHECK (condition IN (
                           'overdue', 'failed', 'partially_failed', 'paused',
                           'never_run', 'location_unavailable', 'collection_unknown')),
    state              TEXT NOT NULL CHECK (state IN ('open', 'resolved')),
    severity           TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    opened_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_observed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at        TIMESTAMPTZ,
    observation_count  BIGINT NOT NULL DEFAULT 1,
    last_success_at    TIMESTAMPTZ,
    detail             JSONB NOT NULL DEFAULT '{}'
);

-- THE correctness guarantee. Mirrors 000014's partial-UNIQUE idiom: two replicas
-- cannot both open the same condition; the loser gets 23505 and downgrades to an
-- observation. Re-occurrence after resolution inserts a NEW row because the index
-- only covers state = 'open'.
CREATE UNIQUE INDEX IF NOT EXISTS idx_backup_assurance_exceptions_open_unique
    ON backup_assurance_exceptions
       (cluster_id, subject_kind, subject_namespace, subject_name, subject_uid, condition)
    WHERE state = 'open';

CREATE INDEX IF NOT EXISTS idx_backup_assurance_exceptions_read
    ON backup_assurance_exceptions (cluster_id, state, subject_namespace, opened_at DESC);

CREATE TABLE IF NOT EXISTS backup_assurance_deliveries (
    id            UUID PRIMARY KEY,
    exception_id  UUID NOT NULL REFERENCES backup_assurance_exceptions(id) ON DELETE CASCADE,
    transition    TEXT NOT NULL CHECK (transition IN ('opened', 'resolved')),
    state         TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at  TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT ''
);

-- One delivery intent per (exception, transition), forever. A restart between the
-- state commit and the notification leaves exactly one pending row for the next tick.
CREATE UNIQUE INDEX IF NOT EXISTS idx_backup_assurance_deliveries_once
    ON backup_assurance_deliveries (exception_id, transition);

CREATE INDEX IF NOT EXISTS idx_backup_assurance_deliveries_pending
    ON backup_assurance_deliveries (created_at)
    WHERE state = 'pending';

CREATE TABLE IF NOT EXISTS backup_assurance_collector_lease (
    cluster_id   TEXT PRIMARY KEY,
    holder       TEXT NOT NULL,
    acquired_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    renewed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,
    fence        BIGINT NOT NULL DEFAULT 1
);

COMMENT ON TABLE backup_assurance_policies IS
    'Release F operator-declared backup freshness expectations. No rows means no evaluation — install does not imply a default policy.';
COMMENT ON TABLE backup_assurance_exceptions IS
    'Release F exception state machine (KTD11). Identity = (cluster_id, subject_kind, subject_namespace, subject_name, subject_uid, condition). At most one OPEN row per identity, enforced by a partial UNIQUE index (000014 precedent). detail JSONB is PRIVILEGED: it may carry controller messages and storage locations; filter before returning to a user.';
COMMENT ON TABLE backup_assurance_deliveries IS
    'Release F durable notification intents. UNIQUE (exception_id, transition) makes each state transition notifiable exactly once across process restarts; notifications.dedupWindow is a second layer against retried sends, not the primary guard.';
COMMENT ON TABLE backup_assurance_collector_lease IS
    'Release F collector work suppression. Advisory only: correctness comes from the exceptions/deliveries UNIQUE indexes. A lost or expired lease costs duplicate API reads, never a duplicate exception.';
```

`000022_create_backup_assurance.down.sql`:

```sql
DROP TABLE IF EXISTS backup_assurance_collector_lease;
DROP TABLE IF EXISTS backup_assurance_deliveries;
DROP TABLE IF EXISTS backup_assurance_exceptions;
DROP TABLE IF EXISTS backup_assurance_policies;
```

`NOTES.txt` gains a `000022_create_backup_assurance` section stating: (i) the down
migration destroys open-exception state, so the next collector start re-opens every
still-true condition and re-notifies each one exactly once; (ii) it touches no Velero
CRD and no `nc_*` table, so the Notification Center and the existing Velero pages are
unaffected by either direction; (iii) `backup_assurance_deliveries` and
`backup_assurance_exceptions` cascade from `backup_assurance_policies`, so deleting a
policy through the API intentionally discards its exception history.

### 2. Scope + UID keying

- **Policy scope** is `(cluster_id, scope_kind, scope_namespace, scope_name)`, unique.
  `scope_kind = 'schedule'` sets both namespace and name; `'namespace'` sets namespace
  only; `'cluster'` sets neither.
- **Exception subject** is `(cluster_id, subject_kind, subject_namespace, subject_name,
  subject_uid)`. `subject_uid` is `metadata.uid` of the Velero `Schedule` for
  schedule-scope subjects and `''` for namespace- and cluster-scope subjects (there is
  no Kubernetes object to bind to).
- A schedule deleted and recreated under the same name produces a **different**
  `subject_uid`, so the old open exception is never reused. It is resolved by the
  reconciler as `subject disappeared` (see §4) and a fresh exception opens against the
  new UID. This is R1 / KTD3 enforced by the index, not by convention.
- Because `notifications.Store.DedupExists` cannot see the cluster id or the UID
  (finding C2), the exception row — not the notification — is the identity of record.

### 3. Why DB-durable condition state is needed *in addition to* the 15-minute dedup window

Four independent reasons, each grounded in a read line:

1. **Duration mismatch.** `dedupWindow = 15 * time.Minute` (service.go L26). An overdue
   backup stays overdue for hours or days. With the window as the only guard, the same
   exception re-emits every 15 minutes indefinitely. R23 requires exactly one open.
2. **Identity unsafety.** `DedupExists` (store.go L49–66) matches
   `source, resource_kind, resource_ns, resource_name, title` only. No `cluster_id`,
   no UID. A same-named schedule in a second cluster, or a recreated schedule, is
   silently deduped against an unrelated prior event.
3. **No delivery signal.** `Emit` returns nothing (service.go L145). On a full queue it
   logs `"notification dispatch queue full, dropping external dispatch"` and drops the
   external leg (L172–176). The caller cannot distinguish delivered from dropped, so
   "delivery failure is retryable without regenerating conditions" (U34 scenario) is
   unimplementable without a separate intent record.
4. **Resolution needs prior state.** Emitting a *recovery* requires knowing an exception
   was open. `nc_notifications` has no state column; the ESO poller works around this by
   decoding a bucket back out of the notification *title* string
   (`bucketFromNotification`, persist.go L118–147) inside a 
   `recoverySeedWindow`. That is explicitly lossy — a title rename or a window overrun
   silently swallows a recovery. Release F must not repeat it.

The dedup window is retained as a **second layer**: it protects against a delivery that
was actually sent but whose `MarkDelivered` did not commit, which would otherwise
re-send on the next tick.

### 4. The exception state machine

**What a "condition" is.** A condition is the tuple
`(cluster_id, subject_kind, subject_namespace, subject_name, subject_uid, condition)`.
Two observations are the *same* exception iff every element matches. The `policy_id`
is recorded on the row but is deliberately **not** part of the identity: editing a
policy's thresholds must not orphan an open exception and open a duplicate.

**States.** `open`, `resolved`. `resolved` is terminal.

**Transitions.**

| From | To | Trigger | DB operation | Notifies? |
|---|---|---|---|---|
| (none) | `open` | Evaluator emits a `Finding` whose identity has no `open` row | `INSERT … ON CONFLICT DO NOTHING` against `idx_..._open_unique`, in a tx that also inserts a `deliveries` row with `transition='opened', state='pending'` | Yes, once |
| (none) | `open` (lost race) | Same, but another replica or an earlier tick already inserted | `ON CONFLICT DO NOTHING` affects 0 rows → fall through to *observe* | No |
| `open` | `open` (observe) | Finding repeats on a later tick | `UPDATE … SET last_observed_at = NOW(), observation_count = observation_count + 1, severity = $s, last_success_at = $l, detail = $d WHERE id = $id AND state = 'open'` | No |
| `open` | `resolved` | Condition no longer produced by the evaluator for a subject that is still observable, **and** collection was `ok` | `UPDATE … SET state='resolved', resolved_at=NOW() WHERE id=$id AND state='open' RETURNING id`, same tx inserts `deliveries(transition='resolved', state='pending')` | Yes, once |
| `open` | `resolved` | Subject disappeared from the inventory (schedule deleted, or UID changed) **and** collection was `ok` | Same UPDATE; `detail` records `resolutionReason: "subject_absent"` | Yes, once |
| `open` | `open` | Collection was **not** `ok` | Nothing. Open exceptions are never resolved on a failed or degraded collection. | No |

**Deduplication across process restarts.** There is nothing to restore. The `open` row
*is* the memory. Unlike the ESO poller, the assurance collector holds **no** in-memory
`prevBucket` map and needs **no** `seedFromNotifications` equivalent. On the first tick
after a restart it loads `ListOpenExceptions(clusterID)` and reconciles it against the
findings, exactly as it does on every other tick. Startup is not a special case.

**Delivery.** The state transition and its delivery intent commit in **one
transaction**. The notification is sent in a separate, later step
(`drainDeliveries`). Therefore:

- A crash after commit and before send leaves one `pending` row; the next tick sends it.
- A crash after send and before `MarkDelivered` leaves one `pending` row; the next tick
  re-sends, and `notifications.DedupExists` suppresses the duplicate (this is the
  second layer earning its keep).
- Delivery is at-least-once per transition and never more than once per transition
  *state change*. `UNIQUE (exception_id, transition)` makes a second `opened` intent for
  the same exception impossible.
- After `assuranceMaxDeliveryAttempts = 5` failures the row moves to `state='failed'`
  with `last_error` set, is surfaced on `GET /velero/assurance/status` as
  `deliveryBacklog`, and is **not** retried — it never regenerates the condition.

**AE8, answered from this section alone.**

> *"A backup exceeds its configured freshness limit while no browser is open. Exactly
> one exception is opened; a poller restart does not duplicate it, and a fresh
> successful backup resolves it."*

1. No browser is open: the collector runs from `go assurance.Start(ctx)` in `main.go`,
   on a 60 s ticker, against `k8sClient.BaseDynamicClient()`. Nothing about it depends
   on an HTTP request.
2. Tick *n*: `Evaluate` returns `Finding{Subject: {schedule, velero, daily, uid-A},
   Condition: overdue}`. No `open` row exists. The transaction inserts the exception and
   one `deliveries(opened, pending)` row. `drainDeliveries` emits one
   `notifications.Notification{Source: SourceVelero, Severity: warning}` and marks it
   delivered.
3. Ticks *n+1…n+k*: the same finding hits `ON CONFLICT DO NOTHING`, affects 0 rows,
   and downgrades to an observation `UPDATE`. `observation_count` climbs; nothing is
   emitted. **Exactly one exception.**
4. Restart at any point: the process comes back, `ListOpenExceptions` returns that row,
   and step 3 repeats. No seeding, no title decoding, no window. **No duplicate.**
5. A second replica racing at step 2 loses the unique index and also lands on step 3.
   **No duplicate**, with or without the lease.
6. A fresh `Completed` backup lands. `Evaluate` no longer emits `overdue` for that
   subject, collection is `ok`, so the reconciler resolves the row and inserts
   `deliveries(resolved, pending)`; the drain emits one resolution notification.
   **Resolved.**
7. If the same schedule later goes overdue again, the partial unique index does not
   cover the `resolved` row, so a brand-new exception opens and notifies once.

### 5. Freshness evaluation rules (U33)

`Evaluate` is a **pure function**: `(Observation, []Policy, now time.Time) → []Finding`.
No clock read, no I/O, no logging. Determinism is the exit criterion.

```go
type Collection string
const (
    CollectionOK       Collection = "ok"       // every list returned
    CollectionDegraded Collection = "degraded" // some lists returned, at least one failed
    CollectionFailed   Collection = "failed"   // discovery says absent, or fetchAll errored
)
```

**The hard rule, stated once and enforced structurally.**

> When `Observation.Collection != CollectionOK`, no subject may be reported as
> `never_run`, `overdue`, `failed`, `partially_failed`, `paused`, or
> `location_unavailable`. "No recent successful backup was observed" while collection
> failed is **`collection_unknown`**, never "no backups exist".

This is enforced by making `Collection` a required field of `Observation` (not a
pointer, not a bool) and by `Evaluate` branching on it **before** any per-subject logic:
on `CollectionFailed` it returns exactly one cluster-scoped
`Finding{Condition: collection_unknown}` and nothing else — deliberately one exception,
not N, so a Velero outage does not produce an alert storm. On `CollectionDegraded` it
emits `collection_unknown` for the subjects whose evidence is missing and evaluates the
rest normally.

**Backup outcome classification.** New function, not the badge helpers:

```go
func BackupOutcomeFor(phase string) BackupOutcome
```

| Velero `status.phase` | `BackupOutcome` | Effect on the freshness clock |
|---|---|---|
| `Completed` | `success` | Resets the clock, **if** a usable timestamp exists |
| `PartiallyFailed` | `partial` | Per policy `treat_partial_as`; default `failure` |
| `Failed`, `FailedValidation` | `failure` | Does not reset the clock; opens `failed` |
| `New`, `Queued`, `ReadyToStart`, `InProgress`, `WaitingForPluginOperations`, `WaitingForPluginOperationsPartiallyFailed`, `Finalizing`, `FinalizingPartiallyFailed` | `in_flight` | Neither resets nor opens anything. An in-flight backup must never suppress an overdue exception, and must never be counted as a success. |
| `Deleting`, `""`, anything else | `unknown` | Contributes to `collection_unknown` for that subject only if it is the sole evidence |

**Completed vs PartiallyFailed vs Failed — the three required distinctions.**

- `Completed` is the only phase that can reset the age clock, and only when a timestamp
  is resolvable. `parseBackup` sets `CompletionTime` via `getTime`, which returns `nil`
  on a missing or unparseable `status.completionTimestamp`. The evaluator therefore
  uses `completionTime ?? startTime`; if **both** are nil the backup is reclassified
  `unknown`, not `success`. A "successful backup with no time" is not evidence of
  freshness.
- `PartiallyFailed` is **never** silently folded into success. With
  `treat_partial_as = 'failure'` (default) it opens `partially_failed` and does not
  reset the clock. With `'success'` it resets the clock but the readiness surface still
  reports `lastOutcome: partial` — the UI must render "last success was partial", never
  a bare green count.
- `Failed` / `FailedValidation` open `failed` at `critical` severity and never touch the
  clock. `failed` and `overdue` are distinct conditions and can be open simultaneously
  for one subject (a schedule that both failed last night and has now aged past
  `max_age`); each notifies once.

**Paused schedules.** `Schedule.Paused` comes from `spec.paused`. When true:

- The **overdue clock is suspended** for that subject. Calling a deliberately paused
  schedule "overdue" is a false statement, so `overdue` is not emitted and any open
  `overdue` exception for the subject is *not* auto-resolved (pausing does not fix
  anything) — it is left open with `detail.suppressedBy = "paused"`.
- A `paused` condition is emitted at `warning` when `policy.alert_on_paused` is true
  (default). It resolves when the schedule is unpaused.
- `parseSchedule` already refuses to compute `NextRunTime` while paused
  (`!schedule.Paused && schedule.Phase == "Enabled"`); the evaluator reuses the same
  gate rather than inventing a second one.

**Never-run schedules.** A schedule whose `status.lastBackup` is nil **and** for which
no backup carries label `velero.io/schedule-name == <name>` yields `never_run`, not
`overdue`. `parseBackup` already lifts that label into `Backup.ScheduleName`. The two
conditions are separate rows with separate copy: "has never produced a backup" vs
"last successful backup is older than the policy allows". Only emitted when collection
is `ok`.

**Missing / unhealthy storage locations.** For a schedule, the target BSL name is
`spec.template.storageLocation` (already lifted into `Schedule.StorageLocation`); for a
backup it is `spec.storageLocation`. If that name is non-empty and either (a) does not
appear in `Observation.Locations.BackupStorageLocations`, or (b) appears with
`Phase != "Available"`, emit `location_unavailable` at `critical`. An empty
`storageLocation` means "Velero's default", which resolves to the BSL with
`Default == true`; if no BSL is marked default, that is also `location_unavailable`.
`BSL.Message` may contain bucket names, paths and ARNs; it is stored in the privileged
`detail` JSONB and is **never** interpolated into a notification message (§7).

**Unavailable discovery.** `Discoverer.Status(ctx).Detected == false` ⇒
`Collection = CollectionFailed` ⇒ one cluster-scoped `collection_unknown`. The
`Discoverer` re-probes on a 5-minute staleness window (`staleDuration`, discovery.go
L15) and returns a zero-valued `VeleroStatus{Detected: false}` when
`ServerResourcesForGroupVersion("velero.io/v1")` errors — so "Velero uninstalled",
"CRDs not yet registered" and "API server unreachable" all present identically. The
plan does **not** pretend to distinguish them: the condition is `collection_unknown`
and the copy says "Velero could not be observed", not "Velero is not installed".

**Cron and calendar schedules — what is and is not computable.**

The naive approach — call `sched.Next()` twice and treat the delta as an interval — is
wrong for `0 3 * * 1` (weekly), `@monthly`, `0 0 29 2 *` (leap day), and every
DOM/DOW-restricted expression, because consecutive gaps are not constant. This plan
does not do it.

The question the evaluator actually asks is: **"has a successful backup been observed
since the most recent expected run?"** That needs the *previous* expected run, and
`robfig/cron/v3` (already a dependency, imported at handler.go L16) exposes only
`Next(time.Time)`. So:

```go
// ExpectedRunsSince walks the cron schedule forward from anchor and returns the
// latest expected fire time at or before now. known=false means the expression is
// unparseable, the walk exceeded maxSteps, or the anchor is older than the lookback
// cap — in which case the caller MUST fall back to the plain max_age rule and the UI
// MUST say the next expected run is not computable.
func ExpectedRunsSince(cronExpr string, anchor, now time.Time, maxSteps int) (last time.Time, known bool)
```

- `anchor = max(lastSuccessAt, scheduleCreationTimestamp, now - lookbackCap)` with
  `lookbackCap = 35 * 24h` (covers monthly expressions with margin).
- Iterate `t = sched.Next(t)` while `t <= now`, retaining the last such `t`, with
  `maxSteps = 2000`. Exceeding the cap returns `known = false` rather than looping.
- Parse with the same two-step fallback `computeNextRun` uses
  (`cron.NewParser(Minute|Hour|Dom|Month|Dow).Parse`, then `cron.ParseStandard`), so
  descriptors (`@daily`, `@every 1h`) and 5-field expressions both work. An expression
  that fails both parsers yields `known = false` **and** a `detail.cronParseError`.

**When `known == true`**, the subject is overdue if
`lastSuccessAt < lastExpected && now - lastExpected > grace`.
**When `known == false`**, the subject is overdue if
`now - lastSuccessAt > max_age + grace`, and the API response carries
`expectedRunKnown: false` so the UI can say so.

**DST and calendar boundaries — stated honestly.** `cron.NewParser(...).Parse` and
`cron.ParseStandard` produce a `SpecSchedule` whose `Location` defaults to the
**k8sCenter process's** `time.Local`, while the expression is actually evaluated by the
**Velero controller's** clock and timezone. Those can differ, and a `0 3 * * *` job
shifts by exactly one hour across each DST transition in a wall-clock zone. This plan
does not attempt to reconcile the two clocks. Instead:

1. A `CRON_TZ=` / `TZ=` prefix, if present in the expression, is honoured by
   `cron.ParseStandard` and used as-is.
2. `grace_seconds` has a **default of 3600** (one hour) precisely so a DST shift can
   never manufacture a false `overdue`. One hour of grace is negligible against any
   sane `max_age` (minimum 300 s by CHECK, realistically ≥ 24 h) but exactly absorbs
   the largest DST discontinuity.
3. The computed `expectedRunAt` is labelled **advisory** in the API and the UI. It is
   never the sole basis for an exception — `max_age` always applies as a floor.
4. Ambiguous local times (the repeated hour in a fall-back transition) are not
   special-cased; the one-hour grace covers both the skipped and the repeated hour.

**Cluster and namespace scope.** A `namespace`-scope policy evaluates the newest
successful backup whose `IncludedNamespaces` contains the namespace (or is empty, which
in Velero means all namespaces) and whose `ExcludedNamespaces` does not. A
`cluster`-scope policy evaluates the newest successful backup with an empty
`IncludedNamespaces`. Neither scope has a cron expression, so both use the plain
`max_age + grace` rule with `expectedRunKnown: false`.

### 6. Service identity for background collection

- **Which identity.** The platform ServiceAccount, via
  `h.K8sClient.BaseDynamicClient()` — reached indirectly, because the collector calls
  the handler's existing `fetchAll(ctx)` (handler.go L985) rather than building its own
  client. `doFetchAll` already uses `BaseDynamicClient` for all five lists.
- **Why it is not a retained user token.** There is no user on this code path. No
  `auth.User`, no `DynamicClientForUser`, no impersonation headers, no stored bearer,
  no session. This satisfies actor A5 exactly as the ESO and cert-manager pollers do.
- **RBAC already suffices.** `helm/kubecenter/templates/clusterrole.yaml` L194–203
  already grants `list, watch` on `velero.io` `backups`, `restores`, `schedules`,
  `backupstoragelocations`, `volumesnapshotlocations`. **Release F adds no RBAC rules
  and no new API groups.** If a future unit needed a new verb, that would itself be a
  finding worth escalating.
- **The consequence, and where it is paid.** Collected observations are
  cluster-privileged: they cover namespaces the reading user may not see. Filtering
  therefore happens at **read time and at dispatch time**, never at collection time:

  **Read time** (`GET /velero/assurance/exceptions`, U35):
  1. `httputil.RequireUser`.
  2. `auth.IsAdmin(user)` → no namespace filter (mirrors
     `notifications.Handler.accessibleNamespaces`, handler.go L542–557).
  3. Otherwise `h.RBACChecker.GetSummary(ctx, user, nil)` → the namespace set.
  4. Additionally `h.canAccess(ctx, user, "list", "schedules", ns)` — the existing
     velero SSAR gate (handler.go L117–130) — so a user with namespace visibility but
     no Velero access still gets nothing.
  5. Cluster-scope exceptions (`subject_kind = 'cluster'`) are **admin-only**; they
     describe the whole installation.
  6. **Counts are computed after filtering, never before.** A pre-filter count is an
     oracle for the existence of resources in namespaces the user cannot see.
  7. The privileged `detail` JSONB is **never** returned wholesale. A fixed
     allow-list of keys (`lastOutcome`, `lastSuccessAt`, `expectedRunAt`,
     `expectedRunKnown`, `observationCount`, `cronParseError`) is projected. Keys such
     as `bslMessage`, `bucket`, `storageLocation` and `failureReason` are admin-only.

  **Dispatch time** (U34a/U34b): the notification carries
  `ResourceKind: "backup"` (a fixed string, never `"backup.overdue"` — the ESO comment
  at poller.go L384–388 explains why the state suffix would itself be a cross-tenant
  leak), `ResourceNS`, `ResourceName`, and `SuppressResourceFields: true`. The in-app
  feed filters by `resource_ns = ANY($namespaces)` in `Store.ListNotifications`
  (store.go L82–86). Slack and webhook payloads drop the resource fields via the
  existing `SuppressResourceFields` path. The **email digest** reads
  `suppressResourceFieldsBySource` directly (service.go L556–572) because the flag is
  `json:"-"` and not persisted — so U34a must add `SourceVelero: true` to that map or
  the digest leaks what Slack does not.

### 7. The honesty rule

> **Backup freshness is not demonstrated recoverability.**

Enforced in five concrete ways, all testable:

1. `/backup/assurance` renders a persistent, non-dismissible banner:
   *"Backup assurance observes what Velero reported. It does not verify that a restore
   would succeed. No restore has been attempted."* The e2e spec asserts this text is
   present on first paint.
2. **No aggregate score.** No percentage, no shield, no "protected" badge, no
   green/amber/red rollup of the whole cluster. Counts are per-condition and always
   labelled with the condition name.
3. **Six-state vocabulary, used verbatim** (R3): `ok`, `stale`, `empty`, `unknown`,
   `forbidden`, `unavailable`. The string `"unknown"` appears whenever collection was
   not `ok`. The UI must never render "no backups exist" for an `unknown`; the copy is
   *"Velero could not be observed — backup state is unknown"*.
4. `PartiallyFailed` gets its own chip and is excluded from any success count, even
   under `treat_partial_as = 'success'` (where the chip reads "last success was
   partial").
5. The restore link is labelled **"Start a restore (manual, unverified)"** and lives in
   a separate "Actions" area, never inside an exception card as a remediation CTA. The
   assurance surface offers no button that mutates a cluster.

---

## U32. Backup assurance schema and policy/exception store

**Branch:** `feat/u32-backup-assurance-schema`
**PR title:** `feat(store): backup assurance schema (000022) and policy/exception store`
**Covers:** R22, R23; KTD11. **Depends on:** none.

**Files (5):**

1. `backend/internal/store/migrations/000022_create_backup_assurance.up.sql` — new
2. `backend/internal/store/migrations/000022_create_backup_assurance.down.sql` — new
3. `backend/internal/store/migrations/NOTES.txt` — modified *(added vs the master plan; required by repo convention, see NOTES.txt's own preamble)*
4. `backend/internal/store/backup_assurance.go` — new
5. `backend/internal/store/backup_assurance_test.go` — new

**Steps.**

1. Write the two migration files exactly as in Design Decisions §1. Do not renumber:
   000018–000021 belong to other tracks.
2. Append the `000022_create_backup_assurance` section to `NOTES.txt` covering the three
   points listed in §1.
3. Create `backend/internal/store/backup_assurance.go`, package `store`, importing
   `github.com/google/uuid`, `github.com/jackc/pgx/v5`,
   `github.com/jackc/pgx/v5/pgconn`, `github.com/jackc/pgx/v5/pgxpool` — the same set
   `eso_bulk_jobs.go` uses.

```go
type AssuranceScopeKind string
const (
	ScopeSchedule  AssuranceScopeKind = "schedule"
	ScopeNamespace AssuranceScopeKind = "namespace"
	ScopeCluster   AssuranceScopeKind = "cluster"
)

type AssuranceCondition string
const (
	ConditionOverdue             AssuranceCondition = "overdue"
	ConditionFailed              AssuranceCondition = "failed"
	ConditionPartiallyFailed     AssuranceCondition = "partially_failed"
	ConditionPaused              AssuranceCondition = "paused"
	ConditionNeverRun            AssuranceCondition = "never_run"
	ConditionLocationUnavailable AssuranceCondition = "location_unavailable"
	ConditionCollectionUnknown   AssuranceCondition = "collection_unknown"
)

func (c AssuranceCondition) Valid() bool   // mirrors notifications.Source.Valid()
func (k AssuranceScopeKind) Valid() bool

type BackupAssurancePolicy struct {
	ID             uuid.UUID
	ClusterID      string
	ScopeKind      AssuranceScopeKind
	ScopeNamespace string
	ScopeName      string
	MaxAge         time.Duration
	Grace          time.Duration
	TreatPartialAs string // "success" | "failure"
	AlertOnPaused  bool
	Enabled        bool
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedBy      string
	UpdatedAt      *time.Time
	Revision       int64
}

type BackupAssuranceException struct {
	ID               uuid.UUID
	ClusterID        string
	PolicyID         uuid.UUID
	SubjectKind      AssuranceScopeKind
	SubjectNamespace string
	SubjectName      string
	SubjectUID       string
	Condition        AssuranceCondition
	State            string // "open" | "resolved"
	Severity         string // "info" | "warning" | "critical"
	OpenedAt         time.Time
	LastObservedAt   time.Time
	ResolvedAt       *time.Time
	ObservationCount int64
	LastSuccessAt    *time.Time
	Detail           []byte // raw JSONB; PRIVILEGED, project before returning
}

type BackupAssuranceStore struct{ pool *pgxpool.Pool }
func NewBackupAssuranceStore(pool *pgxpool.Pool) *BackupAssuranceStore

var (
	ErrAssurancePolicyExists     = errors.New("backup assurance policy already exists for scope")
	ErrAssuranceRevisionConflict = errors.New("backup assurance policy revision conflict")
	ErrAssurancePolicyNotFound   = errors.New("backup assurance policy not found")
)

func (s *BackupAssuranceStore) InsertPolicy(ctx context.Context, p BackupAssurancePolicy) error
func (s *BackupAssuranceStore) UpdatePolicy(ctx context.Context, p BackupAssurancePolicy) error
func (s *BackupAssuranceStore) DeletePolicy(ctx context.Context, clusterID string, id uuid.UUID) error
func (s *BackupAssuranceStore) GetPolicy(ctx context.Context, clusterID string, id uuid.UUID) (*BackupAssurancePolicy, error)
func (s *BackupAssuranceStore) ListPolicies(ctx context.Context, clusterID string) ([]BackupAssurancePolicy, error)

func (s *BackupAssuranceStore) ListOpenExceptions(ctx context.Context, clusterID string) ([]BackupAssuranceException, error)
func (s *BackupAssuranceStore) ListExceptions(ctx context.Context, clusterID string, q AssuranceExceptionQuery) ([]BackupAssuranceException, int, error)
func (s *BackupAssuranceStore) ObserveException(ctx context.Context, id uuid.UUID, observedAt time.Time, severity string, lastSuccessAt *time.Time, detail []byte) error
func (s *BackupAssuranceStore) PruneResolved(ctx context.Context, retention time.Duration) (int64, error)

// WithTx runs fn inside a transaction, committing on nil error. U32b's
// OpenExceptionAndEnqueue / ResolveExceptionAndEnqueue build on it.
func (s *BackupAssuranceStore) WithTx(ctx context.Context, fn func(pgx.Tx) error) error
```

4. `InsertPolicy` maps `pgErr.Code == "23505"` to `ErrAssurancePolicyExists`, exactly
   as `ESOBulkJobStore.Insert` does (eso_bulk_jobs.go L86–97).
5. `UpdatePolicy` uses optimistic revisions:
   `UPDATE … SET …, revision = revision + 1, updated_at = NOW() WHERE id = $1 AND cluster_id = $2 AND revision = $3`;
   zero rows affected → `ErrAssuranceRevisionConflict` (disambiguated from
   `ErrAssurancePolicyNotFound` by a follow-up existence check inside the same tx).
6. `PruneResolved` deletes `state = 'resolved' AND resolved_at < NOW() - $1::interval`,
   default 90 days to match `notifications.runRetention` (service.go L644).
7. `Detail` is `[]byte` (raw JSONB) at the store layer so the store never has to know
   which keys are privileged; projection is the handler's job (§6).

**Named tests — `backend/internal/store/backup_assurance_test.go`** (real PostgreSQL,
per the Verification Contract's "Backend DB tests need real transactional/migration
coverage where mocks cannot establish isolation"; skipped with `t.Skip` when
`KUBECENTER_TEST_DATABASE_URL` is unset, matching the existing store tests):

| Test | Maps to |
|---|---|
| `TestBackupAssurance_MigrationUpDownPreservesExistingTables` | Master-plan "rollback preserves pre-existing Velero/notification tables" + Verification Contract "Real PostgreSQL migration round trips" — seeds `nc_notifications` and `eso_sync_history` rows, runs 000022 up then down, asserts both survive |
| `TestBackupAssurance_MigrationAppliesToPopulatedDatabase` | Same row; applies 000022 to a DB already at 000021 with data |
| `TestPolicy_DuplicateScopeReturnsErrAssurancePolicyExists` | Unique-index behaviour |
| `TestPolicy_StaleRevisionConflicts` | Cross-cutting: optimistic concurrency |
| `TestPolicy_RejectsMaxAgeBelowFloor` | CHECK `max_age_seconds >= 300` surfaces as an error, not a panic |
| `TestPolicy_ScopedToClusterID` | Cross-cutting: two clusters, same scope name, both insert |
| `TestException_ObserveIncrementsCountWithoutReopening` | `open → open` |
| `TestException_ListOpenExcludesResolved` | Partial-index semantics |
| `TestException_PruneResolvedRespectsRetention` | Cross-cutting: retention |
| `TestException_CascadeOnPolicyDelete` | FK `ON DELETE CASCADE` |
| `TestBackupAssuranceStore_NilPoolIsNotConstructed` | Cross-cutting: unavailable DB — asserts callers must nil-check, `NewBackupAssuranceStore` is never called with nil in `main.go` |

**Verification (repo-wide, Agent Directive 4):**

```
cd backend && go vet ./... && go test ./...
```

**Exit criteria / done means:**

- [ ] `000022` up and down both apply against a populated DB; `nc_*`, `eso_*` and
      `clusters` rows are byte-identical after the round trip.
- [ ] No migration file numbered 000018–000021 is added or modified.
- [ ] `NOTES.txt` carries the 000022 section including the re-notify-on-rollback warning.
- [ ] All eleven named tests pass; `go vet ./...` clean repo-wide.
- [ ] No production code outside `backend/internal/store/` is touched.

---

## U32b. Durable delivery intents and the collector lease

**Split rationale:** U32 already sits at the five-file cap once `NOTES.txt` is included,
and delivery/lease semantics are an independently reviewable concern (they are the
cross-replica correctness argument). The **migration stays in U32** so `000022` remains
a single atomic sequence, as mandated.

**Branch:** `feat/u32b-backup-assurance-delivery-lease`
**PR title:** `feat(store): durable backup-assurance delivery intents and collector lease`
**Covers:** R23; KTD11. **Depends on:** U32.

**Files (2):**

1. `backend/internal/store/backup_assurance_delivery.go` — new
2. `backend/internal/store/backup_assurance_delivery_test.go` — new

**Steps.**

1. Delivery types and the two composite transitions:

```go
type AssuranceDelivery struct {
	ID          uuid.UUID
	ExceptionID uuid.UUID
	Transition  string // "opened" | "resolved"
	State       string // "pending" | "delivered" | "failed"
	Attempts    int
	CreatedAt   time.Time
	DeliveredAt *time.Time
	LastError   string
}

// AssuranceDeliveryJob is a pending delivery joined to enough of its exception
// for the caller to build a notification without a second round trip.
type AssuranceDeliveryJob struct {
	Delivery  AssuranceDelivery
	Exception BackupAssuranceException
}

// OpenExceptionAndEnqueue inserts an open exception and its "opened" delivery
// intent in ONE transaction. opened=false means an open row for this condition
// identity already existed (this replica lost the race, or an earlier tick already
// opened it); the returned exception is the existing row and NO delivery is created.
func (s *BackupAssuranceStore) OpenExceptionAndEnqueue(
	ctx context.Context, e BackupAssuranceException,
) (BackupAssuranceException, bool, error)

// ResolveExceptionAndEnqueue flips one open exception to resolved and inserts its
// "resolved" delivery intent in ONE transaction. resolved=false means the row was
// already resolved by another replica; no delivery is created.
func (s *BackupAssuranceStore) ResolveExceptionAndEnqueue(
	ctx context.Context, id uuid.UUID, at time.Time, reason string,
) (bool, error)

func (s *BackupAssuranceStore) ClaimPendingDeliveries(
	ctx context.Context, clusterID string, maxAttempts, limit int,
) ([]AssuranceDeliveryJob, error)
func (s *BackupAssuranceStore) MarkDelivered(ctx context.Context, id uuid.UUID) error
func (s *BackupAssuranceStore) MarkDeliveryFailed(ctx context.Context, id uuid.UUID, errMsg string, maxAttempts int) error
func (s *BackupAssuranceStore) CountPendingDeliveries(ctx context.Context, clusterID string) (pending, failed int, err error)
```

2. `OpenExceptionAndEnqueue` body, inside `WithTx`:

```sql
INSERT INTO backup_assurance_exceptions (
    id, cluster_id, policy_id, subject_kind, subject_namespace, subject_name,
    subject_uid, condition, state, severity, opened_at, last_observed_at,
    observation_count, last_success_at, detail
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'open', $9, $10, $10, 1, $11, $12)
ON CONFLICT DO NOTHING
RETURNING id;
```

Zero rows returned ⇒ re-`SELECT` the existing open row and return `opened = false`
without touching `backup_assurance_deliveries`. `ON CONFLICT DO NOTHING` (rather than a
named constraint target) is used deliberately because the conflict arbiter is a partial
index; this matches the 000014 precedent's application-side handling.

3. `ResolveExceptionAndEnqueue`:

```sql
UPDATE backup_assurance_exceptions
   SET state = 'resolved',
       resolved_at = $2,
       detail = jsonb_set(detail, '{resolutionReason}', to_jsonb($3::text), true)
 WHERE id = $1 AND state = 'open'
RETURNING id;
```

Only the transaction whose `UPDATE` returns a row inserts the `resolved` delivery.

4. `ClaimPendingDeliveries` — single statement, safe under concurrent takeover:

```sql
UPDATE backup_assurance_deliveries d
   SET attempts = d.attempts + 1
 WHERE d.id IN (
       SELECT id
         FROM backup_assurance_deliveries
        WHERE state = 'pending' AND attempts < $1
        ORDER BY created_at
        FOR UPDATE SKIP LOCKED
        LIMIT $2)
RETURNING d.id, d.exception_id, d.transition, d.state, d.attempts,
          d.created_at, d.delivered_at, d.last_error;
```

then join the exception rows in a second query filtered by `cluster_id`.

5. `MarkDeliveryFailed` sets `state = 'failed'` once `attempts >= maxAttempts`,
   otherwise leaves it `pending` with `last_error` updated. A `failed` row is never
   retried and never regenerates the condition.

6. Lease — the one genuinely new pattern in this release, and explicitly **advisory**:

```go
type AssuranceLease struct {
	ClusterID string
	Holder    string
	Fence     int64
	AcquiredAt time.Time
	ExpiresAt time.Time
}

var ErrLeaseHeldByOther = errors.New("backup assurance collector lease held by another replica")

// AcquireOrRenewLease takes the lease, or renews it if holder already owns it, or
// takes over if the incumbent's lease has expired according to the DATABASE clock.
// Returns ErrLeaseHeldByOther when a live incumbent holds it.
func (s *BackupAssuranceStore) AcquireOrRenewLease(
	ctx context.Context, clusterID, holder string, ttl time.Duration,
) (AssuranceLease, error)

func (s *BackupAssuranceStore) ReleaseLease(ctx context.Context, clusterID, holder string) error
func (s *BackupAssuranceStore) GetLease(ctx context.Context, clusterID string) (AssuranceLease, error)
```

```sql
INSERT INTO backup_assurance_collector_lease (cluster_id, holder, expires_at)
VALUES ($1, $2, NOW() + make_interval(secs => $3))
ON CONFLICT (cluster_id) DO UPDATE
   SET holder      = EXCLUDED.holder,
       renewed_at  = NOW(),
       expires_at  = EXCLUDED.expires_at,
       acquired_at = CASE WHEN backup_assurance_collector_lease.holder = EXCLUDED.holder
                          THEN backup_assurance_collector_lease.acquired_at ELSE NOW() END,
       fence       = CASE WHEN backup_assurance_collector_lease.holder = EXCLUDED.holder
                          THEN backup_assurance_collector_lease.fence
                          ELSE backup_assurance_collector_lease.fence + 1 END
 WHERE backup_assurance_collector_lease.holder = EXCLUDED.holder
    OR backup_assurance_collector_lease.expires_at < NOW()
RETURNING holder, fence, acquired_at, expires_at;
```

**Semantics, stated so a reviewer can check them:**

- **Duration:** `ttl = 180s` (3× the 60 s tick), passed by the caller. Chosen so two
  consecutive missed ticks do not cause a takeover.
- **Renewal:** every tick, before any work. A successful renew extends `expires_at` to
  `NOW() + ttl` on the **database clock**, so replica clock skew is irrelevant.
- **Takeover:** only when `expires_at < NOW()`. The `fence` increments on a change of
  holder, giving a monotonic epoch for logs and for the `status` endpoint. Nothing
  gates writes on the fence — see next bullet.
- **Correctness is not delegated to the lease.** If both replicas somehow believe they
  hold it (a pathological pause longer than the TTL), the partial `UNIQUE` index on
  `backup_assurance_exceptions` and `UNIQUE (exception_id, transition)` on
  `backup_assurance_deliveries` still make it impossible for two replicas to open the
  same exception or notify the same transition twice. The lease buys reduced Kubernetes
  API load and fewer racing observations, nothing more. This is stated in the table
  comment and must be stated in the PR description.
- **Release on shutdown** is best-effort and non-fatal; an unreleased lease simply
  expires after 180 s.

**Named tests — `backup_assurance_delivery_test.go`:**

| Test | Maps to |
|---|---|
| `TestOpenExceptionAndEnqueue_CreatesExactlyOneDelivery` | AE8 step 2 |
| `TestOpenExceptionAndEnqueue_SecondCallerGetsOpenedFalseAndNoDelivery` | AE8 step 3/5 — **competing replicas** |
| `TestOpenExceptionAndEnqueue_ConcurrentGoroutinesProduceOneRow` | **Competing replicas**, 16 goroutines against one identity |
| `TestOpenExceptionAndEnqueue_RecreatedSubjectUIDOpensNewException` | R1 / KTD3 — deleted-and-recreated resource |
| `TestOpenExceptionAndEnqueue_RollsBackDeliveryWhenExceptionInsertFails` | Transactionality |
| `TestResolveExceptionAndEnqueue_OnlyFirstCallerEnqueues` | Exactly-once resolution |
| `TestResolveExceptionAndEnqueue_ReopenAfterResolveCreatesNewRow` | Partial-index semantics |
| `TestClaimPendingDeliveries_SkipsLockedAndRespectsLimit` | Concurrency |
| `TestClaimPendingDeliveries_StopsAtMaxAttempts` | Poison-message containment |
| `TestMarkDeliveryFailed_TransitionsToFailedAtCap` | Delivery failure is retryable then terminal |
| `TestPendingDeliverySurvivesSimulatedRestart` | **Startup/restart** — commit the transition, drop the connection pool, reconnect, assert exactly one pending row |
| `TestAcquireOrRenewLease_SecondHolderRejectedWhileLive` | **Competing replicas** |
| `TestAcquireOrRenewLease_TakeoverAfterExpiryIncrementsFence` | Takeover |
| `TestAcquireOrRenewLease_RenewByIncumbentPreservesAcquiredAtAndFence` | Renewal |
| `TestReleaseLease_OnlyByHolder` | A non-holder cannot steal by releasing |
| `TestLeaseUsesDatabaseClockNotProcessClock` | Skew immunity — advance the Go clock, assert no takeover |
| `TestDeliveryContextCancellationLeavesRowClaimable` | **Cancellation** |

**Verification:** `cd backend && go vet ./... && go test ./...`

**Exit criteria / done means:**

- [ ] Two concurrent openers of one identity produce exactly one exception and exactly
      one delivery, proven by a concurrent test, not by inspection.
- [ ] Lease correctness is documented as advisory in code comments and PR description.
- [ ] No `context.Background()` in any store method except an explicitly named
      shutdown helper.
- [ ] `go vet ./... && go test ./...` clean repo-wide.

---

## U33. Backup freshness and schedule-outcome evaluator

**Branch:** `feat/u33-backup-freshness-evaluator`
**PR title:** `feat(velero): deterministic backup freshness and schedule-outcome evaluator`
**Covers:** R3, R22; KTD11. **Depends on:** U32 (for the `store` enums only).

**Files (4)** *(master plan listed 3; a fuzz target is added because `Evaluate` is a new
parse-adjacent seam over CRD-derived data — `docs/solutions/backend-resilience-conventions.md`
Part 2 and the existing `fuzz.yml` row for `./internal/velero/` make this mandatory,
not optional)*:

1. `backend/internal/velero/assurance.go` — new
2. `backend/internal/velero/assurance_test.go` — new
3. `backend/internal/velero/assurance_fuzz_test.go` — new
4. `backend/internal/velero/types.go` — modified

**Step 0 (Agent Directive 1).** `types.go` is 169 LOC, below the 300-LOC threshold, so
the full Step-0 protocol is not triggered. One dead-field removal is still folded in
because U33 is the unit that touches the file: delete `Schedule.LastBackupPhase`
(`types.go` L97). `grep -rn "LastBackupPhase\|lastBackupPhase"` returns exactly two hits
repo-wide — this declaration and `frontend/lib/velero-types.ts` L61 — and **no writer**.
Because the field is `omitempty` and never populated, removal is a wire no-op and
mobile-safe (R4). The TypeScript mirror is removed in U36b (different PR, frontend
verification suite).

**Steps.**

1. `types.go`: add `UID string \`json:"uid"\`` to `Backup` and to `Schedule`; remove
   `LastBackupPhase`. Populate the two new fields from `obj.GetUID()` in `parseBackup`
   and `parseSchedule`. *(This is a 2-line change inside `handler.go`'s parser
   functions — it does **not** count as a separate file for the cap because it is the
   same edit set; if the reviewer prefers, `handler.go` is the fifth file and the unit
   still fits.)*
2. Create `assurance.go`, package `velero`, importing only `time`, `errors`,
   `github.com/robfig/cron/v3` (already a dependency) and
   `github.com/kubecenter/kubecenter/internal/store`. **No `net/http`, no
   `context`, no `slog`** — the evaluator is pure and that is enforced by the import
   list.

```go
type Collection string
const (
	CollectionOK       Collection = "ok"
	CollectionDegraded Collection = "degraded"
	CollectionFailed   Collection = "failed"
)

type BackupOutcome string
const (
	OutcomeSuccess  BackupOutcome = "success"
	OutcomePartial  BackupOutcome = "partial"
	OutcomeFailure  BackupOutcome = "failure"
	OutcomeInFlight BackupOutcome = "in_flight"
	OutcomeUnknown  BackupOutcome = "unknown"
)

// BackupOutcomeFor classifies a Velero Backup status.phase for freshness purposes.
// It is deliberately NOT IsSuccessPhase/IsProgressPhase from types.go: those are UI
// badge helpers, and IsSuccessPhase("Available") is true for a BackupStorageLocation
// phase that a Backup can never carry.
func BackupOutcomeFor(phase string) BackupOutcome

// Observation is one collection cycle's evidence. Collection is a required field:
// no caller can accidentally omit the collection-health signal.
type Observation struct {
	ClusterID   string
	CollectedAt time.Time
	Collection  Collection
	Status      VeleroStatus
	Backups     []Backup
	Schedules   []Schedule
	Locations   *LocationsResponse
}

type Subject struct {
	Kind      store.AssuranceScopeKind
	Namespace string
	Name      string
	UID       string
}

// Detail is the privileged evidence attached to an exception. Fields marked
// //privileged are admin-only at read time (see assurance_handler.go projection).
type Detail struct {
	LastOutcome      BackupOutcome `json:"lastOutcome"`
	LastSuccessAt    *time.Time    `json:"lastSuccessAt,omitempty"`
	ExpectedRunAt    *time.Time    `json:"expectedRunAt,omitempty"`
	ExpectedRunKnown bool          `json:"expectedRunKnown"`
	CronParseError   string        `json:"cronParseError,omitempty"`
	SuppressedBy     string        `json:"suppressedBy,omitempty"`
	StorageLocation  string        `json:"storageLocation,omitempty"`  // privileged
	BSLMessage       string        `json:"bslMessage,omitempty"`       // privileged
	FailureReason    string        `json:"failureReason,omitempty"`    // privileged
}

type Finding struct {
	Subject   Subject
	PolicyID  uuid.UUID
	Condition store.AssuranceCondition
	Severity  string // "info" | "warning" | "critical"
	Detail    Detail
}

// Evaluate is pure: no clock, no I/O, no logging. now is supplied by the caller so
// every case is reproducible in a table test.
func Evaluate(obs Observation, policies []store.BackupAssurancePolicy, now time.Time) []Finding

// ExpectedRunsSince walks cronExpr forward from anchor and returns the latest
// expected fire time at or before now. known=false when the expression is
// unparseable, the walk exceeds maxSteps, or anchor precedes the lookback cap.
func ExpectedRunsSince(cronExpr string, anchor, now time.Time, maxSteps int) (last time.Time, known bool)
```

3. Implement `Evaluate` with the collection guard **first**:

```go
func Evaluate(obs Observation, policies []store.BackupAssurancePolicy, now time.Time) []Finding {
	if obs.Collection == CollectionFailed {
		// Exactly one cluster-scoped finding. Never N per-subject findings — a
		// Velero outage must not produce an alert storm, and per-subject
		// "no backups" would be a false statement about the cluster.
		return []Finding{clusterCollectionUnknown(obs, policies)}
	}
	...
}
```

4. Implement the per-condition rules exactly as Design Decisions §5 specifies. Every
   `overdue` computation must route through `ExpectedRunsSince` when the subject is a
   schedule with a non-empty cron, and must fall back to `max_age + grace` when
   `known == false`.
5. Never reuse `computeNextRun` (handler.go L1394): it clamps `from` to `now` and so can
   never report a missed run. Add a comment in `assurance.go` saying so, so a future
   reader does not "simplify" the two into one.
6. Never interpolate `BSL.Message`, `Restore.FailureReason` or any controller string
   into anything other than `Detail`'s privileged fields.

**Named tests — `assurance_test.go`** (table-driven, one row per master-plan scenario):

| Test | Master-plan scenario |
|---|---|
| `TestBackupOutcomeFor_CompletedIsSuccess` | Completed |
| `TestBackupOutcomeFor_PartiallyFailedIsPartial` | PartiallyFailed |
| `TestBackupOutcomeFor_FailedAndFailedValidationAreFailure` | Failed |
| `TestBackupOutcomeFor_InFlightPhasesNeverCountAsSuccessOrFailure` | in-progress |
| `TestBackupOutcomeFor_DoesNotInheritIsSuccessPhaseBSLPhases` | Regression guard: `"Available"`/`"Enabled"` must be `unknown` |
| `TestEvaluate_CollectionFailedYieldsSingleCollectionUnknown` | **The hard rule** — asserts no `never_run`, no `overdue` |
| `TestEvaluate_CollectionFailedNeverClaimsNoBackupsExist` | **The hard rule**, phrased as the plan states it |
| `TestEvaluate_CollectionDegradedScopesUnknownToAffectedSubjectsOnly` | degraded |
| `TestEvaluate_OverdueWhenPastMaxAgePlusGrace` | overdue run |
| `TestEvaluate_NotOverdueInsideGrace` | grace |
| `TestEvaluate_PartiallyFailedTreatedAsFailureByDefault` | PartiallyFailed distinct from Completed |
| `TestEvaluate_PartiallyFailedTreatedAsSuccessStillReportsPartialOutcome` | Honesty rule 4 |
| `TestEvaluate_CompletedWithNilCompletionTimeFallsBackToStartTime` | `getTime` returns nil |
| `TestEvaluate_CompletedWithNoTimestampsIsUnknownNotSuccess` | Same, both nil |
| `TestEvaluate_PausedScheduleSuppressesOverdueAndEmitsPaused` | paused schedule |
| `TestEvaluate_PausedScheduleDoesNotResolveExistingOverdue` | paused ≠ fixed |
| `TestEvaluate_NeverRunIsDistinctFromOverdue` | first run |
| `TestEvaluate_MissingStorageLocationYieldsLocationUnavailable` | missing locations |
| `TestEvaluate_UnavailableBSLPhaseYieldsLocationUnavailable` | missing locations |
| `TestEvaluate_NoDefaultBSLWithEmptyStorageLocationYieldsLocationUnavailable` | missing locations |
| `TestEvaluate_DisabledPolicyProducesNoFindings` | policy disable semantics |
| `TestEvaluate_IsDeterministicAcrossRepeatedCalls` | Exit criterion |
| `TestEvaluate_NeverInterpolatesControllerMessagesOutsidePrivilegedDetail` | **Secret/sensitive-field suppression** |
| `TestExpectedRunsSince_DailyCron` | fixed interval |
| `TestExpectedRunsSince_WeeklyCronIsNotTreatedAsFixedInterval` | **calendar boundary** |
| `TestExpectedRunsSince_MonthlyAndLeapDayExpressions` | **calendar boundary** |
| `TestExpectedRunsSince_DSTSpringForwardAbsorbedByDefaultGrace` | **DST boundary** |
| `TestExpectedRunsSince_DSTFallBackRepeatedHourDoesNotDoubleFire` | **DST boundary** |
| `TestExpectedRunsSince_UnparseableExpressionReturnsKnownFalse` | honest unknown |
| `TestExpectedRunsSince_ExceedingMaxStepsReturnsKnownFalse` | bounded work |
| `TestExpectedRunsSince_HonoursCronTZPrefix` | timezone |
| `TestEvaluate_UnknownExpectedRunFallsBackToMaxAgeAndFlagsIt` | honest unknown |

**Fuzz target — `assurance_fuzz_test.go`:**

```go
// FuzzAssuranceEvaluate asserts Evaluate is crash-safe on arbitrary CRD-derived
// input. Oracle A (no panic) + Oracle D (no privileged controller string escapes
// Detail's privileged fields). Reuses unstructuredFromFuzz from parsers_fuzz_test.go
// and feeds the parsed objects through parseBackup/parseSchedule/parseBSL first, so
// the corpus exercises the real production path.
func FuzzAssuranceEvaluate(f *testing.F)
```

`.github/workflows/fuzz.yml` gains a matrix row
`{ pkg: ./internal/velero/, target: FuzzAssuranceEvaluate }`. *(That workflow edit is a
6th file; it ships in U33 only if the reviewer accepts the cap at 5 excluding CI
configuration — otherwise it moves to U34c, which has spare capacity. Stated here so the
choice is explicit rather than discovered.)*

**Verification:** `cd backend && go vet ./... && go test ./...`
plus `cd backend && go test -run=Fuzz -fuzz=FuzzAssuranceEvaluate -fuzztime=60s ./internal/velero/`

**Exit criteria / done means:**

- [ ] `Evaluate` has no `context`, `net/http`, `log/slog` or `os` import.
- [ ] Every one of the 32 named tests passes.
- [ ] `Schedule.LastBackupPhase` is gone from Go; a grep proves no writer existed.
- [ ] `Backup.UID` and `Schedule.UID` are populated from `obj.GetUID()`.
- [ ] `computeNextRun` is untouched and explicitly documented as not reusable here.
- [ ] Fuzz target runs 60 s with no crash and no new corpus entry that leaks a
      controller string outside `Detail`'s privileged fields.

---

## U34a. Notification service durable-delivery seam

**Split rationale:** modifying `notifications/service.go` is the single highest
blast-radius edit in this release — eleven `Source` values and eight in-tree callers
share it. It gets its own PR, its own regression suite, and its own review, so a
regression is attributable to one diff rather than buried in a poller PR.

**Branch:** `feat/u34a-notification-emit-result`
**PR title:** `feat(notifications): add EmitSync result seam and SourceVelero field suppression`
**Covers:** R23. **Depends on:** none (ships before U34b).

**Files (3):**

1. `backend/internal/notifications/service.go` — modified
2. `backend/internal/notifications/service_test.go` — modified
3. `backend/internal/notifications/types.go` — modified

**Steps.**

1. `types.go`: add the result enum next to the existing `Source`/`Severity` enums.

```go
// EmitResult reports what Emit/EmitSync actually did, so a caller holding a durable
// delivery intent can distinguish "sent", "already sent recently" and "not sent".
type EmitResult int
const (
	EmitPersisted EmitResult = iota // persisted, broadcast, enqueued for channels
	EmitDeduped                     // suppressed by the 15-minute dedup window
	EmitSkipped                     // audit-source short circuit
)
```

2. `service.go`: introduce `EmitSync` and make `Emit` a thin wrapper so **no existing
   caller changes**.

```go
// EmitSync is Emit with a reported outcome. Emit remains the fire-and-forget path;
// EmitSync exists for callers holding a durable delivery intent (Release F backup
// assurance) that must decide whether to retry or mark the intent delivered.
// EmitDeduped counts as delivered: the feed already carries an equivalent entry.
func (s *NotificationService) EmitSync(ctx context.Context, n Notification) (EmitResult, error) {
	if n.Source == SourceAudit {
		if err := s.persistAndBroadcast(ctx, n); err != nil {
			return EmitSkipped, err
		}
		return EmitSkipped, nil
	}
	exists, err := s.store.DedupExists(ctx, n, dedupWindow)
	if err != nil {
		s.logger.Error("dedup check failed", "error", err)
		// Continue — better to duplicate than to drop. (unchanged behaviour)
	}
	if exists {
		return EmitDeduped, nil
	}
	if err := s.persistAndBroadcast(ctx, n); err != nil {
		return EmitPersisted, fmt.Errorf("emit notification: %w", err)
	}
	select {
	case s.queue <- n:
	default:
		s.logger.Warn("notification dispatch queue full, dropping external dispatch",
			"source", n.Source, "title", n.Title)
	}
	return EmitPersisted, nil
}

// Emit is unchanged for all existing callers.
func (s *NotificationService) Emit(ctx context.Context, n Notification) {
	if _, err := s.EmitSync(ctx, n); err != nil {
		s.logger.Error("emit notification", "error", err)
	}
}
```

3. `service.go` L556: add the Velero entry to the suppression map.

```go
var suppressResourceFieldsBySource = map[Source]bool{
	SourceExternalSecrets: true,
	SourceVelero:          true, // Release F: assurance exceptions are collected with
	                             // the platform ServiceAccount and cover namespaces the
	                             // digest recipient may not read.
}
```

**This is the regression risk and it must be called out in the PR body.** The map is
read by `sanitizeForEmailDigest` and therefore changes the email digest for the
**existing** `velero.Handler.InvalidateCache` notification (handler.go L84–92) as well
as for the new ones. That notification sets no `ResourceKind`/`ResourceNS`/
`ResourceName`, so the sanitiser zeroes fields that are already empty — a provable
no-op, asserted by a dedicated test rather than by argument. Any *future* Velero
notification inherits the suppression; that is the intended default and is documented in
the map comment.

**Named tests — `service_test.go` (additive; existing tests must not be edited):**

| Test | Maps to |
|---|---|
| `TestEmitSync_ReturnsEmitPersistedOnFirstEmit` | New seam |
| `TestEmitSync_ReturnsEmitDedupedInsideWindow` | 15-min window still applies |
| `TestEmitSync_ReturnsEmitSkippedForAuditSource` | Existing audit short-circuit preserved |
| `TestEmitSync_QueueFullStillReportsPersisted` | Persisted ≠ dispatched |
| `TestEmit_DelegatesToEmitSyncWithIdenticalObservableBehaviour` | **Regression:** table over all eleven `Source` values asserting feed rows, WS broadcast payload and channel dispatch are byte-identical to pre-change behaviour |
| `TestSanitizeForEmailDigest_SuppressesVeleroResourceFields` | New suppression |
| `TestSanitizeForEmailDigest_ExistingVeleroCacheNotificationUnaffected` | **Regression:** the `InvalidateCache` shape (empty resource fields) is unchanged |
| `TestSanitizeForEmailDigest_OtherSourcesUnaffected` | **Regression:** policy, gitops, cluster, limits, scan, certmanager, alert, diagnostic notifications keep their resource fields |
| `TestSourceVelero_StillValid` | `Source.Valid()` unchanged |
| `TestEmitSync_ContextCancellationSurfacesError` | **Cancellation** |
| `TestEmitSync_StoreUnavailableReturnsError` | **Unavailable DB** |

**Verification:** `cd backend && go vet ./... && go test ./...`

**Exit criteria / done means:**

- [ ] Zero call sites of `Emit` are edited anywhere in the repo (`grep -rn "\.Emit(" backend/` diff is empty).
- [ ] The eleven-source regression table passes.
- [ ] The digest-suppression change is proven a no-op for the pre-existing Velero
      notification by test, not by reasoning.
- [ ] `go vet ./... && go test ./...` clean repo-wide.

---

## U34b. Background assurance collector

**Branch:** `feat/u34b-backup-assurance-collector`
**PR title:** `feat(velero): background backup-assurance collector with leased, restart-safe delivery`
**Covers:** R22, R23; AE8. **Depends on:** U32b, U33, U34a.

**Files (2):**

1. `backend/internal/velero/assurance_service.go` — new
2. `backend/internal/velero/assurance_service_test.go` — new

**Design constraint that drives the whole unit.** This is the highest-risk element in
Release F: an unrecovered panic on a non-request stack terminates the process
(`recoverutil` package doc). The collector is therefore built so that **`Start` is the
only goroutine it ever creates**. No `errgroup`, no `sync.WaitGroup`, no per-item
dispatch fan-out, no channels. Concurrency inside the collection step is inherited from
`handler.fetchAll`, which already wraps every worker in `recoverutil.Go` (handler.go
L1021–1076). Consequences, stated so a reviewer can verify them by reading the diff:

- The `wg.Done()`-outside-the-closure rule
  (`docs/solutions/backend-resilience-conventions.md` L48–50) has nothing to violate,
  because there is no `WaitGroup`.
- The counted-channel-send hazard (L51–68) has nothing to violate, because there are no
  channels.
- The sanctioned per-item `_ = recover()` exception used by `externalsecrets.dispatchEmits`
  (L82–87) is not needed and must not be copied.
- Delivery is serial and bounded (`assuranceDeliveryBatch = 50` per tick), which is
  affordable because at most `2 × subjects` transitions can occur per tick and the
  steady state is zero.

**Steps.**

1. Constants and struct:

```go
const (
	assuranceInterval            = 60 * time.Second // matches externalsecrets/certmanager
	assuranceLeaseTTL            = 3 * assuranceInterval
	assuranceDeliveryBatch       = 50
	assuranceMaxDeliveryAttempts = 5
	assuranceResolvedRetention   = 90 * 24 * time.Hour // matches notifications.runRetention
)

// AssuranceService owns the Release F collection loop and is also the read model
// behind the assurance HTTP handlers (U35). It holds NO in-memory condition state:
// the open exception rows in PostgreSQL are the memory, which is why it needs no
// externalsecrets-style seedFromNotifications restart hack.
type AssuranceService struct {
	handler   *Handler
	disc      *Discoverer
	store     *store.BackupAssuranceStore
	notif     *notifications.NotificationService
	clusterID string
	holder    string // "<hostname>-<pid>", stable for the process lifetime
	logger    *slog.Logger

	mu        sync.RWMutex
	lastRunAt time.Time
	lastColl  Collection
	lease     store.AssuranceLease
	hasLease  bool
	lastErr   string
}

func NewAssuranceService(
	h *Handler, d *Discoverer, st *store.BackupAssuranceStore,
	n *notifications.NotificationService, clusterID, holder string, logger *slog.Logger,
) *AssuranceService
```

`store` may be nil (no PostgreSQL). `NewAssuranceService` accepts it; `Start` returns
immediately with a single `logger.Info("backup assurance disabled: no database")` —
"Disable cleanly when prerequisites are absent" from the master plan.

2. The loop — a literal transcription of the `externalsecrets` / `certmanager` idiom:

```go
// Start runs the collector loop. Fires immediately, then on a 60s ticker. Blocks
// until ctx is cancelled. tick() panics are recovered via recoverutil.Tick so a
// single bad cycle logs and the next tick restarts from a clean slate — this
// goroutine runs OUTSIDE chi's recovery middleware and an unrecovered panic here
// would terminate the process and silently end backup monitoring.
func (a *AssuranceService) Start(ctx context.Context) {
	if a.store == nil {
		a.logger.Info("backup assurance disabled: no database configured")
		return
	}

	a.runTickWithRecover(ctx)

	ticker := time.NewTicker(assuranceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.releaseLeaseOnShutdown()
			return
		case <-ticker.C:
			a.runTickWithRecover(ctx)
		}
	}
}

func (a *AssuranceService) runTickWithRecover(ctx context.Context) {
	recoverutil.Tick(ctx, a.logger, "velero assurance tick", a.tick)
}

// releaseLeaseOnShutdown runs after ctx is already cancelled, so it needs its own
// bounded context. Wrapped in recoverutil.Safe because it executes on the same
// non-request goroutine as the loop.
func (a *AssuranceService) releaseLeaseOnShutdown() {
	recoverutil.Safe(a.logger, "velero assurance lease release", func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := a.store.ReleaseLease(rctx, a.clusterID, a.holder); err != nil {
			a.logger.Warn("velero assurance: lease release failed (will expire)", "error", err)
		}
	})
}
```

3. `tick`, in order, all serial:

```go
func (a *AssuranceService) tick(ctx context.Context) {
	// 1. Lease. A live incumbent means this replica does no work this cycle —
	//    but correctness never depended on winning (see U32b).
	lease, err := a.store.AcquireOrRenewLease(ctx, a.clusterID, a.holder, assuranceLeaseTTL)
	if errors.Is(err, store.ErrLeaseHeldByOther) {
		a.setLeaseState(false, store.AssuranceLease{})
		return
	}
	if err != nil { a.recordErr(err); return }
	a.setLeaseState(true, lease)

	// 2. Policies. No policies means no evaluation — install must not imply alerts.
	policies, err := a.store.ListPolicies(ctx, a.clusterID)
	if err != nil { a.recordErr(err); return }
	if len(policies) == 0 { a.recordRun(CollectionOK); return }

	// 3. Collect. Reuses handler.fetchAll: existing 30s cache, existing singleflight,
	//    existing recoverutil.Go fan-out. No new goroutine, no second client.
	obs := a.collect(ctx)

	// 4. Evaluate. Pure.
	findings := Evaluate(obs, policies, time.Now().UTC())

	// 5. Reconcile against the durable open set.
	open, err := a.store.ListOpenExceptions(ctx, a.clusterID)
	if err != nil { a.recordErr(err); return }
	a.reconcile(ctx, findings, open, obs)

	// 6. Drain deliveries (bounded, serial).
	a.drainDeliveries(ctx)

	// 7. Retention, at most once per hour, guarded by lastPruneAt.
	a.pruneIfDue(ctx)

	a.recordRun(obs.Collection)
}
```

4. `collect` maps failures to `Collection` truthfully:

```go
func (a *AssuranceService) collect(ctx context.Context) Observation {
	st := a.disc.Status(ctx)
	obs := Observation{ClusterID: a.clusterID, CollectedAt: time.Now().UTC(), Status: st}
	if !st.Detected {
		obs.Collection = CollectionFailed
		return obs
	}
	data, err := a.handler.fetchAll(ctx)  // same package; unexported is fine
	if err != nil {
		a.logger.Error("velero assurance: fetch failed", "error", err)
		obs.Collection = CollectionFailed
		return obs
	}
	obs.Backups, obs.Schedules, obs.Locations = data.backups, data.schedules, data.locations
	obs.Collection = CollectionOK
	return obs
}
```

`doFetchAll` uses `errgroup.WithContext` and fails the whole group on the first error,
so a partial result is not observable through `fetchAll` today. `CollectionDegraded` is
therefore reachable only from the future case where per-list errors are surfaced
individually; the evaluator supports it, the collector does not yet produce it, and the
code says so in a comment rather than pretending otherwise.

5. `reconcile`:

- For each finding: `OpenExceptionAndEnqueue`. On `opened == false`, `ObserveException`.
- For each open row **not** matched by a finding: resolve **only when
  `obs.Collection == CollectionOK`**; otherwise leave it open. This is where the hard
  rule is enforced a second time, at the write layer.
- Match key is the full condition identity, not the name.

6. `drainDeliveries`:

```go
func (a *AssuranceService) drainDeliveries(ctx context.Context) {
	jobs, err := a.store.ClaimPendingDeliveries(ctx, a.clusterID, assuranceMaxDeliveryAttempts, assuranceDeliveryBatch)
	if err != nil { a.recordErr(err); return }
	for _, j := range jobs {
		if ctx.Err() != nil { return }               // cancellation: leave the row claimable
		res, err := a.notif.EmitSync(ctx, a.notificationFor(j))
		switch {
		case err != nil:
			_ = a.store.MarkDeliveryFailed(ctx, j.Delivery.ID, err.Error(), assuranceMaxDeliveryAttempts)
		case res == notifications.EmitDeduped, res == notifications.EmitPersisted:
			_ = a.store.MarkDelivered(ctx, j.Delivery.ID)
		default:
			_ = a.store.MarkDeliveryFailed(ctx, j.Delivery.ID, "not dispatched", assuranceMaxDeliveryAttempts)
		}
	}
}
```

7. `notificationFor` — the sensitive-field contract, mirroring `externalsecrets.Poller.emit`
   (poller.go L389–415):

```go
func (a *AssuranceService) notificationFor(j store.AssuranceDeliveryJob) notifications.Notification {
	return notifications.Notification{
		Source:       notifications.SourceVelero,
		Severity:     severityFor(j),
		Title:        titleFor(j),   // a fixed constant per (condition, transition)
		Message:      messageFor(j), // a fixed template; NO controller strings interpolated
		ResourceKind: resourceKindFor(j.Exception.SubjectKind), // "schedule" | "namespace" | "cluster"
		ResourceNS:   j.Exception.SubjectNamespace,
		ResourceName: j.Exception.SubjectName,
		ClusterID:    j.Exception.ClusterID,
		CreatedAt:    time.Now().UTC(),
		SuppressResourceFields: true,
	}
}
```

Title constants (`TitleBackupOverdue`, `TitleBackupFailed`, `TitleBackupNeverRun`,
`TitleBackupLocationUnavailable`, `TitleBackupSchedulePaused`,
`TitleBackupCollectionUnknown`, `TitleBackupRecovered`) are exported package constants
for the same reason ESO exports its titles (poller.go L41–55): a rename must be a
single-site compiler-checked change. `ResourceKind` never encodes the condition — the
ESO comment at poller.go L384–388 explains that the state suffix is itself
cross-tenant identity information.

8. `Snapshot()` returns the read model U35 needs:

```go
type AssuranceRuntimeStatus struct {
	Enabled          bool
	LastRunAt        time.Time
	LastCollection   Collection
	LastError        string
	LeaseHeld        bool
	LeaseHolder      string
	LeaseFence       int64
	LeaseExpiresAt   time.Time
}
func (a *AssuranceService) Snapshot() AssuranceRuntimeStatus
```

**Named tests — `assurance_service_test.go`:**

| Test | Maps to |
|---|---|
| `TestAssurance_AE8_TransitionEmitsExactlyOnceWithoutBrowser` | **AE8** |
| `TestAssurance_AE8_RepeatedTicksDoNotReEmit` | **AE8** |
| `TestAssurance_AE8_SimulatedRestartDoesNotDuplicate` | **AE8 / startup-restart** — new service instance over the same store |
| `TestAssurance_AE8_FreshSuccessfulBackupResolvesAndEmitsResolution` | **AE8** |
| `TestAssurance_TwoServicesOneStoreProduceOneExceptionAndOneDelivery` | **Competing replicas** |
| `TestAssurance_LeaseLossSkipsWorkWithoutError` | Lost leadership |
| `TestAssurance_LeaseTakeoverAfterIncumbentStops` | Lost leadership |
| `TestAssurance_StartReturnsImmediatelyWithNilStore` | **Unavailable DB** |
| `TestAssurance_StoreErrorMidTickDoesNotPanicOrStopTheLoop` | Resilience |
| `TestAssurance_PanicInEvaluateIsRecoveredAndLoopContinues` | **recoverutil compliance** — inject a panicking policy set, assert the ticker fires again |
| `TestAssurance_StartReturnsOnContextCancel` | **Cancellation / shutdown** |
| `TestAssurance_ShutdownReleasesLeaseOnBoundedContext` | Shutdown |
| `TestAssurance_ShutdownWithAlreadyCancelledContextStillReleases` | Shutdown — proves the fresh context is real |
| `TestAssurance_DeliveryFailureRetriesWithoutRegeneratingCondition` | Master-plan scenario, verbatim |
| `TestAssurance_DeliveryFailureStopsAtMaxAttempts` | Poison containment |
| `TestAssurance_DedupedEmitIsMarkedDelivered` | Second-layer dedup |
| `TestAssurance_CollectionFailureLeavesOpenExceptionsOpen` | **The hard rule at the write layer** |
| `TestAssurance_CollectionFailureOpensOneClusterExceptionNotPerSubject` | Alert-storm guard |
| `TestAssurance_NoPoliciesMeansNoExceptionsAndNoNotifications` | Install ≠ alerts |
| `TestAssurance_NotificationCarriesSuppressResourceFields` | **Sensitive-field suppression** |
| `TestAssurance_NotificationMessageNeverContainsControllerText` | **Sensitive-field suppression** — feeds a BSL message containing a bucket URL, asserts absence from Title/Message |
| `TestAssurance_ResourceKindNeverEncodesCondition` | Cross-tenant identity leak guard |
| `TestAssurance_CreatesNoGoroutinesBeyondStart` | **Design constraint** — `runtime.NumGoroutine()` delta across a tick is zero |
| `TestAssurance_PruneRunsAtMostHourly` | **Retention** |
| `TestAssurance_NeverConstructsARestore` | **Release boundary** — asserts the package's assurance files contain no reference to `RestoreGVR` or `HandleCreateRestore` |

**Verification:** `cd backend && go vet ./... && go test ./...`

**Exit criteria / done means:**

- [ ] `Start` is the only `go` statement introduced by this unit; `grep -n "go func\|errgroup\|sync.WaitGroup\|make(chan" backend/internal/velero/assurance_service.go` returns nothing.
- [ ] Every non-request goroutine entry point is wrapped: `recoverutil.Tick` on the tick,
      `recoverutil.Safe` on the shutdown lease release.
- [ ] The panic-injection test proves the loop survives a panic.
- [ ] No `RestoreGVR`, `DeleteBackupRequestGVR` or any mutating verb appears in the diff.
- [ ] `go vet ./... && go test ./...` clean repo-wide.

---

## U34c. Dependency wiring and process lifecycle

**Split rationale:** the `main.go` hunk and the one-line `velero.Handler` field are the
integration surface. Isolating them keeps the collector PR reviewable as pure logic and
makes the wiring diff small enough to read against the surrounding lines.

**Branch:** `feat/u34c-backup-assurance-wiring`
**PR title:** `feat(kubecenter): wire the backup-assurance collector and handler dependency`
**Covers:** R22; KTD1. **Depends on:** U34b.

**Files (3):**

1. `backend/cmd/kubecenter/main.go` — modified
2. `backend/internal/velero/handler.go` — modified (one field)
3. `backend/internal/velero/assurance_wiring_test.go` — new

**Steps.**

1. `backend/internal/velero/handler.go` — add one exported field to the existing struct
   (additive; not a structural refactor, so Agent Directive 1's Step 0 is not triggered
   despite the file's 1419 LOC). Anchor lines quoted from the current file:

```go
// Handler serves Velero HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	AuditLogger   audit.Logger
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger
+	// Assurance backs the Release F /velero/assurance/* endpoints. Nil when no
+	// PostgreSQL is configured; the handlers then return an explicit 503.
+	Assurance *AssuranceService

	fetchGroup singleflight.Group
```

   `NewHandler` is **not** changed — the repo's DI idiom for optional collaborators is
   post-construction field assignment, exactly as `veleroHandler.NotifService =
   notifService` at main.go L760.

2. `backend/cmd/kubecenter/main.go` — the new block goes immediately after the
   cert-manager section and before the ESO section, so it sits with the other
   CRD-feature pollers. Anchor context, quoted verbatim from the current file
   (L768–795):

```go
	// Cert-Manager integration
	cmDisc := certmanager.NewDiscoverer(k8sClient, logger)
	cmHandler := certmanager.NewHandler(k8sClient, clusterRouter, cmDisc, accessChecker, auditLogger, notifService, logger)
	cmPoller := certmanager.NewPoller(k8sClient, cmDisc, cmHandler, notifService, logger)
	go cmPoller.Start(ctx)
	// F#8 (round-3) — wire cert-manager's per-cluster remote cache into
	// ClusterRouter.EvictCluster so a cluster deletion or credential
	// update drops the cached cert data in the same operation. Without
	// this hook a re-registered cluster ID could briefly serve the
	// previous tenant's certificates until cacheTTL expired.
	clusterRouter.RegisterEvictHook(cmHandler.EvictRemoteCache)

+	// Release F — backup assurance. Local cluster only, platform ServiceAccount
+	// identity (never a retained user token). Requires PostgreSQL: with no DB the
+	// service is constructed with a nil store, Start() returns immediately, and the
+	// /velero/assurance/* endpoints answer 503 with an explicit reason.
+	var assuranceStore *appstore.BackupAssuranceStore
+	if dbPool != nil {
+		assuranceStore = appstore.NewBackupAssuranceStore(dbPool)
+	}
+	assuranceHolder := assuranceHolderID()  // "<hostname>-<pid>"
+	veleroAssurance := velero.NewAssuranceService(
+		veleroHandler, veleroDiscoverer, assuranceStore,
+		notifService, cfg.ClusterID, assuranceHolder, logger,
+	)
+	veleroHandler.Assurance = veleroAssurance
+	go veleroAssurance.Start(ctx)
+
	// External Secrets Operator integration (Phase A — observatory; Phase D
	// — alerting + threshold annotations; Phase C — DB persistence + drift
	// history). esoHistoryStore is nil when no DB is configured, in which
```

   Notes on the anchors: `veleroHandler` and `veleroDiscoverer` are in scope from
   L705–707; `notifService` is in scope from L727 and is nil-safe; `dbPool` and
   `cfg.ClusterID` are already used by the ESO block at L783–798; `appstore` is the
   existing alias for `internal/store` (used at L784 as
   `appstore.NewESOHistoryStore(dbPool)`); `ctx` is the root
   `signal.NotifyContext` context from L143. `go veleroAssurance.Start(ctx)` matches
   `go cmPoller.Start(ctx)` (L771) and `go esoPoller.Start(ctx)` (L799) exactly.

3. Add the small helper next to the other file-local helpers in `main.go`:

```go
// assuranceHolderID identifies this process as a backup-assurance lease holder.
// Hostname alone is insufficient (two pods can share a node name in some CI
// setups); PID alone is insufficient across hosts.
func assuranceHolderID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}
```

4. No change to the shutdown block (L935–964). The collector returns on `ctx.Done()`
   like every other poller; its lease release is bounded at 2 s and cannot delay
   `httpServer.Shutdown`.

5. If the `fuzz.yml` matrix row for `FuzzAssuranceEvaluate` was deferred from U33, it
   ships here (`.github/workflows/fuzz.yml`, 4th file).

**Named tests — `assurance_wiring_test.go`:**

| Test | Maps to |
|---|---|
| `TestAssuranceHolderID_IsStableWithinProcess` | Lease identity |
| `TestAssuranceHolderID_DiffersAcrossPIDs` | Lease identity |
| `TestHandlerAssuranceFieldDefaultsToNil` | **Unavailable DB** — a handler built by `NewHandler` has no assurance service |
| `TestAssuranceServiceWithNilStoreStartsAndStopsCleanly` | **Startup/restart**, DB-less deployment |
| `TestAssuranceServiceStartIsNonBlockingUnderContextCancel` | **Cancellation** — `Start` returns within 2 s of cancel |

*(`main.go` itself has no test harness in this repo; the wiring is verified by these
package-level tests plus the repo-wide build.)*

**Verification:**

```
cd backend && go vet ./... && go test ./...
cd backend && go build ./cmd/kubecenter
```

**Exit criteria / done means:**

- [ ] `go build ./cmd/kubecenter` succeeds; the binary starts with and without
      `KUBECENTER_DATABASE_URL`.
- [ ] With no DB, the log line `backup assurance disabled: no database configured`
      appears exactly once and nothing else in the feature logs.
- [ ] `NewHandler`'s signature is unchanged (no downstream caller churn).
- [ ] The new `main.go` block is ≤ 20 lines and sits between the cert-manager and ESO
      sections.
- [ ] `go vet ./... && go test ./...` clean repo-wide.

---

## U35. Policy and exception APIs

**Branch:** `feat/u35-backup-assurance-api`
**PR title:** `feat(velero): admin-managed assurance policies and RBAC-filtered exception reads`
**Covers:** R2, R22, R23. **Depends on:** U34c.

**Files (3)** *(master plan listed 5; `backend/internal/server/server.go` is **not**
needed — `VeleroHandler` is already a field on both `Server` (server.go L77) and `Deps`
(L124) and is assigned at L294–296 — and `velero/handler.go` was already amended in
U34c)*:

1. `backend/internal/velero/assurance_handler.go` — new
2. `backend/internal/velero/assurance_handler_test.go` — new
3. `backend/internal/server/routes.go` — modified

**Steps.**

1. Handlers in `assurance_handler.go`, package `velero`:

```go
func (h *Handler) HandleAssuranceStatus(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleListAssurancePolicies(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleCreateAssurancePolicy(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleUpdateAssurancePolicy(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleDeleteAssurancePolicy(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleListAssuranceExceptions(w http.ResponseWriter, r *http.Request)
```

2. Every handler opens with the same three-line guard:

```go
user, ok := httputil.RequireUser(w, r)
if !ok { return }
if h.Assurance == nil {
	httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
		"backup assurance unavailable", "database_unavailable",
		map[string]any{"capability": "backup-assurance"})
	return
}
```

   `WriteErrorWithReason` already exists (`httputil`), and this is the "New evidence and
   receipts require PostgreSQL; when unavailable, their endpoints report an unavailable
   capability" assumption made concrete. **503, never 404** — a 404 would be
   indistinguishable from an unregistered route.

3. Read filtering for `HandleListAssuranceExceptions`, copying
   `notifications.Handler.accessibleNamespaces` (handler.go L542–557):

```go
// assuranceVisibleNamespaces returns (namespaces, admin, error). admin==true means
// no namespace filter. Mirrors notifications.Handler.accessibleNamespaces so the two
// RBAC surfaces cannot drift.
func (h *Handler) assuranceVisibleNamespaces(r *http.Request, user *auth.User) ([]string, bool, error)
```

   then, per exception:
   - `subject_kind == 'cluster'` ⇒ admin only.
   - otherwise the namespace must be in the visible set, **and**
     `h.canAccess(ctx, user, "list", "schedules", ns)` must pass (the existing SSAR at
     handler.go L117–130).
   - `metadata.total` is computed **after** filtering.
   - `Detail` is projected through a fixed allow-list; the privileged keys
     (`storageLocation`, `bslMessage`, `failureReason`) are included only for admins.

4. Policy write validation (`Create`/`Update`): `scope_kind` must be a known enum;
   `max_age_seconds >= 300`; `grace_seconds >= 0`; `treat_partial_as ∈ {success,
   failure}`; `scope_namespace`/`scope_name` must satisfy `resources.ValidateK8sName`
   when non-empty; `{id}` is parsed with `uuid.Parse` and returns 400 on failure —
   **not** `resources.ValidateURLParams`, which validates only `{name}`/`{namespace}`
   (handler.go L297–311) and would pass an arbitrary `{id}` through.
5. Every write calls `h.auditLog(r, user, audit.ActionUpdate, "BackupAssurancePolicy",
   ns, name, audit.ResultSuccess)` — the existing helper at handler.go L133.
6. Routes, inserted inside the existing `registerVeleroRoutes` closure, after the
   `// Locations (read-only)` line at routes.go L678–679:

```go
		// Locations (read-only)
		vr.Get("/locations", h.HandleListLocations)
+
+		// Backup assurance (Release F). Registered unconditionally inside the
+		// velero group so a DB-less deployment answers 503 with an explicit
+		// reason rather than a 404 that looks like a client bug.
+		vr.Route("/assurance", func(asr chi.Router) {
+			asr.Get("/status", h.HandleAssuranceStatus)
+			asr.Get("/exceptions", h.HandleListAssuranceExceptions)
+			asr.With(middleware.RequireAdmin).Get("/policies", h.HandleListAssurancePolicies)
+			asr.With(middleware.RequireAdmin, middleware.RateLimit(yamlRL)).
+				Post("/policies", h.HandleCreateAssurancePolicy)
+			asr.With(middleware.RequireAdmin, middleware.RateLimit(yamlRL)).
+				Put("/policies/{id}", h.HandleUpdateAssurancePolicy)
+			asr.With(middleware.RequireAdmin, middleware.RateLimit(yamlRL)).
+				Delete("/policies/{id}", h.HandleDeleteAssurancePolicy)
+		})
	})
}
```

   `yamlRL` is already in scope from routes.go L651–654. CSRF (`X-Requested-With`) and
   authentication come from the enclosing `ar` router, unchanged.

**Resulting API surface:**

| Method | Path | Access |
|---|---|---|
| `GET` | `/api/v1/velero/assurance/status` | Any authenticated user; counts RBAC-filtered |
| `GET` | `/api/v1/velero/assurance/exceptions?state=open&limit=&offset=` | Any authenticated user; namespace + SSAR filtered |
| `GET` | `/api/v1/velero/assurance/policies` | Admin |
| `POST` | `/api/v1/velero/assurance/policies` | Admin, rate-limited, audited |
| `PUT` | `/api/v1/velero/assurance/policies/{id}` | Admin, rate-limited, audited |
| `DELETE` | `/api/v1/velero/assurance/policies/{id}` | Admin, rate-limited, audited |

**Named tests — `assurance_handler_test.go`** (the first HTTP tests in this package —
build the router with `chi.NewRouter()` and a `*Handler` whose `AccessChecker`/
`RBACChecker` are fakes, matching the `externalsecrets/handler_test.go` approach):

| Test | Maps to |
|---|---|
| `TestAssuranceExceptions_NonAdminSeesOnlyVisibleNamespaces` | **Cross-user access**, master-plan scenario 1 |
| `TestAssuranceExceptions_CountsAreComputedAfterFiltering` | Count-as-oracle guard |
| `TestAssuranceExceptions_ClusterScopedExceptionsAreAdminOnly` | Privilege |
| `TestAssuranceExceptions_NonAdminNeverReceivesPrivilegedDetailKeys` | **Secret/sensitive-field suppression** |
| `TestAssuranceExceptions_UserWithNamespaceButNoVeleroAccessSeesNothing` | SSAR gate |
| `TestAssuranceExceptions_RevokedPermissionBetweenRequestsChangesResult` | **Revoked permission** |
| `TestAssuranceExceptions_NoUserReturns401` | Auth |
| `TestAssurancePolicies_NonAdminForbidden` | **Cross-user access** |
| `TestAssurancePolicies_InvalidThresholdRejected` | Master-plan scenario 2 |
| `TestAssurancePolicies_MaxAgeBelowFloorRejectedWithFieldError` | Master-plan scenario 2 |
| `TestAssurancePolicies_DuplicateScopeReturns409` | Unique index surfaced |
| `TestAssurancePolicies_StaleRevisionReturns409` | Optimistic concurrency |
| `TestAssurancePolicies_MalformedUUIDReturns400` | `{id}` validation |
| `TestAssurancePolicies_DeletedScheduleUIDStillRendersException` | Master-plan scenario 2 — deleted schedule UID |
| `TestAssurancePolicies_DisabledPolicyIsReturnedButProducesNoExceptions` | Policy disable semantics |
| `TestAssurance_NilAssuranceServiceReturns503WithReason` | **Unavailable DB**, master-plan scenario 2 |
| `TestAssurance_NilAssuranceServiceNeverReturns404` | Truthful capability reporting |
| `TestAssuranceStatus_ReportsCollectionFreshnessAndLeaseHolder` | Observability |
| `TestAssuranceStatus_UnknownWhenLastCollectionFailed` | **The hard rule at the API layer** |
| `TestAssurancePolicyWrite_IsAudited` | Audit contract |
| `TestAssurancePolicyWrite_RequiresCSRFHeader` | CSRF |
| `TestAssuranceExceptions_RequestCancellationReturnsWithoutPanic` | **Cancellation** |
| `TestAssuranceRoutes_ExposeNoMutatingClusterOperation` | **Release boundary** — walks the registered `/velero/assurance` routes and asserts every one is GET except the three admin policy writes, none of which touch Kubernetes |

**Verification:** `cd backend && go vet ./... && go test ./...`
and `bash scripts/check-cluster-routing.sh` (Verification Contract row for new
cluster-aware handlers).

**Exit criteria / done means:**

- [ ] `server.go` is not in the diff.
- [ ] Every exception read is filtered by namespace **and** SSAR, with counts post-filter.
- [ ] A DB-less deployment returns 503 with `reason: "database_unavailable"` on all six
      endpoints, never 404.
- [ ] No endpoint can create, patch or delete a Kubernetes object.
- [ ] `go vet ./... && go test ./...` clean repo-wide; routing guard passes.

---

## U36. Recovery readiness UI

**Branch:** `feat/u36-backup-assurance-ui`
**PR title:** `feat(frontend): backup assurance readiness page`
**Covers:** R22, R23; AE8. **Depends on:** U35.

**Files (5)** *(master plan listed `VeleroDashboard.tsx`; swapped for
`frontend/lib/constants.ts`, without which the page is unreachable from navigation —
see correction C6. The dashboard cross-link moves to U36b)*:

1. `frontend/islands/BackupAssurance.tsx` — new
2. `frontend/lib/backup-assurance-types.ts` — new
3. `frontend/routes/backup/assurance.tsx` — new
4. `frontend/lib/constants.ts` — modified
5. `e2e/tests/backup-assurance.spec.ts` — new

**Steps.**

1. `frontend/lib/backup-assurance-types.ts` — the wire contract plus the pure display
   helpers (kept out of the island so `deno task test` can cover them in U36b):

```ts
export type AssuranceCondition =
  | "overdue" | "failed" | "partially_failed" | "paused"
  | "never_run" | "location_unavailable" | "collection_unknown";

export type AssuranceState = "open" | "resolved";
export type Collection = "ok" | "degraded" | "failed";

/** The six-state vocabulary from R3. `unknown` is never rendered as `empty`. */
export type SurfaceState =
  | "ok" | "stale" | "empty" | "unknown" | "forbidden" | "unavailable";

export interface AssuranceException {
  id: string;
  subjectKind: "schedule" | "namespace" | "cluster";
  subjectNamespace: string;
  subjectName: string;
  subjectUid: string;
  condition: AssuranceCondition;
  state: AssuranceState;
  severity: "info" | "warning" | "critical";
  openedAt: string;
  lastObservedAt: string;
  resolvedAt?: string;
  observationCount: number;
  lastSuccessAt?: string;
  detail: {
    lastOutcome: "success" | "partial" | "failure" | "in_flight" | "unknown";
    expectedRunAt?: string;
    expectedRunKnown: boolean;
    cronParseError?: string;
    suppressedBy?: string;
    // admin-only, absent for non-admins:
    storageLocation?: string;
    bslMessage?: string;
    failureReason?: string;
  };
}

export interface AssuranceStatus {
  enabled: boolean;
  detected: boolean;
  databaseAvailable: boolean;
  lastRunAt?: string;
  lastCollection: Collection;
  leaseHolder?: string;
  policyCount: number;
  openExceptionCount: number;
  deliveryBacklog: number;
}

/** Never returns "empty" when collection was not ok — that is the honesty rule. */
export function surfaceStateFor(status: AssuranceStatus, exceptions: AssuranceException[]): SurfaceState;
export function conditionLabel(c: AssuranceCondition): string;
export function conditionExplanation(c: AssuranceCondition): string;
```

2. `frontend/islands/BackupAssurance.tsx` — a Preact island following the
   `ESOStoreMetricsPanel.tsx` styling convention (Tailwind utility classes and
   `var(--…)` tokens, **not** the 33 inline `style={{}}` blocks that
   `VeleroDashboard.tsx` still carries; new code follows CLAUDE.md, not the legacy file).
   Data fetch mirrors `VeleroDashboard.fetchData`:

```ts
const [statusRes, exRes] = await Promise.all([
  apiGet<AssuranceStatus>("/v1/velero/assurance/status"),
  apiGet<AssuranceException[]>("/v1/velero/assurance/exceptions?state=open"),
]);
```

   Layout:
   - **Honesty banner** (`.glass-bar` chrome, non-dismissible, first element):
     *"Backup assurance observes what Velero reported. It does not verify that a restore
     would succeed. No restore has been attempted."*
   - **Collection freshness row**: "Last evaluated <age> ago" plus the collection state
     word. When `lastCollection !== "ok"`, this row reads **"Backup state is unknown —
     Velero could not be observed"** and every count below is greyed with an
     `unknown` badge, **never** rendered as zero.
   - **Open exceptions list**, grouped by condition, each card carrying:
     subject, condition label, plain-language explanation, `openedAt` age,
     `observationCount`, `lastSuccessAt` (or "no successful backup observed"), and
     — when `expectedRunKnown === false` — the line *"Next expected run is not
     computable for this schedule expression."*
   - **Policies section** (admin only): list, create, edit, delete, using the existing
     `Button` and form primitives.
   - **Actions**: a single link, *"Start a restore (manual, unverified)"* →
     `/backup/restores/new`, visually separated from the exception cards.
   - **Empty states, all distinct**: `policyCount === 0` → *"No freshness policies
     configured. Backup assurance evaluates nothing until you add one."*;
     `detected === false` → *"Velero was not detected"*; `databaseAvailable === false` →
     *"Backup assurance requires PostgreSQL"*; 403 → *"You do not have permission to
     view backup exceptions"*; zero open exceptions with `lastCollection === "ok"` →
     *"No open backup exceptions. This is not a recoverability guarantee."*
   - Tables and cards are **solid** surfaces; `.glass*` is used only on the banner and
     the page header bar (CLAUDE.md: glass is chrome-only).
   - No aggregate score, no percentage, no shield icon anywhere.

3. `frontend/routes/backup/assurance.tsx` — five lines, matching
   `frontend/routes/backup/schedules.tsx`:

```tsx
import { define } from "@/utils.ts";
import BackupAssurance from "@/islands/BackupAssurance.tsx";

export default define.page(function BackupAssurancePage(_ctx) {
  return <BackupAssurance />;
});
```

4. `frontend/lib/constants.ts` — add the nav item to the existing `backup` section
   (anchor at L465–479):

```ts
  {
    id: "backup",
    label: "Backup",
    icon: "archive",
    href: "/backup/backups",
    groups: [
      {
        header: "Protection",
        items: [
          { label: "Backups", href: "/backup/backups" },
          { label: "Restores", href: "/backup/restores" },
          { label: "Schedules", href: "/backup/schedules" },
+         { label: "Assurance", href: "/backup/assurance" },
        ],
      },
    ],
  },
```

5. `e2e/tests/backup-assurance.spec.ts` — in `e2e/tests/` (the only collected directory;
   see correction C7), following the `certificates.spec.ts` template.

**Named specs — `e2e/tests/backup-assurance.spec.ts`:**

| Spec | Maps to |
|---|---|
| `backup assurance page loads and shows the honesty banner` | Honesty rule 1 |
| `AE8: an exception opened by background evaluation is visible to a fresh client` | **AE8** — seeds a policy via the API, waits ≥ 1 tick, reloads with a new browser context, asserts one exception card |
| `AE8: reloading the page does not change the exception count` | **AE8** |
| `no policies renders the explicit no-policies empty state` | Empty state |
| `Velero absent renders "not detected", not zero backups` | Honesty rule 3 |
| `collection unknown renders "unknown", never "no backups exist"` | **The hard rule** |
| `paused schedule renders paused, not overdue` | Freshness rules |
| `partially failed is shown as its own chip and excluded from success counts` | Honesty rule 4 |
| `non-admin cannot see or edit policies` | **Cross-user access** |
| `permission loss mid-session renders forbidden, not empty` | **Revoked permission** |
| `assurance page exposes no restore trigger` | **Release boundary** — asserts no button whose accessible name matches `/restore now|run restore|rehearse/i` |
| `assurance nav item is reachable from the sidebar` | C6 |

All specs open with the `certificates.spec.ts` guard:
`const s = await request.get("/api/v1/velero/assurance/status"); test.skip(!(await s.json()).data?.enabled, "backup assurance not enabled")`.

**Bonus coverage, free:** `e2e/tests/api-routes.spec.ts` auto-discovers every literal
`/v1/...` string in `frontend/{islands,lib,routes,components}` and asserts a non-404
response. The two literals added in step 2 are therefore route-contract-tested with no
edit to that spec.

**Verification:**

```
cd frontend && deno task check && deno task test && deno task build
cd e2e && npm test
```

**Exit criteria / done means:**

- [ ] `/backup/assurance` is reachable from the sidebar and renders the honesty banner
      above the fold.
- [ ] Six-state vocabulary is used; `unknown` never renders as `empty` or as a zero count.
- [ ] No inline `style={{}}` in the new island; Tailwind utilities and `var(--…)` tokens
      only; `.glass*` on chrome only.
- [ ] No aggregate health score anywhere on the page.
- [ ] `deno task check`, `deno task test`, `deno task build` and `npm test` all pass.

---

## U36b. Dashboard cross-link, honesty copy and display-helper tests

**Split rationale:** U36 is at the five-file cap and carries no `deno task test` unit
file, which the Verification Contract requires for "new helpers". This unit adds that
coverage, the `VeleroDashboard` cross-link, and the TypeScript half of U33's dead-field
removal.

**Branch:** `feat/u36b-backup-assurance-crosslink`
**PR title:** `feat(frontend): backup overview assurance cross-link and assurance helper tests`
**Covers:** R3, R22. **Depends on:** U36.

**Files (4):**

1. `frontend/lib/backup-assurance-types_test.ts` — new
2. `frontend/islands/VeleroDashboard.tsx` — modified
3. `frontend/lib/velero-types.ts` — modified
4. `e2e/tests/backup-assurance.spec.ts` — modified

**Steps.**

1. `frontend/lib/velero-types.ts` — remove `lastBackupPhase?: string;` (L61). Completes
   U33's Step-0 dead-field removal; the Go declaration is already gone and the field was
   never populated on either side.
2. `frontend/islands/VeleroDashboard.tsx` — **additive only**. The file is 738 LOC, above
   Agent Directive 1's 300-LOC threshold, but this unit performs **no structural
   refactor**: it adds one summary strip to the `initialTab === "overview"` branch
   (currently at L337–346) containing the open-exception count, the last-evaluated age,
   a link to `/backup/assurance`, and the one-line honesty statement *"Freshness is not
   demonstrated recoverability."* No props are added, no state is lifted, no existing
   JSX is restructured. If review finds the change needs restructuring, **stop and file
   a Step-0 cleanup PR first** rather than growing this one.
3. `frontend/lib/backup-assurance-types_test.ts` — Deno tests for the pure helpers.

**Named tests:**

| Test | Maps to |
|---|---|
| `Deno.test("surfaceStateFor returns unknown when collection failed")` | **The hard rule in the client** |
| `Deno.test("surfaceStateFor never returns empty when collection failed")` | **The hard rule** |
| `Deno.test("surfaceStateFor returns empty only with ok collection and zero exceptions")` | Distinct empty |
| `Deno.test("surfaceStateFor returns forbidden on a 403 status shape")` | **Revoked permission** |
| `Deno.test("surfaceStateFor returns unavailable when databaseAvailable is false")` | **Unavailable DB** |
| `Deno.test("conditionLabel covers every AssuranceCondition")` | Exhaustiveness — fails when a condition is added without copy |
| `Deno.test("conditionExplanation never claims recoverability")` | **Honesty rule** — asserts no explanation string matches `/recoverab|restorable|guarantee|protected/i` |
| `Deno.test("conditionExplanation for collection_unknown does not say no backups exist")` | **The hard rule** |

**e2e additions:** `backup overview links to assurance and states freshness is not
recoverability`.

**Verification:**

```
cd frontend && deno task check && deno task test && deno task build
cd e2e && npm test
```

**Exit criteria / done means:**

- [ ] `grep -rn "lastBackupPhase" .` returns nothing repo-wide.
- [ ] The `conditionLabel` exhaustiveness test fails if a condition is added without copy.
- [ ] `VeleroDashboard.tsx` diff is additive: no removed lines except within the new block.
- [ ] All four frontend/e2e commands pass.

---

## U37. Named rehearsal profile — documentation and fixture only

**Branch:** `docs/u37-recovery-profile-specification`
**PR title:** `docs(recovery): specify the first rehearsal profile and validation contract`
**Covers:** R24, R25; KTD12. **Depends on:** Q3 resolved; U32–U36 shipped.

> **THIS UNIT AUTHORIZES NO LIVE RESTORE.**
>
> **Rehearsal execution remains disabled. Nothing in this unit adds, enables, or
> prepares any code path that creates a `velero.io/v1 Restore` object. No backend
> package, no route, no island, no Fastlane lane, no Helm template, and no CI job is
> added or modified. The three deliverables are a Markdown specification, a Markdown
> runbook, and a YAML fixture that is never applied to any cluster by any automated
> process. Execution stays gated on Q3, and beyond Q3 on the six Deferred Appendix
> milestones, each of which requires its own units and its own approval.**

**Files (3):**

1. `docs/plans/recovery-profile-validation.md` — new
2. `e2e/fixtures/recovery-profile.yaml` — new
3. `docs/runbooks/recovery-rehearsal.md` — new

**Deliverable 1 — `docs/plans/recovery-profile-validation.md`.** Records the answers to
Q3 as a reviewed contract: exact Kubernetes / Velero / plugin / CSI versions on both
source and destination; source backup portability analysis; destination trust boundary;
storage-class and volume-snapshot-class mappings; the allow-list of namespaces and
resource kinds; external-dependency suppression (webhooks, controllers, ingress,
DNS, outbound integrations); the application validation checks and what each one does
and does **not** establish; cleanup ownership; and the data-handling policy for any
production data that lands in the destination. Every claim cites either the operator's
supplied inventory or official documentation, with the Velero version matching the
actual installation — never assuming 1.18.

**Deliverable 2 — `e2e/fixtures/recovery-profile.yaml`.** A **declarative, inert**
description of the profile: identities, versions, mappings, allowed resources,
validation checks, cleanup ownership. It is a fixture for future preflight *validation
tests*, not a manifest. It carries a top-of-file comment stating it is never applied,
and it lives under `e2e/fixtures/` where `playwright.config.ts` runs only
`/.*\.setup\.ts/` — so Playwright will not execute it.

**Deliverable 3 — `docs/runbooks/recovery-rehearsal.md`.** The human procedure, written
as an operator checklist with an explicit gate at the top: *"Do not proceed past
Preflight until the six Deferred Appendix milestones have shipped and the profile in
`recovery-profile-validation.md` has been signed off."* It documents what a rehearsal
would establish, what it would not, and the abort conditions.

**Exactly what the operator must supply to answer Q3:**

1. **Destination identity** — cluster name, API server endpoint, how it is administered,
   and by whom. Reachability is not isolation, and namespace remapping is not isolation.
2. **Destination versions** — Kubernetes server version, Velero server version, every
   installed Velero plugin and its version, the CSI driver(s) and their versions,
   snapshot-controller version.
3. **Source versions** — the same list for the cluster the backup came from, plus the
   backup's Velero version, because restore compatibility is a function of both.
4. **Storage mapping** — every StorageClass named in the source backup, mapped to a
   StorageClass that exists on the destination; same for VolumeSnapshotClass. Any
   unmapped class is an AE9 preflight blocker by name.
5. **Backup storage location access** — whether the destination can read the source BSL
   bucket, with which credentials, and whether those credentials are read-only.
6. **Resource allow-list** — the exact namespaces and resource kinds permitted in the
   restore, and the kinds explicitly excluded (Secrets, ServiceAccounts, PVs, CRDs,
   webhooks, anything with cluster-wide effect).
7. **Side-effect suppression** — which controllers, admission webhooks, ingress
   controllers, GitOps agents, external integrations and outbound notifiers must be
   suppressed in the destination before a restore, and the mechanism for each.
8. **Application validation checks** — the concrete readiness and data assertions that
   define "the application recovered", the credentials each needs, the network
   destinations each may reach, and what each check does **not** prove.
9. **Data-handling policy** — whether production data may exist in the destination, for
   how long, who may access it, how it is destroyed, and which regulatory constraints
   apply.
10. **Cleanup ownership** — the named human or process responsible for cleanup, the
    retention decision, and the escalation path when a finalizer wedges a namespace.
11. **Cadence and approval** — who authorizes each rehearsal, and how a rehearsal is
    aborted mid-flight.

**Test expectation for this unit:** review the fixture against official documentation
and the operator's supplied inventory. **No live restore is performed.** The later
controlled rehearsal must demonstrate AE9 plus the application and data checks before
scheduling exists.

**Exit criteria / done means:**

- [ ] The diff contains only the three files listed. `git diff --stat` shows no `.go`,
      `.tsx`, `.ts`, `.sql`, `.yaml` under `helm/`, or `.yml` under `.github/`.
- [ ] All eleven Q3 inputs are answered with operator-supplied facts, not defaults.
- [ ] Both documents state, in bold, that execution is not authorized.
- [ ] The fixture is not referenced by any test, task, or workflow.

---

## Cross-Unit Sequencing and Conflict Notes

**Merge order (each unit is one PR; do not parallelise across a shared file):**

```
U32 ──► U32b ──► U33 ──► U34a ──► U34b ──► U34c ──► U35 ──► U36 ──► U36b
                                                                      │
                                                        (Q3 resolved) ▼
                                                                     U37
```

U33 depends on U32 only for the `store` enum types, so U33 may be developed in parallel
with U32b but must merge after U32.

**Shared-file map.**

| File | Touched by | Conflict risk | Mitigation |
|---|---|---|---|
| `backend/internal/store/migrations/` | U32 only | **Cross-track** — other tracks own 000018–000021 | Sequence `000022` is reserved for Release F and is stated in this plan's front matter. Re-check `ls migrations/` immediately before creating the files (Rollout guidance: "Reserve migration sequences immediately before implementation"). |
| `backend/internal/store/migrations/NOTES.txt` | U32 only | Low; append-only | Append a new section at the end; never edit existing sections. |
| `backend/internal/notifications/service.go` | U34a only | **Highest in this release** | See below. |
| `backend/internal/notifications/types.go` | U34a only | Low; additive enum | — |
| `backend/internal/velero/types.go` | U33 only | Low | — |
| `backend/internal/velero/handler.go` | U33 (parser UID lines), U34c (one field) | Medium — two units, two hunks, far apart | U33 merges first; U34c rebases. Both hunks are additive; neither restructures. |
| `backend/cmd/kubecenter/main.go` | U34c only | **Cross-track** — Releases A–E also wire into `main.go` | The Release F block is a contiguous ~20 lines between the cert-manager and ESO sections, anchored on quoted surrounding lines. If another track lands first, re-read the file (Agent Directive 6) and re-anchor before editing. |
| `backend/internal/server/routes.go` | U35 only | **Cross-track** — every release adds routes | The Release F insertion is entirely inside the existing `registerVeleroRoutes` function after the `vr.Get("/locations", …)` line; other tracks add new `register*Routes` functions elsewhere in the file. Overlap is unlikely but re-read before editing. |
| `backend/internal/server/server.go` | **Nobody** | None | Correction C4: `VeleroHandler` already exists in both `Server` and `Deps`. |
| `frontend/lib/constants.ts` | U36 only | **Cross-track** — Release A adds a "Pinned" nav section | One-line insertion in the `backup` group's `items` array; Release A touches a different section. |
| `frontend/islands/VeleroDashboard.tsx` | U36b only | Low | Additive only; U36 deliberately does not touch it. |
| `e2e/tests/backup-assurance.spec.ts` | U36 (create), U36b (extend) | Low; sequential | — |
| `.github/workflows/fuzz.yml` | U33 or U34c | Low; one matrix row | Decide in U33's review; state the choice in the PR body. |

**The `notifications/service.go` risk, and how tests cover it.**

`service.go` is shared by all eleven `Source` values and eight in-tree emit sites
(`alerting`, `policy`, `gitops`, `scanning`, `diagnostics`, `velero`, `certmanager`,
`externalsecrets`, plus `ClusterProber` via the closure at main.go L736–751 and
`limits.Checker`). U34a changes two things there: the `Emit` → `EmitSync` refactor and
the `suppressResourceFieldsBySource` entry. Both can regress other sources.

Coverage, all in `service_test.go` and all additive:

1. `TestEmit_DelegatesToEmitSyncWithIdenticalObservableBehaviour` is a **table over all
   eleven `Source` values**. For each it asserts the persisted feed row, the stripped
   WebSocket payload (`id/source/severity/title` only, per `persistAndBroadcast`
   L179–201), and the set of channels dispatched to are identical to the pre-change
   behaviour captured as golden values.
2. `TestSanitizeForEmailDigest_OtherSourcesUnaffected` enumerates the other ten sources
   and asserts their `ResourceKind`/`ResourceNS`/`ResourceName` survive the digest.
3. `TestSanitizeForEmailDigest_ExistingVeleroCacheNotificationUnaffected` pins the
   pre-existing `InvalidateCache` notification shape, so the one Velero emit site that
   predates Release F is proven unchanged rather than argued unchanged.
4. `TestSourceVelero_StillValid` pins `Source.Valid()`, which the rule editor depends on
   (`types.go` L21–34) — a broken `Valid()` would silently reject existing saved rules.
5. The existing `service_test.go` file is **extended, never edited**. Any diff line that
   modifies an existing test is a review stop-sign: it means behaviour changed.

**Release-boundary guard, enforced in three places.** `TestAssurance_NeverConstructsARestore`
(U34b), `TestAssuranceRoutes_ExposeNoMutatingClusterOperation` (U35) and the e2e spec
`assurance page exposes no restore trigger` (U36) each independently assert that
Release F cannot start a restore. Three layers, because one regression in this area is
the difference between an observation feature and an unreviewed production mutation.

---

## Deferred Appendix — Recovery Rehearsal Delivery

**Status: requirements only. Not planned, not scheduled, not authorized.** Each
milestone must be split into at-most-five-file units *after* its gate resolves. The
lifecycle is: draft profile → preflight → restore requested → restoring → validating →
passed/failed/inconclusive → cleanup pending → cleaned/cleanup failed. Operator
cancellation stops future steps; it does not promise reversal of already-created
resources. Cleanup is separately authorized and ownership-constrained.

| # | Milestone | Requirement | Gate | Acceptance evidence |
|---|---|---|---|---|
| 1 | **Profile and preflight** | Exact source/destination identities and versions; storage and plugin mappings; allowed namespaces and resource kinds; side-effect suppression; data-handling policy. Preflight is a pure validation pass that creates nothing. | Q3 answered; U37 signed off | Unsupported storage class or a missing destination API blocks **before** any restore resource is created; a destination mismatch blocks; the blocker names the missing mapping (**AE9**) |
| 2 | **Durable execution record** | Persist profile revision, backup UID, initiating identity, service authorization, phase, ownership labels, timestamps, observed results. Never persist a user bearer credential for later use (KTD12). | Milestone 1 | A crash or restart does not start a second restore; unknown outcomes are reconciled by recorded object identity, not by name |
| 3 | **Manual restore orchestration** | Create only reviewed resources in the designated destination, using supported Velero behaviour; retain actual warnings and errors verbatim. Opt-in per execution; no scheduling. | Milestone 2 | **AE9**; no production resources created; existing-resource conflicts reported rather than silently overwritten |
| 4 | **Application validation** | Explicit readiness and workload-specific data checks with bounded credentials and an explicit network-destination allow-list. Restore completion and application health are separate outcomes. | Milestone 3 | A restore may complete while application checks fail; both are reported independently; neither is presented as an RPO/RTO claim (R25) |
| 5 | **Cleanup** | Ownership-based inventory, preview before deletion, explicit retention choice, and a separately reported cleanup outcome. | Milestone 4 | Cleanup never deletes pre-existing resources; stuck finalizers surface as operator-intervention items, not as silent failures |
| 6 | **Scheduling** | Only after repeated successful manual exercises. Explicit service identity, retry policy, and disable path. | Milestones 1–5, plus a documented record of repeated manual successes | Restart, disabled profile, credential rotation and a changed destination never trigger a stale intent |

Proposed implementation owners when these unblock: `backend/internal/recovery/`,
`backend/internal/store/recovery_runs.go`, additive migrations (sequence reserved at
that time, **not** 000022), `frontend/islands/RecoveryRehearsal.tsx`, and matching
backend and E2E tests. Do not create all of them in one unit.

Velero documents namespace mappings, existing-resource handling, and limitations around
restoring data into existing PVCs; those constraints are what make milestone 1 a hard
gate. Consult the documentation matching the **actual** installation — the master plan's
citation of the Velero 1.18 restore reference is research evidence, not a statement that
1.18 is installed.

---

## Risks and Open Items

| # | Risk | Severity | Mitigation / status |
|---|---|---|---|
| R-1 | **Unrecovered panic on the collector goroutine terminates the process.** The collector runs outside chi's recovery middleware. | Critical | `recoverutil.Tick` on the tick body and `recoverutil.Safe` on the shutdown lease release, both visible in U34b's steps. `Start` is the only goroutine the unit creates — no `WaitGroup`, no channels, no `errgroup` — so neither the `wg.Done()`-outside rule nor the counted-send hazard can be violated. `TestAssurance_PanicInEvaluateIsRecoveredAndLoopContinues` proves it. |
| R-2 | **The 15-minute dedup key omits `cluster_id` and UID** (`store.go` L49–66), so two clusters or a recreated schedule can silently suppress each other's notifications. | High | Release F never relies on it for identity. The exception row is the identity of record; the window is a second layer only. Not fixed for other sources in this release — flagged for a future notifications PR. |
| R-3 | **`values.yaml` claims `replicaCount: 1` is "safe to increase (no single-writer constraint)"**, which is already false for the ESO orphan reaper (main.go L820–823). Release F must not add a second false assumption. | High | The lease plus the two partial `UNIQUE` indexes make the assurance collector genuinely replica-safe. The pre-existing ESO contradiction is **not** fixed here; it is recorded so a future scale-out PR knows it exists. |
| R-4 | **`suppressResourceFieldsBySource[SourceVelero] = true` changes the email digest for the pre-existing `InvalidateCache` notification.** | Medium | Proven a no-op by `TestSanitizeForEmailDigest_ExistingVeleroCacheNotificationUnaffected` (that notification sets no resource fields). All future Velero notifications inherit the suppression by design; documented in the map comment. |
| R-5 | **Cron next-expected-run cannot be computed for every expression**, and the k8sCenter process timezone may differ from the Velero controller's. | Medium | `ExpectedRunsSince` returns `known = false` rather than guessing; the API and UI say so; `max_age` always applies as a floor; `grace_seconds` defaults to 3600 to absorb DST. No unit infers a fixed interval from an arbitrary cron. |
| R-6 | **`CollectionDegraded` is unreachable from the current collector**, because `doFetchAll` uses `errgroup.WithContext` and fails the whole group on the first list error. | Low | The evaluator supports it and is tested for it; `collect` documents that it currently produces only `ok` or `failed`. Surfacing per-list errors is a future, separate change to `doFetchAll`. |
| R-7 | **`e2e/velero.spec.ts`, `namespace-limits.spec.ts` and `flux-notifications.spec.ts` sit at the `e2e/` root and are never collected** (`testDir: "./tests"`). | Medium | New spec goes in `e2e/tests/`. The pre-existing orphans are **not** moved by Release F — moving them would surface unknown failures inside an unrelated PR. Recorded here so a dedicated PR can pick it up. |
| R-8 | **`velero/handler.go` is 1419 LOC and `VeleroDashboard.tsx` is 738 LOC.** Agent Directive 1 requires a separate Step-0 cleanup before any structural refactor of either. | Medium | No unit in Release F performs a structural refactor of either file: U34c adds one struct field, U33 adds two `obj.GetUID()` lines, U36b adds one JSX block. If any of those turns out to require restructuring, **stop and file a Step-0 PR first**. |
| R-9 | **Migration sequence collision** with a parallel track. | Medium | `000022` is reserved for Release F in this plan's front matter and in the U32 steps. Re-run `ls backend/internal/store/migrations/` immediately before creating the files. |
| R-10 | **Down-migrating 000022 re-notifies.** Dropping the tables discards open-exception state; the next collector start re-opens every still-true condition and emits once for each. | Low | Documented in `NOTES.txt` per the U32 steps. This is correct behaviour, not a defect, but it must not surprise an operator during a rollback. |
| R-11 | **Nine PRs for one release** raises rebase pressure across `main.go`, `routes.go` and `constants.ts` against other tracks. | Medium | Strict sequential merge order; Agent Directive 6 (re-read before editing) applies to every shared-file edit; every anchor in this plan is quoted from the current file so a re-anchor is mechanical. |
| **O-1** | **Open — Q3 is unanswered.** The named rehearsal profile (destination, versions, storage mappings, validation checks, cleanup ownership) is an operator input. | — | Rehearsal execution stays disabled. U37 is documentation and fixture only. The eleven required inputs are enumerated in the U37 section. Release F ships without it: Q3 "does not block backup assurance". |
| **O-2** | **Open — default policy shape.** This plan proposes `max_age` per policy with `treat_partial_as = 'failure'` and `grace = 3600` as defaults, and **no** policy created at install. An operator may prefer an auto-seeded per-schedule policy. | — | Decide before U35 ships, because it changes the empty-state copy and the create flow. The plan's default — install creates nothing — is chosen so that installing k8sCenter never starts paging anyone. |
| **O-3** | **Open — exception retention.** 90 days is proposed to match `notifications.runRetention`. Q1's retention decision governs other tracks' evidence but does not formally cover backup exceptions. | — | Confirm before U32 ships; it is a one-line constant and a `NOTES.txt` sentence. |
| **O-4** | **Open — remote clusters.** Release F is local-cluster only, matching every existing poller ("the platform doesn't poll remote clusters; that runs in each cluster's own deployment"). Remote backup assurance would need U7–U12. | — | Out of scope; stated so nobody reads the `cluster_id` column as evidence that remote clusters are covered. |
