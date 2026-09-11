---
title: "Release B — ESO Evidence Completion — Implementation Plan"
parent_plan: docs/plans/2026-09-10-1315-feat-feature-expansion-plan.md
release: B
units: U13–U19
migration_sequence: 000019
date: 2026-09-10
---

# Release B — ESO Evidence Completion

Implements master-plan Release B (`U13`–`U19`) covering R1, R2, R3, R11, R12, R13
and KTD7, with acceptance examples AE4 and AE5.

**One PR per unit. Max 5 files per unit (tests included).** Two units are split
(`U14` → `U14a`/`U14b`, `U19` → `U19a`/`U19b`) to hold that ceiling. Final count:
**9 units**.

---

## Codebase Findings

Every row below was read in full or in the cited range during planning.

| File / range | What it constrains |
|---|---|
| `backend/internal/store/eso_history.go` (1–175) | `ESOSyncHistoryEntry` already carries `ClusterID`. `Insert` binds `cluster_id` and uses `ON CONFLICT (uid, attempt_at)`. `QueryByUID(ctx, uid, limit)` and `LatestByUID(ctx, uid)` have **no `cluster_id` predicate**. Both are **dead code — zero callers repo-wide** (verified by grep), so U13 may change their signatures freely. `Cleanup` is global and stays. The doc comment already states the future key-name RBAC contract that AE4 formalises. |
| `backend/internal/store/migrations/000011_create_eso_sync_history.up.sql` | Full current DDL (reproduced below). `cluster_id TEXT NOT NULL DEFAULT 'local'` **already exists**. |
| `.../000012_eso_sync_history_attempt_at_index.{up,down}.sql` | Adds the standalone `(attempt_at)` index for the retention DELETE. Must survive 000019. |
| `.../000013_create_eso_bulk_refresh_jobs.up.sql`, `.../000014_unique_active_bulk_jobs.up.sql` | Precedent for "drop a non-unique partial index, create a unique one" inside a single migration file. 000019 follows the same shape. |
| `.../migrations/NOTES.txt` | Operator-facing note format; 000016/000017 set the precedent for documenting a **binary-rollback constraint**. 000019 needs an entry (its `down` can fail). |
| `backend/internal/store/migrate.go` | golang-migrate + `iofs` embedded FS, `m.Up()` only, dirty-state guard. Each file runs inside one transaction ⇒ **`CREATE INDEX CONCURRENTLY` is illegal in 000019**. |
| `backend/internal/store/` (directory listing) | **Zero `*_test.go` files.** There is no PostgreSQL test harness anywhere in the repo (`grep pgxpool/pgxmock/testcontainers --include=*_test.go` → only `externalsecrets/persist_test.go`, which fakes at a higher layer). U13 must create the harness. |
| `backend/internal/externalsecrets/handler.go` (1–340, 700–780) | `Handler` struct + optional-dependency pattern (`BulkJobStore`, `MonitoringDisc` are nil-tolerant fields set in `main.go`, not constructor args). Test seams are unexported func fields (`dynForUserOverride`, `clientForUserOverride`). `canAccess` hardcodes `GroupName` ⇒ a **second** helper is required for `core/secrets`. `getCached` = singleflight + 30s TTL. `HandleGetExternalSecret` reads **live** through the impersonating dynamic client (not the cache) — this matters for U19's poll loop. |
| `backend/internal/externalsecrets/types.go` (1–248) | GVRs are **hardcoded to `v1`** (`ExternalSecretGVR` … `PushSecretGVR`). All five API types expose `UID`. `GroupName = "external-secrets.io"`. |
| `backend/internal/externalsecrets/discovery.go` | 5-min `staleDuration` probe cache; probes `GroupName + "/v1"` only. Confirms the version-pinning constraint U15 must work around. |
| `backend/internal/externalsecrets/persist.go` (171–410) | `persistOne` writes `Reason: es.ReadyReason`, `Message: es.ReadyMessage` **verbatim** — controller free-form text is already in the table. `resolveDiffKeys` derives `diff_keys_*` from the **live Secret** (not from the ES spec), so key names can include keys the ES spec never mentions (`dataFrom`, templates). This is the exact reason AE4 gates them on `get secrets`. |
| `backend/internal/externalsecrets/actions.go` (whole) | `HandleForceSyncExternalSecret` → 202 `{"data":{"status":"force-syncing"}}` with **no baseline**. `patchForceSyncOnce` already performs a `Get` immediately before the `Patch` — the correct place to capture U19's baseline. `rejectNonLocalClusterWrite` is the local-only precedent. `errAlreadyRefreshing` / `errUIDDrifted` / `inFlightWindow`. |
| `backend/internal/externalsecrets/bulk_worker.go:33-36` | `BulkJobReadWriter` — the repo's narrow-interface-over-concrete-store pattern. U14a's history reader copies it. |
| `backend/internal/server/routes.go` (695–777) | Exact `registerExternalSecretsRoutes` body. `er.With(resources.ValidateURLParams).Get(...)` is the house style. |
| `backend/cmd/kubecenter/main.go` (775–840, 900–922) | `esoHistoryStore` is already built (`appstore.NewESOHistoryStore(dbPool)`) and passed **only** to the poller. `esoHandler.MonitoringDisc = monDiscoverer` at line 793 is the anchor for the new field assignments. `ExternalSecretsHandler: esoHandler` at line 913. |
| `backend/internal/config/config.go:26`, `defaults.go:11` | `Config.ClusterID` (`koanf:"clusterid"`), `DefaultClusterID = "local"`. The poller stamps rows with `cfg.ClusterID`; the read path must use the **same** value, not the literal `"local"`. |
| `backend/internal/k8s/resources/secrets.go` (1–40) | **The masking helper is `maskedSecret(*corev1.Secret) *corev1.Secret`, unexported, in package `resources`.** It masks `Data`/`StringData` values to `"****"` and strips `kubectl.kubernetes.io/last-applied-configuration`. It is **not reusable for history**: history persists *key names*, never values, so there is nothing for `maskedSecret` to mask. See correction C6. |
| `backend/internal/k8s/resources/access.go` (80–240) | `CanAccessGroupResource(ctx, clusterID, username, groups, verb, apiGroup, resource, namespace)`; the doc says **"For core API resources, pass apiGroup=''"**. `accessCacheTTL = 60 * time.Second` ⇒ a revoked `get secrets` grant persists in the projection decision for up to 60s. |
| `backend/internal/k8s/resources/adapter_events.go` (whole) | Events come from the **service-account informer cache**, all-namespace or per-namespace, filtered by **label** selector only. No `involvedObject` support at all. |
| `backend/internal/k8s/resources/handler.go` (114–129, 266–311) | `parseListParams` reads **only** `limit`, `labelSelector`, `continue`. `writeList` → `api.Response{Data, Metadata{Total, Continue}}`. `ValidateURLParams` rejects any `namespace`/`name` failing `ValidateK8sName` ⇒ it **cannot** be used on a route that uses `_` for cluster scope. |
| `backend/internal/k8s/resources/crud.go` (21–55) | Generic list path: RBAC pre-check → `adapter.ListFromCache` → `paginateAny`. Query params beyond the three are silently dropped. |
| `backend/internal/yaml/handler.go` (235–330, 350–380) | `HandleExport` = `GET /v1/yaml/export/{kind}/{namespace}/{name}`, `_` for cluster scope, impersonating dynamic client via `ClusterRouter`, **501 on remote**, blocks `secrets`, returns the YAML string in the standard envelope. `resolveGVR` matches **any** discovered resource by lowercase plural name and returns that group's **served** version — no hardcoded `v1`. |
| `backend/internal/httputil/response.go` | `WriteJSON` / `WriteError` (strips detail on 5xx) / `WriteData` / `WriteErrorWithReason(status, message, reason, extra)` / `RequireUser`. |
| `backend/pkg/api/*.go` | `Response{Data, Metadata, Error}`, `Metadata{Total, Continue, Page, PageSize}`, `APIError{Code, Message, Detail, Reason, Extra}`. |
| `docs/solutions/backend-resilience-conventions.md` (105–190) | Oracle taxonomy A/B/C/D; in-package `*_fuzz_test.go`; teeth-by-mutation; `-list` drift guard; hermetic (no DB/network) ⇒ a *cursor decoder* is fuzzable, a *DB query* is not. |
| `.github/workflows/fuzz.yml` | 18 matrix rows today, incl. `{ ./internal/externalsecrets/, FuzzExternalSecretsNormalizers }`. SHA-pinned actions; `-fuzztime=5m`. |
| `backend/internal/externalsecrets/normalize_fuzz_test.go` (1–60) | `unstructuredFromFuzz` helper + seed-corpus style to copy. |
| `frontend/islands/ESOExternalSecretDetail.tsx` (292 LOC) | Tabs at 135–164; placeholders at 265/274/283; real `ESOChainPanel` at 287. `onForceSync` (39–61) sets a one-shot string message — the thing U19 replaces. |
| `frontend/islands/ESOStoreDetail.tsx` (227), `ESOClusterStoreDetail.tsx` (223), `ESOClusterExternalSecretDetail.tsx` (276), `ESOPushSecretDetail.tsx` (246) | Identical 5-tab shells. Chain is real on Store/ClusterStore, placeholder on CES/PushSecret. |
| `frontend/lib/api.ts` (33–215) | `api<T>()` injects `Bearer` + `X-Cluster-ID` from the `selectedCluster` signal, auto-refreshes on 401, throws `ApiError{status, code, detail, reason, body}`; `errorExtra(err,key)`; `apiGet(path, signal?)` accepts an `AbortSignal`. |
| `frontend/lib/eso-api.ts`, `lib/eso-types.ts` | `esoApi` object of thin `apiGet`/`apiPost` wrappers; camelCase TS mirrors of the Go structs. |
| `frontend/lib/score-color_test.ts` | Deno test idiom: `import { assertEquals } from "jsr:@std/assert@1"` + flat `Deno.test("name", () => {})`. |
| `frontend/deno.json` | `check` = `deno fmt --check . && deno lint . && deno check`; `test` = `deno test -A`; `build` = `vite build`. |
| `frontend/islands/ResourceDetail.tsx` (415–445, 831–847, 1299) | Sends `involvedObjectKind` + `involvedObjectName`, assigns `res.data` straight to `events.value`, renders `<EventsTable>`. **No client-side filter.** |
| `e2e/tests/eso-templates.spec.ts`, `e2e/fixtures/base.ts`, `e2e/helpers.ts` | Spec idiom: `import { expect, test } from "../fixtures/base.ts"`, `test.describe`, `page.goto` + `waitForLoadState("networkidle")`, helpers `getAuthHeaders`, `e2eName`, `deleteResource`, `waitForTableLoaded`. |

### Where the master plan is wrong about this codebase

| # | Master-plan claim | Reality | Consequence |
|---|---|---|---|
| **C1** | U13: "Add cluster+UID query constraints… `NNNNNN_scope_eso_history`" implies a schema/column gap. | `cluster_id TEXT NOT NULL DEFAULT 'local'` has existed since **000011**. The defects are (a) `idx_eso_sync_history_dedup` is `UNIQUE (uid, attempt_at)` — cluster-blind, and (b) the **Go** methods filter on `uid` alone. | **No column add. No backfill.** 000019 is index-only, plus a Go rewrite. |
| **C2** | U15: "Provide ESO YAML and event adapters". | `GET /v1/yaml/export/{kind}/{namespace}/{name}` already does authorized, version-agnostic, impersonated YAML for **any** discovered GVR, already uses `_` for cluster scope, and already 501s on remote. All five ESO plural names (`externalsecrets`, `clusterexternalsecrets`, `secretstores`, `clustersecretstores`, `pushsecrets`) are globally unique. | U15 builds **events only**. YAML is a frontend wiring change in U16. |
| **C3** | U15/U16 implicitly assume the existing generic Events path is reusable. | `ResourceDetail.tsx:432-436` sends `involvedObjectKind`/`involvedObjectName`; `parseListParams` (`resources/handler.go:115`) ignores them; nothing filters client-side. **The existing Events tab shows every event in the namespace.** Reusing it would violate R1 (a recreated same-name object inherits prior evidence). | U15 must use an API-server `involvedObject.uid` field selector. The generic defect is a **separate follow-up**, not Release B scope (see Risk R-5). |
| **C4** | U13 lists `eso_history_test.go` as if a store test idiom exists. | `internal/store` has **no test files at all** and there is no PG harness. | U13 must create the harness: pure unit tests always-on, plus a `//go:build pgintegration` round trip gated on `KUBECENTER_TEST_DATABASE_URL`. |
| **C5** | U14's file list has exactly 5 entries, and the Verification Contract additionally demands fuzz coverage for "new unstructured parsing, redaction". | Adding `*_fuzz_test.go` + a `fuzz.yml` row would make U14 seven files. | Split into **U14a** (endpoint) and **U14b** (fuzz). Same reasoning splits U19 into backend + frontend. |
| **C6** | "U14's redaction must reuse the existing masking helper." | The only masking helper is `maskedSecret` in package `resources` — unexported, `*corev1.Secret`-typed, and it masks **values**. History has no values, only key names and controller text. There is nothing to reuse. | U14a introduces `sanitizeControllerText` + a projection function; the plan states explicitly that `maskedSecret` is the wrong tool and why, so review does not flag it as reinvention. `FuzzMaskedSecret` / `FuzzSecretPipeline` remain the guards on the actual Secret path. |
| **C7** | U15: "Never hardcode every kind to one API version" reads as advice. | `types.go` **does** hardcode `v1` in all five GVRs, and `discovery.go` probes `GroupName + "/v1"` only. | U15 must resolve the GVR from live discovery at request time, never from the package GVR vars. |
| **C8** | U16 and U15 both list `frontend/lib/eso-evidence.ts` as "new"; U16, U17, U18 and U19 all list `e2e/tests/eso-evidence.spec.ts` as "new". | Only one unit can create a file. | Resolved in **Cross-unit sequencing**. |
| **C9** | AE4 gates only *history* on Secret access. | ESO writes provider error text into `status.conditions[].message`, which routinely names the remote **path/key** (`"key 'prod/db/password' not found"`), and that text reaches the Events feed as well as history. | Release B applies the **same** projection to Events. |

### Current vs target DDL — `eso_sync_history`

**Current (000011 + 000012), verbatim:**

```sql
CREATE TABLE IF NOT EXISTS eso_sync_history (
    id                       BIGSERIAL PRIMARY KEY,
    cluster_id               TEXT NOT NULL DEFAULT 'local',
    uid                      TEXT NOT NULL,
    namespace                TEXT NOT NULL,
    name                     TEXT NOT NULL,
    attempt_at               TIMESTAMPTZ NOT NULL,
    outcome                  TEXT NOT NULL CHECK (outcome IN ('success','failure','partial')),
    reason                   TEXT NOT NULL DEFAULT '',
    message                  TEXT NOT NULL DEFAULT '',
    diff_keys_added          TEXT[] NOT NULL DEFAULT '{}',
    diff_keys_removed        TEXT[] NOT NULL DEFAULT '{}',
    diff_keys_changed        TEXT[] NOT NULL DEFAULT '{}',
    synced_resource_version  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX        idx_eso_sync_history_uid_attempt       ON eso_sync_history (uid, attempt_at DESC);
CREATE INDEX        idx_eso_sync_history_cluster_failures  ON eso_sync_history (cluster_id, attempt_at DESC) WHERE outcome <> 'success';
CREATE UNIQUE INDEX idx_eso_sync_history_dedup             ON eso_sync_history (uid, attempt_at);
CREATE INDEX        idx_eso_sync_history_attempt_at        ON eso_sync_history (attempt_at);   -- 000012
```

**Diff to target:**

| Object | Current | Target | Action |
|---|---|---|---|
| all columns | as above | **unchanged** | none — no `ALTER TABLE` in 000019 |
| `idx_eso_sync_history_dedup` | `UNIQUE (uid, attempt_at)` | dropped | `DROP INDEX` |
| `idx_eso_sync_history_dedup_cluster` | — | `UNIQUE (cluster_id, uid, attempt_at)` | `CREATE UNIQUE INDEX` |
| `idx_eso_sync_history_keyset` | — | `(cluster_id, uid, attempt_at DESC, id DESC)` | `CREATE INDEX` |
| `idx_eso_sync_history_uid_attempt` | `(uid, attempt_at DESC)` | dropped | `DROP INDEX` |
| `idx_eso_sync_history_cluster_failures` | partial, `(cluster_id, attempt_at DESC)` | **unchanged** | none |
| `idx_eso_sync_history_attempt_at` | `(attempt_at)` | **unchanged** (retention DELETE) | none |

**Legacy rows.** Because `cluster_id` is `NOT NULL DEFAULT 'local'` and `persistOne` has
always bound `p.clusterID`, **there are no NULL/unstamped rows and no backfill is
required.** The one legacy hazard: if an operator ever changed `KUBECENTER_CLUSTERID`,
pre-change rows carry the old id and become invisible to the new read path. We
deliberately do **not** rewrite them — an `UPDATE … SET cluster_id = 'x'` cannot
distinguish "renamed cluster" from "genuinely different cluster" and would fabricate
identity, violating R1. Those rows age out under the existing 90-day retention. This is
recorded in `NOTES.txt`.

**Widening safety.** `UNIQUE (cluster_id, uid, attempt_at)` is strictly weaker than
`UNIQUE (uid, attempt_at)`: every row set satisfying the old constraint satisfies the new
one, so the `CREATE UNIQUE INDEX` **cannot** fail on existing data. The `down` migration
inverts that and **can** fail — see U13.

**Locking.** golang-migrate wraps each file in one transaction, so `CREATE INDEX
CONCURRENTLY` is not available. Plain `CREATE INDEX` takes a `SHARE` lock that blocks
writes to `eso_sync_history` for the build. At the table COMMENT's stated 2.16M-row
steady state that is seconds, the only writer is the single-replica poller (`Insert`),
and a blocked `Insert` is retried on the next 60s tick without data loss.

### Placeholder-tab inventory (exact, from `grep -rn "coming in Phase"`)

| File | Line | Tab | Disposition |
|---|---|---|---|
| `frontend/islands/ESOExternalSecretDetail.tsx` | 265 | YAML — "YAML editor coming in Phase B." | U17 → real YAML |
| " | 274 | Events — "Events feed coming in Phase B." | U17 → real Events |
| " | 283 | History — "History timeline coming in Phase C." | U17 → real History |
| `frontend/islands/ESOStoreDetail.tsx` | 200 | YAML | U17 → real YAML |
| " | 209 | Events | U17 → real Events |
| " | 218 | History | U17 → **tab removed** (unsupported) |
| `frontend/islands/ESOClusterStoreDetail.tsx` | 196 | YAML | U18 → real YAML |
| " | 205 | Events | U18 → real Events |
| " | 214 | History | U18 → **tab removed** |
| `frontend/islands/ESOClusterExternalSecretDetail.tsx` | 244 | YAML | U18 → real YAML |
| " | 253 | Events | U18 → real Events |
| " | 262 | History | U18 → **replaced by "Generated ExternalSecrets"** |
| " | 271 | Chain — "Chain visualization coming in Phase I." | **out of Release B scope; left as-is** |
| `frontend/islands/ESOPushSecretDetail.tsx` | 214 | YAML | U18 → real YAML |
| " | 223 | Events | U18 → real Events |
| " | 232 | History | U18 → **tab removed** |
| " | 241 | Chain | **out of scope; left as-is** |

**17 placeholders; 15 in scope, 2 (Chain) explicitly out.** Release B's exit condition is
"no misleading *evidence* tabs"; the two Chain placeholders are truthful about a
different, un-started feature and are named here so review does not read their survival
as an oversight.

**Agent Directive 1 (Step-0 dead-code cleanup before a structural refactor on >300 LOC)
does NOT apply to any of the five islands** — 292 / 227 / 223 / 276 / 246 LOC, all under
300. Re-check `ESOExternalSecretDetail.tsx` at the start of U19b: U17 will have grown it,
and if it has crossed 300 LOC the Step-0 cleanup commit is required first.

---

## Design Decisions

### D1. Migration 000019 (index-only)

`backend/internal/store/migrations/000019_scope_eso_history.up.sql`:

```sql
-- Release B / U13. Cluster-scope the ExternalSecret sync-history identity and
-- add the keyset-pagination index. No columns change: cluster_id has existed
-- since 000011 (NOT NULL DEFAULT 'local'), so there is nothing to backfill.
--
-- CONCURRENTLY is deliberately absent: golang-migrate runs each file inside a
-- transaction. Plain CREATE INDEX takes a SHARE lock; the single-replica ESO
-- poller is the only writer and retries a blocked Insert on its next 60s tick.

-- 1. Cluster-blind dedup -> cluster-scoped dedup. Widening only; cannot fail
--    on existing data (every row satisfying the old key satisfies the new one).
DROP INDEX IF EXISTS idx_eso_sync_history_dedup;
CREATE UNIQUE INDEX IF NOT EXISTS idx_eso_sync_history_dedup_cluster
    ON eso_sync_history (cluster_id, uid, attempt_at);

-- 2. Keyset pagination: matches the ORDER BY attempt_at DESC, id DESC tuple
--    walk in ESOHistoryStore.QueryPage exactly.
CREATE INDEX IF NOT EXISTS idx_eso_sync_history_keyset
    ON eso_sync_history (cluster_id, uid, attempt_at DESC, id DESC);

-- 3. Retire the now-redundant index: every query that used (uid, attempt_at)
--    is being rewritten to carry a cluster_id predicate, so it can only serve
--    dead plans.
DROP INDEX IF EXISTS idx_eso_sync_history_uid_attempt;

COMMENT ON INDEX idx_eso_sync_history_dedup_cluster IS
    'Release B/U13: dedup key is (cluster_id, uid, attempt_at). A cluster restored from a Velero backup into a second registered cluster reproduces identical object UIDs; the pre-000019 (uid, attempt_at) key silently merged the two timelines.';
```

`…/000019_scope_eso_history.down.sql`:

```sql
-- ROLLBACK HAZARD — read migrations/NOTES.txt before running.
-- Recreating the stricter UNIQUE (uid, attempt_at) FAILS if two clusters ever
-- recorded the same (uid, attempt_at). Find offenders first:
--
--   SELECT uid, attempt_at, count(DISTINCT cluster_id)
--     FROM eso_sync_history GROUP BY 1,2 HAVING count(DISTINCT cluster_id) > 1;
--
-- There is no automatic resolution: deleting either side destroys one cluster's
-- audit trail. The operator must choose and delete explicitly.
DROP INDEX IF EXISTS idx_eso_sync_history_keyset;
CREATE INDEX IF NOT EXISTS idx_eso_sync_history_uid_attempt
    ON eso_sync_history (uid, attempt_at DESC);
DROP INDEX IF EXISTS idx_eso_sync_history_dedup_cluster;
CREATE UNIQUE INDEX IF NOT EXISTS idx_eso_sync_history_dedup
    ON eso_sync_history (uid, attempt_at);
```

`Insert`'s conflict target changes to `ON CONFLICT (cluster_id, uid, attempt_at) DO NOTHING`
in the same PR — a mismatched target is a runtime `42P10` error, so the Go change and the
migration **must** ship together.

### D2. Cursor encoding

Keyset over the tuple `(attempt_at DESC, id DESC)`. `id` is the `BIGSERIAL` tiebreaker for
the equal-timestamp case the master plan calls out.

```
cursor := base64url_nopad( "<attempt_at as int64 unix microseconds>:<id as int64>" )
```

Decode rules — each failure is a **400** with `reason: "invalid_cursor"`, never a silent
reset to page 1 (a silent reset would loop a paginating client forever):

1. not valid unpadded base64url → reject;
2. decoded bytes are not valid UTF-8, or longer than 64 bytes → reject;
3. not exactly one `:` → reject;
4. either half fails `strconv.ParseInt(_, 10, 64)` → reject;
5. `micros` outside `[0, 4102444800000000]` (year 2100) or `id < 1` → reject.

```go
var ErrInvalidESOHistoryCursor = errors.New("invalid eso history cursor")

type ESOHistoryCursor struct {
    AttemptAt time.Time
    ID        int64
}
func EncodeESOHistoryCursor(c ESOHistoryCursor) string
func DecodeESOHistoryCursor(s string) (ESOHistoryCursor, error)
```

**The cursor is not authenticated, and does not need to be.** It carries no authority:
`cluster_id` comes from server config and `uid` is re-resolved from the live cluster on
every request (D4 step 5). The `WHERE` clause pins both. A forged cursor can therefore
only move the caller's window *within rows they are already authorised to read* — it
cannot cross a cluster, cross an object, or widen the projection. Signing it would add key
management for zero boundary gain. This reasoning goes into the code comment so a later
reviewer does not "fix" it.

Query (`limit` clamped to `[1, 200]`, default 50):

```sql
SELECT id, cluster_id, uid, namespace, name, attempt_at, outcome, reason, message,
       diff_keys_added, diff_keys_removed, diff_keys_changed, synced_resource_version
FROM eso_sync_history
WHERE cluster_id = $1
  AND uid = $2
  AND ($3::timestamptz IS NULL OR (attempt_at, id) < ($3::timestamptz, $4::bigint))
ORDER BY attempt_at DESC, id DESC
LIMIT $5
```

The row-value comparison `(attempt_at, id) < (…)` is the tuple form Postgres can walk
directly on `idx_eso_sync_history_keyset`. `NextCursor` is set from the last returned row
**only when `len(rows) == limit`**; otherwise `""`.

**A DB fault is never an empty page.** `QueryPage` returns `(ESOHistoryPage{}, err)`; the
caller maps `err != nil` → 503 `history_unavailable`, and `len(Entries)==0 && err==nil` →
200 with an empty `entries` array. R3's "unavailable vs empty" distinction lives exactly
here.

### D3. Redaction projection matrix (AE4 — the security core)

Two server-resolved levels, both evaluated per request against the ES's namespace:

- **L1 `outcome-only`** — `get externalsecrets` (group `external-secrets.io`) allowed, `get secrets` (group `""`) denied.
- **L2 `full`** — both allowed.

The Secret check is a **second** call, `CanAccessGroupResource(ctx, clusterID, user…, "get", "", "secrets", ns)`,
because `Handler.canAccess` hardcodes `GroupName`. A new sibling
`canAccessCore(ctx, user, verb, resource, ns)` is added next to it.

| Response field | L1 `outcome-only` | L2 `full` | Why |
|---|---|---|---|
| `id` | present | present | opaque row id, no secret content |
| `attemptAt` | present | present | timing is already visible via the ES `status` the caller can read |
| `outcome` | present (`success`/`failure`/`partial`) | present | R11 "permitted sync outcomes" |
| `reason` | **allowlisted**; any token not on the list → `"Unknown"` | present, sanitized | controller-set token; the allowlist stops an unexpected value smuggling text through the low-privilege channel |
| `message` | **key absent from JSON** | present, sanitized + truncated | free-form provider text; routinely names the remote path/key |
| `messageTruncated` | absent | `true`/`false` | only meaningful alongside `message` |
| `diffKeysAdded` / `diffKeysRemoved` / `diffKeysChanged` | **keys absent from JSON** | present | derived from the **live Secret**, so they can name keys the ES spec never mentions (`dataFrom`, templates) — `persist.go:335` |
| `diffKeyCounts {added,removed,changed}` | present | present | cardinality only, never a name; strictly less than what an ES reader already derives from `spec.data[].secretKey` |
| `syncedResourceVersion` | **key absent** | present | a Secret attribute, not an ES attribute |
| `namespace` / `name` / `uid` (envelope-level) | present | present | already in the request path / already resolved |

**Absent, not empty.** L1 omits fields entirely (`omitempty` on a pointer / nil slice)
rather than sending `""` / `[]`. The client can then render *"hidden — requires Secret
read in this namespace"* instead of *"no message"*. Sending `[]` would make a redacted
response indistinguishable from a genuinely empty diff — an R3 violation.

**`reason` allowlist.** Concrete starting set, to be finalised against the installed ESO
version during implementation (the repo does not encode ESO's condition vocabulary, so
this list is stated as a starting point, not as verified fact): `SecretSynced`,
`SecretSyncError`, `SecretSyncedError`, `SecretDeleted`, `SecretMissing`,
`SecretUpdateFailed`, `InvalidProviderConfig`, `SecretStoreNotReady`, `SecretStoreError`.
Anything else → `"Unknown"`. The allowlist is a package-level `map[string]struct{}` so the
fuzz target can assert the closure property.

**`sanitizeControllerText(s string, maxBytes int) (out string, truncated bool)`** — applies
at L2 only, and to Events messages as well as history messages:

1. replace invalid UTF-8 sequences with `U+FFFD` (`strings.ToValidUTF8`);
2. drop all ASCII control characters **except** `\n` and `\t` — this removes ANSI/OSC
   escape sequences that would otherwise execute in a terminal-based API consumer;
3. collapse runs of `\n` longer than 2;
4. truncate to `maxBytes` **on a rune boundary** (2048 for history, 1024 for events),
   append `"…"`, return `truncated = true`.

It does **not** HTML-escape: Preact escapes text children on render, and pre-escaping
would double-encode. The e2e suite asserts a `<script>`-bearing message renders as visible
text (U17).

**`maskedSecret` is deliberately not reused** — see correction C6. History persists key
*names* and controller *text*; it never holds a Secret value, so there is no value to
mask. `maskedSecret` remains the guard on the actual Secret path and keeps its own
`FuzzMaskedSecret` / `FuzzSecretPipeline` rows.

**Known 60s window.** `accessCacheTTL = 60s` (`resources/access.go:19`) means a revoked
`get secrets` grant can keep a caller at L2 for up to a minute. This matches every other
RBAC decision on the platform and is accepted, not fixed here; it is stated in the handler
doc comment and asserted by a test using a predicate-mode `AccessChecker` (which bypasses
the cache), so the *logic* is proven independent of the cache.

### D4. API surface

**History** — new route, immediately after the force-sync line in
`registerExternalSecretsRoutes`:

```go
er.With(resources.ValidateURLParams).
    Get("/externalsecrets/{namespace}/{name}/history", h.HandleGetExternalSecretHistory)
```

`GET /api/v1/externalsecrets/externalsecrets/{namespace}/{name}/history?limit=50&cursor=<opaque>`

Ordered gate sequence (this order is normative):

1. `httputil.RequireUser` → 401.
2. **Cluster binding.** `middleware.ClusterIDFromContext(r.Context())`; if non-empty and
   `!= h.ClusterID` (the configured local id, from `cfg.ClusterID` — **not** the literal
   `"local"`) → **501** `reason: "remote_history_unsupported"`. History rows only ever
   exist for the locally-polled cluster; an empty 200 would read as "this ES never
   synced". R1 + R3.
3. `h.Discoverer.IsAvailable` → 503 `reason: "eso_not_detected"`.
4. `h.canAccess(ctx, user, "get", "externalsecrets", ns)` → 403.
5. **Resolve the live ES** via `h.dynForUser(...).Resource(ExternalSecretGVR).Namespace(ns).Get(...)`
   → 403 / 404 mapped. `uid := obj.GetUID()`. **A UID is never accepted from the request** —
   this is the "do not accept arbitrary UID as sufficient authorization" boundary. A
   deleted-and-recreated ES therefore resolves to the new UID and the old object's rows are
   unreachable, satisfying R1.
6. `level := L2 if h.canAccessCore(ctx, user, "get", "secrets", ns) else L1`.
7. `h.HistoryStore == nil` → 503 `reason: "history_unavailable"`, detail
   `"history persistence is not configured"`.
8. `h.HistoryStore.QueryPage(ctx, h.ClusterID, uid, cursor, limit)`; `err != nil` → 503
   `reason: "history_unavailable"`. Malformed cursor → 400 `reason: "invalid_cursor"`.

Response (`data` is an object; precedent: `HandleGetBulkRefreshJob`, `dashboard-summary`):

```json
{
  "data": {
    "uid": "8f0c…",
    "clusterId": "local",
    "projection": { "level": "outcome-only", "droppedFields": ["message","diffKeys","syncedResourceVersion"] },
    "entries": [
      { "id": 4711, "attemptAt": "2026-09-10T12:00:00Z", "outcome": "success",
        "reason": "SecretSynced", "diffKeyCounts": { "added": 1, "removed": 0, "changed": 2 } }
    ]
  },
  "metadata": { "total": 1, "continue": "MTc1Nz…" }
}
```

The cursor lives **only** in `metadata.continue`, reusing the existing envelope field — no
`pkg/api` change. `metadata.total` is the page length, matching `writeList`'s existing
loose semantics (it passes the pre-pagination slice length, not a global count).

**Events** — one route for all five kinds, `_` for cluster scope:

```go
er.Get("/evidence/{kind}/{namespace}/{name}/events", h.HandleGetEvidenceEvents)
```

`resources.ValidateURLParams` is **not** attached — it would reject `namespace == "_"`.
`HandleGetEvidenceEvents` validates inline exactly as `yaml.HandleExport` does
(`resources.ValidateK8sName(name)`; `namespace == "_" || ValidateK8sName(namespace)`).

`{kind}` is checked against a five-entry allowlist. The GVR **and the namespaced flag** are
resolved from **live discovery** by plural name within `GroupName` (never from the
`v1`-pinned package vars — C7), cached 5 minutes in a `sync.Map` mirroring the
`Discoverer`'s own TTL.

Response:

```json
{ "data": {
    "uid": "8f0c…",
    "projection": { "level": "full", "droppedFields": [] },
    "events": [ { "type": "Warning", "reason": "UpdateFailed", "message": "…",
                  "count": 3, "firstTimestamp": "…", "lastTimestamp": "…",
                  "source": "external-secrets" } ],
    "truncated": false },
  "metadata": { "total": 3 } }
```

**YAML** — no new backend route. The frontend calls the existing
`GET /api/v1/yaml/export/{resource}/{namespace|_}/{name}` (C2).

### D5. Evidence support matrix (R12)

| Kind | YAML | Events | History |
|---|---|---|---|
| **ExternalSecret** | SUPPORTED — `/yaml/export/externalsecrets/{ns}/{name}` | SUPPORTED — UID field selector, namespace-scoped | SUPPORTED — **real**, `/externalsecrets/{ns}/{name}/history`, projected |
| **SecretStore** | SUPPORTED — `/yaml/export/secretstores/{ns}/{name}` | SUPPORTED — namespace-scoped | **UNSUPPORTED** — no store-level reconcile collector exists. **The History tab is removed**, not filled with downstream ES attempts. |
| **ClusterSecretStore** | SUPPORTED — `/yaml/export/clustersecretstores/_/{name}` | SUPPORTED — all-namespace list + UID selector | **UNSUPPORTED** — tab removed |
| **ClusterExternalSecret** | SUPPORTED — `/yaml/export/clusterexternalsecrets/_/{name}` | SUPPORTED — all-namespace list + UID selector | **UNSUPPORTED as "its own" history.** The tab is **replaced** by *"Generated ExternalSecrets"* — a list built from `status.provisionedNamespaces` / `failedNamespaces` linking to each child ES's own history page. Truthful and more useful than either a lie or a blank. |
| **PushSecret** | SUPPORTED — `/yaml/export/pushsecrets/{ns}/{name}` | SUPPORTED — namespace-scoped | **UNSUPPORTED** — tab removed. PushSecret is read-only in v1; no write affordance is added. |

The rule this encodes: **a tab exists only when a collector for that kind exists.** An ES
attempt row is evidence about *that ES object*. Rendering it under a store — even labelled
"related" — is the exact R12 failure, so it is not rendered under a store at all. The
panel's absence is explained once in the store's Overview (*"Reconciliation history is
collected per ExternalSecret. Open an ExternalSecret that references this store to see its
sync attempts."*) rather than as a ghost tab.

**Cluster-scoped events cost a stronger grant.** Listing events for a cluster-scoped object
means `Events("").List(…)` — cluster-wide `list events`. A caller with only
namespace-scoped rights gets **403 `events_forbidden`** naming the required grant, not an
empty list. That is R12/U18's "a namespaced permission cannot reveal cluster-scoped detail"
working as intended.

### D6. Refresh-observation state machine (U19 / AE5 / R13)

**Baseline is captured server-side**, inside the existing pre-patch `Get` in
`patchForceSyncOnce` — never by a client GET before the POST, which would race the
controller and could capture a baseline that already contains the new sync.

The 202 body becomes:

```json
{ "data": {
    "status": "force-syncing",
    "correlation": "strong",
    "baseline": {
      "uid": "8f0c…",
      "resourceVersion": "918273",
      "generation": 7,
      "refreshTime": "2026-09-10T11:00:00Z",
      "readyLastTransitionTime": "2026-09-10T10:00:00Z",
      "syncedResourceVersion": "abc123",
      "requestedAt": "2026-09-10T12:00:00.123Z"
    } } }
```

`correlation` is `"strong"` iff `status.refreshTime` was present in the baseline (ESO
populates it, so a strictly-later value proves a **new** reconcile). Otherwise `"weak"`:
the UI may say *"observed after your request"* and must never say *"caused by"*. This is
the master plan's "correlate strongly only when controller fields support it".

States: `idle → requested → accepted → awaitingObservation →`
`observedSuccess | observedFailure | timeout | cancelled | accessLost | targetChanged`.

**Why an old `Ready=True` cannot satisfy a new request (AE5).** The success predicate is
never `ready === true`. `isNewEvidence(baseline, sample, correlation, requestedAtMs)`:

- `correlation === "strong"`: `sample.refreshTime > baseline.refreshTime`.
- `correlation === "weak"`: `sample.readyLastTransitionTime > baseline.readyLastTransitionTime`
  **or** `sample.syncedResourceVersion !== baseline.syncedResourceVersion`
  **or** (`sample.resourceVersion !== baseline.resourceVersion` **and** a Ready condition
  exists whose `lastTransitionTime >= requestedAt`).

An ES sitting at `Ready=True` with an unchanged `refreshTime` and an unchanged
`lastTransitionTime` matches none of these, so the machine stays in `awaitingObservation`.
That is the literal AE5 assertion.

Once `isNewEvidence` is true, the outcome is read from the **new** sample: `SyncFailed` →
`observedFailure` (reason/message under the same D3 projection, since the observer polls
the ES detail endpoint the caller is already authorised for); otherwise `observedSuccess`.

Terminal transitions:

| Trigger | State | User-facing claim |
|---|---|---|
| bound expires (90 s) with no new evidence | `timeout` | "Request accepted. No new reconciliation observed within 90 s." — explicitly **not** a failure |
| any poll → 403 | `accessLost` | "You no longer have access to this ExternalSecret. The refresh may still be in progress." |
| poll `uid !== baseline.uid`, or 404 | `targetChanged` | "This ExternalSecret was replaced. Discarded the pending observation." (R1) |
| `selectedCluster` signal changes | `targetChanged` | same; no state write after the change |
| unmount / navigation / explicit cancel | `cancelled` | no state write after abort |
| 3 consecutive 5xx/network errors | `timeout` with `reason: "observation_unavailable"` | distinguished from a real timeout |

Polling: `nextPollDelayMs(attempt)` = 1000, 2000, 3000, then 5000 capped; total bound
`OBSERVE_TIMEOUT_MS = 90_000`. The observer polls `esoApi.getExternalSecret`, which reads
**live** through the impersonating dynamic client (`handler.go:733`), so the 30 s
`cacheTTL` does not bound observation latency.

`frontend/lib/eso-refresh-observer.ts` is a **pure reducer**, not a timer class, so the
tests need no fake clock:

```ts
export type Correlation = "strong" | "weak";
export type ObserverPhase =
  | "idle" | "requested" | "accepted" | "awaitingObservation"
  | "observedSuccess" | "observedFailure" | "timeout" | "cancelled"
  | "accessLost" | "targetChanged";
export interface ObserverState {
  phase: ObserverPhase; baseline?: Baseline; correlation?: Correlation;
  attempt: number; consecutiveErrors: number; deadlineMs?: number; detail?: string;
}
export type ObserverEvent =
  | { type: "request" }
  | { type: "accepted"; baseline: Baseline; correlation: Correlation; nowMs: number }
  | { type: "sample"; sample: Sample; nowMs: number }
  | { type: "forbidden" } | { type: "notFound" } | { type: "error" }
  | { type: "clusterChanged" } | { type: "tick"; nowMs: number } | { type: "cancel" };
export function reduceObserver(s: ObserverState, e: ObserverEvent): ObserverState;
export function isNewEvidence(b: Baseline, s: Sample, c: Correlation, requestedAtMs: number): boolean;
export function nextPollDelayMs(attempt: number): number;
export const OBSERVE_TIMEOUT_MS = 90_000;
```

The island owns `setTimeout` + `AbortController` and feeds events in.

---

## U13. Cluster-scope ESO history storage and add keyset pagination

**Branch** `feat/u13-eso-history-cluster-scope` · **PR** *feat(store): cluster-scope ESO sync history and add keyset pagination*
**Covers** R1, R11, KTD7 · **Depends on** none

**Files (5)**

1. `backend/internal/store/eso_history.go`
2. `backend/internal/store/eso_history_test.go` — new
3. `backend/internal/store/migrations/000019_scope_eso_history.up.sql` — new
4. `backend/internal/store/migrations/000019_scope_eso_history.down.sql` — new
5. `backend/internal/store/migrations/NOTES.txt` — append the 000019 section

**Steps**

1. Write the two migration files exactly as in **D1**.
2. `NOTES.txt`: append a `000019_scope_eso_history` section covering (a) no columns change /
   no backfill, (b) the `KUBECENTER_CLUSTERID`-rename legacy-row caveat and why we do not
   rewrite, (c) the `down` failure mode with the duplicate-finder query, (d) the
   `SHARE`-lock note.
3. `eso_history.go`:
   - `Insert`: conflict target → `ON CONFLICT (cluster_id, uid, attempt_at) DO NOTHING`.
   - **Delete** `QueryByUID` and `LatestByUID` outright (both have zero callers — Directive 1
     dead-code removal; keeping cluster-blind readers next to cluster-scoped ones invites
     misuse).
   - Add:
     ```go
     type ESOHistoryCursor struct { AttemptAt time.Time; ID int64 }
     type ESOHistoryPage   struct { Entries []ESOSyncHistoryEntry; NextCursor string }
     var ErrInvalidESOHistoryCursor = errors.New("invalid eso history cursor")
     func EncodeESOHistoryCursor(c ESOHistoryCursor) string
     func DecodeESOHistoryCursor(s string) (ESOHistoryCursor, error)
     func (s *ESOHistoryStore) QueryPage(ctx context.Context, clusterID, uid string, after *ESOHistoryCursor, limit int) (ESOHistoryPage, error)
     func (s *ESOHistoryStore) LatestByClusterUID(ctx context.Context, clusterID, uid string) (*ESOSyncHistoryEntry, error)
     ```
     with the SQL from **D2**. `limit` clamped to `[1,200]`, default 50. `NextCursor` set
     only when `len(rows) == limit`.
   - Move the existing key-name RBAC warning comment onto `QueryPage` and update it from
     "future endpoints MUST" to a pointer at U14a's projection.
4. `eso_history_test.go` — two layers, because no harness exists (C4):
   - **always-on, hermetic**: `TestEncodeDecodeESOHistoryCursor_RoundTrip`,
     `TestDecodeESOHistoryCursor_Rejects` (table: empty, `"abc"`, non-base64, no colon, two
     colons, `"x:1"`, `"1:x"`, negative id, year-3000 micros, 4 KiB blob),
     `TestClampHistoryLimit`.
   - **`//go:build pgintegration`**, skipped unless `KUBECENTER_TEST_DATABASE_URL` is set;
     applies the embedded migrations to a scratch schema, then asserts:
     `TestQueryPage_ClusterIsolation` (same UID, two `cluster_id`s → no mixing),
     `TestQueryPage_SameNameDistinctUID`, `TestQueryPage_EqualTimestampsDeterministic`
     (three rows, identical `attempt_at`, paged at limit 1 → strictly decreasing `id`, no
     repeat, no skip), `TestQueryPage_ForgedCursorRejected`,
     `TestQueryPage_DBFaultIsNotEmpty` (close the pool; assert `err != nil` **and**
     `len(Entries) == 0`), `TestInsert_DedupIsClusterScoped` (same `(uid, attempt_at)`, two
     clusters → **two** rows), `TestQueryPage_ContextCancelled`,
     `TestMigration000019_RoundTrip` (up → seed → down → up; unrelated rows preserved).
   - Document in the file header how to run it: `make dev-db`, then
     `KUBECENTER_TEST_DATABASE_URL=… go test -tags=pgintegration ./internal/store/`.

**Verification** — `cd backend && go vet ./... && go test ./...`; plus the gated
`go test -tags=pgintegration ./internal/store/` against `make dev-db`.

**Done means** — 000019 applies clean on a fresh DB *and* on a DB populated at 000018;
`Insert` dedups per cluster; `QueryPage` never returns rows from another cluster;
equal-timestamp paging is exact; a DB fault is `(empty, err)`; no cluster-blind reader
remains in the package.

---

## U14a. Expose the redacted ES history endpoint

**Branch** `feat/u14a-eso-history-endpoint` · **PR** *feat(eso): redacted ExternalSecret sync-history endpoint*
**Covers** R2, R3, R11; AE4 · **Depends on** U13

**Files (5)**

1. `backend/internal/externalsecrets/history_handler.go` — new
2. `backend/internal/externalsecrets/history_handler_test.go` — new
3. `backend/internal/externalsecrets/handler.go`
4. `backend/internal/server/routes.go`
5. `backend/cmd/kubecenter/main.go`

**Steps**

1. `handler.go` — add two nil-tolerant fields next to `BulkJobStore` / `MonitoringDisc`
   (same optional-dependency pattern, **not** constructor args):
   ```go
   // HistoryStore is optional; nil => the history endpoint answers 503
   // history_unavailable rather than panicking. Wired in main.go.
   HistoryStore ESOHistoryReader
   // ClusterID is the configured id this process polls (cfg.ClusterID).
   // History rows are stamped with it; the read path pins it.
   ClusterID string
   ```
   and the core-group sibling to `canAccess`:
   ```go
   func (h *Handler) canAccessCore(ctx context.Context, user *auth.User, verb, resource, namespace string) bool
   ```
   (calls `CanAccessGroupResource` with `apiGroup: ""` — the documented core path,
   `access.go:183`).
2. `history_handler.go`:
   ```go
   type ESOHistoryReader interface {
       QueryPage(ctx context.Context, clusterID, uid string, after *store.ESOHistoryCursor, limit int) (store.ESOHistoryPage, error)
   }
   type projectionLevel string
   const (
       projectionOutcomeOnly projectionLevel = "outcome-only"
       projectionFull        projectionLevel = "full"
   )

   func (h *Handler) HandleGetExternalSecretHistory(w http.ResponseWriter, r *http.Request)
   func projectHistoryEntry(e store.ESOSyncHistoryEntry, lvl projectionLevel) historyEntryDTO
   func sanitizeControllerText(s string, maxBytes int) (string, bool)
   func projectReason(reason string, lvl projectionLevel) string
   var knownESOReasons = map[string]struct{}{ /* D3 list */ }
   ```
   The DTO uses pointer / nil-slice + `omitempty` so L1 **omits** keys (D3). Gate order
   exactly as **D4**.
3. `routes.go` — insert after line 723 (`…/force-sync`), inside `registerExternalSecretsRoutes`:
   ```go
   		// Release B / U14 — paginated, redacted sync history for one
   		// ExternalSecret. Key names and controller messages require
   		// `get secrets` in the ES namespace; see history_handler.go.
   		er.With(resources.ValidateURLParams).
   			Get("/externalsecrets/{namespace}/{name}/history", h.HandleGetExternalSecretHistory)
   ```
4. `main.go` — after line 793 (`esoHandler.MonitoringDisc = monDiscoverer`):
   ```go
   	// Release B / U14 — history read path. nil-safe: the endpoint answers
   	// 503 history_unavailable when no DB is configured.
   	if esoHistoryStore != nil {
   		esoHandler.HistoryStore = esoHistoryStore
   	}
   	esoHandler.ClusterID = cfg.ClusterID
   ```
   (`esoHistoryStore` already exists at lines 783–786; no new construction.)
5. `history_handler_test.go` — build on the existing `handler_test.go` seams
   (`dynForUserOverride`, predicate-mode `AccessChecker`) plus a fake `ESOHistoryReader`:

| Test | Scenario |
|---|---|
| `TestHistory_ESOnlyReader_OmitsKeysAndMessage` | AE4 primary: L1 body has no `message`, no `diffKeys*`, no `syncedResourceVersion`; **has** `diffKeyCounts` and `outcome` |
| `TestHistory_ESPlusSecretReader_ReturnsFullProjection` | AE4: L2 sees all fields |
| `TestHistory_ForbiddenES_Returns403` | denied `get externalsecrets` |
| `TestHistory_HistoryStoreNil_Returns503Unavailable` | reason `history_unavailable` |
| `TestHistory_StoreError_Returns503NotEmpty200` | DB fault is not an empty history |
| `TestHistory_ForgedCursor_Returns400` | reason `invalid_cursor` |
| `TestHistory_CursorCannotCrossObject` | cursor minted for object A, replayed on object B's URL → rows still scoped to B's live UID |
| `TestHistory_RemoteClusterID_Returns501` | `X-Cluster-ID: prod` → 501 `remote_history_unsupported` |
| `TestHistory_UIDResolvedLive_NotFromRequest` | a `?uid=` query string is ignored; the store is called with the live UID |
| `TestHistory_RecreatedES_DoesNotInheritOldRows` | store seeded under the old UID; live Get returns the new UID → empty page, 200 |
| `TestHistory_CrossUserIsolation` | two identities, differing SAR predicates → different projections for the same object |
| `TestHistory_RevokedSecretAccess_DropsToL1` | predicate flips mid-test (cache-free) → L1 |
| `TestHistory_ESNotFound_Returns404` | |
| `TestHistory_ESONotDetected_Returns503` | reason `eso_not_detected` |
| `TestHistory_ContextCancelled_NoPartialWrite` | client disconnect |
| `TestSanitizeControllerText_*` | ANSI escape stripped; invalid UTF-8 → U+FFFD; rune-boundary truncation sets `truncated` |
| `TestProjectReason_UnknownTokenBecomesUnknown` | allowlist closure at L1 |

**Verification** — `cd backend && go vet ./... && go test ./...`; plus
`scripts/check-cluster-routing.sh` (new cluster-aware handler).

**Done means** — an ES-only reader can never obtain a key name, a controller message, or a
synced resource version through this endpoint; unavailable / forbidden / empty are three
distinct responses; the UID is always server-resolved.

---

## U14b. Fuzz the new parse and redaction seams

**Branch** `feat/u14b-eso-evidence-fuzz` · **PR** *test(eso): fuzz history cursor decoding and controller-text redaction*
**Covers** Verification Contract "new unstructured parsing, redaction" · **Depends on** U14a

**Files (3)**

1. `backend/internal/store/eso_history_fuzz_test.go` — new
2. `backend/internal/externalsecrets/history_fuzz_test.go` — new
3. `.github/workflows/fuzz.yml`

**Which units need a fuzz target, and which do not** (per `docs/solutions/backend-resilience-conventions.md`):

| Unit | Fuzz target needed? | Why |
|---|---|---|
| U13 | **Yes** — `FuzzDecodeESOHistoryCursor` | `DecodeESOHistoryCursor` turns an attacker-supplied string into typed values. Hermetic (no DB). |
| U14a | **Yes** — `FuzzESOHistoryProjection` | `sanitizeControllerText` + `projectHistoryEntry` are the redaction boundary; Oracle D. |
| U15 | **No new target** | The events adapter parses `corev1.Event` (typed client-go, already validated) and reuses `sanitizeControllerText`, covered by U14b. The ESO `unstructured` normalizers are already covered by the existing `FuzzExternalSecretsNormalizers` row. |
| U16–U19b | **No** | Frontend TypeScript / a JSON-tag addition; no Go parse seam. |

**Steps**

1. `FuzzDecodeESOHistoryCursor` (package `store`) — **Oracle A** (never panics) + **Oracle B**
   (`Decode∘Encode` is identity for any decodable cursor; `Decode` of the re-`Encode`d value
   is stable). Teeth seeds: `""`, `"="`, `"AAAA"`, `":"`, `"1:2:3"`, `"-1:-1"`,
   `"9223372036854775808:1"`, a 4 KiB base64 blob, `"\x00\xff"`. Verify teeth by mutation:
   temporarily delete the field-count check — the `"1:2:3"` seed must fail.
2. `FuzzESOHistoryProjection` (package `externalsecrets`) — **Oracle A** + **Oracle D**: build
   an `ESOSyncHistoryEntry` whose `Message`, `Reason` and `DiffKeys*` come from fuzz bytes,
   project at **L1**, then assert the marshalled JSON contains **none** of the input
   key-name bytes and **no** `message` key. Re-derive the "must be absent" field list
   independently in the test (do **not** import the production `droppedFields` slice) so the
   oracle detects the guard being removed. Teeth: a diff key literally named `"message"`, a
   reason containing `\x1b]0;`, invalid UTF-8, a 1 MiB message.
3. `fuzz.yml` — two matrix rows appended, adjacent to the existing ones:
   ```yaml
   - { pkg: ./internal/store/, target: FuzzDecodeESOHistoryCursor }
   - { pkg: ./internal/externalsecrets/, target: FuzzESOHistoryProjection }
   ```
   No action SHAs change (7-day supply-chain cooldown: reuse the already-vetted pins in the
   file).

**Verification** — `cd backend && go vet ./... && go test ./...`; locally
`go test ./internal/store/ -run=^$ -fuzz=^FuzzDecodeESOHistoryCursor$ -fuzztime=15s` and the
same for `FuzzESOHistoryProjection`; confirm the `-list` drift guard matches both.

**Done means** — both targets exist, both are in the nightly matrix, and each has at least
one mutation-verified tooth.

---

## U15. UID-scoped ESO events adapter

**Branch** `feat/u15-eso-events-adapter` · **PR** *feat(eso): UID-scoped events evidence for all five ESO kinds*
**Covers** R1, R3, R11, R12 · **Depends on** U14a

**Files (3)** — corrected from the master plan's 5 (C2: no YAML adapter; C8: `eso-evidence.ts` moves to U16)

1. `backend/internal/externalsecrets/detail_evidence.go` — new
2. `backend/internal/externalsecrets/detail_evidence_test.go` — new
3. `backend/internal/server/routes.go`

**Steps**

1. `detail_evidence.go`:
   ```go
   // evidenceResources is the kind allowlist. The namespaced flag is NOT stored
   // here — it comes from discovery (APIResource.Namespaced) so the table cannot
   // drift from the cluster.
   var evidenceResources = map[string]struct{}{
       "externalsecrets": {}, "clusterexternalsecrets": {},
       "secretstores": {}, "clustersecretstores": {}, "pushsecrets": {},
   }

   func (h *Handler) HandleGetEvidenceEvents(w http.ResponseWriter, r *http.Request)
   func (h *Handler) resolveESOGVR(ctx context.Context, resource string) (gvr schema.GroupVersionResource, namespaced bool, err error)
   ```
   - `resolveESOGVR` walks `h.K8sClient.DiscoveryClient().ServerGroupsAndResources()`, matches
     `gv.Group == GroupName` and `EqualFold(r.Name, resource)`, and returns that group's
     **served** version plus `r.Namespaced`. Cached in a `sync.Map` with a 5-minute TTL
     mirroring `discovery.staleDuration`. This is the C7 fix — never `ExternalSecretGVR`
     et al. It duplicates ~20 lines of `yaml.resolveGVR`; the alternative (exporting it)
     would add a cross-package file to this PR and couple `externalsecrets` to
     `internal/yaml`. Noted in the doc comment.
   - Handler flow: `RequireUser` → local-cluster gate (same rule as D4 step 2, reason
     `remote_events_unsupported`) → `Discoverer.IsAvailable` → inline param validation
     (`_` allowed for namespace, exactly as `yaml.HandleExport`) → kind allowlist (unknown →
     400 `unknown_evidence_kind`) → `resolveESOGVR` → `canAccess(get, <resource>, ns)` →
     impersonated dynamic `Get` to obtain **the live UID** → `canAccessCore(get, secrets, ns)`
     for the projection level → events list.
   - Events: `h.clientForUser(...).CoreV1().Events(listNS).List(ctx, metav1.ListOptions{
     FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", uid).String(),
     Limit: 200})`, where `listNS` is the object's namespace for namespaced kinds and `""`
     (all namespaces) for cluster-scoped kinds. `apierrors.IsForbidden` → 403
     `events_forbidden` with a detail naming the missing grant. Sort by `lastTimestamp`
     desc; `truncated = true` when the API returns a `Continue` token.
   - Message projection: L1 → `message` key omitted; L2 → `sanitizeControllerText(msg, 1024)`.
     Reuses U14a's helper (same package, no new file).
2. `routes.go` — after the history route added in U14a:
   ```go
   		// Release B / U15 — UID-scoped Kubernetes events for any ESO kind.
   		// `_` in {namespace} selects the cluster-scoped form, mirroring
   		// /yaml/export; ValidateURLParams is intentionally NOT attached
   		// because it rejects "_" as a namespace.
   		er.Get("/evidence/{kind}/{namespace}/{name}/events", h.HandleGetEvidenceEvents)
   ```
3. `detail_evidence_test.go` — fake dynamic + fake typed clients via the existing
   `dynForUserOverride` / `clientForUserOverride` seams plus a fake discovery:

| Test | Scenario |
|---|---|
| `TestEvidenceEvents_ExcludesReplacedSameNameObject` | two events, one carrying the old UID → only the live-UID event is returned (R1) |
| `TestEvidenceEvents_ForbiddenIsNotEmpty` | events `List` → 403 ⇒ 403 `events_forbidden`, distinct from a 200 with `events: []` (R3) |
| `TestEvidenceEvents_EachKindResolvesDiscoveredVersion` | discovery serves `v1beta1` ⇒ the `Get` uses `v1beta1`, not the pinned `v1` (C7) |
| `TestEvidenceEvents_ClusterScopedListsAllNamespaces` | ClusterSecretStore ⇒ `Events("")` |
| `TestEvidenceEvents_NamespacedPermissionCannotReachClusterScoped` | namespace-only grant on a cluster-scoped kind ⇒ 403 |
| `TestEvidenceEvents_UnknownKind_Returns400` | `?kind=secrets` and `?kind=../` |
| `TestEvidenceEvents_ESOnlyReader_OmitsMessages` | AE4 extended to events (C9) |
| `TestEvidenceEvents_RemoteCluster_Returns501` | |
| `TestEvidenceEvents_DiscoveryFailure_Returns503` | discovery down is not "no events" |
| `TestEvidenceEvents_ContextCancelled` | |
| `TestResolveESOGVR_CachesAndExpires` | a second call within the TTL issues no discovery call |

**Verification** — `cd backend && go vet ./... && go test ./...`;
`scripts/check-cluster-routing.sh`.

**Done means** — every ESO kind resolves its own discovered version and scope; events are
UID-filtered server-side; forbidden is never rendered as empty; a recreated object never
inherits its predecessor's events.

---

## U16. Reusable ESO evidence panel

**Branch** `feat/u16-eso-evidence-panel` · **PR** *feat(eso): reusable YAML/Events/History evidence panel*
**Covers** R3, R11, R12 · **Depends on** U15

**Files (5)**

1. `frontend/lib/eso-evidence.ts` — **new (created here, not in U15 — C8)**
2. `frontend/lib/eso-evidence_test.ts` — new
3. `frontend/islands/ESOEvidencePanel.tsx` — new
4. `frontend/lib/eso-api.ts`
5. `frontend/lib/eso-types.ts`

**Steps**

1. `eso-types.ts` — add `HistoryEntry`, `HistoryPage`, `EvidenceProjection`, `EvidenceEvent`,
   `EvidenceEventsResponse`, `EvidenceKind`, and
   `EvidenceUnavailableReason = "remote_unsupported" | "eso_not_detected" | "history_unavailable" | "forbidden" | "unsupported_kind" | "error"`.
2. `eso-api.ts` — three thin wrappers, each taking an `AbortSignal` (the third argument
   `apiGet` already accepts):
   ```ts
   getExternalSecretHistory(ns, name, opts?: { limit?: number; cursor?: string; signal?: AbortSignal })
   getEvidenceEvents(kind: EvidenceKind, ns: string | null, name: string, signal?: AbortSignal)
   exportEvidenceYaml(resource: string, ns: string | null, name: string, signal?: AbortSignal) // -> /v1/yaml/export/...
   ```
   `ns === null` renders as `_`.
3. `eso-evidence.ts` — pure, no fetch:
   - `EVIDENCE_SUPPORT: Record<EvidenceKind, { yaml: boolean; events: boolean; history: "real" | "unsupported" | "children" }>`
     — the **D5 matrix as data**, so a kind cannot acquire a tab by accident.
   - `evidenceTabsFor(kind): Array<{ key: string; label: string }>` — derived from `EVIDENCE_SUPPORT`.
   - `classifyEvidenceError(err: ApiError): EvidenceUnavailableReason` — 403 → `forbidden`,
     501 → `remote_unsupported`, 503 + `reason` → that reason, else `error`.
   - `mergeHistoryPages(prev, next)` — append, drop duplicate `id`s, assert non-increasing
     `attemptAt`.
   - `isStaleResponse(requestUid, responseUid)` — the late-response guard.
   - `describeProjection(p: EvidenceProjection): string` — the "hidden — requires Secret read" copy.
4. `ESOEvidencePanel.tsx` — props `{ kind, namespace: string | null, name, uid, initialTab? }`.
   One island, five consumers:
   - renders only the tabs `evidenceTabsFor(kind)` returns;
   - lazy-fetches per tab on first activation (the `ResourceDetail` precedent);
   - one `AbortController` per tab, aborted on unmount / prop change; every resolved response
     passes through `isStaleResponse` before writing a signal;
   - "Load more" appends via `mergeHistoryPages` using `metadata.continue`;
   - distinct renders for empty / forbidden / unavailable / redacted / stale — never a shared
     "no data" state;
   - all controller text rendered as `{text}` children (Preact escapes); no
     `dangerouslySetInnerHTML` anywhere in the file;
   - Tailwind utilities + theme custom properties only, per the project convention.
5. `eso-evidence_test.ts` — Deno idiom (`jsr:@std/assert@1`, flat `Deno.test`):
   `EVIDENCE_SUPPORT` has no `history: "real"` outside `externalsecrets`;
   `evidenceTabsFor("secretstores")` omits History; `classifyEvidenceError` for
   403/501/503+reason/500; `mergeHistoryPages` dedup + order; `isStaleResponse` true on UID
   change; `describeProjection` for both levels.

**Verification** — `cd frontend && deno task check && deno task test && deno task build`.

**Done means** — the panel cannot render a tab for an unsupported (kind, evidence) pair;
five response classes render distinctly; a late response for a changed target is discarded.

---

## U17. ExternalSecret and SecretStore detail integration

**Branch** `feat/u17-eso-detail-evidence` · **PR** *feat(eso): real evidence tabs on ExternalSecret and SecretStore detail*
**Covers** R11, R12; AE4 · **Depends on** U16

**Files (3)**

1. `frontend/islands/ESOExternalSecretDetail.tsx`
2. `frontend/islands/ESOStoreDetail.tsx`
3. `e2e/tests/eso-evidence.spec.ts` — **new (created here — C8)**

**Steps**

1. `ESOExternalSecretDetail.tsx`: delete the three placeholder blocks at **265 / 274 / 283**;
   replace the whole `yaml|events|history` branch with
   `<ESOEvidencePanel kind="externalsecrets" namespace={es.namespace} name={es.name} uid={es.uid} />`.
   Keep `overview` and the existing real `chain` (287) untouched. Drive `TabKey` from
   `evidenceTabsFor("externalsecrets")` so the strip cannot drift from the panel.
2. `ESOStoreDetail.tsx`: delete **200 / 209 / 218**; mount
   `<ESOEvidencePanel kind="secretstores" … />`. The History tab **disappears from the strip**
   (D5). Add one Overview line: *"Reconciliation history is collected per ExternalSecret. Open
   an ExternalSecret that references this store to see its sync attempts."* Existing metrics
   and chain panels are untouched.
3. `e2e/tests/eso-evidence.spec.ts` — create with the repo idiom
   (`import { expect, test } from "../fixtures/base.ts"`),
   `test.describe("eso evidence — ES and SecretStore")`:

| Spec | Assertion |
|---|---|
| `ES detail renders YAML, Events and History tabs` | all three present; no "coming in Phase" text anywhere on the page |
| `ES History renders real rows or an explicit unavailable reason` | either rows, or a visible reason string — never a blank panel |
| `ES-only reader sees the restricted history projection` | second identity from `fixtures/auth.setup.ts`; asserts the "requires Secret read" copy and the absence of any diff-key chip |
| `SecretStore detail has no History tab` | `getByRole("tab", { name: "History" })` count 0, and the Overview explainer is visible (R12) |
| `SecretStore never labels ES attempts as its own sync history` | no row on the store page carries an ES name |
| `no ESO detail page contains "coming in Phase"` | page-wide regression guard |
| `controller text renders as text` | a seeded `<script>`-bearing message appears as literal text; no script executes |

**Verification** — `cd frontend && deno task check && deno task test && deno task build`;
`cd e2e && npm test`.

**Done means** — zero "coming in Phase" strings on the two primary ESO detail routes; a
store shows no history surface of any kind.

---

## U18. Remaining three ESO detail kinds

**Branch** `feat/u18-eso-detail-remaining-kinds` · **PR** *feat(eso): truthful evidence coverage for ClusterStore, CES and PushSecret*
**Covers** R12 · **Depends on** U17

**Files (4)**

1. `frontend/islands/ESOClusterStoreDetail.tsx`
2. `frontend/islands/ESOClusterExternalSecretDetail.tsx`
3. `frontend/islands/ESOPushSecretDetail.tsx`
4. `e2e/tests/eso-evidence.spec.ts` — **extended, not created (C8)**

**Steps**

1. `ESOClusterStoreDetail.tsx`: delete **196 / 205 / 214**; mount
   `<ESOEvidencePanel kind="clustersecretstores" namespace={null} … />` (`null` → `_`).
   History tab removed; Overview explainer added. Existing chain panel (218) untouched.
2. `ESOClusterExternalSecretDetail.tsx`: delete **244 / 253 / 262**; mount the panel with
   `kind="clusterexternalsecrets"`, `namespace={null}`. `EVIDENCE_SUPPORT` marks its history
   as `"children"`, so the panel renders **"Generated ExternalSecrets"** — a list built from
   `status.provisionedNamespaces` / `failedNamespaces` (already on the `ClusterExternalSecret`
   type) linking to each child's own history route. Leave the Chain placeholder at **271**
   (out of scope).
3. `ESOPushSecretDetail.tsx`: delete **214 / 223 / 232**; mount the panel with
   `kind="pushsecrets"`. History removed. Read-only — no write affordance added. Leave the
   Chain placeholder at **241**.
4. Append `test.describe("eso evidence — cluster-scoped and PushSecret")`:

| Spec | Assertion |
|---|---|
| `ClusterSecretStore detail resolves cluster scope` | the YAML request path contains `/_/`; no namespace segment |
| `ClusterSecretStore has no History tab` | count 0 |
| `CES History tab is replaced by Generated ExternalSecrets` | no tab labelled "History"; the generated list links to `/external-secrets/external-secrets/{ns}/{name}` |
| `CES never presents child attempts as its own history` | no "sync history" heading on the CES page |
| `PushSecret shows YAML and Events, no History, and remains read-only` | no force-sync / edit control |
| `namespaced-only identity is refused cluster-scoped events` | second identity → visible forbidden state, not an empty list |
| `Chain placeholders remain on CES and PushSecret` | asserted **present** — documents them as deliberate out-of-scope survivors so a future reviewer does not read them as a miss |

**Verification** — `cd frontend && deno task check && deno task test && deno task build`;
`cd e2e && npm test`.

**Done means** — all five ESO detail kinds have truthful evidence coverage; no kind shows a
history surface without a collector.

---

## U19a. Return a refresh baseline from force-sync

**Branch** `feat/u19a-force-sync-baseline` · **PR** *feat(eso): return a pre-patch observation baseline from force-sync*
**Covers** R1, R13; AE5 (server half) · **Depends on** U17

**Files (2)**

1. `backend/internal/externalsecrets/actions.go`
2. `backend/internal/externalsecrets/actions_test.go`

**Steps**

1. `actions.go`:
   ```go
   type refreshBaseline struct {
       UID                     string `json:"uid"`
       ResourceVersion         string `json:"resourceVersion"`
       Generation              int64  `json:"generation,omitempty"`
       RefreshTime             string `json:"refreshTime,omitempty"`
       ReadyLastTransitionTime string `json:"readyLastTransitionTime,omitempty"`
       SyncedResourceVersion   string `json:"syncedResourceVersion,omitempty"`
       RequestedAt             string `json:"requestedAt"`
   }
   func baselineFromObject(obj *unstructured.Unstructured, requestedAt time.Time) (refreshBaseline, string /*correlation*/)
   ```
   `patchForceSyncOnce` already holds the pre-patch `obj` (it reads `status.refreshTime` from
   it for the in-flight check) — extend its return set to carry the baseline upward through
   `patchForceSyncPinned` / `patchForceSync`. `correlation = "strong"` iff
   `status.refreshTime` parsed; else `"weak"`. All field reads go through guarded type
   assertions (the same shape the existing `refreshTime` read uses), so a malformed `status`
   yields a zero-value baseline rather than a panic. The 202 body becomes the **D6** JSON.
   `RequestedAt` is the server clock, captured *before* the `Patch`. The bulk-worker path
   passes the baseline nowhere and is unchanged.
2. `actions_test.go` — extend:

| Test | Scenario |
|---|---|
| `TestForceSync_202IncludesBaseline` | UID, resourceVersion, requestedAt present |
| `TestForceSync_CorrelationStrongWhenRefreshTimePresent` | |
| `TestForceSync_CorrelationWeakWhenRefreshTimeAbsent` | |
| `TestForceSync_BaselineCapturedBeforePatch` | the fake client records call order; the baseline's `resourceVersion` is the pre-patch one |
| `TestForceSync_MalformedStatusYieldsWeakBaselineNoPanic` | `status` is a string / an array |
| `TestForceSync_AlreadyRefreshing409StillHasNoBaseline` | 409 shape unchanged (mobile compatibility, R4) |
| `TestForceSync_RemoteCluster501Unchanged` | existing behaviour preserved |

**Backward compatibility (R4).** The change is **purely additive** inside `data`; the
existing `data.status == "force-syncing"` field is untouched, so the Flutter `executeAction`
path and `resource_actions.dart` continue to work with no mobile change. Stated here because
the master plan's R4 requires it.

**Verification** — `cd backend && go vet ./... && go test ./...`.

**Done means** — every accepted force-sync returns a baseline captured before the patch, with
an honest correlation strength.

---

## U19b. Observe the refresh outcome in the UI

**Branch** `feat/u19b-eso-refresh-observer` · **PR** *feat(eso): observe refresh outcomes instead of only acceptance*
**Covers** R13; AE5 · **Depends on** U19a, U17

**Files (4)**

1. `frontend/lib/eso-refresh-observer.ts` — new
2. `frontend/lib/eso-refresh-observer_test.ts` — new
3. `frontend/islands/ESOExternalSecretDetail.tsx`
4. `e2e/tests/eso-evidence.spec.ts` — extended

**Step 0 check.** Re-measure `ESOExternalSecretDetail.tsx` after U17. It is 292 LOC today; if
U17 pushed it over 300, Agent Directive 1 requires a separate dead-code-cleanup commit
**before** the observer wiring.

**Steps**

1. `eso-refresh-observer.ts` — the pure reducer and predicates from **D6**. No `fetch`, no
   `setTimeout`, no signals; all time arrives as `nowMs` on events.
2. `ESOExternalSecretDetail.tsx` — replace `onForceSync`'s one-shot `forceSyncMsg` string
   (lines 39–61) with the reducer:
   - `POST` → dispatch `{ type: "accepted", baseline, correlation, nowMs }`;
   - a `setTimeout` chain driven by `nextPollDelayMs`, each poll calling
     `esoApi.getExternalSecret` with an `AbortController` signal;
   - responses dispatch `sample` / `forbidden` / `notFound` / `error`; an effect on the
     `selectedCluster` signal dispatches `clusterChanged`; unmount dispatches `cancel`;
   - render one line per phase, with the weak-correlation wording carrying *"observed after
     your request"* rather than any causal claim;
   - on `observedSuccess` / `observedFailure`, refresh the History tab if it is mounted.
3. `eso-refresh-observer_test.ts`:

| Test | Scenario |
|---|---|
| `AE5: pre-existing Ready=True does not satisfy a new request` | baseline `ready=true`, sample identical → stays `awaitingObservation` |
| `strong correlation: later refreshTime yields observedSuccess` | |
| `strong correlation: equal refreshTime stays awaiting` | |
| `weak correlation: later readyLastTransitionTime yields observedSuccess` | |
| `weak correlation: changed syncedResourceVersion yields observedSuccess` | |
| `controller failure yields observedFailure with the reason` | |
| `deadline expiry yields timeout, not failure` | asserts phase `timeout` and that the copy contains no failure claim |
| `403 mid-wait yields accessLost` | |
| `404 mid-wait yields targetChanged` | |
| `uid change mid-wait yields targetChanged` | |
| `clusterChanged yields targetChanged` | |
| `cancel is terminal — later samples are ignored` | |
| `three consecutive errors yield timeout with observation_unavailable` | |
| `an unrelated reconcile under weak correlation is labelled, not claimed` | asserts the copy |
| `nextPollDelayMs is 1s, 2s, 3s, 5s, 5s… capped` | |

4. Append `test.describe("eso evidence — refresh observation")`: force-sync on a live ES shows
   an explicit awaiting state (never an immediate "success"); navigating away mid-wait leaves
   no orphaned banner; switching clusters mid-wait discards the observation.

**Verification** — `cd frontend && deno task check && deno task test && deno task build`;
`cd e2e && npm test`.

**Done means** — requested, observed-success, observed-failure, timeout, cancelled and
access-lost are six visually distinct states, and no state claims causation the evidence does
not support.

---

## Cross-unit sequencing and conflict notes

**Merge order (strict).** `U13 → U14a → U14b → U15 → U16 → U17 → {U18, U19a} → U19b`.
`U18` and `U19a` are independent of each other (different trees) and may run in parallel;
everything else is serial.

**Shared-file collisions**

| File | Units | Resolution |
|---|---|---|
| `backend/internal/server/routes.go` | U14a (history), U15 (evidence events) | Both edit `registerExternalSecretsRoutes`, ~15 lines apart. U14a lands first; U15 rebases and appends after the history line. No same-line overlap. |
| `backend/cmd/kubecenter/main.go` | U14a only | `esoHistoryStore` already exists (line 783); U14a only adds two assignments after line 793. |
| `backend/internal/externalsecrets/handler.go` | U14a only | U15 uses existing seams and adds nothing to `handler.go`. |
| `sanitizeControllerText` / `projectionLevel` | defined in U14a's `history_handler.go`, used by U15's `detail_evidence.go` | Same Go package — no import, no file conflict. U15 must not redefine them. |
| **`frontend/lib/eso-evidence.ts`** | master plan lists it "new" in **both U15 and U16** | **U16 creates it. U15 is backend-only and does not touch the frontend at all** (C2 removed U15's frontend rationale). |
| **`e2e/tests/eso-evidence.spec.ts`** | master plan lists it "new" in **U16, U17, U18 and U19** | **U17 creates it.** U18 and U19b each append a new `test.describe` block. **U16 does not touch e2e** — it ships pure helpers plus an unmounted island, which `deno task test` covers; an e2e spec for a component with no route would be a hollow test. |
| `frontend/islands/ESOExternalSecretDetail.tsx` | U17 (panel), U19b (observer) | Serial. U19b re-reads the file first (Directives 6 and 9) and re-checks the 300-LOC threshold. |
| `frontend/lib/eso-api.ts`, `frontend/lib/eso-types.ts` | U16 only | U19b adds no API call — it reuses `getExternalSecret`. |
| `.github/workflows/fuzz.yml` | U14b only | Two appended matrix rows; no SHA changes (supply-chain cooldown). |

**File-count audit**: U13 = 5, U14a = 5, U14b = 3, U15 = 3, U16 = 5, U17 = 3, U18 = 4,
U19a = 2, U19b = 4. All ≤ 5.

**Per-PR verification (Agent Directive 4 — repo-wide, never scoped)**

- Backend-touching (U13, U14a, U14b, U15, U19a): `cd backend && go vet ./... && go test ./...`,
  plus `scripts/check-cluster-routing.sh` for U14a and U15 (new cluster-aware handlers).
- Frontend-touching (U16, U17, U18, U19b): `cd frontend && deno task check && deno task test && deno task build`.
- E2E-touching (U17, U18, U19b): `cd e2e && npm test`.
- U13 additionally: `KUBECENTER_TEST_DATABASE_URL=… go test -tags=pgintegration ./internal/store/`
  against `make dev-db`, exercising the up → seed → down → up round trip.
- Every PR: `gh run list --limit 1` / `gh run view` after push; `/ce:review` before merge;
  homelab smoke test whenever backend or frontend changed.

**Release-B exit evidence** — AE4 demonstrated end-to-end with two real identities (ES-only
and ES+Secret) against the homelab cluster; AE5 demonstrated with a live force-sync on an ES
already at `Ready=True`; a page-wide grep proving zero `"coming in Phase"` strings remain on
the ESO **evidence** tabs (the two Chain placeholders survive by design and are named in
U18's spec).

---

## Risks and open items

| # | Risk | Impact | Mitigation / owner decision |
|---|---|---|---|
| **R-1** | `involvedObject.uid` field-selector support is assumed for `core/v1` events. If the target API server rejects it, U15's list call 400s. | U15 blocked | The first implementation step is a live `kubectl get events --field-selector involvedObject.uid=<uid>` against the homelab cluster. Fallback: list by namespace and filter in-process on `event.InvolvedObject.UID` — behaviourally identical, cheaper to write, more expensive to run. Decide on evidence, not in this document. |
| **R-2** | The ESO `reason` allowlist (D3) is stated as a **starting point**; the repo does not encode ESO's condition vocabulary. | Over-eager `"Unknown"` at L1 | U14a's first step is to enumerate `status.conditions[].reason` values observed in the homelab cluster and in the ESO source for the installed version, then finalise the map. Unknown → `"Unknown"` is fail-closed, so an incomplete list degrades safely. |
| **R-3** | No PostgreSQL test harness exists anywhere in the repo (C4); the `pgintegration` tests will not run in CI without a service container. | U13's isolation guarantees are unverified in CI | U13 ships the harness build-tagged and documents the local command. **Open item:** adding a `postgres:` service to `ci.yml` is outside Release B's file budget and needs its own PR. Until then, the always-on hermetic tests plus a documented pre-merge local run are the gate. |
| **R-4** | `accessCacheTTL = 60s` lets a revoked `get secrets` grant hold L2 for up to a minute. | Bounded over-disclosure | Accepted; platform-wide precedent; documented in the handler comment and proven independent of the cache by a predicate-mode test. Flagged for the operator docs. |
| **R-5** | The generic `/v1/resources/events` path ignores `involvedObjectKind`/`involvedObjectName` (C3), so **every** non-ESO detail page's Events tab shows the whole namespace. | Pre-existing correctness and least-disclosure defect, wider than ESO | **Deliberately out of Release B scope.** Filed as a follow-up: either teach `parseListParams` a `fieldSelector` passthrough, or filter in `eventAdapter`. Release B must not silently inherit the bug — hence U15's dedicated endpoint. |
| **R-6** | Down-migrating 000019 can fail on genuinely cross-cluster duplicate `(uid, attempt_at)`. | Rollback stalls | `down.sql` header + `NOTES.txt` carry the detector query and state that no automatic resolution exists. Consistent with the 000016/000017 precedent. |
| **R-7** | `resolveESOGVR` duplicates ~20 lines of `yaml.resolveGVR`. | Drift between two discovery walkers | Accepted to keep U15 at 3 files and avoid coupling `externalsecrets` to `internal/yaml`. Documented in the doc comment. **Open item:** if a third caller appears, promote it to `internal/k8s`. |
| **R-8** | `diffKeyCounts` are exposed at L1 (D3). | Cardinality disclosure to an ES-only reader | Judged compliant with R11/AE4 ("no diff key names or sensitive free-form messages") and strictly less than what `spec.data[].secretKey` already gives that reader. **Reversible in one line** (drop the field from the L1 DTO) if review disagrees. |
| **R-9** | Cluster-scoped events require cluster-wide `list events`, which many ESO operators will not hold. | ClusterSecretStore / CES Events tabs commonly show forbidden | This is the intended R12 behaviour, not a bug. The 403 detail names the exact grant so an operator can decide. |
| **R-10** | The 90 s observation bound (D6) may be short for slow providers. | Honest `timeout` rather than a wrong claim | `timeout` is explicitly not a failure and the copy says so. If homelab evidence shows ESO routinely exceeding 90 s, raise the constant — it is one exported value in `eso-refresh-observer.ts`. |
| **R-11** | No mobile parity work is in Release B. | Web/Flutter divergence | R4 is satisfied because U19a is purely additive to the 202 body and no existing wire field changes. Mobile ESO evidence parity belongs to its own milestone; this plan does not claim native parity. |
