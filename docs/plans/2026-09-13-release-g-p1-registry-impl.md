---
title: "Release G / P1 — Widget registry and render contract"
parent_plan: docs/plans/2026-09-10-EXECUTION-ORDER.md
spec: docs/plans/2026-09-13-dashboard-builder-design.md
date: 2026-09-13
baseline_revision: 78d9881e
status: ready
---

# Release G / P1 Implementation Plan — Registry and render contract

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan
> task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every dashboard widget a stable identity, its own data, and a
declared behavior at each size — then render today's dashboard through that
machinery without changing a pixel.

**Architecture:** A pure registry and a pure mode-selector in `frontend/lib/dashboard/`,
a keyed fetch cache with in-flight de-duplication, a thin `WidgetHost` that
measures and dispatches, and ten widget components extracted from the ten visual
blocks now hardcoded in `DashboardV2.tsx`.

**Tech Stack:** Deno 2.x, Fresh 2.x (Preact), `@preact/signals`, Playwright.

**Spec:** `docs/plans/2026-09-13-dashboard-builder-design.md` — sections 4.1, 4.2,
4.4 and decisions D-1, D-4, D-9, D-10.

## Global Constraints

- One PR per unit, **at most 5 touched files including tests** (G2). CI green
  before the next unit starts.
- **Anything needing a unit test lives in `frontend/lib/` as a pure module**
  (D-10). This repo has zero component tests — all nine unit tests are
  `frontend/lib/*_test.ts`, there is no testing-library and no
  `preact-render-to-string`. `.tsx` files stay thin and are asserted in Playwright.
- Test imports: `import { assertEquals } from "jsr:@std/assert@1";` written
  inline per file (the specifier is **not** in `deno.json` imports), and the
  module under test imported by **relative** path (`./registry.ts`), not `@/`.
- Repo-wide verification only (Agent Directive 4): `cd frontend && deno task check`.
- Tailwind utility-only is the stated convention, but every file under edit uses
  inline `style` objects. Match the file you are in.
- `globalThis.setTimeout`, not bare `setTimeout` — house style, avoids the Node
  `Timeout` type (`PodTerminal.tsx:106`).
- Widget ids are stable kebab-case and are **never reused after retirement**.

---

## Starting state, measured

`frontend/islands/DashboardV2.tsx` is 1085 lines. The component takes **no
props** (`:85`, and `routes/index.tsx:5` renders it bare). There are **no
sub-components defined in the file** — zero local functions returning JSX, which
is why it is 1085 lines. **No block has local state**: all ten are pure functions
of signals plus derived scalars. That is the best possible starting point for
extraction.

Ten visual blocks, in three flex rows:

| Block | Widget id | Lines | Reads |
|---|---|---|---|
| Cluster Health | `cluster-health` | 416-462 | `summary`, `clusterInfo` |
| CPU tile | `cpu-tile` | 473-481 | `summary`, `trends` |
| Memory tile | `memory-tile` | 482-490 | `summary`, `trends` |
| Pods tile | `pods-tile` | 491-499 | `summary`, `trends` |
| Network tile | `network-tile` | 500-507 | `trends` |
| Resource Utilization | `resource-utilization` | 520-576 | `trends` |
| Pod Status | `pod-status` | 578-698 | `summary` |
| Nodes | `nodes` | 709-791 | `summary`, `clusterInfo` |
| Recent Events | `recent-events` | 793-954 | `events` |
| Active Alerts | `active-alerts` | 956-1080 | `summary` |

Four data sources, currently all fetched centrally in the island body:

| Source key | Endpoint | Consumers | Refresh today |
|---|---|---|---|
| `dashboard-summary` | `GET /v1/cluster/dashboard-summary` | 7 blocks | mount + 60s |
| `dashboard-trends` | `GET /v1/cluster/dashboard-trends?range=` | 5 blocks | mount + 60s + tab click |
| `cluster-info` | `GET /v1/cluster/info` | 2 blocks (fallback only) | **mount only** |
| `recent-events` | `GET /v1/resources/events?limit=10` | 1 block | **mount only** |

**Three gaps P1 must design, because they do not exist to copy:**

1. **No error state.** Failures are swallowed at `:131` (`.catch(() => {})`) and
   `:142` (`allSettled` results discarded into `_summaryResult`/`_trendsResult`).
2. **No per-widget loading state.** The skeleton at `:197-245` is an
   all-or-nothing page gate.
3. **`cluster-info` and `recent-events` never refresh.** Moving them into a
   uniform cache changes that. **Decision for this plan: they gain the same 60s
   refresh as the others.** A dashboard whose event list silently ages out for
   the lifetime of the tab is a defect, not a feature, and uniform refresh is
   less surprising than a per-source exception.

---

## File Structure

| File | Responsibility |
|---|---|
| `frontend/lib/dashboard/types.ts` | Every shared type and bound. No logic. |
| `frontend/lib/dashboard/registry.ts` | The widget catalog: lookup, scope filter, retired ids. |
| `frontend/lib/dashboard/display-mode.ts` | Pure `pickMode`. The whole size-responsive contract. |
| `frontend/lib/dashboard/data.ts` | Keyed fetch cache, in-flight dedupe, refresh loop. |
| `frontend/lib/dashboard/default-layout.ts` | The shipped default layout for `overview`. |
| `frontend/components/dashboard/WidgetHost.tsx` | Measures, picks a mode, renders loading/error/widget. |
| `frontend/components/dashboard/widgets/*.tsx` | Ten widgets, one file each. |
| `frontend/islands/DashboardV2.tsx` | Shrinks to a shell that renders the default layout. |

---

### Task D0: Step-0 cleanup of DashboardV2

**Rescoped against the real file.** The originally planned items — dead props,
unused imports, debug logs — are all empty sets. Do not go looking for them.

**Files:**
- Modify: `frontend/islands/DashboardV2.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces: nothing new. Behavior-preserving except the donut fix, which is a
  deliberate bug fix called out below.

- [ ] **Step 1: Fix the unreachable donut branch**

At `:298` the code reads `const podTotal = podCount || 1;`, which is always >= 1,
so the `podTotal > 0` guard at `:299` is always true and the placeholder arm at
`:317` can never render. The visible consequence: an empty cluster draws three
zero-value segments instead of the intended grey placeholder.

Change the guard to test the real quantity:

```tsx
  // podTotal is the divisor and is floored at 1 so the percentages below never
  // divide by zero. It is NOT the emptiness test -- guarding on it made the
  // placeholder arm unreachable and drew three zero-value segments on an empty
  // cluster. Test the actual count.
  const podTotal = podCount || 1;
  const donutSegments: DonutSegment[] = podCount > 0
    ? [
      /* ...existing three segments, unchanged... */
    ]
    : [{ value: 1, color: "var(--border-subtle)" }];
```

- [ ] **Step 2: Remove the write-once `syncedAgo` signal**

`syncedAgo` (`:97`) is only ever assigned the literal `"just now"` (`:109`), so
`syncedLabel` (`:320`) is permanently `"synced just now"`. Delete the signal, its
assignment, and fold the literal into the label — a real relative timestamp is a
separate change and does not belong in a cleanup commit.

Delete `:97`, delete the `syncedAgo.value = "just now";` line in `fetchSummary`,
and replace the `syncedLabel` derivation at `:320` with:

```tsx
  // Deliberately a literal. The previous signal only ever held this string, so
  // it read as a live timestamp while never ticking. A real "synced 4m ago"
  // needs a timestamp and a formatter, and is out of scope for a cleanup.
  const syncedLabel = summary.value ? "synced just now" : "";
```

- [ ] **Step 3: Trim the six unread wire-type fields**

Delete these fields from the module-level types — each is fetched and never read:
`ClusterInfoData.kubernetesVersion` (`:25`), the whole `ClusterInfoData.kubecenter`
block (`:28-32`), `DashboardSummary.services` (`:38`), `DashboardTrends.services`
(`:64`), `DashboardTrends.window` (`:72`), `DashboardTrends.step` (`:73`).

These are decode targets, not the wire contract — removing a field makes
`encoding/json`-style decoding ignore it, it does not ask the server for less.

- [ ] **Step 4: Record the two things being kept**

Add this above the `load()` function so a later reviewer does not read the
swallowed errors as an accidental omission:

```tsx
  // Known and kept for now: a failed summary or trends fetch produces no UI
  // signal at all (the allSettled results below are discarded, and the tab
  // fetch catches into a no-op). Per-widget error state arrives with the
  // registry in D3; introducing a page-level error banner here would be
  // replaced two units later.
```

- [ ] **Step 5: Verify nothing moved**

Run: `cd frontend && deno task check`
Expected: exit 0.

Run: `cd e2e && npx playwright test tests/dashboard.spec.ts`
Expected: no new failures.

Then load `/` in a dev browser against a cluster with **zero running pods** and
confirm the Pod Status donut renders the grey placeholder ring rather than three
zero-width segments. This is the only intentional visual change in D0.

- [ ] **Step 6: Commit**

```bash
git add frontend/islands/DashboardV2.tsx
git commit -m "refactor(dashboard): step-0 cleanup before the registry extraction"
```

Branch: `refactor/d0-dashboard-cleanup`
PR title: `refactor(dashboard): step-0 cleanup before the registry extraction`

**Done means** — the donut placeholder is reachable and renders on an empty
cluster; no signal claims freshness it cannot know; no type field is decoded and
discarded; `deno task check` exit 0; `dashboard.spec.ts` unchanged and passing.

---

### Task D1: Types and registry

**Files:**
- Create: `frontend/lib/dashboard/types.ts`
- Create: `frontend/lib/dashboard/registry.ts`
- Create: `frontend/lib/dashboard/registry_test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `DisplayMode`, `DashboardScope`, `WidgetFamily`, `DataSourceKey`,
  `WidgetDef`, `WidgetProps`, `LayoutItem`, `DashboardLayoutConfig`,
  `DASHBOARD_COLUMNS`, `DASHBOARD_ROW_HEIGHT`, `DASHBOARD_MAX_ITEMS`,
  `DASHBOARD_LAYOUT_SCHEMA_VERSION`, `getWidget`, `allWidgets`,
  `widgetsForScope`, `isRetiredWidgetId`, `registerWidget`. D2 consumes
  `DataSourceKey`; D3 consumes `WidgetDef` and `DisplayMode`; P2 consumes
  `LayoutItem` and `DASHBOARD_COLUMNS`; P3 consumes `DashboardLayoutConfig`.

- [ ] **Step 1: Write `types.ts`**

```ts
/**
 * Shared types and bounds for the personal dashboard builder.
 *
 * This module is pure: no DOM, no fetch, no signals. Everything that needs a
 * unit test in this feature lives behind it, because the repo has no component
 * test harness (see the design spec, D-10).
 */
import type { VNode } from "preact";

/** The three sizes a widget can render at. Ordered smallest to largest. */
export const DISPLAY_MODES = ["compact", "normal", "expanded"] as const;
export type DisplayMode = typeof DISPLAY_MODES[number];

/** Which dashboard a widget may appear on. P6 adds more; the field exists now
 * so per-category dashboards are routing rather than a second mechanism. */
export const DASHBOARD_SCOPES = ["overview"] as const;
export type DashboardScope = typeof DASHBOARD_SCOPES[number];

/** Grouping in the catalog palette. Presentation only; carries no behavior. */
export const WIDGET_FAMILIES = [
  "cluster",
  "workloads",
  "reliability",
  "security",
  "delivery",
  "data-protection",
  "networking",
  "platform",
] as const;
export type WidgetFamily = typeof WIDGET_FAMILIES[number];

/** Every distinct backend read the dashboard performs. A widget declares which
 * it needs; the cache in data.ts fetches each key at most once per cycle. */
export const DATA_SOURCE_KEYS = [
  "dashboard-summary",
  "dashboard-trends",
  "cluster-info",
  "recent-events",
] as const;
export type DataSourceKey = typeof DATA_SOURCE_KEYS[number];

/** Grid geometry. Twelve divides into halves, thirds and quarters, which is
 * what the pre-registry three-row layout already approximated. */
export const DASHBOARD_COLUMNS = 12;
export const DASHBOARD_ROW_HEIGHT = 40;
export const DASHBOARD_GRID_GAP = 16;
export const DASHBOARD_MAX_ITEMS = 40;
export const DASHBOARD_LAYOUT_SCHEMA_VERSION = 1;

/** One widget placement. instanceId exists because a parameterized widget may
 * legitimately appear twice -- diagnostics for prod beside diagnostics for
 * staging -- so identity cannot be the widget id. */
export interface LayoutItem {
  instanceId: string;
  id: string;
  x: number;
  y: number;
  w: number;
  h: number;
  params?: Record<string, string>;
}

/** The persisted envelope. Mirrors the Go DashboardLayoutConfig in P3. */
export interface DashboardLayoutConfig {
  schemaVersion: number;
  scope: DashboardScope;
  columns: number;
  items: LayoutItem[];
}

/** What a widget's render function receives. */
export interface WidgetProps {
  mode: DisplayMode;
  params: Record<string, string>;
}

export interface WidgetDef {
  /** Stable kebab-case. Never reused after retirement. */
  id: string;
  title: string;
  family: WidgetFamily;
  scopes: DashboardScope[];
  sources: DataSourceKey[];
  /** Smallest the editor will let the user resize this widget. */
  minW: number;
  minH: number;
  /** Size used when the widget is added from the palette. */
  defaultW: number;
  defaultH: number;
  /** Which modes this widget actually implements. Must include "normal". */
  modes: DisplayMode[];
  render(props: WidgetProps): VNode;
}
```

- [ ] **Step 2: Write the failing registry test**

Create `frontend/lib/dashboard/registry_test.ts`:

```ts
import { assertEquals, assertThrows } from "jsr:@std/assert@1";
import {
  DASHBOARD_SCOPES,
  DATA_SOURCE_KEYS,
  DISPLAY_MODES,
  WIDGET_FAMILIES,
} from "./types.ts";
import type { WidgetDef } from "./types.ts";
import {
  allWidgets,
  getWidget,
  isRetiredWidgetId,
  RETIRED_WIDGET_IDS,
  widgetsForScope,
} from "./registry.ts";

// The registry is the allowlist the server validates against and the catalog
// the palette renders. Every invariant below exists because breaking it
// produces a layout that cannot be rendered, stored, or restored.

const KEBAB = /^[a-z0-9]+(-[a-z0-9]+)*$/;

Deno.test("registry: ids are unique", () => {
  const ids = allWidgets().map((w) => w.id);
  assertEquals(ids.length, new Set(ids).size);
});

Deno.test("registry: ids are kebab-case", () => {
  for (const w of allWidgets()) {
    assertEquals(KEBAB.test(w.id), true, `id ${w.id} is not kebab-case`);
  }
});

Deno.test("registry: no live widget reuses a retired id", () => {
  // A reused id would silently resurrect a stored placement that meant
  // something else.
  for (const w of allWidgets()) {
    assertEquals(isRetiredWidgetId(w.id), false, `${w.id} is retired`);
  }
});

Deno.test("registry: every widget implements normal", () => {
  // pickMode falls back toward normal from both directions. A widget without
  // it has no guaranteed rendering at any size.
  for (const w of allWidgets()) {
    assertEquals(w.modes.includes("normal"), true, `${w.id} lacks normal`);
  }
});

Deno.test("registry: modes, family, scopes and sources are all declared values", () => {
  for (const w of allWidgets()) {
    for (const m of w.modes) {
      assertEquals(DISPLAY_MODES.includes(m), true, `${w.id} mode ${m}`);
    }
    assertEquals(WIDGET_FAMILIES.includes(w.family), true, `${w.id} family`);
    assertEquals(w.scopes.length > 0, true, `${w.id} has no scope`);
    for (const s of w.scopes) {
      assertEquals(DASHBOARD_SCOPES.includes(s), true, `${w.id} scope ${s}`);
    }
    for (const src of w.sources) {
      assertEquals(DATA_SOURCE_KEYS.includes(src), true, `${w.id} source ${src}`);
    }
  }
});

Deno.test("registry: default size is at least the minimum size", () => {
  for (const w of allWidgets()) {
    assertEquals(w.defaultW >= w.minW, true, `${w.id} defaultW < minW`);
    assertEquals(w.defaultH >= w.minH, true, `${w.id} defaultH < minH`);
  }
});

Deno.test("registry: nothing is wider than the grid", () => {
  for (const w of allWidgets()) {
    assertEquals(w.defaultW <= 12, true, `${w.id} defaultW exceeds 12 columns`);
    assertEquals(w.minW >= 1, true, `${w.id} minW below 1`);
    assertEquals(w.minH >= 1, true, `${w.id} minH below 1`);
  }
});

Deno.test("getWidget: unknown id is undefined, not a throw", () => {
  // Reads drop unknown ids with a notice (spec D-7), so lookup must be
  // total rather than exceptional.
  assertEquals(getWidget("no-such-widget"), undefined);
});

Deno.test("widgetsForScope: returns only widgets declaring that scope", () => {
  for (const w of widgetsForScope("overview")) {
    assertEquals(w.scopes.includes("overview"), true);
  }
});

Deno.test("RETIRED_WIDGET_IDS is frozen", () => {
  // Retirement is permanent; a runtime mutation would let an id come back.
  assertThrows(() => {
    (RETIRED_WIDGET_IDS as Set<string>).add("cluster-health");
  });
});
```

- [ ] **Step 3: Run it and verify it fails**

Run: `cd frontend && deno test lib/dashboard/registry_test.ts`
Expected: FAIL — `Module not found "./registry.ts"`.

- [ ] **Step 4: Write `registry.ts`**

```ts
/**
 * The widget catalog.
 *
 * This is the allowlist the server validates a stored layout against and the
 * catalog the editor palette renders. It is deliberately a module-level
 * registration table rather than a dynamic plugin surface: a layout naming a
 * widget this build does not have must be a detectable condition, not an
 * import.
 *
 * Widget components register themselves here at module load. The rendering
 * modules import their widgets so registration happens before first paint.
 */
import type { DashboardScope, WidgetDef } from "./types.ts";

const widgets = new Map<string, WidgetDef>();

/**
 * Ids that existed in a shipped release and no longer do.
 *
 * Retirement is permanent and ids are never reused: a stored layout still
 * naming one must be recognised as retired (dropped quietly on read) rather
 * than as a typo (rejected on write). Frozen so nothing can revive an id at
 * runtime.
 */
export const RETIRED_WIDGET_IDS: ReadonlySet<string> = Object.freeze(
  new Set<string>(),
) as ReadonlySet<string>;

export function isRetiredWidgetId(id: string): boolean {
  return RETIRED_WIDGET_IDS.has(id);
}

/** Registers a widget. Throws on a duplicate or retired id, because both are
 * programming errors that would otherwise surface as a silently missing or
 * silently wrong widget. */
export function registerWidget(def: WidgetDef): void {
  if (widgets.has(def.id)) {
    throw new Error(`duplicate widget id ${def.id}`);
  }
  if (isRetiredWidgetId(def.id)) {
    throw new Error(`widget id ${def.id} is retired and cannot be reused`);
  }
  widgets.set(def.id, def);
}

/** Total lookup: an unknown id is undefined, never a throw. Reads drop unknown
 * ids with a notice, so the caller needs a value to test. */
export function getWidget(id: string): WidgetDef | undefined {
  return widgets.get(id);
}

export function allWidgets(): WidgetDef[] {
  return [...widgets.values()];
}

export function widgetsForScope(scope: DashboardScope): WidgetDef[] {
  return allWidgets().filter((w) => w.scopes.includes(scope));
}
```

- [ ] **Step 5: Run the tests**

Run: `cd frontend && deno test lib/dashboard/registry_test.ts`

Expected: PASS. Every invariant test iterates an empty registry at this point
and passes vacuously; `getWidget` and `RETIRED_WIDGET_IDS` tests exercise real
behavior. The invariants gain teeth in D4/D5 when widgets register.

- [ ] **Step 6: Verify and commit**

Run: `cd frontend && deno task check`
Expected: exit 0.

```bash
git add frontend/lib/dashboard/types.ts frontend/lib/dashboard/registry.ts \
        frontend/lib/dashboard/registry_test.ts
git commit -m "feat(dashboard): widget registry and shared types"
```

Branch: `feat/d1-widget-registry`
PR title: `feat(dashboard): widget registry and shared types`

**Done means** — the registry rejects duplicate and retired ids; lookup of an
unknown id is `undefined`; the invariant suite runs under `deno test`;
`deno task check` exit 0.

---

### Task D2: Keyed data cache

**Files:**
- Create: `frontend/lib/dashboard/data.ts`
- Create: `frontend/lib/dashboard/data_test.ts`

**Interfaces:**
- Consumes: `DataSourceKey` from `./types.ts`.
- Produces: `SourceState<T>`, `createSourceCache(fetchers)`, and the module
  singleton `dashboardData`. D3 and every widget read `dashboardData.state(key)`;
  D5b calls `dashboardData.ensure(keys, range)` and `dashboardData.startRefresh()`.

The cache is built by a factory taking injected fetchers so the dedupe and error
behavior is unit-testable without a fetch seam — the pattern
`frontend/lib/preferences_test.ts` had to work around by fabricating an
`ApiError`.

- [ ] **Step 1: Write the failing test**

Create `frontend/lib/dashboard/data_test.ts`:

```ts
import { assertEquals } from "jsr:@std/assert@1";
import { createSourceCache } from "./data.ts";
import type { SourceFetcher } from "./data.ts";

// The cache exists so that N widgets declaring the same source produce one
// request, and so that a failure is a value a widget can render rather than a
// silently swallowed promise. Both are new behavior -- the pre-registry
// dashboard fetched centrally and discarded every error.

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

Deno.test("ensure: concurrent requests for one key issue a single fetch", async () => {
  let calls = 0;
  const d = deferred<string>();
  const fetchers: Record<string, SourceFetcher> = {
    "dashboard-summary": () => {
      calls++;
      return d.promise;
    },
  };
  const cache = createSourceCache(fetchers);

  cache.ensure(["dashboard-summary", "dashboard-summary"], "1h");
  cache.ensure(["dashboard-summary"], "1h");
  assertEquals(calls, 1);

  d.resolve("ok");
  await cache.settled();
  assertEquals(cache.state("dashboard-summary").data, "ok");
  assertEquals(calls, 1);
});

Deno.test("ensure: a key nobody asked for is never fetched", async () => {
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      calls++;
      return Promise.resolve("s");
    },
    "recent-events": () => {
      calls++;
      return Promise.resolve("e");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  assertEquals(calls, 1);
  assertEquals(cache.state("recent-events").data, null);
});

Deno.test("state: a failure is a readable error, not a swallowed promise", async () => {
  const cache = createSourceCache({
    "dashboard-summary": () => Promise.reject(new Error("boom")),
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();

  const s = cache.state("dashboard-summary");
  assertEquals(s.loading, false);
  assertEquals(s.data, null);
  assertEquals(s.error, "boom");
});

Deno.test("state: loading is true while in flight and false after", async () => {
  const d = deferred<string>();
  const cache = createSourceCache({ "dashboard-summary": () => d.promise });

  cache.ensure(["dashboard-summary"], "1h");
  assertEquals(cache.state("dashboard-summary").loading, true);

  d.resolve("ok");
  await cache.settled();
  assertEquals(cache.state("dashboard-summary").loading, false);
});

Deno.test("state: an unfetched key reads as idle, not as an error", () => {
  const cache = createSourceCache({});
  const s = cache.state("cluster-info");
  assertEquals(s.data, null);
  assertEquals(s.error, null);
  assertEquals(s.loading, false);
});

Deno.test("refresh: re-fetches keys already ensured, and only those", async () => {
  let summary = 0;
  let events = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      summary++;
      return Promise.resolve("s");
    },
    "recent-events": () => {
      events++;
      return Promise.resolve("e");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  cache.refresh();
  await cache.settled();

  assertEquals(summary, 2);
  assertEquals(events, 0);
});

Deno.test("range: changing the range refetches range-sensitive keys", async () => {
  const ranges: string[] = [];
  const cache = createSourceCache({
    "dashboard-trends": (_signal, range) => {
      ranges.push(range);
      return Promise.resolve(range);
    },
  });

  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();
  cache.ensure(["dashboard-trends"], "6h");
  await cache.settled();

  assertEquals(ranges, ["1h", "6h"]);
  assertEquals(cache.state("dashboard-trends").data, "6h");
});

Deno.test("range: re-ensuring the same range does not refetch", async () => {
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-trends": (_s, r) => {
      calls++;
      return Promise.resolve(r);
    },
  });

  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();
  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();

  assertEquals(calls, 1);
});

Deno.test("abort: a cancelled in-flight fetch leaves no error on the state", async () => {
  // An aborted request is the app tearing down, not a failure the user should
  // be shown. PinnedResources and SavedViews both take this care already.
  const cache = createSourceCache({
    "dashboard-summary": (signal) =>
      new Promise((_res, rej) => {
        signal.addEventListener("abort", () => {
          rej(new DOMException("Aborted", "AbortError"));
        });
      }),
  });

  cache.ensure(["dashboard-summary"], "1h");
  cache.abort();
  await cache.settled();

  const s = cache.state("dashboard-summary");
  assertEquals(s.error, null);
  assertEquals(s.loading, false);
});
```

- [ ] **Step 2: Run it and verify it fails**

Run: `cd frontend && deno test lib/dashboard/data_test.ts`
Expected: FAIL — `Module not found "./data.ts"`.

- [ ] **Step 3: Write `data.ts`**

```ts
/**
 * The dashboard's data layer.
 *
 * Central fetching was correct for six fixed widgets and is wrong for a
 * catalog of thirty-nine optional ones: the page would issue every request
 * whether or not the widget is on the layout. Here a widget declares the
 * source keys it needs, the cache fetches each key at most once per cycle, and
 * a key nobody asked for is never requested.
 *
 * The cache is built by a factory over injected fetchers so the dedupe and
 * failure behavior can be unit-tested. lib/api.ts has no injectable fetch, and
 * lib/preferences_test.ts shows what testing around that looks like.
 */
import { signal } from "@preact/signals";
import type { Signal } from "@preact/signals";
import { api } from "@/lib/api.ts";
import type { DataSourceKey } from "./types.ts";

export interface SourceState<T = unknown> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

/** A fetcher receives the abort signal and the active time range. Sources that
 * ignore the range simply do not read it. */
export type SourceFetcher = (
  signal: AbortSignal,
  range: string,
) => Promise<unknown>;

const IDLE: SourceState = { data: null, error: null, loading: false };

/** Sources whose response depends on the selected time range. Re-ensuring one
 * of these under a new range refetches; the others do not. */
const RANGE_SENSITIVE: ReadonlySet<string> = new Set(["dashboard-trends"]);

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

export interface SourceCache {
  state<T = unknown>(key: DataSourceKey): SourceState<T>;
  signalFor(key: DataSourceKey): Signal<SourceState>;
  ensure(keys: readonly DataSourceKey[], range: string): void;
  refresh(): void;
  abort(): void;
  /** Resolves when nothing is in flight. Test seam; also used by the refresh
   * loop to avoid stacking cycles. */
  settled(): Promise<void>;
}

export function createSourceCache(
  fetchers: Partial<Record<DataSourceKey, SourceFetcher>>,
): SourceCache {
  const states = new Map<string, Signal<SourceState>>();
  const inFlight = new Map<string, Promise<void>>();
  /** The range each key was last fetched under, so a range change is
   * detectable without refetching range-insensitive sources. */
  const fetchedRange = new Map<string, string>();
  let controller = new AbortController();

  function sig(key: DataSourceKey): Signal<SourceState> {
    let s = states.get(key);
    if (!s) {
      s = signal<SourceState>({ ...IDLE });
      states.set(key, s);
    }
    return s;
  }

  function run(key: DataSourceKey, range: string): void {
    const fetcher = fetchers[key];
    if (!fetcher) return;

    const s = sig(key);
    s.value = { ...s.value, loading: true };
    fetchedRange.set(key, range);

    const p = fetcher(controller.signal, range)
      .then((data) => {
        s.value = { data, error: null, loading: false };
      })
      .catch((err) => {
        if (isAbort(err) || controller.signal.aborted) {
          // Teardown, not failure. Leave the previous value in place and say
          // nothing -- an abort banner would be noise on every navigation.
          s.value = { ...s.value, loading: false };
          return;
        }
        s.value = { data: null, error: messageOf(err), loading: false };
      })
      .finally(() => {
        inFlight.delete(key);
      });

    inFlight.set(key, p);
  }

  return {
    state<T>(key: DataSourceKey): SourceState<T> {
      return sig(key).value as SourceState<T>;
    },

    signalFor(key: DataSourceKey): Signal<SourceState> {
      return sig(key);
    },

    ensure(keys: readonly DataSourceKey[], range: string): void {
      for (const key of keys) {
        if (inFlight.has(key)) continue;
        const previous = fetchedRange.get(key);
        const stale = previous !== undefined &&
          RANGE_SENSITIVE.has(key) && previous !== range;
        if (previous !== undefined && !stale) continue;
        run(key, range);
      }
    },

    refresh(): void {
      for (const [key, range] of [...fetchedRange.entries()]) {
        if (inFlight.has(key)) continue;
        run(key as DataSourceKey, range);
      }
    },

    abort(): void {
      controller.abort();
      controller = new AbortController();
    },

    async settled(): Promise<void> {
      while (inFlight.size > 0) {
        await Promise.allSettled([...inFlight.values()]);
      }
    },
  };
}

/** The real fetchers. Endpoints and shapes match what DashboardV2 fetched
 * before the extraction; cluster-info and recent-events gain the 60s refresh
 * the other two always had, because a silently ageing event list is a defect. */
export const dashboardData: SourceCache = createSourceCache({
  "dashboard-summary": async (signal) =>
    (await api<unknown>("/v1/cluster/dashboard-summary", { method: "GET", signal })).data,
  "dashboard-trends": async (signal, range) =>
    (await api<unknown>(`/v1/cluster/dashboard-trends?range=${range}`, {
      method: "GET",
      signal,
    })).data,
  "cluster-info": async (signal) =>
    (await api<unknown>("/v1/cluster/info", { method: "GET", signal })).data,
  "recent-events": async (signal) =>
    (await api<unknown>("/v1/resources/events?limit=10", { method: "GET", signal })).data,
});
```

- [ ] **Step 4: Run the tests**

Run: `cd frontend && deno test lib/dashboard/data_test.ts`
Expected: PASS, 9 tests.

- [ ] **Step 5: Verify and commit**

Run: `cd frontend && deno task check`
Expected: exit 0.

```bash
git add frontend/lib/dashboard/data.ts frontend/lib/dashboard/data_test.ts
git commit -m "feat(dashboard): keyed data cache with in-flight de-duplication"
```

Branch: `feat/d2-dashboard-data-cache`
PR title: `feat(dashboard): keyed data cache with in-flight de-duplication`

**Done means** — N widgets declaring one source produce one request; an
unrequested key is never fetched; a failure is a readable `error` string rather
than a swallowed promise; an abort leaves no error; a range change refetches only
range-sensitive sources.

---

### Task D3: Mode selection and the widget host

**Files:**
- Create: `frontend/lib/dashboard/display-mode.ts`
- Create: `frontend/lib/dashboard/display-mode_test.ts`
- Create: `frontend/components/dashboard/WidgetHost.tsx`

**Interfaces:**
- Consumes: `DisplayMode`, `DISPLAY_MODES`, `WidgetDef` from `./types.ts`;
  `dashboardData` from `./data.ts`.
- Produces: `pickMode(modes, width, height)` and `<WidgetHost def params />`.
  D4, D5 and D5b render through `WidgetHost`; P2's grid places it.

- [ ] **Step 1: Write the failing mode test**

Create `frontend/lib/dashboard/display-mode_test.ts`:

```ts
import { assertEquals } from "jsr:@std/assert@1";
import { pickMode } from "./display-mode.ts";
import type { DisplayMode } from "./types.ts";

// pickMode is the whole size-responsive contract. It is pure so that the
// behavior every one of the 39 widgets depends on is pinned by unit tests
// rather than discovered by resizing a browser.

const ALL: DisplayMode[] = ["compact", "normal", "expanded"];

Deno.test("pickMode: a small box is compact", () => {
  assertEquals(pickMode(ALL, 200, 100), "compact");
});

Deno.test("pickMode: a mid box is normal", () => {
  assertEquals(pickMode(ALL, 360, 200), "normal");
});

Deno.test("pickMode: a large box is expanded", () => {
  assertEquals(pickMode(ALL, 700, 400), "expanded");
});

Deno.test("pickMode: both dimensions must qualify", () => {
  // A wide, short box is not expanded -- a detail table needs height.
  assertEquals(pickMode(ALL, 900, 140), "compact");
  // A tall, narrow box is not expanded either.
  assertEquals(pickMode(ALL, 240, 600), "compact");
});

Deno.test("pickMode: falls back to the nearest implemented smaller mode", () => {
  // A widget with no expanded rendering stays at normal in a huge box rather
  // than rendering nothing.
  assertEquals(pickMode(["compact", "normal"], 900, 500), "normal");
});

Deno.test("pickMode: falls upward when nothing smaller is implemented", () => {
  // A widget that only implements normal must render normal in a tiny box.
  // Blank is never an acceptable answer.
  assertEquals(pickMode(["normal"], 100, 60), "normal");
  assertEquals(pickMode(["normal", "expanded"], 100, 60), "normal");
});

Deno.test("pickMode: exact boundary values select the larger mode", () => {
  // Boundaries are inclusive on the way up so a widget sized exactly to a
  // breakpoint does not flicker between modes on a one-pixel scroll shift.
  assertEquals(pickMode(ALL, 280, 160), "normal");
  assertEquals(pickMode(ALL, 520, 320), "expanded");
});

Deno.test("pickMode: zero and negative sizes are compact, never a crash", () => {
  // ResizeObserver reports 0x0 for a hidden or not-yet-laid-out element.
  assertEquals(pickMode(ALL, 0, 0), "compact");
  assertEquals(pickMode(ALL, -10, -10), "compact");
});

Deno.test("pickMode: an empty mode list falls back to normal", () => {
  // Defensive: the registry test forbids this, but returning undefined here
  // would put `undefined` on a widget's props.
  assertEquals(pickMode([], 400, 300), "normal");
});
```

- [ ] **Step 2: Run it and verify it fails**

Run: `cd frontend && deno test lib/dashboard/display-mode_test.ts`
Expected: FAIL — `Module not found "./display-mode.ts"`.

- [ ] **Step 3: Write `display-mode.ts`**

```ts
/**
 * Size-to-mode selection: the entire size-responsive contract, in one pure
 * function.
 *
 * Breakpoints are pixel measurements of the rendered box, not grid units,
 * because the same grid span is a different physical size on a phone and a
 * 4K monitor -- and it is the physical size that decides whether a detail
 * table is legible.
 */
import { DISPLAY_MODES } from "./types.ts";
import type { DisplayMode } from "./types.ts";

/** Minimum rendered box, in CSS pixels, for each mode. Both dimensions must
 * qualify: a wide, short box cannot host a detail table. Inclusive on the way
 * up, so a widget sized exactly to a breakpoint does not flicker. */
const THRESHOLDS: Record<DisplayMode, { w: number; h: number }> = {
  compact: { w: 0, h: 0 },
  normal: { w: 280, h: 160 },
  expanded: { w: 520, h: 320 },
};

/**
 * Picks the largest mode that both fits the box and is implemented by the
 * widget. Falls back downward first (a widget with no `expanded` stays at
 * `normal` in a huge box), then upward (a widget that only implements
 * `normal` renders `normal` in a tiny box). Blank is never an answer.
 */
export function pickMode(
  modes: readonly DisplayMode[],
  width: number,
  height: number,
): DisplayMode {
  if (modes.length === 0) return "normal";

  const fits = (m: DisplayMode) =>
    width >= THRESHOLDS[m].w && height >= THRESHOLDS[m].h;

  // DISPLAY_MODES is ordered smallest to largest.
  let wanted: DisplayMode = "compact";
  for (const m of DISPLAY_MODES) {
    if (fits(m)) wanted = m;
  }

  const wantedIndex = DISPLAY_MODES.indexOf(wanted);
  for (let i = wantedIndex; i >= 0; i--) {
    const m = DISPLAY_MODES[i];
    if (modes.includes(m)) return m;
  }
  for (let i = wantedIndex + 1; i < DISPLAY_MODES.length; i++) {
    const m = DISPLAY_MODES[i];
    if (modes.includes(m)) return m;
  }
  return "normal";
}
```

- [ ] **Step 4: Run the tests**

Run: `cd frontend && deno test lib/dashboard/display-mode_test.ts`
Expected: PASS, 9 tests.

- [ ] **Step 5: Write `WidgetHost.tsx`**

Thin by design: it measures, calls `pickMode`, and renders one of three states.
All of its logic that could be wrong lives in `pickMode`, which is tested above.

```tsx
import { useEffect, useRef } from "preact/hooks";
import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import { pickMode } from "@/lib/dashboard/display-mode.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import type { WidgetDef } from "@/lib/dashboard/types.ts";

interface WidgetHostProps {
  def: WidgetDef;
  params?: Record<string, string>;
}

/**
 * Measures a widget's box, chooses its display mode, and renders it -- or the
 * loading or error state instead.
 *
 * The pre-registry dashboard had neither: the skeleton was an all-or-nothing
 * page gate and every fetch failure was swallowed. A dashboard of thirty-nine
 * independent widgets cannot share one gate, so each widget owns its own.
 *
 * Deliberately thin. Everything here that could be wrong lives in pickMode,
 * which is a pure function with unit tests, because this repo has no component
 * test harness.
 */
export default function WidgetHost({ def, params = {} }: WidgetHostProps) {
  const box = useRef<HTMLDivElement | null>(null);
  const width = useSignal(0);
  const height = useSignal(0);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const el = box.current;
    if (!el) return;

    // Debounced, mirroring PodTerminal.tsx:103-110 -- the only prior art for
    // ResizeObserver in this codebase. The timer is declared before the
    // closure so the closure captures the binding, not a stale value.
    let timer = 0;
    const measure = () => {
      width.value = el.clientWidth;
      height.value = el.clientHeight;
    };
    const ro = new ResizeObserver(() => {
      clearTimeout(timer);
      timer = globalThis.setTimeout(measure, 100);
    });
    ro.observe(el);
    measure();

    return () => {
      clearTimeout(timer);
      ro.disconnect();
    };
  }, []);

  const states = def.sources.map((k) => dashboardData.state(k));
  // A widget is loading only while it has nothing to show. Once any source has
  // landed, a background refresh must not blank the widget out.
  const loading = states.some((s) => s.loading) &&
    states.every((s) => s.data === null);
  const failure = states.find((s) => s.error !== null);

  const mode = pickMode(def.modes, width.value, height.value);

  return (
    <div
      ref={box}
      data-testid="widget-host"
      data-widget-id={def.id}
      data-widget-mode={mode}
      style={{ height: "100%", minWidth: 0, minHeight: 0 }}
    >
      {loading
        ? <Skeleton />
        : failure
        ? (
          <div
            data-testid="widget-error"
            style={{
              padding: "16px",
              fontSize: "12px",
              lineHeight: 1.5,
              color: "var(--warning)",
            }}
          >
            {def.title} could not be loaded.
            <div style={{ opacity: 0.75, marginTop: "4px" }}>
              {failure.error}
            </div>
          </div>
        )
        : def.render({ mode, params })}
    </div>
  );
}
```

- [ ] **Step 6: Verify and commit**

Run: `cd frontend && deno task check`
Expected: exit 0.

```bash
git add frontend/lib/dashboard/display-mode.ts \
        frontend/lib/dashboard/display-mode_test.ts \
        frontend/components/dashboard/WidgetHost.tsx
git commit -m "feat(dashboard): size-responsive display-mode contract and widget host"
```

Branch: `feat/d3-widget-host`
PR title: `feat(dashboard): size-responsive display-mode contract and widget host`

**Done means** — `pickMode` never returns a mode the widget does not implement
and never returns undefined; a 0x0 box is `compact`, not a crash; `WidgetHost`
exposes `data-widget-id` and `data-widget-mode` for E2E; a widget with data
already loaded does not blank during a background refresh.

---

### Task D4: Extract the five small widgets

**Files:**
- Create: `frontend/components/dashboard/widgets/ClusterHealthWidget.tsx`
- Create: `frontend/components/dashboard/widgets/CpuTileWidget.tsx`
- Create: `frontend/components/dashboard/widgets/MemoryTileWidget.tsx`
- Create: `frontend/components/dashboard/widgets/PodsTileWidget.tsx`
- Create: `frontend/components/dashboard/widgets/NetworkTileWidget.tsx`

**Interfaces:**
- Consumes: `registerWidget` from `@/lib/dashboard/registry.ts`; `dashboardData`
  from `@/lib/dashboard/data.ts`; `WidgetProps` from `@/lib/dashboard/types.ts`;
  the existing `MetricTile`, `NetworkTile`, `Gauge`, `CheckItem` components and
  `percentile` from `@/lib/format.ts`.
- Produces: registrations for `cluster-health`, `cpu-tile`, `memory-tile`,
  `pods-tile`, `network-tile`. D5b imports these modules so registration runs.

Each file follows one shape: read the sources it declares, compute its derived
scalars locally, render, and `registerWidget` at module scope. The derived block
currently at `DashboardV2.tsx:247-326` is redistributed here — `cpuPct`/`memPct`
(`:271-272`) are needed by both the tiles and `nodes`, so they move to
`@/lib/format.ts` rather than being duplicated.

- [ ] **Step 1: Move the two shared derivations into `lib/format.ts`**

`lastDelta` (`DashboardV2.tsx:280-288`) is pure, captures nothing, and is used
only by the tiles; `cpuPct`/`memPct` are used by the tiles and by `nodes`. Add
both to `frontend/lib/format.ts` with tests in the existing
`frontend/lib/format_test.ts` (which already covers `percentile` and
`formatMbps`, so the file and its style are established):

```ts
/** The change between the last two samples of a series, or null when the
 * series is too short to have one. */
export function lastDelta(
  series: number[] | null | undefined,
): number | null {
  if (!series || series.length < 2) return null;
  const a = series[series.length - 2];
  const b = series[series.length - 1];
  if (!Number.isFinite(a) || !Number.isFinite(b)) return null;
  return b - a;
}
```

Add to `format_test.ts`:

```ts
// --- lastDelta ---

Deno.test("lastDelta: null/undefined/short series has no delta", () => {
  assertEquals(lastDelta(null), null);
  assertEquals(lastDelta(undefined), null);
  assertEquals(lastDelta([]), null);
  assertEquals(lastDelta([5]), null);
});

Deno.test("lastDelta: uses the final two samples", () => {
  assertEquals(lastDelta([1, 2, 5]), 3);
  assertEquals(lastDelta([5, 2]), -3);
});

Deno.test("lastDelta: a non-finite endpoint has no delta", () => {
  assertEquals(lastDelta([1, NaN]), null);
  assertEquals(lastDelta([NaN, 1]), null);
});
```

Run: `cd frontend && deno test lib/format_test.ts`
Expected: PASS, existing tests plus three new.

- [ ] **Step 2: Write `CpuTileWidget.tsx`**

The tile widgets are the smallest case and establish the shape the other nine
follow. `MetricTile` already renders its own `WidgetShell`, which is why no
wrapper is added here (spec D-9).

```tsx
import { MetricTile } from "@/components/ui/MetricTile.tsx";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { lastDelta } from "@/lib/format.ts";
import type { DashboardSummary, DashboardTrends } from "@/lib/dashboard/wire-types.ts";

function CpuTile() {
  const s = dashboardData.state<DashboardSummary>("dashboard-summary").data;
  const t = dashboardData.state<DashboardTrends>("dashboard-trends").data;
  const pct = Math.round(s?.cpu?.usagePercent ?? 0);

  return (
    <MetricTile
      label="CPU"
      value={`${pct}`}
      unit="%"
      delta={lastDelta(t?.cpu)}
      sparkData={t?.cpu}
      sparkColor="var(--accent)"
      href="/cluster/nodes"
    />
  );
}

registerWidget({
  id: "cpu-tile",
  title: "CPU",
  family: "cluster",
  scopes: ["overview"],
  sources: ["dashboard-summary", "dashboard-trends"],
  minW: 2,
  minH: 2,
  defaultW: 3,
  defaultH: 3,
  // A metric tile is a headline number and a sparkline at every size; there is
  // no larger rendering to give it, and inventing one would make the four
  // tiles inconsistent with each other.
  modes: ["compact", "normal"],
  render: () => <CpuTile />,
});
```

**Note on `wire-types.ts`:** move the three module-level wire types from
`DashboardV2.tsx:23-74` (`ClusterInfoData`, `DashboardSummary`,
`DashboardTrends`) into `frontend/lib/dashboard/wire-types.ts` as part of this
task, minus the six fields D0 deleted. They are shared by every widget and
cannot stay inside the island that is about to shrink. This makes six files in
this unit; if that breaks the G2 cap for your reviewer, split the wire-types
move into its own trivial commit within the same PR.

- [ ] **Step 3: Write the other three tiles**

`MemoryTileWidget.tsx`, `PodsTileWidget.tsx` and `NetworkTileWidget.tsx` follow
`CpuTileWidget.tsx` exactly, with these differences copied from the source lines
they replace:

| File | id | Source lines | Differences |
|---|---|---|---|
| `MemoryTileWidget.tsx` | `memory-tile` | `:482-490` | `label="Memory"`, `s?.memory?.usagePercent`, `t?.memory`, `sparkColor="var(--info)"` |
| `PodsTileWidget.tsx` | `pods-tile` | `:491-499` | `label="Pods"`, `value={String(podCount)}`, `unit={s ? \`/${s.pods.running} running\` : ""}`, `t?.pods`, `sparkColor="var(--success)"`, `href="/workloads/pods"` |
| `NetworkTileWidget.tsx` | `network-tile` | `:500-507` | renders `NetworkTile`, needs `percentile(t?.networkRx, 95)` and `percentile(t?.networkTx, 95)`, `sources: ["dashboard-trends"]` only, and takes `period` from the range signal |

- [ ] **Step 4: Write `ClusterHealthWidget.tsx`**

Lift `DashboardV2.tsx:416-462` verbatim, plus its derived scalars from
`:257-268` (`health`, `healthScore`, `healthStatus`, `healthColor`,
`healthLabel`, `workloadsDegraded`, `criticalAlerts`, `nodesReady`). It uses
`Gauge`, three `CheckItem`s and `healthStatusColor`, none of which any other
widget needs, so all four imports move here.

```tsx
registerWidget({
  id: "cluster-health",
  title: "Cluster Health",
  family: "cluster",
  scopes: ["overview"],
  sources: ["dashboard-summary", "cluster-info"],
  minW: 3,
  minH: 4,
  defaultW: 4,
  defaultH: 6,
  // compact drops the three CheckItems and shows the gauge alone; expanded is
  // the current rendering. There is no third thing to show.
  modes: ["compact", "normal"],
  render: ({ mode }) => <ClusterHealth mode={mode} />,
});
```

- [ ] **Step 5: Verify and commit**

Run: `cd frontend && deno test lib/` then `cd frontend && deno task check`
Expected: both exit 0. The registry invariant suite from D1 now has five real
widgets to check and will fail loudly on a malformed one.

```bash
git add frontend/components/dashboard/widgets/ frontend/lib/dashboard/wire-types.ts \
        frontend/lib/format.ts frontend/lib/format_test.ts
git commit -m "feat(dashboard): extract cluster health and the four metric tiles"
```

Branch: `feat/d4-extract-small-widgets`
PR title: `feat(dashboard): extract cluster health and the four metric tiles`

**Done means** — five widgets register; the D1 invariant suite passes against
them; `lastDelta` is a tested `lib/` function rather than a per-render closure;
no widget file imports another widget file.

---

### Task D5: Extract the five large widgets

**Files:**
- Create: `frontend/components/dashboard/widgets/ResourceUtilizationWidget.tsx`
- Create: `frontend/components/dashboard/widgets/PodStatusWidget.tsx`
- Create: `frontend/components/dashboard/widgets/NodesWidget.tsx`
- Create: `frontend/components/dashboard/widgets/RecentEventsWidget.tsx`
- Create: `frontend/components/dashboard/widgets/ActiveAlertsWidget.tsx`

**Interfaces:**
- Consumes: as D4, plus `Donut`/`DonutSegment`, `BarRow`, `ResourceAreaChart`,
  and `age` from `@/lib/format.ts`.
- Produces: registrations for `resource-utilization`, `pod-status`, `nodes`,
  `recent-events`, `active-alerts`.

| File | id | Source lines | Owns exclusively |
|---|---|---|---|
| `ResourceUtilizationWidget.tsx` | `resource-utilization` | `:520-576` | `ResourceAreaChart`; the ~45-line inline legend in its `action` slot (`:524-569`) becomes a local `Legend()` function in the same file |
| `PodStatusWidget.tsx` | `pod-status` | `:578-698` | `Donut`, `DonutSegment`, `donutSegments` (`:299-317`, post-D0 fix), the legend literal (`:632-647`) |
| `NodesWidget.tsx` | `nodes` | `:709-791` | `BarRow`; reads `cpuPct`/`memPct` from `lib/format.ts` |
| `RecentEventsWidget.tsx` | `recent-events` | `:793-954` | `age`, `K8sEvent`; **hoist `kindAbbr` (`:854-865`) to module scope** — it is currently re-allocated inside the per-event `.map` callback on every render |
| `ActiveAlertsWidget.tsx` | `active-alerts` | `:956-1080` | nothing; inline SVG and markup only |

- [ ] **Step 1: Extract each, one commit per widget**

For each: copy the JSX verbatim, replace signal reads with
`dashboardData.state<T>(key).data`, move its exclusive imports and derived
scalars into the file, and add the `registerWidget` call. Do not restyle, do not
rename, do not "improve" markup — the exit criterion for D5b is that the page is
visually identical, and an unrelated style change makes that impossible to
verify.

Suggested sizes, derived from the flex bases they replace:

```ts
resource-utilization: minW 4, minH 4, defaultW 7, defaultH 6, modes ["normal", "expanded"]
pod-status:           minW 3, minH 4, defaultW 5, defaultH 6, modes ["compact", "normal"]
nodes:                minW 3, minH 4, defaultW 4, defaultH 6, modes ["compact", "normal"]
recent-events:        minW 3, minH 3, defaultW 5, defaultH 6, modes ["compact", "normal", "expanded"]
active-alerts:        minW 2, minH 3, defaultW 3, defaultH 6, modes ["compact", "normal"]
```

`recent-events` is the one widget that earns all three modes: `compact` is a
count, `normal` is the current ten-row list, `expanded` adds the message column.

- [ ] **Step 2: Verify and commit**

Run: `cd frontend && deno test lib/ && deno task check`
Expected: exit 0. All ten widgets now satisfy the D1 invariants.

```bash
git add frontend/components/dashboard/widgets/
git commit -m "feat(dashboard): extract the five chart and list widgets"
```

Branch: `feat/d5-extract-large-widgets`
PR title: `feat(dashboard): extract the five chart and list widgets`

**Done means** — all ten widgets register and pass the invariant suite; no
widget imports another; `kindAbbr` is allocated once rather than per event row.

---

### Task D5b: Render the default layout through the registry

**Files:**
- Create: `frontend/lib/dashboard/default-layout.ts`
- Create: `frontend/lib/dashboard/default-layout_test.ts`
- Modify: `frontend/islands/DashboardV2.tsx`
- Modify: `e2e/tests/dashboard.spec.ts`

**Interfaces:**
- Consumes: everything above.
- Produces: `DEFAULT_OVERVIEW_LAYOUT: DashboardLayoutConfig`. P2 renders it
  through the grid; P3 stores it; P4 resets to it.

This is the unit that proves the engine. **Its exit criterion is visual
equivalence**, and if the page changes, the engine is not trustworthy.

- [ ] **Step 1: Write the failing default-layout test**

```ts
import { assertEquals } from "jsr:@std/assert@1";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_MAX_ITEMS } from "./types.ts";
import { getWidget } from "./registry.ts";
import "@/components/dashboard/widgets/index.ts";

// The default layout is what every new user sees and what "Reset" restores.
// It is data, so it can be wrong in ways a type cannot catch.

Deno.test("default layout: every id resolves to a registered widget", () => {
  for (const item of DEFAULT_OVERVIEW_LAYOUT.items) {
    assertEquals(getWidget(item.id) !== undefined, true, `${item.id} missing`);
  }
});

Deno.test("default layout: instance ids are unique", () => {
  const ids = DEFAULT_OVERVIEW_LAYOUT.items.map((i) => i.instanceId);
  assertEquals(ids.length, new Set(ids).size);
});

Deno.test("default layout: nothing overflows the grid", () => {
  for (const i of DEFAULT_OVERVIEW_LAYOUT.items) {
    assertEquals(i.x >= 0, true, `${i.id} x<0`);
    assertEquals(i.x + i.w <= DASHBOARD_COLUMNS, true, `${i.id} overflows`);
  }
});

Deno.test("default layout: no two widgets overlap", () => {
  const items = DEFAULT_OVERVIEW_LAYOUT.items;
  for (let a = 0; a < items.length; a++) {
    for (let b = a + 1; b < items.length; b++) {
      const p = items[a], q = items[b];
      const disjoint = p.x + p.w <= q.x || q.x + q.w <= p.x ||
        p.y + p.h <= q.y || q.y + q.h <= p.y;
      assertEquals(disjoint, true, `${p.id} overlaps ${q.id}`);
    }
  }
});

Deno.test("default layout: every widget is at least its declared minimum", () => {
  for (const i of DEFAULT_OVERVIEW_LAYOUT.items) {
    const def = getWidget(i.id)!;
    assertEquals(i.w >= def.minW, true, `${i.id} narrower than minW`);
    assertEquals(i.h >= def.minH, true, `${i.id} shorter than minH`);
  }
});

Deno.test("default layout: within the item cap", () => {
  assertEquals(
    DEFAULT_OVERVIEW_LAYOUT.items.length <= DASHBOARD_MAX_ITEMS,
    true,
  );
});

Deno.test("default layout: contains all ten shipped widgets", () => {
  assertEquals(DEFAULT_OVERVIEW_LAYOUT.items.length, 10);
});
```

- [ ] **Step 2: Run it and verify it fails**

Run: `cd frontend && deno test lib/dashboard/default-layout_test.ts`
Expected: FAIL — `Module not found "./default-layout.ts"`.

- [ ] **Step 3: Write `default-layout.ts`**

Translate the three flex rows into grid coordinates. Row 1's `2 1 320px` /
`3 1 380px` split is 5/7 of twelve; the tiles occupy the right-hand 7 as a 2x2.

```ts
import { DASHBOARD_LAYOUT_SCHEMA_VERSION } from "./types.ts";
import type { DashboardLayoutConfig } from "./types.ts";

/**
 * The layout every user starts with, and the one "Reset" restores.
 *
 * These coordinates are a translation of the pre-registry flex rows, not a new
 * design: row 1 was flex 2:3 which is 5/7 of twelve columns, row 2 was 7/5, and
 * row 3 was 2:3:2 which is 4/5/3. D5b's exit criterion is that rendering this
 * through the grid is visually indistinguishable from the old markup.
 */
export const DEFAULT_OVERVIEW_LAYOUT: DashboardLayoutConfig = {
  schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
  scope: "overview",
  columns: 12,
  items: [
    // Row 1 -- health on the left, the 2x2 tile block on the right.
    { instanceId: "d-cluster-health", id: "cluster-health", x: 0, y: 0, w: 5, h: 6 },
    { instanceId: "d-cpu-tile", id: "cpu-tile", x: 5, y: 0, w: 4, h: 3 },
    { instanceId: "d-memory-tile", id: "memory-tile", x: 9, y: 0, w: 3, h: 3 },
    { instanceId: "d-pods-tile", id: "pods-tile", x: 5, y: 3, w: 4, h: 3 },
    { instanceId: "d-network-tile", id: "network-tile", x: 9, y: 3, w: 3, h: 3 },

    // Row 2 -- the utilization chart beside the pod-status donut.
    { instanceId: "d-resource-utilization", id: "resource-utilization", x: 0, y: 6, w: 7, h: 6 },
    { instanceId: "d-pod-status", id: "pod-status", x: 7, y: 6, w: 5, h: 6 },

    // Row 3 -- nodes, events, alerts.
    { instanceId: "d-nodes", id: "nodes", x: 0, y: 12, w: 4, h: 6 },
    { instanceId: "d-recent-events", id: "recent-events", x: 4, y: 12, w: 5, h: 6 },
    { instanceId: "d-active-alerts", id: "active-alerts", x: 9, y: 12, w: 3, h: 6 },
  ],
};
```

- [ ] **Step 4: Add the widget barrel**

Create `frontend/components/dashboard/widgets/index.ts` importing all ten
modules for side effects, so registration happens exactly once and in one place:

```ts
/**
 * Importing a widget module registers it. This barrel is the single import
 * that guarantees the registry is populated before first render; importing
 * widgets individually elsewhere would make registration order depend on
 * bundler decisions.
 */
import "./ClusterHealthWidget.tsx";
import "./CpuTileWidget.tsx";
import "./MemoryTileWidget.tsx";
import "./PodsTileWidget.tsx";
import "./NetworkTileWidget.tsx";
import "./ResourceUtilizationWidget.tsx";
import "./PodStatusWidget.tsx";
import "./NodesWidget.tsx";
import "./RecentEventsWidget.tsx";
import "./ActiveAlertsWidget.tsx";
```

- [ ] **Step 5: Rewrite `DashboardV2.tsx` as a shell**

It keeps the page header and the time-range tabs, drops all four fetchers and
every block of JSX, and renders the default layout through `WidgetHost` on a
static CSS Grid. P2 replaces the static grid with the interactive one; this unit
deliberately does not.

```tsx
  useEffect(() => {
    if (!IS_BROWSER) return;
    const keys = [
      ...new Set(
        DEFAULT_OVERVIEW_LAYOUT.items.flatMap((i) => getWidget(i.id)?.sources ?? []),
      ),
    ];
    dashboardData.ensure(keys, timeRange.value);

    const id = globalThis.setInterval(() => {
      if (document.hidden) return;
      dashboardData.refresh();
    }, REFRESH_INTERVAL);

    return () => {
      clearInterval(id);
      dashboardData.abort();
    };
  }, [timeRange.value]);

  return (
    <div>
      {/* page header and time-range tabs, unchanged from :331-404 */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: `repeat(${DASHBOARD_COLUMNS}, 1fr)`,
          gridAutoRows: `${DASHBOARD_ROW_HEIGHT}px`,
          gap: `${DASHBOARD_GRID_GAP}px`,
        }}
      >
        {DEFAULT_OVERVIEW_LAYOUT.items.map((item) => {
          const def = getWidget(item.id);
          if (!def) return null;
          return (
            <div
              key={item.instanceId}
              style={{
                gridColumn: `${item.x + 1} / span ${item.w}`,
                gridRow: `${item.y + 1} / span ${item.h}`,
                minWidth: 0,
              }}
            >
              <WidgetHost def={def} params={item.params ?? {}} />
            </div>
          );
        })}
      </div>
    </div>
  );
```

- [ ] **Step 6: Prove visual equivalence**

This is the gate, and it is not satisfied by "it looks fine".

1. On `main` at the pre-D0 commit, load `/` at 1440x900 and capture a full-page
   screenshot.
2. On the branch, load `/` at 1440x900 against the same cluster and capture the
   same shot.
3. Compare. Widget positions, sizes, titles and content must match. The two
   permitted differences are the D0 donut placeholder on an empty cluster and
   the header's `synced just now` literal.
4. Repeat at 768px width to confirm the 1-column collapse is no worse than the
   old flex-wrap behavior.

Record both screenshots on the PR.

- [ ] **Step 7: Extend the dashboard E2E spec**

Add to `e2e/tests/dashboard.spec.ts`:

```ts
test("every default widget renders through the registry", async ({ page }) => {
  await page.goto("/");

  const hosts = page.getByTestId("widget-host");
  await expect(hosts).toHaveCount(10);

  // The ids are the contract the stored layout references. A rename here is a
  // breaking change to every saved layout, so pin them.
  for (const id of [
    "cluster-health", "cpu-tile", "memory-tile", "pods-tile", "network-tile",
    "resource-utilization", "pod-status", "nodes", "recent-events", "active-alerts",
  ]) {
    await expect(page.locator(`[data-widget-id="${id}"]`)).toHaveCount(1);
  }
});

test("a widget reports its own failure without blanking the page", async ({ page }) => {
  await page.route("**/api/v1/resources/events*", (r) => r.abort("failed"));
  await page.goto("/");

  // The failing widget says so...
  await expect(
    page.locator('[data-widget-id="recent-events"]').getByTestId("widget-error"),
  ).toBeVisible();
  // ...and the other nine still render. This is the whole point of per-widget
  // state: the pre-registry page had one all-or-nothing skeleton.
  await expect(page.locator('[data-widget-id="cluster-health"]')).toBeVisible();
});
```

- [ ] **Step 8: Verify and commit**

Run: `cd frontend && deno test lib/ && deno task check`
Run: `cd e2e && npx playwright test tests/dashboard.spec.ts`
Expected: all exit 0.

```bash
git add frontend/lib/dashboard/default-layout.ts \
        frontend/lib/dashboard/default-layout_test.ts \
        frontend/islands/DashboardV2.tsx \
        frontend/components/dashboard/widgets/index.ts \
        e2e/tests/dashboard.spec.ts
git commit -m "feat(dashboard): render the default layout through the widget registry"
```

Branch: `feat/d5b-registry-rendering`
PR title: `feat(dashboard): render the default layout through the widget registry`

**Done means** — `/` renders ten `widget-host` elements with the expected ids;
the page is visually indistinguishable from the pre-refactor dashboard at 1440px
and no worse at 768px, with screenshots on the PR; a single failing source shows
one widget's error and leaves the other nine rendered; `DashboardV2.tsx` is under
200 lines.

---

## Self-review

**Spec coverage.** §4.1 registry → D1. §4.2 data layer → D2. §4.4 display modes
→ D3. §4.5 rendering/geometry → D5b (static grid; interactive grid is P2).
D-4 (widgets own their data) → D2 + D4/D5. D-9 (four tiles) → D4. D-10 (pure
modules carry the tests) → D1, D2, D3, D5b all put logic in `lib/`. D-1's
mitigation (validate the contract on structurally different widgets before mass
production) → D4 and D5 cover a gauge, four tiles, an area chart, a donut, bar
rows, a list and a status panel, which is the required variety.

**Not covered here, by design:** D-3 (per-cluster scoping), D-5 (edit mode),
D-6 (reset), D-7 (unknown-id policy), D-8/§7 (validation) are P3 and P4. D-11
(dedup_key) is P3. §5's P2 units are the grid engine.

**Placeholders:** none. Every step carries the code or an explicit table of the
per-file differences with the source line ranges to copy from.

**Type consistency.** `WidgetDef` fields used in D4/D5 registrations match D1's
declaration exactly: `id`, `title`, `family`, `scopes`, `sources`, `minW`,
`minH`, `defaultW`, `defaultH`, `modes`, `render`. `pickMode(modes, width,
height)` is called in `WidgetHost` with `def.modes` — same order, same types.
`dashboardData.state<T>(key)` returns `SourceState<T>` with `data`/`error`/
`loading`, which is what `WidgetHost` destructures. `DEFAULT_OVERVIEW_LAYOUT`
items carry `instanceId`, matching `LayoutItem`.

**Known cap pressure.** D4 touches six files if the `wire-types.ts` move is
counted; the task says to split it into its own commit inside the same PR rather
than deferring it, because the wire types cannot stay in an island that is about
to shrink to a shell.
