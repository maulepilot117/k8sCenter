---
title: "Release A — Personal Saved Views and Pins — Implementation Plan"
parent_plan: docs/plans/2026-09-10-1315-feat-feature-expansion-plan.md
release: A
units: U1–U5 (+ deferred U38/U6) → implemented as U1, U2, U3, U4, U5a, U5b
migration_sequence: 000018
date: 2026-09-10
---

# Release A — Personal Saved Views and Pins — Implementation Plan

Covers master-plan requirements **R1, R2, R3, R4, R5, R6**, decisions **KTD1, KTD3, KTD4**,
acceptance example **AE1**, and the Preferences row of the *Proposed Data and API Boundaries*
table. **R7 (dashboard layouts) is explicitly out of scope** and lives in the deferred appendix.

Everything below is grounded in files actually read at revision `b9eb8171`. Where the master
plan is wrong about the codebase, the correction is stated bluntly in **Codebase Findings** and
carried through into the per-unit file lists.

---

## Codebase Findings

### Backend

| File (read) | What it constrains |
|---|---|
| `backend/internal/store/store.go` | `type DB struct { Pool *pgxpool.Pool; logger *slog.Logger }`. `New(ctx, connString, maxConns, minConns, logger) (*DB, error)` connects with retry then calls `db.migrate(connString)`. Every store takes a `*pgxpool.Pool`, never the `*DB`. |
| `backend/internal/store/migrate.go` | golang-migrate v4 over `//go:embed migrations/*.sql` (`iofs` source). `m.Up()` only; a dirty schema hard-fails startup. **Migrations run automatically at boot** — no separate migrate command exists in-repo. |
| `backend/internal/store/eso_history.go` | Canonical store idiom: package-level struct `ESOHistoryStore{pool *pgxpool.Pool}`, `NewESOHistoryStore(pool)`, methods take `ctx` first, errors wrapped `fmt.Errorf("verb table: %w", err)`, `pgx.ErrNoRows` → `(nil, nil)` for "latest" reads, rows scanned field-by-field with `defer rows.Close()` + `rows.Err()`. Doc comments carry RBAC contracts for callers. |
| `backend/internal/store/eso_bulk_jobs.go` | JSONB idiom: `json.Marshal` to `[]byte`, pass as a bind param, `Scan(&raw []byte)` then `json.Unmarshal`. Imports `github.com/google/uuid` (v1.6.0, in `go.mod`) and `github.com/jackc/pgx/v5/pgconn` for `*pgconn.PgError` code inspection. |
| `backend/internal/store/clusters.go` | Record structs carry `json:` tags directly and use `json:"-"` to keep sensitive columns out of responses. `ErrNotFound`-style sentinels are per-package. |
| `backend/internal/store/settings.go` | Cached-service pattern; irrelevant to preferences (no cache wanted — preferences are per-user and low-volume). |
| `backend/internal/store/migrations/` | 17 pairs, `NNNNNN_name.up.sql` / `.down.sql`. **`000017` is the highest; `000018` is free.** Style: `CREATE TABLE IF NOT EXISTS`, inline `CHECK (col IN (...))` enums, `CREATE INDEX IF NOT EXISTS`, `COMMENT ON TABLE` for intent, `gen_random_uuid()` for UUID PKs (proven in `000015`). Down migrations `DROP INDEX IF EXISTS` then `DROP TABLE IF EXISTS` (`000015` pattern). |
| `backend/internal/store/migrations/NOTES.txt` | "One section per migration that needs a heads-up beyond what the .up.sql comment block already says." Additive, non-destructive, no-backfill migrations get no entry. **000018 needs no NOTES.txt entry** — saving a file slot. |
| `backend/internal/auth/provider.go` | `type User struct { ID, Username, Provider, KubernetesUsername string; KubernetesGroups, Roles []string }`. `auth.UserFromContext(ctx) (*User, bool)` / `auth.ContextWithUser(ctx, u)`. `auth.IsAdmin(u)`. |
| `backend/internal/auth/oidc.go:293-296` | `ID: fmt.Sprintf("oidc:%s:%s", p.Config.ID, subject)`, `Provider: "oidc"`. |
| `backend/internal/auth/ldap.go:325-328` | `ID: fmt.Sprintf("ldap:%s:%s", p.config.ID, entry.DN)`, `Provider: "ldap"`. |
| `backend/internal/auth/local.go:55,240` | Local users get `ID: r.ID` — a generated opaque id from `UserStore`. So `User.ID` is provider-qualified for OIDC/LDAP and an opaque id for local. **Master plan is correct on this point.** |
| `backend/internal/server/routes.go` (902 LOC) | Authenticated group at `r.Group(func(ar chi.Router){ ar.Use(middleware.Auth(...)); ar.Use(middleware.CSRF); ar.Use(middleware.ClusterContext) ... })`. Optional features are `if s.XHandler != nil { s.registerXRoutes(ar) }` with one `register*Routes` func per feature. |
| `backend/internal/server/routes.go:864-899` | `registerNotifCenterRoutes` — **the exact model for preferences**: `/notifications/devices` is a per-user sub-route with *no* `RequireAdmin`, and the comment reads "All reads and writes are scoped to the calling user inside the handler." |
| `backend/internal/server/server.go` (391 LOC) | `Server` struct (L44-89) mirrors `Deps` (L91-136); `New(deps)` assigns core deps in one literal (L139-162) and each *optional* handler in its own `if deps.X != nil { s.X = deps.X }` block (L298-327). |
| `backend/internal/server/middleware/auth.go:90-101` | `CSRF` rejects POST/PUT/PATCH/DELETE lacking `X-Requested-With`. GET is exempt. |
| `backend/internal/server/middleware/cluster.go` | `ClusterContext` reads `X-Cluster-ID`, caps it at 64 chars, forces admin for non-`local`, stores it in context. `middleware.ClusterIDFromContext(ctx) string` returns `"local"` by default. **This is the only trustworthy source of cluster identity in a handler.** |
| `backend/internal/httputil/response.go` | `WriteJSON(w, status, v)`, `WriteError(w, status, message, detail)` (strips `detail` on 5xx), `WriteData(w, data)`, `WriteErrorWithReason(w, status, message, reason, extra)`, `RequireUser(w, r) (*auth.User, bool)`. |
| `backend/pkg/api/*.go` | `api.Response{Data any; Metadata *Metadata; Error *APIError}`; `api.Metadata{Total, Continue, Page, PageSize}`; `api.APIError{Code, Message, Detail, Reason, Extra}`. |
| `backend/internal/externalsecrets/handler.go` + `handler_test.go` | Handler is a plain struct with exported dependency fields (no constructor required), plus unexported test-only override seams. Tests are `package externalsecrets` (internal), build `httptest.NewRequest`, inject identity with a local `withUser(r, u)` helper wrapping `auth.ContextWithUser`, route through `chi.NewRouter()` when URL params are needed, and assert on decoded JSON envelopes. Table-driven where the axis is data. |
| `backend/internal/notifications/store.go` | `var ErrNotFound = errors.New("not found")`; `tag.RowsAffected() == 0` → `ErrNotFound` on UPDATE/DELETE. |
| `backend/internal/notifications/handler.go:461-538` | Per-user device CRUD. **Uses `user.KubernetesUsername` as the owner key, not `user.ID`.** See Correction #4. |
| `backend/internal/audit/logger.go` | `audit.Entry{Timestamp, ClusterID, User, SourceIP, ConnectionIP, Action, ResourceKind, ResourceNamespace, ResourceName, Result, Detail}`; generic `ActionCreate/ActionUpdate/ActionDelete`; `ResultSuccess/Failure/Denied`; `Logger` interface is `Log(ctx, Entry) error`. |
| `backend/internal/externalsecrets/actions.go:300-311` | How a non-`server` package writes audit entries: literal `audit.Entry{...}` with `ClusterID: middleware.ClusterIDFromContext(r.Context())`, `User: user.Username`, `SourceIP: r.RemoteAddr`. (`server.newAuditEntry` at `internal/server/response.go:94` is a `*Server` method and is **not** reachable from a new package.) |
| `backend/internal/k8s/resources/registry.go` | `resources.GetAdapter(kind) ResourceAdapter`, `resources.RegisteredKinds() []string`. Adapters self-register in `init()`. `ResourceAdapter.Kind()` returns "the lowercase plural kind used in URL routing (e.g. `deployments`)"; `ClusterScoped() bool`; `APIResource() string`. **This is the server-side allowlist for both saved-view `resourceKind` and pin `resourceKind` — no new registry needed.** |
| `backend/cmd/kubecenter/main.go` (965 LOC) | Store construction lives in the `if cfg.Database.URL != ""` block (L197-219) which sets `dbPool`. Feature stores are built later against `dbPool != nil` (ESO history L783-786; ESO bulk L824-832; notifications L714-726). `server.Deps{...}` literal at L878-921. Package alias is `appstore` for `internal/store`. |
| `backend/internal/store/*_test.go` | **DOES NOT EXIST — zero test files in the entire `store` package.** See Correction #1. |
| `scripts/check-cluster-routing.sh` | Exists. The Verification Contract cites it for "new cluster-aware handlers". Preferences handlers do not construct k8s clients, so this guard is informational here — run it anyway before PR. |

### Frontend

| File (read) | What it constrains |
|---|---|
| `frontend/deno.json` | Tasks: `check` = `deno fmt --check . && deno lint . && deno check`; `test` = `deno test -A`; `build` = `vite build`. Lint tags `["fresh","recommended"]`. `nodeModulesDir: "manual"`. **`@std/assert` is not in the import map** — tests import `"jsr:@std/assert@1"` by full specifier. |
| `frontend/lib/api.ts` (302 LOC) | Header banner: *"Client-only module — MUST NOT be imported in server-rendered components"* because of module-level `accessToken`, `refreshPromise`, `on403Callback`. `api<T>(path, options: RequestInit & { signal?: AbortSignal })` prefixes `/api`, injects `Authorization: Bearer`, `X-Cluster-ID: selectedCluster.value` (**unconditional, read at request time**), `X-Requested-With: XMLHttpRequest` for any non-GET, auto-refreshes once on 401, throws `ApiError{status, code, detail, body, reason}` on non-2xx, returns `{data: undefined}` on 204. **`apiGet` is the only convenience wrapper accepting a signal (`apiGet<T>(path, signal?)`); `apiPost`/`apiPut`/`apiDelete`/`apiPostRaw` accept none.** |
| `frontend/lib/cluster.ts` (20 LOC) | Sole export `selectedCluster = signal(stored ?? "local")`, localStorage key `k8scenter.selectedCluster`. **No subscribe API and no cluster-change event.** |
| `frontend/lib/namespace.ts` (65 LOC) | Same client-only banner. `selectedNamespace = signal<string>(stored ?? "all")`, localStorage key `k8scenter.selectedNamespace`, and the key is **removed** when the value is `"all"`/falsy. `filterByNamespace(items, ns)`. Namespace scope is **app-global**, not per-table. |
| `frontend/lib/nav.ts` (30 LOC) | Documents the SSR hazard verbatim: *"Deno DEFINES localStorage during SSR, but touching it on a read-only rootfs throws … and crashes the server on boot. IS_BROWSER is false during SSR."* Any new module that touches localStorage must guard on `IS_BROWSER` from `fresh/runtime`. |
| `frontend/lib/k8s-links.ts` (23 LOC) | `resourceHref(kind, namespace?, name?): string \| null` built from `RESOURCE_DETAIL_PATHS` in `lib/constants.ts` plus a 3-entry `KIND_PLURALS` irregular map. **This is the pin → URL mechanism; no new routing needed.** |
| `frontend/islands/ResourceTable.tsx` (**839 LOC**) | Props `ResourceTableIslandProps { kind; title; clusterScoped?; enableWS?; createHref?; hideHeader? }` (L37-50). All state is `useSignal`. **The complete saved-view surface is four signals**: `search` (L126), `statusFilter` (L127, values `all\|running\|pending\|failed\|progressing`, chips only rendered for `deployments\|statefulsets\|daemonsets\|pods`), `sortKey` (L128, default `"name"`), `sortDir` (L129, `"asc"\|"desc"`). Namespace comes from the global `selectedNamespace` via `const ns = useComputed(...)` (L151). `PAGE_SIZE = 100` is a module constant (L52) with cursor pagination. **No column-visibility state exists anywhere.** The sort comparator (L357-376) handles **only** `name`, `namespace`, `age`. Fetch cancellation already correct (`fetchAbort` ref, L158, aborted on refetch and unmount L292). |
| `frontend/islands/ResourceTable.tsx` — Directive 1 check | 839 LOC > 300, so Agent Directive 1 nominally applies. **Evidence says the Step-0 cleanup is a no-op**: zero `console.*` calls, all six props used (`clusterScoped` L152, `enableWS` L272/L299, `hideHeader` L571, `createHref` L636/L639), no unused exports. Record that evidence in the PR body instead of shipping an empty cleanup commit. |
| `frontend/islands/SecondaryNav.tsx` (**329 LOC**) | Props `{ currentPath: string }`; mounted from `frontend/routes/_layout.tsx:55`. One local signal `query` (L67). Renders header (L126-197), filter input (L200-241), then a scrolling container (L244) wrapping `groups.map(...)` (L245). **Pinned section anchor: immediately inside the scroll container at L244-245, before `groups.map`.** Items are `<a>` rows carrying a health dot, an ellipsised label and a `<CountBadge>`. `NavItem` comes from `lib/constants.ts` and is a **static taxonomy** — pins need their own type and render branch. |
| `frontend/islands/ResourceDetail.tsx` (**1401 LOC**) | Props `{ kind; name; namespace?; clusterScoped?; title }` (L48-54). Header/actions are delegated to `components/k8s/DetailShell.tsx` (`DetailShellProps { icon, title, subtitle, status, actions, tabs, active, onTab, rail, children }`); mount at L1098-1108. `actionButtons` is built at L974-1058 and is **`undefined` whenever `actions.value.length === 0`** (RBAC-filtered) — a pin button appended naively there disappears for read-only users. UID is available as `resource.value?.metadata.uid` but **only after the fetch resolves**. **No `apiVersion`/group/version is typed on `K8sResource`, and this island does not import `lib/cluster.ts`.** |
| `frontend/lib/*_test.ts` (7 files) | `compliance-violations_test.ts`, `eso-yaml-templates_test.ts`, `format_test.ts`, `nav-domain_test.ts`, `score-color_test.ts`, `secretstore-template-nav_test.ts`, `wizard-constants_test.ts`. Idiom is uniformly `import { assertEquals } from "jsr:@std/assert@1";` + relative `./x.ts` + `Deno.test("name", () => { ... })` with **no options object**. **There are no tests under `islands/`, `components/` or `routes/`, and no DOM/component harness exists.** |
| `frontend/islands/DashboardV2.tsx` (1084 LOC) | Six hard-coded `<WidgetShell>` blocks, zero props, **no widget registry, no ids, no ordering array**. Deferred-appendix input only. |
| localStorage inventory | Only 6 keys exist (`k8scenter.selectedCluster`, `k8scenter.selectedNamespace`, `kc.navCollapsed`, `kc.theme`, `k8scenter-animations`, legacy `k8scenter-theme`) plus a dead `access_token` read in `LogLiveTail.tsx:57`. **No existing view/filter persistence of any kind.** |

### E2E

| File (read) | What it constrains |
|---|---|
| `e2e/playwright.config.ts` | `testDir: "./tests"`. Three projects: `setup` (`testDir: "./fixtures"`, `testMatch: /.*\.setup\.ts/`), `chromium` (`storageState: "playwright/.auth/admin.json"`, `dependencies: ["setup"]`, `testIgnore: /api-routes\.spec\.ts/`), `route-contract` (`testMatch: /api-routes\.spec\.ts/`, `dependencies: ["chromium"]`). `fullyParallel: false`. Two sequential `webServer` entries: Go backend on :8080 with `KUBECENTER_DEV=true` and `KUBECENTER_DATABASE_URL` defaulting to `postgresql://k8scenter:k8scenter@localhost:5432/k8scenter?sslmode=disable`, then the Fresh frontend. |
| `e2e/fixtures/auth.setup.ts` | Idempotent `POST /api/v1/setup/init` with `setupToken: "e2e-setup-token"` and `failOnStatusCode: false`, then a **UI login** (`admin`/`admin123`), then a second API login whose `data.accessToken` is written to `localStorage["e2e_access_token"]`, then `storageState({path: authFile})`. |
| `e2e/fixtures/base.ts` | Exports the extended `test`/`expect`. Sets `emulateMedia({reducedMotion:"reduce"})` and installs an `addInitScript` that monkey-patches `globalThis.fetch` to attach `Authorization: Bearer <localStorage e2e_access_token>` to any `/api/` or `/ws/` URL. |
| `e2e/helpers.ts` | Exports `getAuthHeaders(page)`, `e2eName(kind)`, `deleteResource(request, kind, ns, name)`, `waitForTableLoaded(page)`. |
| `e2e/package.json` | Exactly two scripts: `"test": "playwright test"`, `"test:smoke": "playwright test --grep @smoke"`. |
| Orphaned specs | `e2e/flux-notifications.spec.ts`, `e2e/namespace-limits.spec.ts`, `e2e/velero.spec.ts` sit at the `e2e/` root, **outside `testDir`, so 893 lines never run.** New specs MUST go in `e2e/tests/`. |

### Where the master plan is wrong

**Correction 1 — U1's `preferences_test.go` has no harness to inherit.**
The master plan's Verification Contract demands "Real PostgreSQL migration round trips" and U1's exit criterion is "Store supports isolated CRUD". But `backend/internal/store/` contains **zero `*_test.go` files**, and a repo-wide grep for `testcontainers`, `dockertest`, `TEST_DATABASE`, `KUBECENTER_TEST_DB` returns nothing. There is no DB test harness anywhere in the backend's 147 test files. U1 must **create** one. That is a fifth file (`backend/internal/store/testdb_test.go`) and it changes CI's meaning: `go test ./...` will *skip* the DB tests unless an env var is set. This plan resolves it with an env-gated `t.Skip` harness and flags the CI wiring as an open item.

**Correction 2 — U3's file list double-counts a U2 file.**
The master plan lists `backend/internal/preferences/handler_test.go` as **new** in *both* U2 and U3. It can only be new once. U3's real fifth slot is unnecessary: U3 needs `main.go` plus the three frontend client files. Corrected below (U3 = 4 files).

**Correction 3 — `frontend/lib/preferences.ts` is listed as "new" in both U3 and U4.**
Resolved: **U3 creates `frontend/lib/preferences.ts` complete** (views *and* pins transport) and **U4/U5 do not touch it**. Master plan's U4 list is corrected accordingly.

**Correction 4 — the owner-key precedent in the repo disagrees with the plan.**
The master plan (Assumptions) says "Personal settings use the authenticated `auth.User.ID`". The one existing per-user table, `mobile_push_devices`, keys on `user.KubernetesUsername` (`notifications/handler.go:493,512,532`). This plan follows the **master plan** (`auth.User.ID`) because `KubernetesUsername` is derived from claim/attribute mapping and can be re-pointed by an admin, silently transferring another user's saved data. The divergence is deliberate and is called out as an open item.

**Correction 5 — U4's "restore only allowlisted table state" is a much smaller set than implied.**
The saved-view payload can capture exactly four things: `search`, `statusFilter`, `sortKey`, `sortDir` (plus the app-global namespace). **Column visibility does not exist** (`components/ui/DataTable.tsx` `Column` has no `hidden`/`visible` field and `ResourceTable.tsx` maps `RESOURCE_COLUMNS[kind]` straight through). **Page size does not exist** (`PAGE_SIZE = 100` module constant, cursor pagination). Any plan text implying a persisted column set or page size is describing net-new features, not persistence. They are out of scope for Release A.

**Correction 6 — sort round-trip is lossy today.**
`ResourceTable.tsx:357-376` only compares `name`, `namespace`, `age`. A saved view holding any other `sortKey` will silently sort by name. The allowlist is therefore exactly those three, enforced server-side *and* client-side, and an out-of-allowlist stored value must surface as a visible degradation notice rather than a silent fallback (R3).

**Correction 7 — KTD3's full `group/version/kind/resource` pin key is not obtainable on the client.**
`K8sResource` (`lib/k8s-types.ts:69-73`) has no typed `apiVersion`; `ResourceDetail.tsx` has neither group/version nor a cluster id. Building it would mean new discovery plumbing in a Release-A UI unit. **Resolution:** the pin key is `(clusterID from X-Cluster-ID, adapter kind slug, namespace, name)` and the adapter slug is validated server-side against `resources.GetAdapter()`, which maps 1:1 onto a single GVR in the existing routing table. The optional `uid` is stored as *evidence*, not as part of identity, so a recreated same-name object collides with the existing pin and is reported as **replaced** rather than silently inheriting the old identity (R1/R6). Group/version fields are reserved in the JSONB envelope (`"group": "", "version": ""`) so a later CRD-pin release can populate them without a schema break.

**Correction 8 — U5's "keyboard navigation" scenario has no test vehicle in the frontend.**
There is no DOM/component test harness. Every island assertion must be a Playwright spec in `e2e/tests/`, and only pure `lib/` modules are unit-testable. This forces the pure-module split used throughout the unit plan below.

**Correction 9 — U5's touch set cannot fit in five files if `ResourceDetail.tsx` is included.**
`ResourceDetail.tsx` is 1401 LOC and its `actionButtons` ternary must be restructured (Finding above). Combining it with `PinnedResources.tsx` + `SecondaryNav.tsx` + a pure module + a spec exceeds the cap and mixes a large-file edit with greenfield work. **U5 is split into U5a (nav-side pins) and U5b (detail-side pin toggle + E2E).**

**Correction 10 — cluster switching does not re-fetch.**
`ResourceTable`'s effect deps are `[kind, ns.value, enableWS]` and `ResourceDetail`'s are `[kind, name, namespace]`; neither reads `selectedCluster`. Per-cluster views and pins therefore need explicit `selectedCluster.value` reactivity added in the new islands (`PinnedResources`, `SavedViews`), not inherited.

**Correction 11 — localStorage-backed state would leak across E2E tests.**
`auth.setup.ts` captures `storageState` into `playwright/.auth/admin.json`, which every `chromium`-project test loads. Any localStorage key added for views/pins would persist across the whole suite. This plan keeps **all** view/pin state server-side; the only client persistence remains the pre-existing cluster/namespace signals.

---

## Design Decisions

### D1. Ownership model

Owner key is `auth.User.ID` verbatim, supplied **only** by the server from `httputil.RequireUser(w, r)`. A client-supplied `ownerId` in any request body is ignored, never echoed, and never used for lookup. Every store method takes `ownerID` as its first non-context parameter and every SQL statement carries `owner_id = $n` — including the `Get` used to disambiguate 404 from 409, so a guessed record id belonging to another user always reads as 404 with no metadata.

No foreign key to `local_users`: OIDC/LDAP ids (`oidc:<providerID>:<sub>`, `ldap:<providerID>:<dn>`) have no row there.

### D2. Authorization at read time

Preferences store **no cluster-derived content** — only strings the user themselves typed or selected. Listing them therefore discloses nothing the user did not author, and no RBAC re-check is performed at preferences read time. Authorization is enforced where it always was: when the saved view issues `GET /v1/resources/:kind` or the pin opens `GET /v1/resources/:kind/:ns/:name`, both of which already impersonate. A pin whose target the user can no longer read renders as **forbidden**, distinct from **deleted** and from **replaced** (R3). This reasoning must appear as a doc comment on the preferences handler.

### D3. Table DDL — `000018_create_user_preferences`

`backend/internal/store/migrations/000018_create_user_preferences.up.sql`:

```sql
-- Personal saved views and resource pins (Release A; R5/R6, KTD3/KTD4).
--
-- owner_id is auth.User.ID verbatim: an opaque id for local users,
-- "oidc:<providerID>:<sub>" for OIDC, "ldap:<providerID>:<dn>" for LDAP.
-- It is deliberately NOT a foreign key to local_users — external identities
-- have no row there. Deleting a local user does not cascade; orphaned rows
-- are inert and are cleaned up by the operator, not by a cascade.
--
-- config is a validated, versioned JSONB envelope. The server rejects any
-- field outside the per-kind allowlist before the row is written, so the
-- database is the second line of defence, not the first.

CREATE TABLE IF NOT EXISTS user_preferences (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       TEXT        NOT NULL,
    kind           TEXT        NOT NULL CHECK (kind IN ('saved_view', 'pin')),
    name           TEXT        NOT NULL,
    cluster_id     TEXT        NOT NULL DEFAULT 'local',
    dedup_key      TEXT        NOT NULL,
    schema_version INTEGER     NOT NULL DEFAULT 1,
    revision       BIGINT      NOT NULL DEFAULT 1,
    config         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT user_preferences_owner_len   CHECK (char_length(owner_id)  BETWEEN 1 AND 512),
    CONSTRAINT user_preferences_name_len    CHECK (char_length(name)      BETWEEN 1 AND 128),
    CONSTRAINT user_preferences_cluster_len CHECK (char_length(cluster_id) BETWEEN 1 AND 64),
    CONSTRAINT user_preferences_dedup_len   CHECK (char_length(dedup_key) BETWEEN 1 AND 512),
    CONSTRAINT user_preferences_schema_ver  CHECK (schema_version >= 1),
    CONSTRAINT user_preferences_revision    CHECK (revision >= 1),
    CONSTRAINT user_preferences_config_size CHECK (pg_column_size(config) <= 8192)
);

-- Identity/dedup. For saved views dedup_key is lower(name); for pins it is
-- "<resourceKind>/<namespace>/<name>". UID is intentionally NOT part of the
-- key: a recreated same-name object must collide with the existing pin so it
-- can be reported as replaced rather than silently duplicating (R1, R6).
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_preferences_dedup
    ON user_preferences (owner_id, kind, cluster_id, dedup_key);

CREATE INDEX IF NOT EXISTS idx_user_preferences_owner_kind
    ON user_preferences (owner_id, kind, updated_at DESC);

COMMENT ON TABLE user_preferences IS
    'Per-user saved views and resource pins. Owner is auth.User.ID (provider-qualified). config is an allowlisted, schema-versioned JSONB envelope; revision drives optimistic concurrency. No cluster-derived content is stored — authorization is enforced when the referenced resource is fetched, not here.';
```

`000018_create_user_preferences.down.sql`:

```sql
-- Fully reversible: the table is additive and nothing else references it.
-- Rolling back destroys every user's saved views and pins; there is no
-- backfill and no dependency in any other table.
DROP INDEX IF EXISTS idx_user_preferences_owner_kind;
DROP INDEX IF EXISTS idx_user_preferences_dedup;
DROP TABLE IF EXISTS user_preferences;
```

No `NOTES.txt` entry: the migration is additive, has no backfill, and imposes no operator action.

### D4. JSONB envelopes

Saved view (`kind = 'saved_view'`, `schema_version = 1`):

```json
{
  "schemaVersion": 1,
  "resourceKind": "pods",
  "namespace": "",
  "search": "nginx",
  "statusFilter": "failed",
  "sortKey": "age",
  "sortDir": "desc"
}
```

Pin (`kind = 'pin'`, `schema_version = 1`):

```json
{
  "schemaVersion": 1,
  "resourceKind": "deployments",
  "group": "",
  "version": "",
  "namespace": "prod",
  "name": "api",
  "uid": "8b1e...",
  "displayKind": "Deployment"
}
```

`group`/`version` are reserved-empty in Release A (Correction 7) so a later CRD-pin release adds values without a schema-version bump.

### D5. Allowlists (enforced server-side in `preferences/types.go`, mirrored client-side in `preference-types.ts`)

| Field | Allowed values | Source of truth |
|---|---|---|
| `resourceKind` | any `k` where `resources.GetAdapter(k) != nil` | `backend/internal/k8s/resources/registry.go` |
| `namespace` | `""` (all) or DNS-1123 label, ≤63 chars | `ResourceTable` `ns` computed value |
| `search` | free text, ≤256 chars, control characters rejected | `ResourceTable:126` |
| `statusFilter` | `all`, `running`, `pending`, `failed`, `progressing` | `ResourceTable:698-707` |
| `sortKey` | `name`, `namespace`, `age` | `ResourceTable:357-376` comparator |
| `sortDir` | `asc`, `desc` | `ResourceTable:129` |
| pin `name` | DNS-1123 subdomain, ≤253 chars | k8s object names |
| pin `uid` | `[A-Za-z0-9-]{0,64}` | `metadata.uid` |
| record `name` (label) | 1–128 chars, control characters rejected | DDL check |

Any field not listed is **dropped** during normalization before the write; any listed field carrying an out-of-allowlist value is a **400**, never a silent default. A *stored* value that later falls outside the allowlist (schema drift) is surfaced to the UI as a degradation warning, not silently coerced.

### D6. Limits

`MaxSavedViewsPerUser = 100`, `MaxPinsPerUser = 200`, enforced inside the INSERT statement (conditional insert, so it is race-free without an explicit transaction). Exceeding returns **409** with `reason: "limit_reached"` and `extra: {"limit": 100}`.

### D7. Optimistic concurrency

Every record carries `revision BIGINT` starting at 1. `PUT` requires the client's `revision`; the UPDATE matches on it and increments. Zero rows affected → owner-scoped existence probe → **409** `reason: "revision_conflict"` (record exists) or **404** (it does not, including "belongs to someone else").

### D8. API surface

All under the existing authenticated group (auth + CSRF + ClusterContext already applied).

| Method | Path | Body | Success |
|---|---|---|---|
| GET | `/api/v1/preferences/views` | – | `200 {"data":[Record],"metadata":{"total":n}}` |
| POST | `/api/v1/preferences/views` | `{name, config}` | `201 {"data":Record}` |
| PUT | `/api/v1/preferences/views/{id}` | `{name, revision, config}` | `200 {"data":Record}` |
| DELETE | `/api/v1/preferences/views/{id}` | – | `204` |
| GET | `/api/v1/preferences/pins` | – | `200 {"data":[Record],"metadata":{"total":n}}` |
| POST | `/api/v1/preferences/pins` | `{name, config}` | `201 {"data":Record}` |
| DELETE | `/api/v1/preferences/pins/{id}` | – | `204` |

Pins have no PUT — a pin is created or removed, never edited.

`Record` wire shape:

```json
{
  "id": "0f6a...",
  "kind": "saved_view",
  "name": "prod failing pods",
  "clusterId": "local",
  "schemaVersion": 1,
  "revision": 3,
  "config": { "...": "..." },
  "createdAt": "2026-09-10T12:00:00Z",
  "updatedAt": "2026-09-10T12:04:11Z"
}
```

`clusterId` is **always** taken from `middleware.ClusterIDFromContext(r.Context())`. A `clusterId` in the request body is ignored (asserted by test). `GET` returns every record the owner has across all clusters, with `clusterId` on each — the UI filters against `selectedCluster.value` so a view saved on another cluster is visible-but-disabled rather than invisible.

Error contract (all use `httputil.WriteError` / `WriteErrorWithReason`):

| Condition | Status | `reason` |
|---|---|---|
| No database configured | 503 | `database_unavailable` |
| Not authenticated | 401 | – |
| Body not JSON / oversize | 400 | – |
| Field outside allowlist | 400 | `invalid_config` |
| Unknown `resourceKind` | 400 | `unknown_resource_kind` |
| Unsupported `schemaVersion` | 400 | `unsupported_schema_version` |
| Duplicate name (views) / already pinned (pins) | 409 | `duplicate_name` / `already_pinned` |
| Per-user limit reached | 409 | `limit_reached` |
| Revision mismatch | 409 | `revision_conflict` |
| Unknown or other-owner id | 404 | – |

Request bodies are capped with `http.MaxBytesReader(w, r.Body, 16<<10)` before decode.

### D9. Audit

Writes log through the injected `audit.Logger` with the **existing generic actions** (`ActionCreate`/`ActionUpdate`/`ActionDelete`) and `ResourceKind: "savedView"` or `"pin"`, `ResourceName: <record name>`, `Detail: "<kind> <id>"`. No new `audit.Action` constants — that would push U2 to six files for no behavioural gain. `ClusterID` comes from `middleware.ClusterIDFromContext`, matching `externalsecrets/actions.go:302`.

### D10. Frontend module split (forced by Correction 8)

- `frontend/lib/preference-types.ts` — **pure**. Zero runtime imports. Types, schema-version constants, allowlists, `normalizeSavedViewConfig`, `normalizePinConfig`, `captureViewState`, `applyViewState`, `pinDedupKey`, `classifyPin`. Safe to import from anywhere including SSR. **This is the only unit-testable frontend surface.**
- `frontend/lib/preferences.ts` — **client-only** (carries the same banner as `api.ts` because it imports it). Transport only: one thin function per endpoint, all routed through `api<T>()` directly so an `AbortSignal` can be threaded (Correction: `apiPost`/`apiPut`/`apiDelete` accept none).
- `frontend/lib/pin-store.ts` — **client-only**. A single module-level `pins` signal plus `loadPins(signal?)`, `addPin`, `removePin`, so `SecondaryNav` and `ResourceDetail` share one list. No localStorage (Correction 11).

---

## U1. Persist personal views and pins

**Branch:** `feat/preferences-store`
**PR title:** `feat(store): user_preferences table and owner-scoped preference store`
**Covers:** R1, R5, R6; KTD3, KTD4. **Depends on:** nothing.

### Files (5)

| File | State |
|---|---|
| `backend/internal/store/migrations/000018_create_user_preferences.up.sql` | new |
| `backend/internal/store/migrations/000018_create_user_preferences.down.sql` | new |
| `backend/internal/store/preferences.go` | new |
| `backend/internal/store/preferences_test.go` | new |
| `backend/internal/store/testdb_test.go` | **new — not in the master plan; see Correction 1** |

### Steps

1. Create the two migration files exactly as in **D3**. Do not add a `NOTES.txt` section.

2. Create `backend/internal/store/preferences.go`, `package store`, imports `context`, `encoding/json`, `errors`, `fmt`, `time`, `github.com/google/uuid`, `github.com/jackc/pgx/v5`, `github.com/jackc/pgx/v5/pgconn`, `github.com/jackc/pgx/v5/pgxpool`.

```go
// PreferenceKind is the user_preferences.kind enum.
type PreferenceKind string

const (
	PreferenceKindSavedView PreferenceKind = "saved_view"
	PreferenceKindPin       PreferenceKind = "pin"
)

// Sentinel errors. Handlers map these onto HTTP status + reason codes;
// they must never be returned to a caller verbatim.
var (
	ErrPreferenceNotFound  = errors.New("preference not found")
	ErrPreferenceConflict  = errors.New("preference revision conflict")
	ErrPreferenceDuplicate = errors.New("preference already exists")
	ErrPreferenceLimit     = errors.New("preference limit reached")
)

// PreferenceRecord is one row of user_preferences. OwnerID and DedupKey are
// server-derived and never serialized to a client.
type PreferenceRecord struct {
	ID            uuid.UUID       `json:"id"`
	OwnerID       string          `json:"-"`
	Kind          PreferenceKind  `json:"kind"`
	Name          string          `json:"name"`
	ClusterID     string          `json:"clusterId"`
	DedupKey      string          `json:"-"`
	SchemaVersion int             `json:"schemaVersion"`
	Revision      int64           `json:"revision"`
	Config        json.RawMessage `json:"config"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// PreferenceStore handles owner-scoped CRUD for user_preferences.
//
// Authorization contract for callers: every method takes ownerID and every
// statement constrains on it. A record id belonging to another owner is
// indistinguishable from a nonexistent one — callers MUST NOT probe for
// existence outside this store.
type PreferenceStore struct{ pool *pgxpool.Pool }

func NewPreferenceStore(pool *pgxpool.Pool) *PreferenceStore

func (s *PreferenceStore) List(ctx context.Context, ownerID string, kind PreferenceKind) ([]PreferenceRecord, error)
func (s *PreferenceStore) Get(ctx context.Context, ownerID string, id uuid.UUID) (*PreferenceRecord, error)
func (s *PreferenceStore) Create(ctx context.Context, rec PreferenceRecord, maxPerKind int) (*PreferenceRecord, error)
func (s *PreferenceStore) Update(ctx context.Context, ownerID string, id uuid.UUID, expectedRevision int64,
	name, dedupKey string, schemaVersion int, config json.RawMessage) (*PreferenceRecord, error)
func (s *PreferenceStore) Delete(ctx context.Context, ownerID string, id uuid.UUID) error
func (s *PreferenceStore) CountByKind(ctx context.Context, ownerID string, kind PreferenceKind) (int, error)
```

3. `List` SQL (ordered newest-updated first, matching `idx_user_preferences_owner_kind`):

```sql
SELECT id, owner_id, kind, name, cluster_id, dedup_key,
       schema_version, revision, config, created_at, updated_at
  FROM user_preferences
 WHERE owner_id = $1 AND kind = $2
 ORDER BY updated_at DESC
```

Scan `config` into a `[]byte` then assign `json.RawMessage(raw)`. Follow `eso_history.go`: `defer rows.Close()`, return `rows.Err()`.

4. `Get` SQL is the same `SELECT` with `WHERE owner_id = $1 AND id = $2`; `pgx.ErrNoRows` → `ErrPreferenceNotFound`.

5. `Create` — race-free limit enforcement in a single statement:

```sql
INSERT INTO user_preferences (owner_id, kind, name, cluster_id, dedup_key, schema_version, config)
SELECT $1, $2, $3, $4, $5, $6, $7
 WHERE (SELECT count(*) FROM user_preferences WHERE owner_id = $1 AND kind = $2) < $8
RETURNING id, owner_id, kind, name, cluster_id, dedup_key,
          schema_version, revision, config, created_at, updated_at
```

- `pgx.ErrNoRows` from `QueryRow(...).Scan(...)` → `ErrPreferenceLimit` (the `WHERE` filtered the row out).
- Unique-violation: `var pgErr *pgconn.PgError; if errors.As(err, &pgErr) && pgErr.Code == "23505" { return nil, ErrPreferenceDuplicate }` (same idiom already used in `eso_bulk_jobs.go`).
- Any other error: `fmt.Errorf("create user_preference: %w", err)`.

6. `Update` — optimistic revision:

```sql
UPDATE user_preferences
   SET name = $4, dedup_key = $5, schema_version = $6, config = $7,
       revision = revision + 1, updated_at = now()
 WHERE id = $1 AND owner_id = $2 AND revision = $3
RETURNING id, owner_id, kind, name, cluster_id, dedup_key,
          schema_version, revision, config, created_at, updated_at
```

On `pgx.ErrNoRows`, run the owner-scoped existence probe:

```sql
SELECT 1 FROM user_preferences WHERE id = $1 AND owner_id = $2
```

Row present → `ErrPreferenceConflict`; `pgx.ErrNoRows` → `ErrPreferenceNotFound`. Unique violation → `ErrPreferenceDuplicate`.
**The probe must carry `owner_id`** — otherwise a cross-user id leaks 409-vs-404.

7. `Delete` — `DELETE FROM user_preferences WHERE id = $1 AND owner_id = $2`; `tag.RowsAffected() == 0` → `ErrPreferenceNotFound` (mirrors `notifications/store.go`).

8. `CountByKind` — `SELECT count(*) FROM user_preferences WHERE owner_id = $1 AND kind = $2`.

9. Create `backend/internal/store/testdb_test.go` — the missing harness:

```go
package store

// testPool returns a pool against a real PostgreSQL instance with all
// migrations applied, or skips the test when no test database is
// configured. There is no in-repo DB test harness to inherit — this file
// introduces the first one (Release A, Correction 1).
//
// Local:  make dev-db  &&  export KUBECENTER_TEST_DATABASE_URL="postgresql://k8scenter:k8scenter@localhost:5432/k8scenter?sslmode=disable"
// Tests isolate by generating a unique owner_id prefix per test rather than
// by creating schemas, so they are safe to run against a shared database
// and never touch rows they did not create.
func testPool(t *testing.T) *pgxpool.Pool
func testOwner(t *testing.T) string // returns "test:" + uuid.NewString()
```

`testPool` reads `KUBECENTER_TEST_DATABASE_URL`, `t.Skip`s when empty, opens a `pgxpool`, runs migrations via the in-package `(&DB{Pool: pool, logger: slog.Default()}).migrate(url)`, registers `t.Cleanup(pool.Close)`, and returns the pool. `testOwner` registers a `t.Cleanup` that deletes every row for that owner.

### Test plan — `backend/internal/store/preferences_test.go`

| Test function | Master-plan scenario / cross-cutting case |
|---|---|
| `TestPreferenceStore_CreateAndList` | baseline CRUD |
| `TestPreferenceStore_TwoUsersSameName_Isolated` | "Two users using identical view names cannot read or overwrite each other's records" |
| `TestPreferenceStore_GetOtherOwner_ReturnsNotFound` | cross-user access |
| `TestPreferenceStore_UpdateOtherOwner_ReturnsNotFound` | cross-user write (must be 404-shaped, not conflict) |
| `TestPreferenceStore_DeleteOtherOwner_ReturnsNotFound` | cross-user delete |
| `TestPreferenceStore_ProviderQualifiedOwnerIDs` | "OIDC and LDAP IDs persist correctly" — table-driven over `local-uuid`, `oidc:corp:sub-123`, `ldap:dir:cn=alice,ou=eng,dc=example,dc=com` |
| `TestPreferenceStore_StaleRevisionConflicts` | "stale revisions conflict" |
| `TestPreferenceStore_UpdateBumpsRevision` | revision monotonicity |
| `TestPreferenceStore_DuplicateDedupKeyRejected` | duplicate name / already-pinned |
| `TestPreferenceStore_SameDedupKeyDifferentCluster_Allowed` | cluster scoping of the unique index |
| `TestPreferenceStore_LimitReached` | 100 views / 200 pins |
| `TestPreferenceStore_OversizedConfigRejected` | "oversized or unsupported configuration is rejected" — >8 KiB JSONB hits the DDL check |
| `TestPreferenceStore_ContextCancelled` | request cancellation — cancel the ctx, assert `context.Canceled` surfaces and no row is written |
| `TestPreferenceStore_PinIdentityIgnoresUID` | deleted/recreated resource: same kind/ns/name with a new uid collides (`ErrPreferenceDuplicate`), it does not create a second pin |
| `TestPreferenceMigration_UpDownUp` | "Migration applies to a populated DB and rollback removes only the newly introduced preference objects" — seed an unrelated table row, migrate down one step, assert `user_preferences` is gone and the unrelated row survives, migrate up again |

"Unavailable DB" is a handler-level concern, covered in U2.

### Verification (run repo-wide, per Agent Directive 4)

```
cd backend && go vet ./... && go test ./...
KUBECENTER_TEST_DATABASE_URL="postgresql://k8scenter:k8scenter@localhost:5432/k8scenter?sslmode=disable" \
  go test ./internal/store/... -run TestPreference -count=1 -v
```

### Exit criteria / done means

- [ ] `000018` up applies to an empty DB and to a DB already carrying 000001–000017 data.
- [ ] `000018` down drops only the two indexes and the one table; every other table's row count is unchanged.
- [ ] Every `PreferenceStore` method constrains on `owner_id`; a cross-owner id is indistinguishable from a missing one.
- [ ] Limits and revision conflicts are enforced by SQL, not by a read-then-write race.
- [ ] `go test ./...` passes with the DB tests skipping (no env var) **and** passes with them running.
- [ ] The store compiles with no reference to `net/http`, `auth`, or any handler package.

---

## U2. Expose validated preference APIs

**Branch:** `feat/preferences-api`
**PR title:** `feat(api): owner-scoped preferences endpoints for saved views and pins`
**Covers:** R2, R5, R6; KTD4. **Depends on:** U1.

### Files (5)

| File | State |
|---|---|
| `backend/internal/preferences/types.go` | new |
| `backend/internal/preferences/handler.go` | new |
| `backend/internal/preferences/handler_test.go` | new |
| `backend/internal/server/routes.go` | existing |
| `backend/internal/server/server.go` | existing |

### Steps

1. `backend/internal/preferences/types.go` — allowlists, envelopes, validation. No `net/http` import.

```go
package preferences

const (
	SavedViewSchemaVersion = 1
	PinSchemaVersion       = 1

	MaxSavedViewsPerUser = 100
	MaxPinsPerUser       = 200

	maxRecordNameLen = 128
	maxSearchLen     = 256
	maxBodyBytes     = 16 << 10
)

var (
	allowedStatusFilters = map[string]struct{}{
		"all": {}, "running": {}, "pending": {}, "failed": {}, "progressing": {},
	}
	// Mirrors the ResourceTable comparator (islands/ResourceTable.tsx:357-376),
	// which sorts only these three keys. Widening this set requires widening
	// that comparator first, or saved views silently sort by name.
	allowedSortKeys = map[string]struct{}{"name": {}, "namespace": {}, "age": {}}
	allowedSortDirs = map[string]struct{}{"asc": {}, "desc": {}}
)

type SavedViewConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	ResourceKind  string `json:"resourceKind"`
	Namespace     string `json:"namespace"`
	Search        string `json:"search"`
	StatusFilter  string `json:"statusFilter"`
	SortKey       string `json:"sortKey"`
	SortDir       string `json:"sortDir"`
}

type PinConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	ResourceKind  string `json:"resourceKind"`
	Group         string `json:"group"`   // reserved-empty in Release A
	Version       string `json:"version"` // reserved-empty in Release A
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	UID           string `json:"uid"`
	DisplayKind   string `json:"displayKind"`
}

// ValidationError carries the reason code the handler puts on the wire.
type ValidationError struct{ Reason, Message string }
func (e *ValidationError) Error() string

// ValidateSavedView normalizes and validates a saved-view envelope.
// Unknown fields are dropped by json.Decoder.DisallowUnknownFields at the
// call site; listed fields carrying out-of-allowlist values are rejected.
func ValidateSavedView(raw json.RawMessage) (SavedViewConfig, json.RawMessage, error)
func ValidatePin(raw json.RawMessage) (PinConfig, json.RawMessage, error)

// SavedViewDedupKey returns strings.ToLower(strings.TrimSpace(name)).
func SavedViewDedupKey(name string) string
// PinDedupKey returns "<resourceKind>/<namespace>/<name>" — UID excluded on
// purpose so a recreated object collides with the existing pin (R1, R6).
func PinDedupKey(c PinConfig) string
```

`ValidateSavedView` rejects: `schemaVersion != SavedViewSchemaVersion` (`unsupported_schema_version`); `resources.GetAdapter(c.ResourceKind) == nil` (`unknown_resource_kind`); a non-empty `Namespace` on a `ClusterScoped()` adapter (`invalid_config`); out-of-allowlist `StatusFilter`/`SortKey`/`SortDir`; `len(Search) > maxSearchLen`; any control character in `Search`. It re-marshals the *typed struct* (not the caller's bytes) so unlisted fields cannot survive into the database.

2. `backend/internal/preferences/handler.go`:

```go
package preferences

// Handler serves per-user preference endpoints.
//
// Authorization contract: the owner is ALWAYS auth.User.ID from the request
// context; the request body's owner (if any) is ignored. cluster identity is
// ALWAYS middleware.ClusterIDFromContext. No RBAC re-check happens here —
// preferences hold only strings the user authored, and the referenced
// resource's authorization is enforced when that resource is fetched.
//
// Store may be nil when no database is configured; every endpoint then
// responds 503 with reason "database_unavailable" rather than pretending to
// persist. Mirrors handle_clusters.go's ClusterStore == nil behaviour.
type Handler struct {
	Store       *store.PreferenceStore // nil when no DB
	AuditLogger audit.Logger
	Logger      *slog.Logger
}

func (h *Handler) HandleListViews(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleCreateView(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleUpdateView(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleDeleteView(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleListPins(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleCreatePin(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleDeletePin(w http.ResponseWriter, r *http.Request)
```

Shared internals:

```go
// requireStore writes 503 and returns false when no database is configured.
func (h *Handler) requireStore(w http.ResponseWriter) bool

// decodeBody applies http.MaxBytesReader and DisallowUnknownFields.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error

// writeRecord serializes a store record to the wire DTO.
func writeRecord(w http.ResponseWriter, status int, rec *store.PreferenceRecord)

// mapStoreError translates store sentinels onto the D8 error table.
func (h *Handler) mapStoreError(w http.ResponseWriter, err error, dupReason string)

// audit records a preference write. Never logs config contents.
func (h *Handler) audit(r *http.Request, user *auth.User, action audit.Action,
	kindLabel, name string, result audit.Result)
```

Each write handler: `httputil.RequireUser` → `requireStore` → `decodeBody` → `Validate*` → build `store.PreferenceRecord{OwnerID: user.ID, Kind: ..., ClusterID: middleware.ClusterIDFromContext(r.Context()), DedupKey: ..., SchemaVersion: ..., Config: normalized}` → store call → `mapStoreError` or `writeRecord` → `h.audit(...)`.

List handlers write `api.Response{Data: records, Metadata: &api.Metadata{Total: len(records)}}` via `httputil.WriteJSON`. Empty result is `"data": []`, never `null` — initialize the slice.

`{id}` is parsed with `uuid.Parse(chi.URLParam(r, "id"))`; a malformed uuid is **404**, not 400, so id-probing yields one indistinguishable response.

3. `backend/internal/server/server.go` — three edits.

Add to the `Server` struct, immediately after the anchor lines

```go
	NotifCenterHandler *notifications.Handler
	NotifCenterService *notifications.NotificationService
```

the line `PreferencesHandler *preferences.Handler`. Add the identical field to `Deps` after its own `NotifCenterHandler`/`NotifCenterService` pair. Add the import `"github.com/kubecenter/kubecenter/internal/preferences"` alongside `"github.com/kubecenter/kubecenter/internal/policy"`.

Then in `New(deps)` insert a new optional-handler block after the existing one at L319-323:

```go
	// Notification center
	if deps.NotifCenterHandler != nil {
		s.NotifCenterHandler = deps.NotifCenterHandler
		s.NotifCenterService = deps.NotifCenterService
	}

	// Personal preferences (saved views + resource pins)
	if deps.PreferencesHandler != nil {
		s.PreferencesHandler = deps.PreferencesHandler
	}
```

4. `backend/internal/server/routes.go` — two edits.

Register inside the authenticated group, right after the notification-center block:

```go
			// Notification center routes
			if s.NotifCenterHandler != nil {
				s.registerNotifCenterRoutes(ar)
			}

			// Personal preferences — per-user saved views and resource pins.
			// No RequireAdmin: all reads and writes are scoped to the calling
			// user inside the handler. Registered even without a database; the
			// handler answers 503 so clients can tell "unavailable" from
			// "not implemented".
			if s.PreferencesHandler != nil {
				s.registerPreferencesRoutes(ar)
			}
```

Then add the function next to `registerNotifCenterRoutes` at the end of the file:

```go
// registerPreferencesRoutes wires the personal saved-view and pin endpoints.
// Modelled on registerNotifCenterRoutes' /devices sub-route: authenticated,
// CSRF-protected by the parent group, owner-scoped inside the handler.
func (s *Server) registerPreferencesRoutes(ar chi.Router) {
	h := s.PreferencesHandler
	ar.Route("/preferences", func(pr chi.Router) {
		pr.Route("/views", func(vr chi.Router) {
			vr.Get("/", h.HandleListViews)
			vr.Post("/", h.HandleCreateView)
			vr.Put("/{id}", h.HandleUpdateView)
			vr.Delete("/{id}", h.HandleDeleteView)
		})
		pr.Route("/pins", func(pnr chi.Router) {
			pnr.Get("/", h.HandleListPins)
			pnr.Post("/", h.HandleCreatePin)
			pnr.Delete("/{id}", h.HandleDeletePin)
		})
	})
}
```

Do **not** apply `resources.ValidateURLParams` — these routes have no `{name}`/`{namespace}` params. Do **not** apply `middleware.RateLimit(s.RateLimiter)` — that is the 5 req/min auth bucket and pin toggling would exhaust it.

### Test plan — `backend/internal/preferences/handler_test.go`

Package-internal tests, `httptest` + a `chi.NewRouter()` for `{id}` routes, `withUser(r, u)` helper copied from `externalsecrets/handler_test.go`. A small in-memory `PreferenceStore` seam is **not** available (the store is a concrete struct), so these tests use the same `KUBECENTER_TEST_DATABASE_URL`-gated pool helper pattern; validation-only tests need no DB.

| Test function | Scenario |
|---|---|
| `TestValidateSavedView_Allowlist` | table-driven over every allowlisted and rejected value of `statusFilter`, `sortKey`, `sortDir` |
| `TestValidateSavedView_UnknownResourceKind` | `resources.GetAdapter` gate → `unknown_resource_kind` |
| `TestValidateSavedView_NamespaceOnClusterScopedKind` | `invalid_config` |
| `TestValidateSavedView_DropsUnlistedFields` | an envelope with `"script"`/`"url"` extras round-trips **without** them (KTD4: no scripts or redirect targets) |
| `TestValidateSavedView_UnsupportedSchemaVersion` | `unsupported_schema_version` |
| `TestValidatePin_DedupKeyExcludesUID` | two UIDs, one dedup key |
| `TestHandler_NoDatabase_Returns503` | **unavailable DB** — `Handler{Store: nil}`, all 7 endpoints answer 503 `database_unavailable`, none answers 200 |
| `TestHandler_Unauthenticated_Returns401` | no user in context |
| `TestHandler_ForgedOwnerInBody_Ignored` | body carries `ownerId` of another user; the stored row's owner is the caller |
| `TestHandler_ForgedClusterIdInBody_Ignored` | body `clusterId: "other"`; stored `cluster_id` equals the `X-Cluster-ID` context value |
| `TestHandler_GuessedRecordID_Returns404NoMetadata` | **cross-user access** — user B GETs/PUTs/DELETEs user A's id; 404 with no name, no cluster, no timestamps in the body |
| `TestHandler_MalformedUUID_Returns404` | id-probing yields the same shape as a real miss |
| `TestHandler_StaleRevision_Returns409Conflict` | **stale revision** → `revision_conflict` |
| `TestHandler_DuplicateName_Returns409` | `duplicate_name` |
| `TestHandler_LimitReached_Returns409WithLimit` | `limit_reached` + `extra.limit` |
| `TestHandler_OversizeBody_Returns400` | `MaxBytesReader` boundary |
| `TestHandler_RequestCancelled_NoPartialWrite` | **request cancellation** — cancel mid-flight, assert no row lands |
| `TestHandler_EmptyList_ReturnsEmptyArray` | `"data": []`, not `null` |
| `TestHandler_WriteEmitsAudit` | a fake `audit.Logger` records `ActionCreate`/`ActionDelete` with `ResourceKind` `savedView`/`pin` and **no config contents in `Detail`** |
| `TestRoutes_PreferencesRequireCSRF` | in `internal/server`: POST without `X-Requested-With` → 403 (add to the existing routes coverage if one exists; otherwise assert in this file via the real middleware chain) |

**Revoked permission** is not testable at this layer by design (D2) and is covered in U5b's E2E, where a pin's target becomes unreadable.

### Verification

```
cd backend && go vet ./... && go test ./...
cd backend && go test ./internal/preferences/... -count=1 -v
bash scripts/check-cluster-routing.sh
```

### Exit criteria / done means

- [ ] All 7 endpoints reachable behind the existing auth + CSRF + ClusterContext group.
- [ ] Every error path in the D8 table is exercised by a named test.
- [ ] `Handler{Store: nil}` returns 503 on every endpoint — no endpoint silently succeeds without persistence.
- [ ] No endpoint reads an owner or cluster id from a request body.
- [ ] Nothing outside the allowlist can reach the `config` column (proved by `TestValidateSavedView_DropsUnlistedFields`).
- [ ] `routes.go` and `server.go` diffs are additive only.

---

## U3. Wire preference persistence and client contracts

**Branch:** `feat/preferences-wiring`
**PR title:** `feat: wire preference store into main and add typed browser client`
**Covers:** R4, R5, R6; KTD1. **Depends on:** U2.

### Files (4) — corrected from the master plan (Corrections 2 and 3)

| File | State | Master plan said |
|---|---|---|
| `backend/cmd/kubecenter/main.go` | existing | same |
| `frontend/lib/preference-types.ts` | new | same |
| `frontend/lib/preference-types_test.ts` | new | listed as `preferences_test.ts` |
| `frontend/lib/preferences.ts` | new | listed as new in U3 **and** U4 — U3 owns it |
| ~~`backend/internal/preferences/handler_test.go`~~ | — | **dropped: created in U2** |

`preference-types_test.ts` replaces the master plan's `preferences_test.ts` because `preferences.ts` imports `api.ts`, which is client-only with module-level singletons and no injectable transport — it is not unit-testable under `deno test` without a fetch monkey-patch, and there is no such precedent in the repo's 7 test files. All testable logic therefore lives in the pure module.

### Steps

1. `backend/cmd/kubecenter/main.go` — one insertion. Anchor on the Gateway/ready block near L870:

```go
	gwDisc := gateway.NewDiscoverer(k8sClient, logger)
	gwHandler := gateway.NewHandler(k8sClient, gwDisc, accessChecker, logger)

	// Personal preferences (saved views + resource pins). prefStore is nil
	// when no database is configured; the handler then answers 503 on every
	// endpoint rather than pretending to persist. Mirrors the esoHistoryStore
	// pattern above.
	var prefStore *appstore.PreferenceStore
	if dbPool != nil {
		prefStore = appstore.NewPreferenceStore(dbPool)
	}
	preferencesHandler := &preferences.Handler{
		Store:       prefStore,
		AuditLogger: auditLogger,
		Logger:      logger,
	}

	// Ready state: true after informer sync, false during shutdown
	var ready atomic.Bool
```

Add `"github.com/kubecenter/kubecenter/internal/preferences"` to the import block, and add one line to the `server.Deps{...}` literal immediately after `NotifCenterService: notifService,`:

```go
		NotifCenterHandler:     notifCenterHandler,
		NotifCenterService:     notifService,
		PreferencesHandler:     preferencesHandler,
```

The handler is constructed **unconditionally** so the route is always registered and a DB-less deployment gets a truthful 503 instead of a 404.

2. `frontend/lib/preference-types.ts` — pure module, zero runtime imports, safe for SSR.

```ts
/**
 * Pure, dependency-free preference contract. Safe to import from routes,
 * components and islands alike — it touches no browser API and no module
 * state. All network access lives in lib/preferences.ts.
 *
 * The allowlists here MUST stay in lockstep with
 * backend/internal/preferences/types.go. The server is authoritative; these
 * copies exist so the UI can refuse an impossible save before a round trip
 * and can explain a stored value that has drifted out of the allowlist.
 */

export const SAVED_VIEW_SCHEMA_VERSION = 1;
export const PIN_SCHEMA_VERSION = 1;
export const MAX_SAVED_VIEWS = 100;
export const MAX_PINS = 200;

export const SAVED_VIEW_STATUS_FILTERS = ["all", "running", "pending", "failed", "progressing"] as const;
export const SAVED_VIEW_SORT_KEYS = ["name", "namespace", "age"] as const;
export const SAVED_VIEW_SORT_DIRS = ["asc", "desc"] as const;

export type StatusFilter = typeof SAVED_VIEW_STATUS_FILTERS[number];
export type SortKey = typeof SAVED_VIEW_SORT_KEYS[number];
export type SortDir = typeof SAVED_VIEW_SORT_DIRS[number];

export interface SavedViewConfig {
  schemaVersion: number;
  resourceKind: string;
  namespace: string;     // "" = all namespaces
  search: string;
  statusFilter: StatusFilter;
  sortKey: SortKey;
  sortDir: SortDir;
}

export interface PinConfig {
  schemaVersion: number;
  resourceKind: string;
  group: string;    // reserved-empty in Release A
  version: string;  // reserved-empty in Release A
  namespace: string;
  name: string;
  uid: string;
  displayKind: string;
}

export interface PreferenceRecord<C> {
  id: string;
  kind: "saved_view" | "pin";
  name: string;
  clusterId: string;
  schemaVersion: number;
  revision: number;
  config: C;
  createdAt: string;
  updatedAt: string;
}

/** Table state a saved view can capture — exactly what ResourceTable holds. */
export interface TableViewState {
  resourceKind: string;
  namespace: string;
  search: string;
  statusFilter: string;
  sortKey: string;
  sortDir: string;
}

export function captureViewState(s: TableViewState): SavedViewConfig;

/**
 * Restores a stored config onto table state, reporting every value that had
 * to be degraded. Callers MUST surface `warnings` — silently coercing an
 * unsupported sortKey to "name" would violate R3 (missing observations must
 * not appear healthy).
 */
export function applyViewState(
  c: SavedViewConfig,
): { state: TableViewState; warnings: string[] };

export function isSupportedSavedViewSchema(v: number): boolean;
export function isSupportedPinSchema(v: number): boolean;

/** "<resourceKind>/<namespace>/<name>" — UID excluded, matching the server. */
export function pinDedupKey(c: PinConfig): string;

export type PinLiveState = "ok" | "replaced" | "missing" | "forbidden" | "unknown";

/**
 * Compares a stored pin against a live lookup outcome. `liveUid` is the
 * fetched object's metadata.uid, or null when the fetch failed.
 * A stored pin with an empty uid resolves "unknown", never "ok" — an
 * un-evidenced pin must not be presented as verified (R3).
 */
export function classifyPin(
  stored: PinConfig,
  outcome: { status: "ok" | "notFound" | "forbidden" | "error"; liveUid?: string },
): PinLiveState;
```

3. `frontend/lib/preferences.ts` — client-only transport for **both** views and pins (so U4 and U5 never touch it).

```ts
/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * It imports lib/api.ts, whose module-level access token is a process-global
 * singleton in Deno; importing this server-side would leak auth state across
 * SSR requests.
 *
 * Every call goes through api<T>() rather than apiPost/apiPut/apiDelete
 * because only api() and apiGet() accept an AbortSignal — callers need to
 * cancel in-flight preference work when the cluster or route changes.
 */
import { api, type ApiError } from "@/lib/api.ts";
import type { PinConfig, PreferenceRecord, SavedViewConfig } from "@/lib/preference-types.ts";

export type SavedViewRecord = PreferenceRecord<SavedViewConfig>;
export type PinRecord = PreferenceRecord<PinConfig>;

export const preferencesApi = {
  listViews: (signal?: AbortSignal) => ...,   // GET  /v1/preferences/views
  createView: (name: string, config: SavedViewConfig, signal?: AbortSignal) => ..., // POST
  updateView: (id: string, name: string, revision: number, config: SavedViewConfig, signal?: AbortSignal) => ..., // PUT /{id}
  deleteView: (id: string, signal?: AbortSignal) => ...,  // DELETE /{id}
  listPins: (signal?: AbortSignal) => ...,    // GET  /v1/preferences/pins
  createPin: (name: string, config: PinConfig, signal?: AbortSignal) => ..., // POST
  deletePin: (id: string, signal?: AbortSignal) => ...,   // DELETE /{id}
};

/** Narrows an unknown error to the preference reason codes from D8. */
export type PreferenceReason =
  | "database_unavailable" | "invalid_config" | "unknown_resource_kind"
  | "unsupported_schema_version" | "duplicate_name" | "already_pinned"
  | "limit_reached" | "revision_conflict";
export function preferenceReason(e: unknown): PreferenceReason | undefined;
```

Implementation notes the generating agent must follow:
- `api<T>()` already prefixes `/api`, so paths start `/v1/preferences/...`.
- `api<T>()` sets `X-Cluster-ID` from `selectedCluster.value` at request time and `X-Requested-With` for every non-GET. Do not set either manually.
- List responses unwrap as `(await api<SavedViewRecord[]>(...)).data ?? []`. Do **not** copy `notifApi`'s double-wrapped `apiGet<{data: T[]}>` typing — that pattern makes callers read `.data.data`.
- `DELETE` returns 204; `api()` yields `{data: undefined}`. Return `void`.
- Do not catch `ApiError` here; let callers classify via `preferenceReason`.

### Test plan — `frontend/lib/preference-types_test.ts`

Idiom: `import { assertEquals, assertThrows } from "jsr:@std/assert@1";` + `import { ... } from "./preference-types.ts";` + bare `Deno.test("name", () => {...})`.

| Test name | Scenario |
|---|---|
| `captureViewState: round-trips the four supported table signals` | `applyViewState(captureViewState(x)).state` deep-equals `x` |
| `applyViewState: unsupported sortKey degrades to name and reports a warning` | Correction 6 — asserts `warnings.length === 1` |
| `applyViewState: unknown statusFilter degrades to all and reports a warning` | R3 |
| `applyViewState: unknown schemaVersion is reported, not silently applied` | "unsupported saved schema version" from the master plan's U4 scenarios |
| `pinDedupKey: identical for two UIDs of the same kind/ns/name` | R1 / recreated resource |
| `classifyPin: same uid returns ok` | baseline |
| `classifyPin: different uid returns replaced` | **deleted/recreated resource** |
| `classifyPin: notFound returns missing` | deleted target |
| `classifyPin: forbidden returns forbidden, never missing` | **revoked permission** stays distinguishable (R3) |
| `classifyPin: empty stored uid never returns ok` | un-evidenced pin |
| `isSupportedSavedViewSchema / isSupportedPinSchema` | version gate |
| `SAVED_VIEW_SORT_KEYS matches the ResourceTable comparator` | a literal-list assertion whose failure is the reminder to widen the comparator |

`preferences.ts` gets no unit test (no transport seam, no precedent); it is covered end-to-end in U4 and U5b.

### Verification

```
cd backend  && go vet ./... && go test ./...
cd frontend && deno task check
cd frontend && deno task test
cd frontend && deno task build
```

### Exit criteria / done means

- [ ] `preferencesHandler` is always non-nil in `main.go`; `prefStore` is nil only when `dbPool` is nil.
- [ ] A DB-less backend returns 503 (not 404) on `GET /api/v1/preferences/views`, verified by hand or by the U2 test.
- [ ] `preference-types.ts` imports nothing at runtime and passes `deno check` when imported from a route file.
- [ ] `preferences.ts` carries the client-only banner and is imported only from islands.
- [ ] Every `preferencesApi` method accepts an optional `AbortSignal`.
- [ ] `deno task check` and `deno task test` pass repo-wide.

---

## U4. Save and reopen resource views

**Branch:** `feat/saved-views-ui`
**PR title:** `feat(ui): saved views for resource tables`
**Covers:** R1, R3, R5; AE1. **Depends on:** U3.

### Files (5) — corrected from the master plan (Correction 3)

| File | State | Master plan said |
|---|---|---|
| `frontend/islands/SavedViews.tsx` | new | same |
| `frontend/islands/ResourceTable.tsx` | existing (839 LOC) | same |
| `frontend/lib/preference-types_test.ts` | existing — extend | listed `preferences_test.ts` as new |
| `e2e/tests/saved-views.spec.ts` | new | same |
| `e2e/helpers.ts` | existing — extend | not listed |
| ~~`frontend/lib/preferences.ts`~~ | — | **dropped: created complete in U3** |

### Steps

0. **Agent Directive 1 / Step 0.** `ResourceTable.tsx` is 839 LOC, so run the dead-code scan first. Expected outcome, from the survey already performed: zero `console.*` calls, all six props used (`clusterScoped` L152, `enableWS` L272/L299, `hideHeader` L571, `createHref` L636/L639), no unused imports or exports. If that holds, **record the evidence in the PR body and skip the cleanup commit** — an empty commit satisfies the letter of the rule and nothing else. If the scan finds anything, land it as a separate commit before step 1.

1. Create `frontend/islands/SavedViews.tsx` — the picker + save/rename/delete control.

```tsx
interface SavedViewsProps {
  /** Adapter kind slug, e.g. "pods". Matches ResourceTableIslandProps.kind. */
  resourceKind: string;
  /** Current table state, read live so "Save" captures what is on screen. */
  current: TableViewState;
  /** Applies a restored view. ResourceTable owns the actual signal writes. */
  onApply: (state: TableViewState, warnings: string[]) => void;
}
```

Behaviour:
- On mount and whenever `selectedCluster.value` changes, call `preferencesApi.listViews(signal)` with a fresh `AbortController`; abort the previous request. **This reactivity is new** — `ResourceTable` does not react to cluster change (Correction 10).
- Filter the returned list to `record.clusterId === selectedCluster.value && record.config.resourceKind === resourceKind`. Views belonging to other clusters are rendered in a disabled group labelled with their cluster id rather than hidden, so R3's "distinguishes unavailable" holds.
- "Save current view" → `createView(name, captureViewState(current))`. On `duplicate_name` offer overwrite (`updateView` with the existing record's `revision`). On `limit_reached` show the 100-view limit explicitly. On `database_unavailable` render the whole control as unavailable with an explanation — never fall back to localStorage.
- Selecting a view → `applyViewState(record.config)` → `onApply(state, warnings)`; render `warnings` as a dismissible notice above the table.
- Rename → `updateView(id, newName, revision, config)`. On `revision_conflict`, re-list and tell the user the view changed elsewhere; do not auto-retry.
- Delete → `deleteView(id)` behind the repo's existing confirm affordance.
- All state is `useSignal`; nothing is written to localStorage (Correction 11).

2. `frontend/islands/ResourceTable.tsx` — additive integration only.

- Import `SavedViews` and `type TableViewState` from `@/lib/preference-types.ts`.
- Build the current state from the four existing signals plus the computed namespace:
  ```ts
  const viewState = useComputed<TableViewState>(() => ({
    resourceKind: kind,
    namespace: ns.value,
    search: search.value,
    statusFilter: statusFilter.value,
    sortKey: sortKey.value,
    sortDir: sortDir.value,
  }));
  ```
- Add `restoreWarnings = useSignal<string[]>([])`.
- `onApply` writes `search`, `statusFilter`, `sortKey`, `sortDir`, sets `restoreWarnings`, and — only when the restored namespace differs — writes `selectedNamespace.value`. **Restoring a namespace mutates app-global state** (Correction 5); the notice must say so before the write, and the write must happen in one batch so the existing `useEffect` on `[kind, ns.value, enableWS]` fires exactly once. The island's existing `fetchAbort` controller (L158) already cancels the superseded load — do not add a second abort path.
- Mount `<SavedViews .../>` in the toolbar row that already holds `<SearchBar>` (L686-692), so it inherits the existing `hideHeader` behaviour.
- Do not add column-visibility or page-size controls (Correction 5) and do not widen the sort comparator in this PR.

3. `e2e/helpers.ts` — add two helpers next to the existing `getAuthHeaders`/`waitForTableLoaded`:

```ts
export async function createSavedView(page, name, config): Promise<string>; // returns record id
export async function deleteAllSavedViews(page): Promise<void>;             // test cleanup
```

Both use `page.request` with `getAuthHeaders(page)`, matching `deleteResource`'s existing shape.

4. `e2e/tests/saved-views.spec.ts` — new spec in `e2e/tests/` (**not** at the `e2e/` root; see Orphaned specs).

### Test plan

Unit additions to `frontend/lib/preference-types_test.ts`:

| Test name | Scenario |
|---|---|
| `captureViewState: namespace "all" is stored as empty string` | matches the `ns` computed value |
| `applyViewState: restoring a view for a cluster-scoped kind yields empty namespace` | mirrors the server's `invalid_config` rule |

Playwright specs in `e2e/tests/saved-views.spec.ts` (project `chromium`, so `storageState` auth applies; `test.describe.serial` because `fullyParallel: false`):

| Spec name | Scenario |
|---|---|
| `saves the current filters and sort and lists the view` | AE1 first half |
| `reopens a saved view in a fresh browser context with the same scope` | **AE1** — `browser.newContext({storageState})`, navigate, apply the view, assert search/filter/sort are restored exactly |
| `does not leak another user's views` | **cross-user access** — create a second local user via `POST /api/v1/users` as admin, log in in a second context, assert the list is empty and a direct `GET /v1/preferences/views/<id>` is 404 |
| `renaming with a stale revision surfaces a conflict` | **stale revision** — mutate via API, then rename in the UI |
| `duplicate view name is offered as an overwrite, not silently dropped` | `duplicate_name` |
| `a view saved on another cluster is shown disabled, never applied silently` | deleted/other cluster (R3, AE1 "no local fallback") |
| `an unsupported stored sortKey shows a degradation notice` | Correction 6 — seed via API with `sortKey: "restarts"`; assert the notice and that the table did not claim the sort applied |
| `a forbidden namespace surfaces the table's forbidden state, not an empty table` | **revoked permission** |
| `switching clusters cancels the in-flight view list` | **request cancellation** — assert no stale list renders |

**Unavailable DB** is not reachable from the E2E harness (the config always provides PostgreSQL); it is covered by `TestHandler_NoDatabase_Returns503` in U2. State that in the spec file header rather than faking it.

### Verification

```
cd frontend && deno task check
cd frontend && deno task test
cd frontend && deno task build
cd e2e && npm test
cd backend && go vet ./... && go test ./...
```

### Exit criteria / done means

- [ ] AE1's first half passes: save → new browser session → reopen with identical scope.
- [ ] Deleted cluster, forbidden namespace, unsupported schema version and revision conflict each render a **distinct** state; none of them renders as an empty-but-healthy table.
- [ ] No localStorage key was added.
- [ ] `ResourceTable.tsx`'s diff is additive: the four existing signals keep their declarations and the existing `fetchAbort` remains the only cancellation path.
- [ ] Step-0 evidence (or the cleanup commit) is recorded in the PR body.
- [ ] The new spec lives in `e2e/tests/` and actually runs (`npx playwright test --list` shows it).

---

## U5a. Personal pin list in the navigation

**Branch:** `feat/pins-nav`
**PR title:** `feat(ui): personal resource pins in the secondary navigation`
**Covers:** R1, R6. **Depends on:** U4.

### Files (4) — U5 split; see Correction 9

| File | State |
|---|---|
| `frontend/lib/pin-store.ts` | new |
| `frontend/islands/PinnedResources.tsx` | new |
| `frontend/islands/SecondaryNav.tsx` | existing (329 LOC) |
| `frontend/lib/preference-types_test.ts` | existing — extend |

### Steps

1. `frontend/lib/pin-store.ts` — client-only shared pin state so `SecondaryNav` and `ResourceDetail` (U5b) do not each fetch.

```ts
/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * Holds a module-level signal, which is a process-global singleton in Deno.
 * Deliberately NOT localStorage-backed: pins are server state, and any
 * localStorage key would be captured into Playwright's storageState and
 * leak across the whole E2E suite.
 */
export const pins = signal<PinRecord[]>([]);
export const pinsLoaded = signal(false);
export const pinsUnavailable = signal<PreferenceReason | undefined>(undefined);

/** Loads pins for the active cluster. Aborts any prior load. */
export async function loadPins(signal?: AbortSignal): Promise<void>;
export async function addPin(name: string, config: PinConfig): Promise<PinRecord>;
export async function removePin(id: string): Promise<void>;
/** Pins for the currently selected cluster only. */
export function pinsForActiveCluster(): PinRecord[];
```

`loadPins` sets `pinsUnavailable` from `preferenceReason(e)` on failure and leaves `pins` untouched, so a transient failure never renders as "no pins".

2. `frontend/islands/PinnedResources.tsx` — the nav section.

```tsx
interface PinnedResourcesProps {
  /** Active route, used to mark the current pin. Mirrors SecondaryNavProps. */
  currentPath: string;
}
```

- Calls `loadPins` on mount and whenever `selectedCluster.value` changes, with an `AbortController` per load (Correction 10).
- Renders nothing but a header when the list is empty for the active cluster; renders an explicit unavailable state when `pinsUnavailable.value` is set — never an empty list (R3).
- Each row is an `<a href={resourceHref(config.resourceKind, config.namespace, config.name) ?? "#"}>` reusing `lib/k8s-links.ts`. When `resourceHref` returns `null` (unknown kind), render a non-navigable row labelled unsupported rather than a dead link.
- The unpin control is a `<button>` **inside** the `<a>`; it must call `e.preventDefault(); e.stopPropagation();` — the whole nav row is an anchor (SecondaryNav finding).
- Long names use the existing ellipsis treatment copied from `SecondaryNav.tsx:304-315`.
- Keyboard: the row anchor is natively focusable; the unpin button needs an `aria-label` of the form `Unpin <displayKind> <name>` and must be reachable in tab order after its row.
- Pins are **not** resolved against the live cluster here. This list shows stored identity only; live classification happens on the detail page (U5b) where the object is fetched anyway. Say so in a comment so nobody later adds an N+1 fan-out from the nav.

3. `frontend/islands/SecondaryNav.tsx` — one additive edit at the scroll-container anchor (L244-245):

```jsx
      {/* grouped items */}
      <div style={{ flex: 1, overflowY: "auto", padding: "2px 10px 16px" }}>
        <PinnedResources currentPath={currentPath} />
        {groups.map((g) => (
```

`PinnedResources` renders its own group header in the existing 11px/600/uppercase/`var(--text-muted)` style (copied from L247-258) so it reads as one more group. Note the early return at L83-93 (`!domain?.groups?.length`) means pins will not render on Overview; either move the pin block above that return or accept the gap — **decide explicitly in the PR** and state which. Recommended: move it above, so pins are reachable from every domain.

`NavItem` from `lib/constants.ts` is a static taxonomy and is **not** reused (SecondaryNav finding); `PinnedResources` owns its own row markup.

### Test plan

Unit additions to `frontend/lib/preference-types_test.ts`:

| Test name | Scenario |
|---|---|
| `pinDedupKey: cluster-scoped pin uses an empty namespace segment` | key stability |
| `classifyPin: error outcome returns unknown, never ok or missing` | **unavailable backend** must stay distinguishable |

Everything else in this unit is island behaviour and is covered by U5b's Playwright spec (Correction 8) — no DOM harness exists. Do not add a placeholder test file that asserts nothing.

### Verification

```
cd frontend && deno task check
cd frontend && deno task test
cd frontend && deno task build
```

### Exit criteria / done means

- [ ] Pins render in the secondary nav for the active cluster only, and a cluster switch re-loads them.
- [ ] Empty, unavailable and forbidden render as three different things.
- [ ] The unpin button does not navigate (verified manually and in U5b's spec).
- [ ] No localStorage key was added; `pin-store.ts` carries the client-only banner.
- [ ] The Overview-domain decision is stated in the PR body.

---

## U5b. Pin toggle on resource detail, and Release A acceptance

**Branch:** `feat/pins-detail`
**PR title:** `feat(ui): pin/unpin on resource detail and Release A acceptance specs`
**Covers:** R1, R3, R6; AE1. **Depends on:** U5a.

### Files (4)

| File | State |
|---|---|
| `frontend/components/k8s/PinToggle.tsx` | new |
| `frontend/islands/ResourceDetail.tsx` | existing (1401 LOC) |
| `e2e/tests/pins.spec.ts` | new |
| `e2e/helpers.ts` | existing — extend |

### Steps

0. **Agent Directive 1 / Step 0.** `ResourceDetail.tsx` is 1401 LOC and step 2 restructures a ternary. Run the dead-code scan (unused props, unused imports, `console.*`, dead branches) **before** touching it; land anything found as a separate commit, and record either the cleanup commit or the "nothing found" evidence in the PR body. Unlike `ResourceTable.tsx`, this file has **not** been surveyed — do not assume it is clean.

1. `frontend/components/k8s/PinToggle.tsx` — a plain component (not an island; `ResourceDetail` is already an island).

```tsx
interface PinToggleProps {
  resourceKind: string;   // adapter slug, e.g. "deployments"
  displayKind: string;    // RESOURCE_API_KINDS[kind] ?? title
  namespace: string;      // "" for cluster-scoped
  name: string;
  uid: string | undefined; // undefined until the detail fetch resolves
}
```

- Reads `pins`/`pinsUnavailable` from `lib/pin-store.ts` and computes the matching record with `pinDedupKey`.
- While `uid === undefined` the button is rendered **disabled with a loading label**, not hidden — hiding it would make the control appear and jump.
- Pin → `addPin(displayLabel, {schemaVersion: PIN_SCHEMA_VERSION, resourceKind, group: "", version: "", namespace, name, uid, displayKind})`.
- Unpin → `removePin(record.id)`.
- `already_pinned` (409) is treated as success and triggers a reload, since it means another tab pinned it first.
- `limit_reached` shows the 200-pin limit explicitly. `database_unavailable` renders the control disabled with an explanation.
- **Replaced-object handling:** when a matching pin exists whose `config.uid` differs from the live `uid`, `classifyPin` returns `replaced`; render "Pinned (replaced)" with an action to re-pin the current object. Re-pinning is a `removePin` + `addPin`, not an update — the pin has no PUT. This is the concrete implementation of "A recreated same-name resource does not inherit the old pin identity."
- Styling copies the `Investigate` anchor at `ResourceDetail.tsx:1022-1055` (padding `6px 12px`, radius 9px, 12px/600, `1px solid var(--border-primary)`).

2. `frontend/islands/ResourceDetail.tsx` — restructure `actionButtons` (L974-1058).

The current expression is `resource.value && actions.value.length > 0 ? (<>…</>) : undefined`, so **the whole block vanishes for RBAC-restricted users** and a naively appended pin button would vanish with it. Change the condition to depend only on `resource.value` and keep the RBAC gate on the action list alone:

```tsx
const actionButtons = resource.value
  ? (
    <>
      {actions.value.map((actionId) => { /* unchanged */ })}
      {/* Investigate link — unchanged */}
      <PinToggle
        resourceKind={kind}
        displayKind={RESOURCE_API_KINDS[kind] ?? title}
        namespace={namespace ?? ""}
        name={name}
        uid={resource.value?.metadata.uid}
      />
    </>
  )
  : undefined;
```

`RESOURCE_API_KINDS` is already imported and used at L428/L1025 — no new import beyond `PinToggle`. Do not touch `DetailShell.tsx`; `actions` is already a `ComponentChildren` slot.

Verify by reading the file after the edit (Agent Directive 9) that the map body and the Investigate anchor are byte-identical to before.

3. `e2e/helpers.ts` — add `createPin(page, config)` and `deleteAllPins(page)` in the same shape as U4's saved-view helpers.

4. `e2e/tests/pins.spec.ts` — new spec in `e2e/tests/`.

### Test plan — `e2e/tests/pins.spec.ts`

`test.describe.serial`, project `chromium`.

| Spec name | Scenario |
|---|---|
| `pins a resource from detail and shows it in the secondary nav` | R6 baseline |
| `unpinning from the nav row does not navigate` | the nested-button `preventDefault` trap |
| `a deleted pin target is marked unavailable and does not switch targets` | **AE1 second half** — delete the object via the API, reload, assert the pin renders as unavailable and clicking it lands on a not-found state rather than a different object |
| `a recreated same-name resource is reported as replaced, not silently inherited` | **deleted/recreated resource**, R1 — delete then recreate with the same name, assert "replaced" and that the stored uid did not change until the user re-pins |
| `re-pinning a replaced object records the new uid` | the recovery path |
| `a pin whose namespace access was revoked renders forbidden, not missing` | **revoked permission** — second user without the namespace |
| `another user's pins are not listed and their ids 404` | **cross-user access** |
| `the pin toggle is visible for a read-only user` | the `actionButtons` restructure regression |
| `the pin toggle is disabled until the detail fetch resolves` | uid-undefined state |
| `switching clusters reloads pins and cancels the prior request` | **request cancellation** + Correction 10 |
| `long resource names stay readable in the nav row` | usability scenario from the master plan |
| `pin and unpin are reachable and labelled for keyboard/screen-reader users` | keyboard navigation scenario — assert `aria-label` and focus order |

**Unavailable DB** and **stale revision** do not apply to pins (no PUT, and the E2E harness always has PostgreSQL); both are covered in U2. Say so in the spec header.

### Verification

```
cd frontend && deno task check
cd frontend && deno task test
cd frontend && deno task build
cd e2e && npm test
cd backend && go vet ./... && go test ./...
```

### Exit criteria / done means

- [ ] **AE1 passes end to end**: save a production pod view, sign out, sign in in another browser context, reopen it with the same scope; a deleted pin is marked unavailable and no local fallback occurs.
- [ ] Two-user isolation is demonstrated in a live spec, not only in Go tests.
- [ ] The pin control renders for a user with zero available actions.
- [ ] `replaced`, `missing`, `forbidden` and `unknown` are four visibly different states.
- [ ] `ResourceDetail.tsx`'s diff touches only the `actionButtons` condition and adds one element; the action map and Investigate anchor are unchanged.
- [ ] Step-0 evidence or the cleanup commit is recorded in the PR body.
- [ ] Both new specs appear in `npx playwright test --list`.

---

## Cross-unit sequencing and conflict notes

**The six units are strictly sequential. Do not parallelize any pair.**

```
U1 ──> U2 ──> U3 ──> U4 ──> U5a ──> U5b
```

Shared-file ownership, which is what forces the order:

| File | Touched by | Note |
|---|---|---|
| `backend/internal/server/routes.go` | U2 only | Also owned by other tracks' U8/U14/U23/U29/U35. Release A must land its single 12-line insertion before or after those, never concurrently. |
| `backend/internal/server/server.go` | U2 only | Three insertions (struct, Deps, `New`). Same cross-track contention as `routes.go`. |
| `backend/cmd/kubecenter/main.go` | U3 only | Also owned by U14/U23/U25/U29/U34. Keep Release A's block contiguous so a rebase conflict is one hunk. |
| `frontend/lib/preferences.ts` | **U3 creates it; nothing else touches it** | Resolves the master plan's double-"new" between U3 and U4 (Correction 3). U4/U5a/U5b consume it unchanged. |
| `frontend/lib/preference-types.ts` | U3 creates it complete | Deliberately front-loaded with the pin helpers so U5a/U5b need not re-open it. |
| `frontend/lib/preference-types_test.ts` | U3 creates; U4 and U5a append | Append-only, at the end of the file, so a rebase conflict is trivial. |
| `e2e/helpers.ts` | U4 and U5b append | Append-only, adjacent to `deleteResource`. |
| `frontend/islands/ResourceTable.tsx` | U4 only | 839 LOC; see Step 0 note. |
| `frontend/islands/ResourceDetail.tsx` | U5b only | 1401 LOC; isolated into its own PR precisely so its Step-0 scan does not block pin-nav work. |

**Cross-track sequencing:** U2's `routes.go`/`server.go` edits and U3's `main.go` edit collide with Release B's U14, Release C's U8, Release D's U23/U25, Release E's U29 and Release F's U34. Release A is first in the master plan's delivery order; land U1–U3 before any other track opens a PR against those three files.

**Migration sequence:** `000018` is reserved for this release only. Releases B/D/E/F own `000019`–`000022` and must not reuse `000018` even if Release A slips.

---

## Deferred appendix: U38 + U6 — constrained personal dashboard layouts

**Status: requirements-only. Gated on Q5 (widget catalog). Do not implement as part of Release A.**

The master plan sequences **U38 before U6**: the server-side dashboard preference contract must exist before the editor is built. Both are excluded from Release A because R7 is a "later extension" in the Release Scope table and because Q5 is unresolved.

### What Release A leaves ready

- `user_preferences.kind` is a `CHECK (kind IN ('saved_view','pin'))` constraint. Adding `'dashboard_layout'` is a small additive migration (a new sequence, not `000018`) that drops and recreates the check constraint. Nothing else changes.
- `schema_version` + `revision` + the allowlisted-JSONB validation pipeline already exist; U38 adds one `ValidateDashboardLayout` function and one route pair to the existing `/preferences` group.
- `preferencesApi` gains two methods; `preference-types.ts` gains one config type.

### What must be decided before U38 can start (Q5)

1. **The widget catalog.** `frontend/islands/DashboardV2.tsx` (1084 LOC) has **six hard-coded `<WidgetShell>` blocks with no ids, no registry and no ordering array** (L417 "Cluster Health", L522, L580 "Pod Status", L711, L795, L958). There is no widget identity to persist. U38 cannot define a stable `widgetId` allowlist until someone assigns ids to those six blocks and decides whether the catalog is exactly those six.
2. **Reset semantics.** What "reset to default" means when the catalog changes between releases: reset to the shipped default order, or to the user's last valid layout.
3. **Unknown-id policy.** The master plan requires unknown/removed widget ids to "fall back safely". Decide whether an unknown id is dropped-with-notice (consistent with Release A's `applyViewState` warnings) or rejected outright at write time.

### Requirements carried forward verbatim

- R7: a personal dashboard release persists an ordered selection of **approved existing widgets**; saved layouts cannot contain executable queries or arbitrary URLs.
- KTD4: allowlisted fields, schema versioning, optimistic revisions; no credentials, tokens, raw Secret data, scripts or redirect targets.
- U38 exit: "U6 can build against a validated server-side dashboard contract."
- U6 exit: "An operator can arrange existing widgets; this is not an arbitrary dashboard builder."
- U6 test scenarios: unknown/removed widget ids fall back safely; reorder persists and reset restores the default; no arbitrary URL/query/script accepted; layout works at narrow widths and by keyboard.

### Expected shape when unblocked

U38 (backend, ≤5 files): `backend/internal/store/migrations/NNNNNN_add_dashboard_layout_kind.{up,down}.sql`, `backend/internal/preferences/types.go` (extend), `backend/internal/preferences/handler.go` (extend), `backend/internal/preferences/handler_test.go` (extend).
U6 (frontend, ≤5 files): `frontend/islands/DashboardV2.tsx`, `frontend/islands/DashboardLayoutEditor.tsx` (new), `frontend/lib/dashboard-layout.ts` (new), `frontend/lib/dashboard-layout_test.ts` (new), `e2e/tests/dashboard-layout.spec.ts` (new).
Note that the master plan's U38 marks `types.go` and `handler.go` as **new**, which will be false once U2 lands — they are extensions by then.

---

## Risks and open items

Items a human must decide or accept **before** U1 starts:

1. **Owner key divergence (blocking-ish, cheap to resolve).** This plan uses `auth.User.ID`, per the master plan. The only existing per-user table (`mobile_push_devices`) uses `user.KubernetesUsername`. Confirm `auth.User.ID` is correct, and accept the consequence: an LDAP user's id embeds their DN (`ldap:<providerID>:<dn>`), so **moving a user between OUs orphans their saved views and pins**. Mitigation options, none implemented here: (a) accept it; (b) add an admin-run re-key tool later; (c) key on a stable directory attribute instead of the DN, which is an `auth/ldap.go` change outside Release A's scope.

2. **No DB test harness exists, and CI will skip the new tests by default.** U1 introduces `KUBECENTER_TEST_DATABASE_URL`-gated tests. `.github/workflows/ci.yml` currently runs `go test ./...` with no PostgreSQL service, so U1's DB tests will **skip in CI** and the migration round-trip will be unverified there. Adding a `services: postgres` block plus the env var is a one-file change to `ci.yml` — deliberately excluded to keep U1 at five files. **Decide:** ship U1 with CI-skipped DB tests and a follow-up CI PR, or make the CI change a sixth file / seventh unit.

3. **Audit volume.** Every pin and unpin writes an `audit_logs` row (90-day retention). A user churning pins generates noise in the admin audit view. Options: keep it (current plan, satisfies the CLAUDE.md checklist), or exclude preference writes from audit on the grounds that they mutate no cluster state. **Recommend keeping it**; flag if the operator disagrees.

4. **Namespace restore is app-global.** Applying a saved view rewrites `selectedNamespace`, which every other island reads and which is persisted to `localStorage["k8scenter.selectedNamespace"]`. There is no per-table namespace override to hook into. This plan makes the mutation explicit in the UI. **Confirm** that is acceptable rather than requiring a per-table namespace override (which would be a much larger `ResourceTable` change).

5. **Sort round-trip is limited to three keys.** Until `ResourceTable.tsx:357-376` grows a real comparator, `sortKey` is `name|namespace|age` only. Confirm that is acceptable for Release A, or add a preceding unit to widen the comparator.

6. **Cross-cluster views and pins are listed, not hidden.** `GET /preferences/views` returns every record the owner has across all clusters and the UI disables the ones for other clusters. The alternative (server-side filtering by `X-Cluster-ID`) hides the fact that the record exists, which reads as data loss after a cluster switch. **Confirm** the chosen behaviour.

7. **Non-local clusters require admin.** `middleware.ClusterContext` forces the admin role for any `X-Cluster-ID != "local"`. A non-admin therefore can only ever create `cluster_id = 'local'` preferences. That is existing platform behaviour, not new, but it means the "one cluster per workspace" requirement is effectively local-only for non-admins today.

8. **`gen_random_uuid()` availability.** Used by `000015` already, so PostgreSQL ≥13 (or pgcrypto) is proven in every deployment that has migrated past 000015. No new requirement, but worth stating in the PR body.

9. **893 lines of orphaned E2E specs.** `e2e/flux-notifications.spec.ts`, `e2e/namespace-limits.spec.ts` and `e2e/velero.spec.ts` sit outside `testDir` and never run. Unrelated to Release A, but anyone citing E2E coverage should know. Worth a separate cleanup issue.

10. **Q1 is not a blocker for Release A** and is already resolved per the user (personal ownership + explicit grants, current authorization re-checked at read time, 30-day configurable retention). Release A stores no cluster-derived evidence, so no retention job is needed and none is planned here. The ownership half of Q1 is what D1/D2 implement.
