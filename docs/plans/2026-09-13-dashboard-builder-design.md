---
title: "Personal Dashboard Builder — Design"
parent_plan: docs/plans/2026-09-10-EXECUTION-ORDER.md
supersedes: "Release A deferred appendix (U38 + U6)"
date: 2026-09-13
baseline_revision: 78d9881e
status: design-approved
---

# Personal Dashboard Builder — Design

This document resolves **Q5** and replaces the Release A deferred appendix
(U38 + U6). It is a design spec, not an implementation plan; the plan is
generated from it.

---

## 1. What changed, and what that overrides

Release A's carried-forward requirements say:

- **R7** — "a personal dashboard release persists an ordered selection of
  **approved existing widgets**".
- **U6 exit** — "An operator can arrange existing widgets; **this is not an
  arbitrary dashboard builder**."

The product decision on 2026-09-13 was to build the dashboard builder: a
snapping grid with drag-and-drop and resize, widgets that change their data
presentation with their size, a catalog the user adds from, and per-user saved
layouts — starting with the main overview page and generalizing to per-category
overview pages (workloads, networking, storage, security) later.

R7 and the U6 exit criterion are therefore **superseded** by this document.
**KTD4 is not.** The prohibition on persisting credentials, tokens, raw Secret
data, scripts, executable queries and redirect targets survives intact and is
restated normatively in section 7.

U38 and U6 as scoped no longer exist as units. Their content is absorbed into
sub-projects P3 and P4 below.

---

## 2. Starting state

`frontend/islands/DashboardV2.tsx` is 1084 LOC and renders a fixed three-row
flex layout. Measured, not assumed:

| Row | Contents | Flex sizing |
|---|---|---|
| 1 | `Cluster Health` WidgetShell (L417) · metric-tile 2x2 grid — CPU, Memory, Pods, Network (L473-505), **not** a WidgetShell | `2 1 320px` / `3 1 380px` |
| 2 | `Resource Utilization` (L522) · `Pod Status` (L580) | `3 1 380px` / rest |
| 3 | `Nodes` (L711) · `Recent Events` (L795) · `Active Alerts` (L958) | `2 1 260px` / `3 1 300px` / `2 1 240px` |

Two facts that shaped the design:

1. **There are seven visible blocks, not six.** The metric-tile grid is a bare
   `div`, but a user perceives it as a card. The plan's "is the catalog exactly
   those six?" had a hidden third answer.
2. **Widths are positional, not intrinsic.** Each widget's `flex` basis is tuned
   for where it sits in its row. A layout model that stores only an order would
   let a user put the narrow `Active Alerts` card where the wide
   `Resource Utilization` chart lives. The model must carry size.

The whole file contains exactly one `button` element (L376, the time-range tabs)
and no `aria-label`, `select`, widget id, registry or ordering array.

### Constraints verified, not assumed

- **Mobile does not bind this work.** Every `preferences` hit under `mobile/lib/`
  is Flutter's device-local `SharedPreferences` (theme, onboarding, Sentry
  opt-in). The Flutter app never calls the server `/preferences` API, so R4 does
  not apply.
- **Preferences are already per-cluster.** `backend/internal/preferences/types.go:100-104`
  — `CreateRequest` deliberately carries no `clusterId` because it is derived
  from the cluster-context middleware. Saved views and pins are scoped per
  `(user, cluster)` today.
- **No `preact/compat` alias exists** in `frontend/deno.json`, and
  `jsx: "precompile"` is Fresh's own transform. The repo has no React-component
  dependency; its pattern for complex interaction is a framework-agnostic
  library inside an island (CodeMirror, xterm, fuse.js, d3-force).

---

## 3. Decisions

| # | Decision | Rationale |
|---|---|---|
| **D-1** | Catalog is the full 36 widgets of section 6 (7 existing + 29 new). | Chosen over a 7- or 13-widget v1. Mitigation: P1's render contract is validated against four structurally different widgets before P5 mass-produces the rest, because a late contract change otherwise costs 36 edits. |
| **D-2** | Grid engine is hand-rolled on CSS Grid. | A React grid library would require introducing `react`/`react-dom` aliases into a Preact app on a non-standard JSX transform. A vanilla library (Gridstack) was the runner-up; hand-rolling keeps full control of the display-mode contract and of keyboard operation, which grid libraries handle poorly, and adds no supply-chain surface. |
| **D-3** | Layouts are scoped per `(user, cluster)`, plus a "copy layout from another cluster" action. | Inherits the existing preference scoping with zero new mechanism, lets a prod cluster differ from a sandbox, and keeps reuse to one click. Mirrors the "Saved on another cluster" affordance in `SavedViews.tsx:537`. |
| **D-4** | Widgets own their data through a shared keyed cache. | Central fetching is correct for six fixed widgets and wrong for thirty optional ones — the page would issue every request regardless of layout. |
| **D-5** | Editing is behind an explicit edit mode. | A monitoring dashboard gets clicked through fast; accidentally dragging a widget while reaching for a link is a bad first impression. Also gives Save / Cancel / Reset and the palette a natural home. Cost: one click to start editing. |
| **D-6** | "Reset" restores the **shipped default**, behind one confirm. | "Restore my previous layout" is undo — different state, different feature. Conflating them yields a button whose behavior nobody can predict. |
| **D-7** | Unknown widget ids are **dropped-with-notice on read, rejected on write**. | A widget retired in a later release must never brick a saved layout, so reads drop it and surface a notice (matching Release A's `applyViewState` warnings). A write carrying an unknown id is a client bug and gets a 400 naming the field (matching how `CreateRequest` already rejects unknown fields). |
| **D-8** | No user-authored PromQL and no URL fields, ever. | KTD4 survives section 1. Parameterized widgets take closed validated enums only. |

---

## 4. Architecture

### 4.1 Widget registry — `frontend/lib/dashboard/registry.ts`

```ts
interface WidgetDef {
  id: WidgetId;                 // stable kebab-case; never reused after retirement
  title: string;
  family: WidgetFamily;         // grouping in the catalog palette
  scopes: DashboardScope[];     // ["overview"] today; P6 adds "workloads" etc.
  sources: DataSourceKey[];     // what it needs fetched (see 4.2)
  minW: number; minH: number;   // grid units; the editor clamps resize to these
  defaultW: number; defaultH: number;
  modes: DisplayMode[];         // which of the three it actually implements
  params?: ParamSpec;           // closed enum spec; absent for most widgets
  render(props: WidgetProps): VNode;
}
```

`scopes` exists from the first unit. It is what makes P6 (per-category
dashboards) routing rather than a second mechanism.

### 4.2 Data layer — `frontend/lib/dashboard/data.ts`

A keyed cache with in-flight de-duplication. Three widgets declaring
`dashboard-summary` produce one request; a widget the user removed produces
none. The time-range signal is an input to the cache key, so changing the range
refetches exactly the series-backed sources.

### 4.3 Persisted layout

```ts
interface DashboardLayoutConfig {
  schemaVersion: 1;
  scope: "overview";
  columns: 12;                  // stored, not assumed, so a future change is detectable
  items: {
    instanceId: string;           // unique within the layout; identity of a placement
    id: WidgetId;                 // which widget is placed
    x: number; y: number; w: number; h: number;
    params?: Record<string, string>;
  }[];
}
```

`instanceId` exists because a **parameterized widget may legitimately appear
more than once** — a diagnostics summary for `prod` beside one for `staging`,
or golden signals for two services. Keying placements on `id` alone would make
that impossible. Uniqueness is therefore enforced on `instanceId`, plus a
rejection of two items sharing the same `(id, params)` pair, which is a
duplicate rather than a second view.

### 4.4 Display-mode contract

| Mode | Shows |
|---|---|
| `compact` | headline number + sparkline |
| `normal` | the primary visualization |
| `expanded` | primary + a detail table or list |

`WidgetHost` measures with `ResizeObserver` and selects the largest mode whose
minimum fits. A widget declaring only `normal` renders `normal` at every size.
This is the contract all 36 widgets are built against.

### 4.5 Rendering and geometry

CSS Grid: `grid-template-columns: repeat(12, 1fr)`, fixed `grid-auto-rows`
(~40px), each item `grid-column: x+1 / span w; grid-row: y+1 / span h`. Minimum
widget 2x2; cap 40 items per layout.

Below the narrow breakpoint the grid collapses to one column in `items` order
sorted by `y` then `x` — which also gives keyboard and screen-reader traversal
order for free.

All drag/resize geometry lives in `frontend/lib/dashboard/grid.ts` as **pure
functions over the layout array** (cell-from-cursor, clamp, collision
resolution, compaction). No DOM. This is what makes the hard part testable.

---

## 5. Sub-projects

Units respect G2 (one PR per unit, at most 5 touched files including tests).
Directive 1 applies: `DashboardV2.tsx` is 1084 LOC, so dead-code removal is a
separate commit before any restructuring.

| Unit | Scope |
|---|---|
| **D0** | Step-0 cleanup of `DashboardV2.tsx` — dead props, unused imports, debug logs. No behavior change. |
| | **P1 — registry and render contract** |
| D1 | `lib/dashboard/types.ts`, `registry.ts` + tests |
| D2 | `lib/dashboard/data.ts` + tests — keyed cache, in-flight dedupe, time-range key |
| D3 | `components/dashboard/WidgetHost.tsx` + tests — ResizeObserver, mode selection, loading/error/empty |
| D4-D5 | Extract the seven existing widgets; `DashboardV2` renders the default layout through the registry. **Exit: visually identical to today.** |
| | **P2 — grid engine** |
| D6 | `lib/dashboard/grid.ts` — pure geometry, heavily unit-tested |
| D7 | `DashboardGrid.tsx` island — CSS Grid rendering, 1-column collapse |
| D8 | Drag — pointer events, ghost preview, snap |
| D9 | Resize — edge handles, clamp to `minW`/`minH` |
| D10 | Keyboard move/resize to WCAG 2.2 AA, the bar the mobile app was held to |
| | **P3 — persistence** |
| D11 | Migration + `kind` check constraint + `NOTES.txt` |
| D12 | `ValidateDashboardLayout` + tests |
| D13 | Handler routes + tests, with G5 `requireStore` 503 gating (never a bare 404) |
| D14 | `preferencesApi` methods, load/save wiring, revision-conflict handling |
| | **P4 — editor** |
| D15 | Edit-mode toggle, dirty state, save/cancel |
| D16 | Catalog palette — add widget, grouped by family, searchable |
| D17 | Remove, reset-to-default, copy-layout-from-cluster |
| D18 | E2E specs |
| | **P5 — catalog** |
| D19-D25 | 29 new widgets at ~4 per unit. Parallelizable once D3's contract is proven. |
| | **P6 — per-category dashboards** |
| — | Deferred. Cheap because `scopes` exists from D1. |

**Approximately 26 PRs.** For calibration, Release B is 9. P5 is the natural
place to trim if value is wanted sooner.

---

## 6. Widget catalog

Every entry is backed by a route already registered in
`backend/internal/server/routes.go`. Nothing requires new backend except where
marked.

### Existing — become the default layout

`cluster-health` · `key-metrics` (CPU/Memory/Pods/Network) ·
`resource-utilization` · `pod-status` · `nodes` · `recent-events` ·
`active-alerts`

`key-metrics` requires wrapping today's bare tile grid in a shell so it has an
identity and a title.

### Workloads and scaling

| Widget | Source |
|---|---|
| Workload health roll-up (Deploy/STS/DS ready vs degraded) | `/resources/counts` |
| Pod restart watchlist / CrashLoopBackOff | pods list |
| Top consumers — pods by CPU or memory | `/monitoring/query` (preset PromQL) |
| HPA status — current vs target replicas | `/resources/horizontalpodautoscalers` |
| PDB risk | `/resources/poddisruptionbudgets` |
| Pending / unschedulable pods | pods list |

### Reliability

| Widget | Source |
|---|---|
| Diagnostics summary by severity | `/diagnostics/{ns}/summary` * |
| Namespace quota pressure | `/limits/namespaces` |
| Node conditions and pressure | `/resources/nodes` |
| Storage capacity pressure | `/storage` + preset query |

### Security and compliance

| Widget | Source |
|---|---|
| Policy compliance gauge | `/policy/compliance` |
| Top policy violations | `/policy/violations` |
| Vulnerability severity breakdown | `/security/vulnerabilities` |
| Certificates expiring soon | `/certificates/expiring` |
| ESO sync health / drift | `/externalsecrets` |

### Delivery

| Widget | Source |
|---|---|
| GitOps app health (out-of-sync / degraded) | `/gitops/applications` |
| Recent syncs and commits | `/gitops/commits` |

### Data protection

| Widget | Source |
|---|---|
| Velero backup status | `/velero/backups` |
| Snapshot health | `/storage/snapshots` |

### Networking

| Widget | Source |
|---|---|
| Service mesh golden signals | `/mesh/golden-signals` * |
| mTLS coverage | `/mesh/mtls` * |
| Hubble flows / denied flows | `/networking/hubble/flows` |
| Gateway and Ingress routes | `/networking/gateways`, `/httproutes` |

### Platform

| Widget | Source |
|---|---|
| Multi-cluster status grid | `/clusters` — also partly answers pre-existing defect #3 |
| Notifications feed | `/notifications` |
| Audit activity | `/audit/logs` |
| Grafana panel embed | `/monitoring/dashboards` + proxy ** |
| Saved views launcher | `/preferences/views` — reuses Release A |
| Pinned resources | `/preferences/pins` — reuses Release A |

\* requires a parameter (namespace, or namespace + service)

\*\* picker over **discovered** dashboards only; never a free URL field

---

## 7. Security and validation

Server-side validation rejects: unknown widget id, `x < 0`,
`x + w > columns`, `w < minW`, `h < minH`, duplicate `instanceId`, two items
sharing the same `(id, params)` pair, overlapping rectangles, item count over
the cap, and any param value outside that widget's `ParamSpec` enum. Same
allowlisted-JSONB pipeline saved views already use, plus
`revision` for optimistic concurrency and `schemaVersion` for forward
compatibility.

- **No executable queries, no URLs.** Widgets referencing PromQL use
  registry-side preset queries keyed by id. The Grafana widget stores a
  dashboard uid validated against discovery output, never a URL.
- **Namespace parameters are re-authorized at read time** via
  `CanAccessGroupResource`, per G1 and G7. A stored parameter is never evidence
  of current authorization. `CanAccess` must not be used — it short-circuits to
  allow in predicate-fake mode (`access.go:111`).
- **Retired ids** live in a `RETIRED_WIDGET_IDS` set so validation can
  distinguish "retired" (drop quietly on read, log) from "never existed"
  (400 on write). Retired ids are never reused.
- **DB unavailable is 503, never 404**, per G5 and the
  `externalsecrets/bulk.go:294` precedent.

---

## 8. Amendment to G4 — migration sequencing

`backend/internal/store/migrate.go:25` runs plain `m.Up()`, which applies only
migrations numbered **above** the database's current version.

G4 pre-assigned `000019 -> B`, `000020 -> D`, `000021 -> E`, `000022 -> F`. If
this project ships before Release B and takes `000023`, Release B's `000019`
will never run on any database that already reached 23 — silently, with no
error.

**Amendment:** G4's table is *intent*, not assignment. **Sequence numbers are
assigned at merge time, and the merging unit takes the next free number.** If
the dashboard builder merges first, it takes `000019` and B/D/E/F shift to
20/21/22/23.

G4's escalation rule ("any unit needing an unlisted sequence stops and escalates
rather than picking a number") is satisfied by this amendment being recorded
here and propagated to `docs/plans/2026-09-10-EXECUTION-ORDER.md`.

---

## 9. Verification contract

- Repo-wide, per Agent Directive 4: `cd frontend && deno task check` and
  `cd backend && go vet ./... && go test ./...`. Scoped checks do not satisfy
  this.
- **D4-D5 exit is visual equivalence** with the pre-refactor dashboard. This is
  the single most important gate in P1: if rendering today's layout through the
  registry changes what the page looks like, the engine is not trustworthy.
- `grid.ts` geometry is covered by pure unit tests — collision resolution and
  compaction are where a hand-rolled grid actually fails.
- E2E (D18): reorder persists across reload; reset restores the default; unknown
  ids fall back safely; layout is operable by keyboard; layout is usable at
  narrow widths.
- Migration round trip (up, seed, down, up) under the U0 env-gated Postgres
  harness, per G3.

---

## 10. Open items

- **Where this fits in the roadmap sequence is not decided by this document.**
  It is ~26 PRs against Release B's 9, and Releases B, C, F, E and D are already
  ordered. That decision is taken separately.
- P6's per-category widget assignment (which widgets appear on the workloads
  dashboard versus networking) is not specified here; `scopes` makes it an
  additive edit per widget.
- Whether the metric tiles should later become four independently placeable
  widgets rather than one `key-metrics` block. Deferred: they share a 2x2 grid
  and uniform height today, and splitting them is additive once the contract is
  proven.
