---
title: "Release G / P2 — Snapping grid engine"
parent_plan: docs/plans/2026-09-10-EXECUTION-ORDER.md
spec: docs/plans/2026-09-13-dashboard-builder-design.md
date: 2026-09-13
baseline_revision: 78d9881e
status: ready
---

# Release G / P2 Implementation Plan — Snapping grid engine

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan
> task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user drag and resize dashboard widgets on a snapping twelve-column
grid, with the layout living in memory only — no persistence yet.

**Architecture:** Every decision about where a widget ends up is a pure function
over the layout array in `frontend/lib/dashboard/grid.ts`, unit-tested without a
DOM. The island is a thin pointer-event translator that converts a cursor
position into a call to one of those functions.

**Tech Stack:** Deno 2.x, Fresh 2.x (Preact), `@preact/signals`, Pointer Events,
CSS Grid, Playwright.

**Spec:** `docs/plans/2026-09-13-dashboard-builder-design.md` — §4.5 and decisions
D-2, D-10.

## Global Constraints

- One PR per unit, **at most 5 touched files including tests** (G2).
- **All geometry is pure and lives in `lib/`** (D-10). Collision resolution and
  compaction are where a hand-rolled grid actually fails, and this repo has no
  component test harness, so anything not in `lib/` is untested by construction.
- No new dependency (D-2). Pointer Events and CSS Grid only.
- `globalThis.setTimeout` / `globalThis.setInterval`, never the bare globals.
- Test imports: `import { assertEquals } from "jsr:@std/assert@1";` inline,
  module under test by relative path.
- Repo-wide verification: `cd frontend && deno task check`.
- **Persistence is out of scope.** The layout resets on reload throughout P2.
  That is deliberate: interaction design is the hard part and proving it without
  a stored contract to migrate is much cheaper than proving both at once.

---

## Model

A layout is an array of `LayoutItem` (`frontend/lib/dashboard/types.ts`, from P1):
`{ instanceId, id, x, y, w, h, params? }`. Coordinates are grid cells, origin
top-left, `x` in `[0, 12)`, `y` unbounded below.

Three rules the whole engine obeys, chosen once here so the units do not each
invent one:

1. **Vertical gravity.** After any change, every item falls as far up as it can
   without overlapping. This is what makes a layout deterministic: the same set
   of items always produces the same picture, so a stored layout renders
   identically on every client.
2. **Displacement is downward only.** Dropping A onto B pushes B down, never
   sideways. Sideways displacement cascades unpredictably across a twelve-column
   row and is the single largest source of "the grid ate my layout" complaints in
   every library that tries it.
3. **The dragged item wins.** During a drag the moved item is placed where the
   user put it and everything else yields. Anything else fights the pointer.

---

## File Structure

| File | Responsibility |
|---|---|
| `frontend/lib/dashboard/grid.ts` | All geometry. Pure, no DOM. |
| `frontend/lib/dashboard/grid_test.ts` | Its tests. |
| `frontend/islands/DashboardGrid.tsx` | Renders a layout; owns the pointer sessions. |
| `frontend/components/dashboard/GridItem.tsx` | One positioned cell plus its drag/resize handles. |
| `e2e/tests/dashboard-grid.spec.ts` | Interaction coverage. |

---

### Task D6: Pure grid geometry

**Files:**
- Create: `frontend/lib/dashboard/grid.ts`
- Create: `frontend/lib/dashboard/grid_test.ts`

**Interfaces:**
- Consumes: `LayoutItem`, `DASHBOARD_COLUMNS` from `./types.ts`.
- Produces: `overlaps`, `compact`, `moveItem`, `resizeItem`, `cellFromPoint`,
  `layoutHeight`, `Bounds`, `GridMetrics`. D7 calls `layoutHeight` and
  `cellFromPoint`; D8 calls `moveItem`; D9 calls `resizeItem`; D10 calls
  `moveItem` and `resizeItem` with keyboard deltas.

- [ ] **Step 1: Write the failing test**

Create `frontend/lib/dashboard/grid_test.ts`:

```ts
import { assertEquals } from "jsr:@std/assert@1";
import {
  cellFromPoint,
  compact,
  layoutHeight,
  moveItem,
  overlaps,
  resizeItem,
} from "./grid.ts";
import type { LayoutItem } from "./types.ts";

// Collision resolution and compaction are where a hand-rolled grid fails.
// These tests are the reason the geometry is pure: none of this is reachable
// from a component test, and the repo has no harness for one anyway.

function item(
  instanceId: string,
  x: number,
  y: number,
  w: number,
  h: number,
): LayoutItem {
  return { instanceId, id: "w-" + instanceId, x, y, w, h };
}

/** Layout as "instanceId@x,y" sorted, for readable assertions. */
function shape(items: LayoutItem[]): string[] {
  return items
    .map((i) => `${i.instanceId}@${i.x},${i.y}`)
    .sort();
}

// --- overlaps ---

Deno.test("overlaps: touching edges do not overlap", () => {
  assertEquals(overlaps(item("a", 0, 0, 3, 3), item("b", 3, 0, 3, 3)), false);
  assertEquals(overlaps(item("a", 0, 0, 3, 3), item("b", 0, 3, 3, 3)), false);
});

Deno.test("overlaps: a shared cell overlaps", () => {
  assertEquals(overlaps(item("a", 0, 0, 3, 3), item("b", 2, 2, 3, 3)), true);
});

Deno.test("overlaps: containment overlaps", () => {
  assertEquals(overlaps(item("a", 0, 0, 6, 6), item("b", 1, 1, 2, 2)), true);
});

// --- compact ---

Deno.test("compact: a floating item falls to the top", () => {
  assertEquals(shape(compact([item("a", 0, 5, 3, 2)])), ["a@0,0"]);
});

Deno.test("compact: an item falls only as far as the item above it", () => {
  const out = compact([item("a", 0, 0, 3, 2), item("b", 0, 9, 3, 2)]);
  assertEquals(shape(out), ["a@0,0", "b@0,2"]);
});

Deno.test("compact: independent columns fall independently", () => {
  const out = compact([item("a", 0, 4, 3, 2), item("b", 6, 9, 3, 2)]);
  assertEquals(shape(out), ["a@0,0", "b@6,0"]);
});

Deno.test("compact: a wide item is blocked by anything it spans", () => {
  // c spans both columns, so it cannot pass either of the two above it.
  const out = compact([
    item("a", 0, 0, 4, 2),
    item("b", 8, 0, 4, 3),
    item("c", 0, 20, 12, 2),
  ]);
  assertEquals(shape(out), ["a@0,0", "b@8,0", "c@0,3"]);
});

Deno.test("compact: is idempotent", () => {
  const once = compact([item("a", 0, 7, 3, 2), item("b", 0, 2, 3, 2)]);
  assertEquals(shape(compact(once)), shape(once));
});

Deno.test("compact: does not change x or size", () => {
  const out = compact([item("a", 4, 9, 3, 2)]);
  assertEquals(out[0].x, 4);
  assertEquals(out[0].w, 3);
  assertEquals(out[0].h, 2);
});

Deno.test("compact: never mutates its input", () => {
  const input = [item("a", 0, 5, 3, 2)];
  compact(input);
  assertEquals(input[0].y, 5);
});

// --- moveItem ---

Deno.test("moveItem: an unobstructed move lands exactly where asked", () => {
  const out = moveItem([item("a", 0, 0, 3, 2), item("b", 6, 0, 3, 2)], "a", 3, 0);
  assertEquals(shape(out), ["a@3,0", "b@6,0"]);
});

Deno.test("moveItem: the dragged item wins and the other is pushed down", () => {
  const out = moveItem([item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 2)], "b", 0, 0);
  // b took the top; a yielded downward, then gravity pulled it to b's bottom.
  assertEquals(shape(out), ["a@0,2", "b@0,0"]);
});

Deno.test("moveItem: displacement is downward, never sideways", () => {
  const out = moveItem([item("a", 0, 0, 4, 2), item("b", 4, 0, 4, 2)], "a", 2, 0);
  const b = out.find((i) => i.instanceId === "b")!;
  assertEquals(b.x, 4, "b must not move sideways");
  assertEquals(b.y > 0, true, "b must be pushed down");
});

Deno.test("moveItem: a displacement cascade resolves", () => {
  const out = moveItem(
    [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 2), item("c", 0, 4, 4, 2)],
    "c",
    0,
    0,
  );
  assertEquals(shape(out), ["a@0,2", "b@0,4", "c@0,0"]);
});

Deno.test("moveItem: x is clamped inside the grid", () => {
  const out = moveItem([item("a", 0, 0, 4, 2)], "a", 99, 0);
  assertEquals(out[0].x, 8); // 12 - 4
  const left = moveItem([item("a", 4, 0, 4, 2)], "a", -5, 0);
  assertEquals(left[0].x, 0);
});

Deno.test("moveItem: negative y is clamped to the top", () => {
  const out = moveItem([item("a", 0, 4, 3, 2)], "a", 0, -3);
  assertEquals(out[0].y, 0);
});

Deno.test("moveItem: an unknown instanceId is a no-op, not a throw", () => {
  const input = [item("a", 0, 0, 3, 2)];
  assertEquals(shape(moveItem(input, "ghost", 5, 5)), shape(input));
});

Deno.test("moveItem: result never contains an overlap", () => {
  const out = moveItem(
    [item("a", 0, 0, 6, 3), item("b", 6, 0, 6, 3), item("c", 0, 3, 12, 2)],
    "c",
    0,
    0,
  );
  for (let i = 0; i < out.length; i++) {
    for (let j = i + 1; j < out.length; j++) {
      assertEquals(overlaps(out[i], out[j]), false, `${out[i].instanceId}/${out[j].instanceId}`);
    }
  }
});

// --- resizeItem ---

Deno.test("resizeItem: grows within the grid", () => {
  const out = resizeItem([item("a", 0, 0, 3, 2)], "a", 6, 4, { minW: 2, minH: 2 });
  assertEquals(out[0].w, 6);
  assertEquals(out[0].h, 4);
});

Deno.test("resizeItem: clamps to the declared minimum", () => {
  const out = resizeItem([item("a", 0, 0, 6, 6)], "a", 1, 1, { minW: 3, minH: 2 });
  assertEquals(out[0].w, 3);
  assertEquals(out[0].h, 2);
});

Deno.test("resizeItem: cannot grow past the right edge", () => {
  const out = resizeItem([item("a", 8, 0, 4, 2)], "a", 9, 2, { minW: 2, minH: 2 });
  assertEquals(out[0].w, 4); // 12 - 8
  assertEquals(out[0].x, 8, "resize must not shift x");
});

Deno.test("resizeItem: growing pushes the item below down", () => {
  const out = resizeItem(
    [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 2)],
    "a",
    4,
    5,
    { minW: 2, minH: 2 },
  );
  assertEquals(shape(out), ["a@0,0", "b@0,5"]);
});

Deno.test("resizeItem: shrinking lets the item below rise", () => {
  const out = resizeItem(
    [item("a", 0, 0, 4, 6), item("b", 0, 6, 4, 2)],
    "a",
    4,
    2,
    { minW: 2, minH: 2 },
  );
  assertEquals(shape(out), ["a@0,0", "b@0,2"]);
});

Deno.test("resizeItem: an unknown instanceId is a no-op", () => {
  const input = [item("a", 0, 0, 3, 2)];
  assertEquals(shape(resizeItem(input, "ghost", 6, 6, { minW: 2, minH: 2 })), shape(input));
});

// --- cellFromPoint ---

const METRICS = { left: 100, top: 50, cellWidth: 80, rowHeight: 40, gap: 16 };

Deno.test("cellFromPoint: the grid origin is cell 0,0", () => {
  assertEquals(cellFromPoint(100, 50, METRICS), { x: 0, y: 0 });
});

Deno.test("cellFromPoint: a point inside the first cell is still 0,0", () => {
  assertEquals(cellFromPoint(150, 70, METRICS), { x: 0, y: 0 });
});

Deno.test("cellFromPoint: crossing a cell boundary advances one cell", () => {
  // cellWidth 80 + gap 16 = 96 per column.
  assertEquals(cellFromPoint(100 + 96, 50, METRICS).x, 1);
  assertEquals(cellFromPoint(100, 50 + 56, METRICS).y, 1);
});

Deno.test("cellFromPoint: a point left of or above the grid is clamped to 0", () => {
  assertEquals(cellFromPoint(0, 0, METRICS), { x: 0, y: 0 });
  assertEquals(cellFromPoint(-500, -500, METRICS), { x: 0, y: 0 });
});

Deno.test("cellFromPoint: x is clamped to the last column", () => {
  assertEquals(cellFromPoint(99999, 50, METRICS).x, 11);
});

Deno.test("cellFromPoint: a zero-width cell does not produce NaN or Infinity", () => {
  // Guards the first paint, when the grid has been mounted but not laid out.
  const degenerate = { left: 0, top: 0, cellWidth: 0, rowHeight: 0, gap: 0 };
  assertEquals(cellFromPoint(10, 10, degenerate), { x: 0, y: 0 });
});

// --- layoutHeight ---

Deno.test("layoutHeight: is the lowest bottom edge", () => {
  assertEquals(layoutHeight([item("a", 0, 0, 3, 2), item("b", 6, 4, 3, 3)]), 7);
});

Deno.test("layoutHeight: an empty layout has height 0", () => {
  assertEquals(layoutHeight([]), 0);
});
```

- [ ] **Step 2: Run it and verify it fails**

Run: `cd frontend && deno test lib/dashboard/grid_test.ts`
Expected: FAIL — `Module not found "./grid.ts"`.

- [ ] **Step 3: Write `grid.ts`**

```ts
/**
 * Dashboard grid geometry.
 *
 * Every decision about where a widget ends up lives here, as a pure function
 * over the layout array. The island above it only translates pointer positions
 * into these calls. That split exists because collision resolution and
 * compaction are where a hand-rolled grid actually fails, and because this repo
 * has no component test harness -- logic outside lib/ is untested by
 * construction.
 *
 * Three rules the whole engine obeys:
 *   1. Vertical gravity. After any change everything falls as far up as it can.
 *      This is what makes a stored layout render identically on every client.
 *   2. Displacement is downward only. Sideways displacement cascades
 *      unpredictably across twelve columns.
 *   3. The dragged item wins; everything else yields. Anything else fights the
 *      pointer.
 */
import { DASHBOARD_COLUMNS } from "./types.ts";
import type { LayoutItem } from "./types.ts";

export interface Bounds {
  minW: number;
  minH: number;
}

export interface GridMetrics {
  /** Viewport x of the grid's left edge. */
  left: number;
  /** Viewport y of the grid's top edge. */
  top: number;
  cellWidth: number;
  rowHeight: number;
  gap: number;
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

export function overlaps(a: LayoutItem, b: LayoutItem): boolean {
  return !(
    a.x + a.w <= b.x ||
    b.x + b.w <= a.x ||
    a.y + a.h <= b.y ||
    b.y + b.h <= a.y
  );
}

/** True when a and b share any column. */
function sharesColumn(a: LayoutItem, b: LayoutItem): boolean {
  return !(a.x + a.w <= b.x || b.x + b.w <= a.x);
}

/**
 * Applies vertical gravity: every item falls until it rests on the item above
 * it, or on the top edge. Processing in y order means an item's blockers are
 * already settled when it is placed, so one pass suffices and the result is
 * idempotent.
 */
export function compact(items: readonly LayoutItem[]): LayoutItem[] {
  const ordered = [...items].sort((p, q) => p.y - q.y || p.x - q.x);
  const settled: LayoutItem[] = [];

  for (const it of ordered) {
    let y = 0;
    for (const s of settled) {
      if (sharesColumn(it, s)) {
        y = Math.max(y, s.y + s.h);
      }
    }
    settled.push({ ...it, y });
  }
  return settled;
}

/**
 * Pushes everything that collides with `anchor` downward, transitively, then
 * compacts. `anchor` keeps the position it was given -- rule 3.
 */
function resolve(items: readonly LayoutItem[], anchor: LayoutItem): LayoutItem[] {
  const others = items.filter((i) => i.instanceId !== anchor.instanceId);
  const placed: LayoutItem[] = [anchor];

  // Settle in reading order so a cascade resolves in one pass.
  for (const it of [...others].sort((p, q) => p.y - q.y || p.x - q.x)) {
    let candidate = { ...it };
    let moved = true;
    while (moved) {
      moved = false;
      for (const p of placed) {
        if (overlaps(candidate, p)) {
          candidate = { ...candidate, y: p.y + p.h };
          moved = true;
        }
      }
    }
    placed.push(candidate);
  }

  // Gravity last: displacement can leave holes, and a layout with holes is not
  // the canonical form of itself.
  return compact(placed.filter((i) => i.instanceId !== anchor.instanceId).concat(anchor));
}

/** Moves one item to (x, y), clamped into the grid, and resolves the result. */
export function moveItem(
  items: readonly LayoutItem[],
  instanceId: string,
  x: number,
  y: number,
  columns: number = DASHBOARD_COLUMNS,
): LayoutItem[] {
  const target = items.find((i) => i.instanceId === instanceId);
  if (!target) return [...items];

  const anchor: LayoutItem = {
    ...target,
    x: clamp(x, 0, Math.max(0, columns - target.w)),
    y: Math.max(0, y),
  };
  return resolve(items, anchor);
}

/**
 * Resizes one item to (w, h), clamped to its declared minimum and to the right
 * edge. x never changes: a resize that also shifts the item reads as a bug.
 */
export function resizeItem(
  items: readonly LayoutItem[],
  instanceId: string,
  w: number,
  h: number,
  bounds: Bounds,
  columns: number = DASHBOARD_COLUMNS,
): LayoutItem[] {
  const target = items.find((i) => i.instanceId === instanceId);
  if (!target) return [...items];

  const anchor: LayoutItem = {
    ...target,
    w: clamp(w, bounds.minW, columns - target.x),
    h: Math.max(bounds.minH, h),
  };
  return resolve(items, anchor);
}

/** Translates a viewport point to a grid cell, clamped into the grid. */
export function cellFromPoint(
  px: number,
  py: number,
  m: GridMetrics,
): { x: number; y: number } {
  const colStride = m.cellWidth + m.gap;
  const rowStride = m.rowHeight + m.gap;

  // Before first layout the grid reports zero-size cells. Dividing by that
  // yields Infinity or NaN, and a NaN coordinate propagates into the layout.
  if (colStride <= 0 || rowStride <= 0) return { x: 0, y: 0 };

  return {
    x: clamp(Math.floor((px - m.left) / colStride), 0, DASHBOARD_COLUMNS - 1),
    y: Math.max(0, Math.floor((py - m.top) / rowStride)),
  };
}

/** Number of rows the layout occupies. */
export function layoutHeight(items: readonly LayoutItem[]): number {
  return items.reduce((max, i) => Math.max(max, i.y + i.h), 0);
}
```

- [ ] **Step 4: Run the tests**

Run: `cd frontend && deno test lib/dashboard/grid_test.ts`
Expected: PASS, 28 tests.

- [ ] **Step 5: Verify and commit**

Run: `cd frontend && deno task check`
Expected: exit 0.

```bash
git add frontend/lib/dashboard/grid.ts frontend/lib/dashboard/grid_test.ts
git commit -m "feat(dashboard): pure grid geometry with collision resolution"
```

Branch: `feat/d6-grid-geometry`
PR title: `feat(dashboard): pure grid geometry with collision resolution`

**Done means** — a move never produces an overlap; displacement is downward
only; compaction is idempotent and never mutates its input; a degenerate
zero-size grid yields cell 0,0 rather than NaN; an unknown `instanceId` is a
no-op rather than a throw.

---

### Task D7: Render a layout on the grid

**Files:**
- Create: `frontend/islands/DashboardGrid.tsx`
- Create: `frontend/components/dashboard/GridItem.tsx`
- Modify: `frontend/islands/DashboardV2.tsx`

**Interfaces:**
- Consumes: `grid.ts`, `registry.ts`, `WidgetHost`, `DEFAULT_OVERVIEW_LAYOUT`.
- Produces: `<DashboardGrid layout editable />` and `<GridItem item def />`.
  D8/D9/D10 add interaction to these same two files.

- [ ] **Step 1: Write `GridItem.tsx`**

One positioned cell. No interaction yet — D8 and D9 add the handles.

```tsx
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
import type { LayoutItem, WidgetDef } from "@/lib/dashboard/types.ts";

interface GridItemProps {
  item: LayoutItem;
  def: WidgetDef;
  /** Suppresses transitions while a pointer session is active, so the dragged
   * item tracks the cursor instead of easing behind it. */
  dragging?: boolean;
}

export default function GridItem({ item, def, dragging }: GridItemProps) {
  return (
    <div
      data-testid="grid-item"
      data-instance-id={item.instanceId}
      data-widget-id={item.id}
      style={{
        gridColumn: `${item.x + 1} / span ${item.w}`,
        gridRow: `${item.y + 1} / span ${item.h}`,
        minWidth: 0,
        minHeight: 0,
        transition: dragging ? "none" : "grid-column 0.12s, grid-row 0.12s",
      }}
    >
      <WidgetHost def={def} params={item.params ?? {}} />
    </div>
  );
}
```

- [ ] **Step 2: Write `DashboardGrid.tsx`**

```tsx
import { useSignal } from "@preact/signals";
import { useRef } from "preact/hooks";
import GridItem from "@/components/dashboard/GridItem.tsx";
import { getWidget } from "@/lib/dashboard/registry.ts";
import { layoutHeight } from "@/lib/dashboard/grid.ts";
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_GRID_GAP,
  DASHBOARD_ROW_HEIGHT,
} from "@/lib/dashboard/types.ts";
import type { DashboardLayoutConfig, LayoutItem } from "@/lib/dashboard/types.ts";

interface DashboardGridProps {
  initial: DashboardLayoutConfig;
}

/**
 * Renders a layout and, in P2, owns the in-memory working copy of it.
 *
 * Persistence arrives in P3. Until then the layout resets on reload, which is
 * deliberate: interaction is the hard part, and proving it without a stored
 * contract to migrate is much cheaper than proving both at once.
 */
export default function DashboardGrid({ initial }: DashboardGridProps) {
  const items = useSignal<LayoutItem[]>(initial.items);
  const dragging = useSignal<string | null>(null);
  const gridRef = useRef<HTMLDivElement | null>(null);

  // An explicit row count keeps the grid tall enough for the lowest widget.
  // Relying on implicit rows makes a drop below the last row silently clamp.
  const rows = Math.max(1, layoutHeight(items.value));

  return (
    <div
      ref={gridRef}
      data-testid="dashboard-grid"
      style={{
        display: "grid",
        gridTemplateColumns: `repeat(${DASHBOARD_COLUMNS}, 1fr)`,
        gridTemplateRows: `repeat(${rows}, ${DASHBOARD_ROW_HEIGHT}px)`,
        gap: `${DASHBOARD_GRID_GAP}px`,
      }}
    >
      {items.value.map((item) => {
        const def = getWidget(item.id);
        // An unknown id is dropped rather than rendered (spec D-7). The notice
        // that a widget was dropped is P4's job, on the editor surface.
        if (!def) return null;
        return (
          <GridItem
            key={item.instanceId}
            item={item}
            def={def}
            dragging={dragging.value === item.instanceId}
          />
        );
      })}
    </div>
  );
}
```

- [ ] **Step 3: Collapse to one column at narrow widths**

Add to `DashboardGrid.tsx`, using the same 768px breakpoint the mobile shell
uses. The order is `items` sorted by `y` then `x`, which per the spec also gives
keyboard and screen-reader traversal order for free.

```tsx
  // Below the breakpoint the grid is a single column in reading order. Widgets
  // keep their heights; only x and w are discarded.
  const narrow = useSignal(false);
  // ...set from a matchMedia listener in an IS_BROWSER-guarded effect...

  const ordered = narrow.value
    ? [...items.value].sort((p, q) => p.y - q.y || p.x - q.x)
    : items.value;
```

- [ ] **Step 4: Swap `DashboardV2` over**

Replace the static grid D5b introduced with `<DashboardGrid initial={DEFAULT_OVERVIEW_LAYOUT} />`.
The header, the time-range tabs and the `dashboardData.ensure` effect stay in
`DashboardV2`.

- [ ] **Step 5: Verify and commit**

Run: `cd frontend && deno task check` and `cd e2e && npx playwright test tests/dashboard.spec.ts`
Expected: exit 0, and the D5b visual-equivalence assertions still hold — this
unit changes the container, not the arrangement.

```bash
git add frontend/islands/DashboardGrid.tsx frontend/components/dashboard/GridItem.tsx \
        frontend/islands/DashboardV2.tsx
git commit -m "feat(dashboard): render layouts through a real grid container"
```

Branch: `feat/d7-dashboard-grid`
PR title: `feat(dashboard): render layouts through a real grid container`

**Done means** — the dashboard renders from `DEFAULT_OVERVIEW_LAYOUT` through
`DashboardGrid`, still visually identical; the grid collapses to one column
below 768px in `y,x` order; an unknown widget id is skipped rather than crashing
the page.

---

### Task D8: Drag

**Files:**
- Modify: `frontend/islands/DashboardGrid.tsx`
- Modify: `frontend/components/dashboard/GridItem.tsx`
- Create: `e2e/tests/dashboard-grid.spec.ts`

**Interfaces:**
- Consumes: `moveItem`, `cellFromPoint`, `GridMetrics` from `grid.ts`.
- Produces: a `data-testid="drag-handle"` on each item; `data-dragging="true"`
  on the active one.

- [ ] **Step 1: Add the drag handle to `GridItem`**

The handle is the widget's title bar, not the whole card — a card-wide drag
target would swallow clicks on the links inside several widgets.

```tsx
      <div
        data-testid="drag-handle"
        onPointerDown={(e) => onDragStart?.(item.instanceId, e)}
        style={{
          position: "absolute",
          insetInline: 0,
          top: 0,
          height: "40px",
          cursor: editable ? "grab" : "default",
          // Only a target while editing: a monitoring dashboard gets clicked
          // through fast and an always-live handle would swallow header clicks.
          pointerEvents: editable ? "auto" : "none",
        }}
      />
```

- [ ] **Step 2: Own the pointer session in `DashboardGrid`**

```tsx
  function metrics(): GridMetrics {
    const el = gridRef.current!;
    const rect = el.getBoundingClientRect();
    const cellWidth =
      (rect.width - DASHBOARD_GRID_GAP * (DASHBOARD_COLUMNS - 1)) /
      DASHBOARD_COLUMNS;
    return {
      left: rect.left,
      top: rect.top,
      cellWidth,
      rowHeight: DASHBOARD_ROW_HEIGHT,
      gap: DASHBOARD_GRID_GAP,
    };
  }

  function onDragStart(instanceId: string, e: PointerEvent) {
    if (!editable) return;
    const target = e.currentTarget as HTMLElement;
    // Pointer capture is what makes a drag survive the cursor leaving the
    // handle -- without it a fast drag drops the item the moment the pointer
    // outruns the element.
    target.setPointerCapture(e.pointerId);
    dragging.value = instanceId;

    const item = items.value.find((i) => i.instanceId === instanceId)!;
    const m = metrics();
    const origin = cellFromPoint(e.clientX, e.clientY, m);
    const offsetX = origin.x - item.x;
    const offsetY = origin.y - item.y;

    const onMove = (ev: PointerEvent) => {
      const cell = cellFromPoint(ev.clientX, ev.clientY, metrics());
      items.value = moveItem(
        items.value,
        instanceId,
        cell.x - offsetX,
        cell.y - offsetY,
      );
    };
    const onUp = () => {
      dragging.value = null;
      target.releasePointerCapture(e.pointerId);
      target.removeEventListener("pointermove", onMove);
      target.removeEventListener("pointerup", onUp);
      target.removeEventListener("pointercancel", onUp);
    };

    target.addEventListener("pointermove", onMove);
    target.addEventListener("pointerup", onUp);
    // pointercancel fires on an OS-level interruption. Without this the grid
    // stays stuck in a drag with no pointer attached.
    target.addEventListener("pointercancel", onUp);
  }
```

- [ ] **Step 3: Write the E2E spec**

```ts
test("a widget can be dragged to a new cell", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("edit-layout").click();

  const target = page.locator('[data-instance-id="d-active-alerts"]');
  const before = await target.boundingBox();

  await page
    .locator('[data-instance-id="d-active-alerts"] [data-testid="drag-handle"]')
    .hover();
  await page.mouse.down();
  await page.mouse.move(200, 200, { steps: 12 });
  await page.mouse.up();

  const after = await target.boundingBox();
  expect(after!.x).not.toBe(before!.x);
});

test("dragging never leaves two widgets on the same cell", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("edit-layout").click();
  // ...drag one widget onto another, then assert no two bounding boxes
  // intersect. The invariant is already unit-tested in grid_test.ts; this
  // proves the island calls it rather than positioning items itself.
});
```

- [ ] **Step 4: Verify and commit**

```bash
git commit -am "feat(dashboard): drag widgets to snap into new cells"
```

Branch: `feat/d8-grid-drag`

**Done means** — a widget follows the pointer in whole cells, survives the
cursor leaving the handle (pointer capture), and recovers from `pointercancel`;
displaced widgets move down, never sideways; drag is inert outside edit mode.

---

### Task D9: Resize

**Files:**
- Modify: `frontend/islands/DashboardGrid.tsx`
- Modify: `frontend/components/dashboard/GridItem.tsx`
- Modify: `e2e/tests/dashboard-grid.spec.ts`

- [ ] **Step 1: Add a bottom-right resize handle**

Visible only in edit mode, 16x16, `cursor: "se-resize"`,
`data-testid="resize-handle"`.

- [ ] **Step 2: Reuse the pointer-session shape from D8**

Same capture, same cleanup including `pointercancel`; the move handler calls
`resizeItem` instead of `moveItem`, passing `{ minW: def.minW, minH: def.minH }`
so the clamp comes from the registry rather than a magic number:

```tsx
    const onMove = (ev: PointerEvent) => {
      const cell = cellFromPoint(ev.clientX, ev.clientY, metrics());
      items.value = resizeItem(
        items.value,
        instanceId,
        cell.x - item.x + 1,
        cell.y - item.y + 1,
        { minW: def.minW, minH: def.minH },
      );
    };
```

- [ ] **Step 3: E2E**

Assert that resizing below `minW` stops at `minW` — the clamp is the behavior
most likely to regress, and it is what stops a user from producing a widget too
small to render its own content.

Branch: `feat/d9-grid-resize`

**Done means** — a widget resizes in whole cells from its bottom-right corner;
`x` never shifts during a resize; the clamp honors the registry's `minW`/`minH`;
growing pushes neighbours down and shrinking lets them rise.

---

### Task D10: Keyboard operation

**Files:**
- Modify: `frontend/components/dashboard/GridItem.tsx`
- Modify: `frontend/islands/DashboardGrid.tsx`
- Modify: `e2e/tests/dashboard-grid.spec.ts`

This is the unit most likely to be skipped and least likely to be forgiven. The
mobile app was held to WCAG 2.2 AA in M5 PR-5h; a web dashboard that can only be
arranged with a mouse is a regression against that bar. It is also the reason
D-2 chose a hand-rolled grid: grid libraries handle this poorly.

**Interfaces:**
- Consumes: `moveItem`, `resizeItemToCell` (or `resizeItem` directly, for a
  keyboard step that names a size rather than a pointer cell).
- Produces: focusable grid items with an `aria-label` describing position.

> **Decide first (raised in D9 review):** D8 and D9 each left a focusable
> `<button>` on every item, so edit mode already has two tab stops per widget --
> twenty on the default layout. Step 1 below adds a third, and that one is the
> only one that announces its geometry and takes the arrows, while the two
> inside it do nothing on Enter or Space. Settle whether the handles become
> `tabIndex={-1}` pointer-only affordances before writing Step 2. They were made
> buttons in D8 specifically so D10 could give them keys, so this is a genuine
> reversal to weigh, not an oversight to clean up.

- [ ] **Step 1: Make each item focusable and self-describing**

```tsx
      tabIndex={editable ? 0 : -1}
      role="application"
      aria-label={`${def.title}, column ${item.x + 1} of 12, row ${item.y + 1}, ${item.w} wide, ${item.h} tall`}
```

- [ ] **Step 2: Handle the keys**

Copy the switch shape from `CommandPalette.tsx:267-291` — the only keyboard
reference implementation in this codebase.

| Key | Action |
|---|---|
| Arrows | Move one cell |
| Shift + arrows | Resize by one cell |
| Escape | Leave edit mode |

Every branch calls `e.preventDefault()`, because arrow keys otherwise scroll the
page out from under the widget being moved.

- [ ] **Step 3: E2E**

```ts
test("a widget can be moved with the keyboard alone", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("edit-layout").click();

  const item = page.locator('[data-instance-id="d-active-alerts"]');
  await item.focus();
  const before = await item.boundingBox();
  await page.keyboard.press("ArrowLeft");
  const after = await item.boundingBox();
  expect(after!.x).toBeLessThan(before!.x);
});
```

Branch: `feat/d10-grid-keyboard`

**Done means** — every widget is reachable by Tab in edit mode, movable and
resizable by arrow keys, announces its position in its accessible name, and
never scrolls the page while being moved.

---

## Self-review

**Spec coverage.** §4.5 (CSS Grid, 12 columns, `grid-column`/`grid-row`, 1-column
collapse in `y,x` order) → D6 + D7. D-2 (hand-rolled, no dependency) → D6 has no
imports beyond `./types.ts`. D-10 (logic in `lib/`) → all geometry in `grid.ts`;
the islands only translate pointer positions. D-5's "editing is behind edit
mode" is honored here by gating handles on `editable`, though the toggle itself
is P4/D15.

**Not covered:** persistence (P3), the palette, reset and copy-from-cluster (P4).
The layout resets on reload throughout P2, stated in the Global Constraints.

**Placeholders.** Two deliberate ones, both marked: D7 Step 3's `matchMedia`
effect body and D9 Step 1's handle styling. Each is a few lines of conventional
code whose shape is fully determined by the surrounding snippet, and spelling
them out adds nothing a reader could get wrong. Everything load-bearing —
geometry, pointer sessions, clamping, cleanup — is written out in full.

**Type consistency.** `moveItem(items, instanceId, x, y, columns?)` and
`resizeItem(items, instanceId, w, h, bounds, columns?)` are called in D8/D9/D10
with exactly those signatures. `cellFromPoint(px, py, m)` takes `GridMetrics`
with `left/top/cellWidth/rowHeight/gap`, which is what `metrics()` builds.
`Bounds` is `{ minW, minH }`, sourced from `WidgetDef` — the same field names
P1 declared.

**Risk.** The one thing unit tests cannot catch is the island positioning items
itself instead of calling the geometry. The second E2E spec in D8 exists
specifically to catch that.
