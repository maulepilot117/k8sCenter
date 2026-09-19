---
title: "Release G / P3 — Dashboard layout persistence"
parent_plan: docs/plans/2026-09-10-EXECUTION-ORDER.md
spec: docs/plans/2026-09-13-dashboard-builder-design.md
date: 2026-09-13
baseline_revision: 78d9881e
status: ready
---

# Release G / P3 Implementation Plan — Layout persistence

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan
> task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store one dashboard layout per user, per cluster, per scope, validated
server-side against the same allowlisted-JSONB discipline saved views already use.

**Architecture:** A third `kind` on the existing `user_preferences` table. No new
table, no new index, no new store type — `dedup_key = scope` makes migration
000018's existing unique index do the one-layout-per-scope work for free (spec
D-11).

**Tech Stack:** Go 1.26, chi, pgx/v5, golang-migrate; Deno 2.x on the client side.

**Spec:** `docs/plans/2026-09-13-dashboard-builder-design.md` — §4.3, §7, and
decisions D-3, D-7, D-11.

## Global Constraints

- One PR per unit, **at most 5 touched files including tests** (G2).
- **Migration number is taken at merge time, not now** (G4, as amended
  2026-09-13). `migrate.go:25` runs plain `m.Up()`, which applies only migrations
  above the database's current version, so a number reserved at plan time and
  merged out of order is silently skipped. Last applied is `000018`.
- **DB unavailable is 503 with a reason, never 404** (G5). The handler is always
  registered and every method gates on `requireStore`.
- **Env-gated DB tests, no build tag** (G3). `KUBECENTER_TEST_DATABASE_URL`
  absent → `t.Skip`.
- **New parse seam over attacker-influenceable input gets a fuzz target** (G8).
  `backend/internal/preferences/validate_fuzz_test.go` already exists — extend
  it and add the `fuzz.yml` matrix row in the same unit, or the `-list` drift
  guard never runs the target and the fuzzing is decorative.
- Repo-wide verification: `cd backend && go vet ./... && go test ./...`.

---

## What already exists, and what that saves

Read from the shipped code, not assumed:

| Fact | Consequence for this plan |
|---|---|
| `user_preferences.kind` is `CHECK (kind IN ('saved_view','pin'))` | One `ALTER ... DROP/ADD CONSTRAINT` is the whole schema change. No new table. |
| `CREATE UNIQUE INDEX idx_user_preferences_dedup ON (owner_id, kind, cluster_id, dedup_key)` | With `dedup_key = scope`, one layout per `(owner, cluster, scope)` is enforced already. |
| `Create` enforces the per-kind quota inside the INSERT under `pg_advisory_xact_lock` | Pass a ceiling of 1 per scope-kind and the quota path is reused unchanged. |
| `Update` does `WHERE id AND owner_id AND revision`, then `classifyMissingUpdate` disambiguates | Optimistic concurrency is free; `revision_conflict` already maps to 409. |
| Validation lives in `types.go`; the package imports no `net/http` | The new validator goes in a sibling pure file, not the handler. |
| **There is no `NewHandler`** — it is a struct literal at `main.go:885-893` | A new dependency goes on the `Handler` struct and is set there. |
| Config validators **re-marshal their own typed struct**, so unlisted fields are silently dropped, while `decodeBody` uses `DisallowUnknownFields` at the top level | Unknown *fields* are handled. Unknown widget **ids are values, not fields**, so D-7's rejection has to be written explicitly. |

---

## The widget-id allowlist problem

Spec §7 requires the server to reject an unknown widget id. The registry is
TypeScript and the server is Go, so the server cannot read it.

**Decision: a Go-side allowlist, kept honest by a pinned-list test on each side.**
This is the repo's own established discipline — `frontend/lib/preferences_test.ts`
already asserts a sorted `PREFERENCE_REASONS` array against the handler's call
sites, with a comment naming the Go files. Do the same in both directions:

- Go: `allowedWidgetIDs` as a `map[string]struct{}` in the new validator file.
- TS: a test in `frontend/lib/dashboard/registry_test.ts` asserting the sorted
  registry ids equal a literal list, commented with the Go file path.

Adding a widget therefore touches both lists, and a drifted list fails a test on
whichever side was forgotten rather than producing a layout the server silently
refuses.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/store/migrations/NNNNNN_add_dashboard_layout_kind.{up,down}.sql` | Widen the `kind` CHECK |
| `backend/internal/store/migrations/NOTES.txt` | Operator note |
| `backend/internal/store/preferences.go` | `PreferenceKindDashboardLayout` |
| `backend/internal/preferences/dashboard.go` | Types + `ValidateDashboardLayout` |
| `backend/internal/preferences/handler.go` | Two endpoints |
| `backend/internal/server/routes.go` | `/preferences/layouts` |
| `frontend/lib/dashboard/layout-store.ts` | Client load/save, modelled on `pin-store.ts` |

---

### Task D11: Migration and the third kind

**Files:**
- Create: `backend/internal/store/migrations/NNNNNN_add_dashboard_layout_kind.up.sql`
- Create: `backend/internal/store/migrations/NNNNNN_add_dashboard_layout_kind.down.sql`
- Modify: `backend/internal/store/migrations/NOTES.txt`
- Modify: `backend/internal/store/preferences.go`

**Interfaces:**
- Produces: `store.PreferenceKindDashboardLayout`. D12 and D13 consume it.

- [ ] **Step 1: Determine the sequence number**

Run: `ls backend/internal/store/migrations/ | sort | tail -3`

Take the next free number. **Do not use a number reserved in G4's table** —
that table is intent, and the merging unit takes the next free slot.

- [ ] **Step 2: Write the up migration**

```sql
-- Personal dashboard layouts (Release G; spec 2026-09-13, D-11).
--
-- Additive: one new value in the kind CHECK. No column changes, no backfill,
-- no data migration. Every existing row keeps its kind and is untouched.
--
-- No new unique index is needed. idx_user_preferences_dedup already covers
-- (owner_id, kind, cluster_id, dedup_key); dashboard layouts set dedup_key to
-- the scope ("overview"), so one layout per user per cluster per scope falls
-- out of the constraint that is already there.
--
-- The CHECK is dropped and recreated rather than widened in place because
-- PostgreSQL has no ALTER CONSTRAINT for a CHECK expression. The recreate takes
-- an ACCESS EXCLUSIVE lock on user_preferences for the duration of a full-table
-- validation scan. On any realistic preferences table that is milliseconds; on
-- a very large one, run it in a maintenance window.

ALTER TABLE user_preferences
    DROP CONSTRAINT IF EXISTS user_preferences_kind_check;

ALTER TABLE user_preferences
    ADD CONSTRAINT user_preferences_kind_check
    CHECK (kind IN ('saved_view', 'pin', 'dashboard_layout'));
```

**Before writing this, confirm the constraint's real name** — it was created
inline in 000018 so PostgreSQL generated it. Verify with:

```sql
SELECT conname FROM pg_constraint
WHERE conrelid = 'user_preferences'::regclass AND contype = 'c';
```

If the generated name differs, use the real one. A `DROP CONSTRAINT IF EXISTS`
against the wrong name succeeds silently and then the `ADD` fails on a
duplicate-name collision or, worse, leaves two CHECKs where the old one still
rejects the new kind.

- [ ] **Step 3: Write the down migration**

```sql
-- Narrowing the CHECK will FAIL while any dashboard_layout row exists, which
-- is correct: silently deleting a user's layouts to satisfy a rollback is
-- worse than a loud failure. Remove them first if you mean it:
--
--   DELETE FROM user_preferences WHERE kind = 'dashboard_layout';
--
-- Find them before deciding:
--
--   SELECT owner_id, cluster_id, dedup_key, updated_at
--     FROM user_preferences WHERE kind = 'dashboard_layout';

ALTER TABLE user_preferences
    DROP CONSTRAINT IF EXISTS user_preferences_kind_check;

ALTER TABLE user_preferences
    ADD CONSTRAINT user_preferences_kind_check
    CHECK (kind IN ('saved_view', 'pin'));
```

- [ ] **Step 4: Add the NOTES.txt section**

House format is a 72-hyphen rule above and below the title
(`NOTES.txt:4-6`). Note that 000018 has **no** section — the file's header says
one section per migration "that needs a heads-up beyond what the .up.sql comment
block already says", and this one does, because the down migration can fail.

```
------------------------------------------------------------------------
NNNNNN_add_dashboard_layout_kind (Release G dashboard builder 2026-09-13)
------------------------------------------------------------------------

Adds 'dashboard_layout' to the user_preferences.kind CHECK. Additive: no
columns change, nothing is backfilled, and no existing row is touched.

Operator action required:
  None on the way up.

Rollback:
  The down migration narrows the CHECK and WILL FAIL if any dashboard_layout
  row still exists. This is deliberate -- a rollback that silently deleted
  every user's dashboard is worse than one that stops. Inspect first:

      SELECT owner_id, cluster_id, dedup_key, updated_at
        FROM user_preferences WHERE kind = 'dashboard_layout';

  and remove them explicitly if the rollback is intended:

      DELETE FROM user_preferences WHERE kind = 'dashboard_layout';

Locking:
  Recreating a CHECK constraint takes ACCESS EXCLUSIVE on user_preferences
  while PostgreSQL validates the whole table. Milliseconds on a normal
  preferences table; use a maintenance window on a very large one.
```

- [ ] **Step 5: Add the kind constant**

In `backend/internal/store/preferences.go:19-22`:

```go
const (
	PreferenceKindSavedView PreferenceKind = "saved_view"
	PreferenceKindPin       PreferenceKind = "pin"
	// PreferenceKindDashboardLayout stores one layout per (owner, cluster,
	// scope). dedup_key carries the scope, so the existing unique index on
	// (owner_id, kind, cluster_id, dedup_key) is what enforces the "one".
	PreferenceKindDashboardLayout PreferenceKind = "dashboard_layout"
)
```

- [ ] **Step 6: Migration round-trip test**

Add to `backend/internal/store/preferences_test.go`, env-gated per G3:

```go
// TestMigration_DashboardLayoutKind_RoundTrip proves the CHECK actually
// widened, and that the rollback refuses to destroy layouts silently.
func TestMigration_DashboardLayoutKind_RoundTrip(t *testing.T) {
	// up -> insert a dashboard_layout row -> assert it persists
	// -> attempt down -> assert it FAILS while the row exists
	// -> delete the row -> down -> up -> assert unrelated rows survived
}
```

- [ ] **Step 7: Verify and commit**

Run: `cd backend && go vet ./... && go test ./...`
Then with a database: `make dev-db` and re-run with
`KUBECENTER_TEST_DATABASE_URL` set, so the gated tests actually execute rather
than skipping.

Branch: `feat/d11-dashboard-layout-migration`
PR title: `feat(store): dashboard_layout preference kind`

**Done means** — the migration applies clean on a fresh database and on one at
000018; a `dashboard_layout` row inserts; the down migration fails loudly while
layouts exist and succeeds once they are removed; NOTES.txt documents the
rollback hazard.

---

### Task D12: Validation

**Files:**
- Create: `backend/internal/preferences/dashboard.go`
- Create: `backend/internal/preferences/dashboard_test.go`
- Modify: `backend/internal/preferences/validate_fuzz_test.go`
- Modify: `.github/workflows/fuzz.yml`

**Interfaces:**
- Consumes: `invalidf`, `ValidationError` from `types.go`.
- Produces: `DashboardLayoutConfig`, `DashboardLayoutItem`,
  `ValidateDashboardLayout(raw) (DashboardLayoutConfig, json.RawMessage, error)`,
  `DashboardLayoutDedupKey(scope) string`, `DashboardLayoutSchemaVersion`.
  D13 consumes all of them.

- [ ] **Step 1: Write `dashboard.go`**

Mirror `ValidateSavedView` exactly: unmarshal, check schema version, validate,
then **re-marshal the typed struct** so nothing unlisted survives a round trip.

```go
package preferences

// dashboard.go -- validation for personal dashboard layouts (Release G).
//
// Kept beside types.go and under the same rule: pure, no net/http. Split into
// its own file because types.go is already the home of two kinds and a third
// would bury all three.

const (
	DashboardLayoutSchemaVersion = 1

	// dashboardColumns is the fixed grid width. Stored in the config as well
	// so a future change is detectable in existing rows rather than silently
	// reinterpreting them.
	dashboardColumns = 12

	maxDashboardItems   = 40
	maxWidgetIDLen      = 64
	maxInstanceIDLen    = 64
	maxDashboardParams  = 8
	maxParamKeyLen      = 32
	maxParamValueLen    = 253
	maxDashboardRows    = 200
)

// allowedDashboardScopes is the set of dashboards a layout can target. P6 adds
// more; each addition here must also be added to DASHBOARD_SCOPES in
// frontend/lib/dashboard/types.ts.
var allowedDashboardScopes = map[string]struct{}{"overview": {}}

// allowedWidgetIDs is the server-side half of the widget catalog.
//
// The registry itself is TypeScript and unreadable from here, so the two are
// kept honest by a pinned-list test on each side -- the same discipline
// frontend/lib/preferences_test.ts already applies to PREFERENCE_REASONS.
// Adding a widget means adding it here AND to the registry, and forgetting
// either fails a test rather than producing a layout the server refuses.
//
// Ids are never removed from this map when a widget is retired. A retired id
// must stay acceptable on read so a stored layout is not bricked; the client
// drops it with a notice (spec D-7).
var allowedWidgetIDs = map[string]struct{}{
	"cluster-health": {}, "cpu-tile": {}, "memory-tile": {}, "pods-tile": {},
	"network-tile": {}, "resource-utilization": {}, "pod-status": {},
	"nodes": {}, "recent-events": {}, "active-alerts": {},
}

type DashboardLayoutItem struct {
	InstanceID string            `json:"instanceId"`
	ID         string            `json:"id"`
	X          int               `json:"x"`
	Y          int               `json:"y"`
	W          int               `json:"w"`
	H          int               `json:"h"`
	Params     map[string]string `json:"params,omitempty"`
}

type DashboardLayoutConfig struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Scope         string                `json:"scope"`
	Columns       int                   `json:"columns"`
	Items         []DashboardLayoutItem `json:"items"`
}

// DashboardLayoutDedupKey folds a layout to its identity. The scope IS the
// identity: one layout per owner per cluster per scope, enforced by the
// unique index migration 000018 already created.
func DashboardLayoutDedupKey(scope string) string {
	return scope
}

func ValidateDashboardLayout(raw json.RawMessage) (DashboardLayoutConfig, json.RawMessage, error) {
	var cfg DashboardLayoutConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, nil, invalidf("invalid_config", "config is not a valid dashboard-layout object")
	}

	if cfg.SchemaVersion != DashboardLayoutSchemaVersion {
		return cfg, nil, invalidf("unsupported_schema_version",
			"unsupported schemaVersion %d; this server accepts %d",
			cfg.SchemaVersion, DashboardLayoutSchemaVersion)
	}
	if _, ok := allowedDashboardScopes[cfg.Scope]; !ok {
		return cfg, nil, invalidf("invalid_config", "unsupported scope %q", cfg.Scope)
	}
	if cfg.Columns != dashboardColumns {
		return cfg, nil, invalidf("invalid_config",
			"columns is %d; this server lays out %d", cfg.Columns, dashboardColumns)
	}
	if len(cfg.Items) > maxDashboardItems {
		return cfg, nil, invalidf("invalid_config",
			"layout has %d widgets; the maximum is %d", len(cfg.Items), maxDashboardItems)
	}

	seenInstance := make(map[string]struct{}, len(cfg.Items))
	seenIdentity := make(map[string]struct{}, len(cfg.Items))

	for i, it := range cfg.Items {
		if it.InstanceID == "" || utf8.RuneCountInString(it.InstanceID) > maxInstanceIDLen ||
			!isDNS1123Subdomain(it.InstanceID) {
			return cfg, nil, invalidf("invalid_config",
				"items[%d].instanceId is not a valid identifier", i)
		}
		if _, dup := seenInstance[it.InstanceID]; dup {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] repeats instanceId %q", i, it.InstanceID)
		}
		seenInstance[it.InstanceID] = struct{}{}

		if utf8.RuneCountInString(it.ID) > maxWidgetIDLen {
			return cfg, nil, invalidf("invalid_config", "items[%d].id is too long", i)
		}
		// A write carrying an unknown id is a client bug, not a stored-layout
		// problem, so it is refused with the field named (spec D-7). Reads are
		// the forgiving direction and are the client's job.
		if _, ok := allowedWidgetIDs[it.ID]; !ok {
			return cfg, nil, invalidf("unknown_widget_id",
				"items[%d].id %q is not a widget this server knows", i, it.ID)
		}

		// AMENDED 2026-09-19, as shipped: the block below OVERFLOWS and must
		// not be copied. `x+w > columns` wraps -- x=math.MaxInt64 with w=1
		// folds to a large negative, passes this test, and stores a widget
		// quintillions of columns off a twelve-column grid. Found by Step 4's
		// own fuzz target, which is why Step 4 gained a fourth oracle (see the
		// amendment note at the end of this task). Bound each component
		// against the grid FIRST, then take the sums:
		//
		//	if it.W < 1 || it.W > cfg.Columns          { reject }
		//	if it.H < 1 || it.H > maxDashboardRows     { reject }
		//	if it.X < 0 || it.X > cfg.Columns          { reject }
		//	if it.X+it.W > cfg.Columns                 { reject }
		//	if it.Y < 0 || it.Y > maxDashboardRows     { reject }
		//	if it.Y+it.H > maxDashboardRows            { reject }
		//
		// Bounding the components first is also what makes the sums in the
		// error messages and in itemsOverlap safe.
		if it.W < 1 || it.H < 1 {
			return cfg, nil, invalidf("invalid_config", "items[%d] has a non-positive size", i)
		}
		if it.X < 0 || it.X+it.W > cfg.Columns {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] spans columns %d-%d, outside 0-%d", i, it.X, it.X+it.W, cfg.Columns)
		}
		if it.Y < 0 || it.Y+it.H > maxDashboardRows {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] spans rows %d-%d, outside 0-%d", i, it.Y, it.Y+it.H, maxDashboardRows)
		}

		if len(it.Params) > maxDashboardParams {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] carries %d params; the maximum is %d", i, len(it.Params), maxDashboardParams)
		}
		for k, v := range it.Params {
			// No URLs, no queries, no scripts -- KTD4. Params are short,
			// control-free scalars and nothing else.
			if k == "" || utf8.RuneCountInString(k) > maxParamKeyLen || hasControlChars(k) {
				return cfg, nil, invalidf("invalid_config", "items[%d] has an invalid param name", i)
			}
			if utf8.RuneCountInString(v) > maxParamValueLen || hasControlChars(v) {
				return cfg, nil, invalidf("invalid_config",
					"items[%d] param %q has an invalid value", i, k)
			}
		}

		// Two placements of the same widget with the same params are a
		// duplicate; with different params they are two legitimate views
		// (prod beside staging), which is why instanceId exists.
		identity := it.ID + "\x00" + canonicalParams(it.Params)
		if _, dup := seenIdentity[identity]; dup {
			return cfg, nil, invalidf("invalid_config",
				"items[%d] duplicates an identical widget already in the layout", i)
		}
		seenIdentity[identity] = struct{}{}
	}

	// Overlap is checked after every item is individually sound, so the error
	// a caller sees names the real first problem.
	for a := range cfg.Items {
		for b := a + 1; b < len(cfg.Items); b++ {
			if itemsOverlap(cfg.Items[a], cfg.Items[b]) {
				return cfg, nil, invalidf("invalid_config",
					"items[%d] and items[%d] overlap", a, b)
			}
		}
	}

	normalized, err := json.Marshal(cfg)
	if err != nil {
		return cfg, nil, invalidf("invalid_config", "config could not be normalized")
	}
	return cfg, normalized, nil
}

func itemsOverlap(a, b DashboardLayoutItem) bool {
	return !(a.X+a.W <= b.X || b.X+b.W <= a.X || a.Y+a.H <= b.Y || b.Y+b.H <= a.Y)
}

// canonicalParams renders a param map order-independently so two placements
// that differ only in map iteration order compare equal.
func canonicalParams(p map[string]string) string {
	if len(p) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(p[k])
		b.WriteByte('\x00')
	}
	return b.String()
}
```

- [ ] **Step 2: Note the new reason code**

`unknown_widget_id` is a new reason. Three places must learn it, or the client
silently stops classifying it:

1. `frontend/lib/preferences.ts` — add to `PREFERENCE_REASONS`.
2. `frontend/lib/preferences_test.ts` — add to the sorted assertion in
   `"PREFERENCE_REASONS covers the handler's reason codes"`. **This test fails
   until you do**, which is the point.
3. Any reason→copy mapper (`SavedViews.tsx:43-62` style) that P4 adds.

- [ ] **Step 3: Table-driven tests**

Match `handler_test.go`'s style: anonymous-struct table plus `t.Run`, with the
`reasonOf(err)` helper already in the package.

Cases, each with a stated reason: valid minimal layout; wrong schema version;
unknown scope; wrong column count; too many items; empty/over-long/non-DNS
`instanceId`; duplicate `instanceId`; unknown widget id; zero and negative
sizes; `x+w` past the right edge; negative `y`; `y+h` past the row cap; too many
params; control characters in a param key and in a param value; two items with
identical `(id, params)`; two overlapping items; and — critically — **unlisted
fields inside an item are dropped, not rejected**, mirroring
`TestValidateSavedView_DropsUnlistedFields`.

- [ ] **Step 4: Fuzz target and the matrix row**

Extend `backend/internal/preferences/validate_fuzz_test.go`:

```go
// FuzzValidateDashboardLayout drives the new parse seam. Oracle A: never
// panic. Oracle B: an accepted config must round-trip -- re-validating the
// normalized bytes must accept and produce identical bytes, or the normalizer
// is not a fixed point and a stored row could decode differently than it was
// written.
func FuzzValidateDashboardLayout(f *testing.F) {
	f.Add([]byte(`{"schemaVersion":1,"scope":"overview","columns":12,"items":[]}`))
	// ... seeds mutated from the table cases above ...
}
```

Add the matching row to `.github/workflows/fuzz.yml`. **Without the row the
`-list` drift guard never runs the target and the fuzzing is decorative** (G8) —
this is part of the unit, not a follow-up.

- [ ] **Step 5: Verify and commit**

Run: `cd backend && go vet ./... && go test ./...`
Run: `cd backend && go test -run=Fuzz -fuzz=FuzzValidateDashboardLayout -fuzztime=60s ./internal/preferences/`
Run: `cd frontend && deno test lib/preferences_test.ts` (fails until Step 2)

Branch: `feat/d12-dashboard-layout-validation`
PR title: `feat(preferences): validate dashboard layout configs`

**Done means** — every rejection carries a reason code; unknown widget ids are
refused on write; overlapping or out-of-bounds items are refused; params cannot
carry control characters or exceed their bounds; unlisted fields are dropped;
the fuzz target runs in CI via a real `fuzz.yml` row.

**AMENDED 2026-09-19 — what D12 actually shipped (PR #467).** Three divergences
from the text above, each deliberate:

1. **The Step 1 bounds check overflowed.** See the amendment comment inside the
   code block. Fixed in the shipped code; four integer-ceiling cases are pinned
   in the table as regressions.

2. **Step 4 gained a fourth oracle.** Oracles A (no panic) and B (round-trip
   fixed point) were *all satisfied* by the overflowing layout — it does not
   panic, it re-marshals to exactly the envelope keys, and it round-trips
   byte-stably. Only an independent restatement of the bound, in arithmetic
   that cannot wrap, detects it. **Generalize this:** an oracle that reuses the
   code under test's own arithmetic cannot find a bug in that arithmetic. D13's
   and D14's oracles should be written against the *property*, not against the
   implementation's expression of it.

3. **The unit is 8 files, not 5 (G2).** The file list above omits Step 2's two
   frontend files and the "widget-id allowlist problem" section's TS pinned-list
   test; the Go-side pin went into the existing `parity_test.go` rather than a
   parallel mechanism. Each piece is half a contract on its own, so splitting
   would have shipped a drift guard that does not guard. D13's file list is
   likely to be similarly optimistic — count Step 4's `allEndpoints()` edit and
   the handler tests before assuming it fits.

**AMENDED again 2026-09-19, after review.** The three notes above were written
from what the implementation changed, not from what the spec required, and that
is exactly how the gap below survived: `/ce:review` found **two of spec §7's ten
rejection rules missing entirely** — `w < minW` / `h < minH`, and the per-widget
`ParamSpec` enum — neither recorded as deferred, because the amendment only ever
listed divergences its author had already noticed. **Write the next one by
walking the spec's list, not the diff.**

Both are now implemented rather than deferred. `allowedWidgetIDs` became
`allowedWidgets map[string]widgetSpec`, carrying each widget's `MinW`/`MinH` and
its declared `Params` (key → closed value set; empty means "any value within the
generic bounds", absent means the widget takes none). `WidgetDef` gained an
optional `params` field so the registry stays the source of truth, and
`TestContractParity/"widget specs"` pins every minimum and asserts every shipped
widget is still parameterless — so the first parameterized widget cannot ship
without a `ParamSpec` on both sides.

Four other defects the review surfaced, all fixed here: an accepted layout could
normalize to `"items":null`, which the TypeScript mirror declares impossible and
every client consumer throws on; `canonicalParams` was not injective (an `=` in
a param key collided two distinct layouts, and it is now length-prefixed, which
depends on no precondition at all); the geometry rejection messages stated
exclusive bounds as inclusive; and `maxDashboardRows` had no client mirror, so
the editor let users drag past a cap the server enforced.

**Two lessons for D13 and D14, both about guards rather than code:**

- Adding a rule can silently un-cover an existing one. Enforcing per-widget
  minimums made several fuzz seeds and table cases stop at the new size check
  before reaching the overlap and param rules they were written for. Mutation
  testing caught it — the guards had gone quiet without any test turning red.
  After adding a validation rule, re-run the mutations for the rules that came
  before it.
- `go test` caches results. A mutation run that prints `ok (cached)` proved
  nothing; use `-count=1` for every mutation check.

---

### Task D13: Endpoints

**Files:**
- Modify: `backend/internal/preferences/handler.go`
- Modify: `backend/internal/server/routes.go`
- Modify: `backend/internal/preferences/handler_test.go`

**Interfaces:**
- Produces: `GET /api/v1/preferences/layouts/{scope}` and
  `PUT /api/v1/preferences/layouts/{scope}`. D14 consumes both.

**Shape decision.** `PUT`, not `POST`+`PUT`: a layout is a singleton per scope,
so the client should not have to know whether one exists. The handler creates
when absent and updates when present, using the existing `Create`/`Update` store
methods — `revision: 0` in the request body means "I believe none exists".

- [ ] **Step 1: `HandleGetLayout`**

Follows `list` (`handler.go:291-314`): `begin`, then read. A scope with no saved
layout returns **204 No Content**, not 404 — "you have not customized this
dashboard" is a normal state, and 404 would be indistinguishable from a bad
scope. The client falls back to `DEFAULT_OVERVIEW_LAYOUT`.

- [ ] **Step 2: `HandleSaveLayout`**

Follows `HandleCreateView` (`handler.go:89-133`) exactly: `begin`, `decodeBody`,
`ValidateDashboardLayout`, `ValidateDedupKey`, then create or update. Pass a
ceiling of `len(allowedDashboardScopes)` into `Create` so the advisory-lock
quota path is reused rather than bypassed.

Audit with `kindLabel: "dashboardLayout"`, mirroring `"savedView"`.

- [ ] **Step 3: Routes**

In `registerPreferencesRoutes` (`routes.go:914-939`):

```go
		// Layouts are a singleton per scope, so there is no POST and no {id}:
		// the scope is the address. PUT creates or replaces.
		pr.Route("/layouts", func(lr chi.Router) {
			lr.Get("/{scope}", h.HandleGetLayout)
			lr.Put("/{scope}", h.HandleSaveLayout)
		})
```

- [ ] **Step 4: Extend the endpoint table**

`allEndpoints()` in `handler_test.go:445` drives `TestHandler_NoDatabase_Returns503`
and the CSRF test. **Add both new routes to it** — the G5 guard is only as good
as that list, and a route missing from it is a route with no 503 coverage.

- [ ] **Step 5: Handler tests**

Add, in the file's established style (real chi router via `prefRouter`, real
Postgres via `testStore`, per-test random `testUser`):

- `TestHandler_GetLayout_Unsaved_Returns204`
- `TestHandler_SaveLayout_CreatesThenUpdates` — PUT twice, assert the revision
  increments and only one row exists
- `TestHandler_SaveLayout_StaleRevision_Returns409` with `revision_conflict`
- `TestHandler_SaveLayout_UnknownWidget_Returns400` with `unknown_widget_id`
- `TestHandler_SaveLayout_OtherOwnersLayoutIsInvisible` — two users, same
  cluster, same scope; each sees only their own
- `TestHandler_SaveLayout_IsClusterScoped` — same user, two cluster ids, two
  layouts, no bleed
- `TestHandler_SaveLayout_RejectsServerDerivedFields` — a body carrying
  `ownerId` or `clusterId` is 400, naming the field, via `DisallowUnknownFields`

- [ ] **Step 6: Verify and commit**

Run: `cd backend && go vet ./... && go test ./...` with and without
`KUBECENTER_TEST_DATABASE_URL` set — both must pass, skipping cleanly without.

Branch: `feat/d13-dashboard-layout-endpoints`
PR title: `feat(preferences): dashboard layout endpoints`

**Done means** — an unsaved scope answers 204; PUT creates then updates in place
with an incrementing revision; a stale revision is 409 `revision_conflict`; a
no-database deployment answers 503 `database_unavailable` on both routes;
layouts are invisible across owners and across clusters.

**Added 2026-09-19 after review of D12.** Spec §7 carries one read-path
obligation that appears nowhere in D11–D14 and would otherwise be lost: the GET
handler must **re-authorize every namespace parameter at read time via
`CanAccessGroupResource`** — never `CanAccess`, which short-circuits to allow in
predicate-fake mode (`access.go:111`) — and drop or mark the items it cannot
authorize before returning a layout. A stored parameter is evidence of what the
user could see when they saved it, never of what they may see now: a layout
saved while the user had access to `prod` must not keep showing `prod` data
after that access is revoked. D12 deliberately does not discharge this, because
the write path is not where it belongs; this line exists so D13 inherits the
obligation rather than the gap. The same applies to retired widget ids, which
D14 drops quietly on read while the server keeps accepting them on write.

**AMENDED 2026-09-19 — what D13 shipped.** Written by walking this section's
own list, per the lesson recorded on D12, not by reading the diff. Every step
above is discharged; the divergences are below, then what D14 inherits.

**Step 2 diverges twice from "follows `HandleCreateView` exactly".**

1. **`CreateInCluster`, not `Create`.** The instruction to "pass a ceiling of
   `len(allowedDashboardScopes)` into `Create`" is wrong, and wrong in a way
   that only a multi-cluster test can see: `Create` counts the owner's records
   of a kind **across every cluster**, which is right for saved views (generous
   ceiling, listed across clusters) and catastrophic for a layout, whose
   ceiling is exactly the number of scopes. Counted globally, the user's first
   saved layout anywhere refuses their first layout on every other cluster they
   manage. The store grew a second entry point whose count and advisory lock
   are both narrowed to `rec.ClusterID`; `MaxDashboardLayoutsPerUser`'s doc now
   says "per cluster" is the caller's obligation and names the method that
   honours it. D13's own `IsClusterScoped` test is what catches the wrong one.

2. **No `validName` call.** `HandleCreateView` validates a user-supplied label;
   a layout's name IS its scope, which came from the closed allowlist two lines
   earlier. Re-validating a value the allowlist already produced would be
   ceremony.

**Two defects the shape in this section would have shipped**, both found by
running the tests it prescribes rather than by reading:

- **`revision: 0` over an existing layout reported `limit_reached`.** With the
  ceiling equal to the number of scopes, a second create of the SAME scope
  exhausts the quota *before* it reaches the unique index, so the store answers
  `ErrPreferenceLimit` and never `ErrPreferenceDuplicate`. The plan's "create
  when absent and update when present" was left to the store to infer from an
  error; it cannot. `HandleSaveLayout` now reads the scope first and decides
  which write to attempt from what is stored, and translates a limit into a
  conflict only when a layout has appeared under that scope since the read — so
  a genuine quota refusal still says so, with the limit in `extra`.
- **A revision claim for a scope with no layout would have been a create.** It
  is a conflict: the client believes in a layout the server has no record of,
  and honouring it resurrects one at a revision the client made up.

**Three guards this section did not ask for, each because a test could not
otherwise reach the rule:**

- An unserved scope answers **400**, not 404, so it stays distinguishable from
  the 204 a served-but-unsaved scope gives — the whole reason Step 1 chose 204.
- The path's scope and `config.scope` must agree, and the path wins. Only one
  scope ships, so `withTestScope` (a sibling of D12's `withTestWidget`) is what
  makes the guard reachable at all.
- `maxDashboardLayouts`' own ceiling is exercised through a stand-in scope,
  because with one scope a real quota refusal cannot otherwise be produced.

**Read-time re-authorization is discharged** as the 2026-09-19 note requires:
`CanAccessGroupResource(verb=list, group="", resource=pods, ns)`, never
`CanAccess`. A mutation swapping one for the other turns
`TestHandler_GetLayout_ReauthorizesNamespaces` red, which is the point of the
note — under a predicate fake, `CanAccess` short-circuits to allow, so the
weaker call would have passed its own test while authorizing nothing.
Unauthorized placements are **dropped from the response and named in it**; the
stored row is never rewritten by a read, because access can come back and the
layout has to come back with it. With no `AccessChecker` wired the read
withholds every namespaced placement: "could not check" is not "allowed".

**Eight files, not three** (G2 again, and for the reason D12 predicted).
`allEndpoints()`, `wantPreferenceRoutes`, the two store methods and main.go's
wiring each carry half of a guarantee: a route absent from the endpoint table
has no 503 coverage and no CSRF coverage, and a checker nothing constructs
re-authorizes nothing. Splitting them ships the guard without the thing it
guards.

**One mutation survives, deliberately.** Removing the `revision == 0` branch on
the "layout exists" path changes no response: handing 0 to the UPDATE answers
409 anyway, because no stored row can carry it (`revision >= 1` is a CHECK in
000018). It stays, with a comment saying exactly that, because the rule is part
of the endpoint's contract and belongs where it is read rather than inferred
from a migration. **Generalize this:** a guard that cannot go red is either
decoration or belt-and-braces over an enforced rule, and the difference is
worth stating in the code rather than leaving for the next reader to rediscover.

**What D14 inherits — three corrections to its own text, below:**

1. **The PUT body is `{revision, config}`.** It carries **no `name`**. The
   snippet in Step 1 of D14 sends `{name: scope, revision, config}`, which the
   decoder refuses with a 400 naming the field: the scope is the path, and the
   record's name is derived from it. `SaveLayoutRequest` is the Go shape.
2. **GET returns `LayoutResponse`, not a bare record** — the stored record plus
   `withheld?: string[]`, the instanceIds the server removed because the caller
   can no longer see their namespace. `dropUnknownWidgets` handles the retired
   ids; `withheld` is the other half, and a client that ignores it shows a
   dashboard quietly missing widgets and then makes the loss permanent on the
   next save. **D14 must not write back a layout it received with a non-empty
   `withheld`** without saying so.
3. **A create answers 201 and a replace answers 200**, both with the record in
   `data`.

D14's verification commands are also stale: `deno test lib/` and `deno task
check` predate the Bun/Astro migration. The repo-canonical checks are
`bun test` and `bun run check`.

---

### Task D14: Client store

**Files:**
- Create: `frontend/lib/dashboard/layout-store.ts`
- Create: `frontend/lib/dashboard/layout-store_test.ts`
- Modify: `frontend/lib/preferences.ts`
- Modify: `frontend/lib/preferences_test.ts`
- Modify: `frontend/islands/DashboardGrid.tsx`

**Interfaces:**
- Produces: `layout`, `layoutLoaded`, `layoutUnavailable`, `loadLayout(scope)`,
  `saveLayout(scope, config)`, `dropUnknownWidgets(config)`. P4 consumes all.

Modelled on `pin-store.ts`: module-level signals, one in-flight
`AbortController` that aborts a stale load, and **deliberately not**
localStorage-backed — `pin-store.ts:9-13` records the reason, which is that
browser storage is captured into Playwright's `storageState` and leaks across
the E2E suite.

- [ ] **Step 1: Add the API methods**

In `frontend/lib/preferences.ts`, following the existing convention that every
method calls `api<T>()` directly rather than `apiPost`/`apiPut` — only `api()`
accepts an `AbortSignal` (`preferences.ts:6-13`):

```ts
const LAYOUTS = "/v1/preferences/layouts";

  getLayout: async (
    scope: string,
    signal?: AbortSignal,
  ): Promise<LayoutRecord | null> => {
    // 204 means "not customized yet", which api() surfaces as data: undefined.
    const res = await api<LayoutRecord>(
      `${LAYOUTS}/${encodeURIComponent(scope)}`,
      { method: "GET", signal },
    );
    return res.data ?? null;
  },

  saveLayout: async (
    scope: string,
    revision: number,
    config: DashboardLayoutConfig,
    signal?: AbortSignal,
  ): Promise<LayoutRecord> =>
    (await api<LayoutRecord>(`${LAYOUTS}/${encodeURIComponent(scope)}`, {
      method: "PUT",
      body: JSON.stringify({ name: scope, revision, config }),
      signal,
    })).data,
```

Add `"unknown_widget_id"` to `PREFERENCE_REASONS` and to the sorted assertion in
`preferences_test.ts`.

- [ ] **Step 2: Write the failing test for the read-side drop**

`dropUnknownWidgets` is the D-7 read path and is pure, so it is unit-testable:

```ts
Deno.test("dropUnknownWidgets: keeps known ids untouched", () => { /* ... */ });

Deno.test("dropUnknownWidgets: drops an unknown id and names it", () => {
  const { config, warnings } = dropUnknownWidgets({
    schemaVersion: 1, scope: "overview", columns: 12,
    items: [
      { instanceId: "a", id: "cluster-health", x: 0, y: 0, w: 4, h: 4 },
      { instanceId: "b", id: "widget-from-the-future", x: 4, y: 0, w: 4, h: 4 },
    ],
  });
  assertEquals(config.items.length, 1);
  assertEquals(warnings.length, 1);
  // The user is told which widget vanished. "Something was removed" is worse
  // than saying nothing.
  assertStringIncludes(warnings[0], "widget-from-the-future");
});

Deno.test("dropUnknownWidgets: dropping every widget yields an empty layout, not the default", () => {
  // Falling back to the shipped default here would silently discard a layout
  // the user spent time on because one build was missing its widgets.
});
```

This mirrors `applyViewState` (`preference-types.ts:139`), which returns
`{ state, warnings }` and whose callers are required to surface the warnings (R3).

- [ ] **Step 3: Write `layout-store.ts` and wire `DashboardGrid`**

On mount: `loadLayout(scope)`; on 204 or `database_unavailable`, fall back to
`DEFAULT_OVERVIEW_LAYOUT` and keep the grid read-only in the unavailable case.
Surface warnings from `dropUnknownWidgets`. Save is P4's Save button; this unit
only proves load and a programmatic save.

- [ ] **Step 4: Verify and commit**

Run: `cd frontend && deno test lib/ && deno task check`

Branch: `feat/d14-layout-client-store`
PR title: `feat(dashboard): load and save personal layouts`

**Done means** — a saved layout loads and renders; an unsaved scope falls back
to the default; a no-database deployment renders the default and says layouts
are unavailable rather than looking broken; unknown widget ids are dropped with
a warning naming each one; a stale revision surfaces as a conflict rather than
overwriting.

---

## Self-review

**Spec coverage.** §4.3 persisted layout → D12's `DashboardLayoutConfig`. §7
validation list → D12, item by item. D-3 per-cluster scoping → inherited from
the cluster-context middleware, asserted in D13's `IsClusterScoped` test. D-7
unknown ids → rejected on write (D12), dropped with notice on read (D14). D-11
dedup_key = scope → D11. G5 503 → D13 Step 4. G8 fuzz → D12 Step 4.

**Not covered:** the editor UI, reset, and copy-from-cluster are P4. Copy is a
create under a different `cluster_id` and needs no new endpoint.

**Placeholders.** D11's sequence number is intentionally `NNNNNN` — G4's
amendment requires it to be chosen at merge time, and writing a number here
would reintroduce exactly the failure the amendment exists to prevent. D12's
fuzz seeds and D13's handler bodies are described by the pattern they copy plus
the file and line range; the validator, the migrations, the NOTES section and
the route block are written out in full.

**Type consistency.** Go `DashboardLayoutConfig{SchemaVersion, Scope, Columns,
Items}` matches the TS interface in P1's `types.ts` field for field, including
`instanceId` on items and `params` as `map[string]string` / `Record<string,
string>`. `ValidateDashboardLayout` returns `(cfg, normalized, error)`, the same
signature shape as `ValidateSavedView`, so `HandleSaveLayout` can be a copy of
`HandleCreateView` with two substitutions.

**Risk.** The widget-id allowlist is duplicated across languages. That is a
maintenance cost accepted deliberately, with the mitigation named: a pinned-list
test on each side, matching how `PREFERENCE_REASONS` is already kept honest.
