---
title: "Release G / P4 — Dashboard editor"
parent_plan: docs/plans/2026-09-10-EXECUTION-ORDER.md
spec: docs/plans/2026-09-13-dashboard-builder-design.md
date: 2026-09-13
baseline_revision: 78d9881e
status: ready
---

# Release G / P4 Implementation Plan — Dashboard editor

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan
> task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the grid from P2 and the persistence from P3 into something a user
operates: enter edit mode, add widgets from a catalog, remove them, save, cancel,
reset, and copy a layout from another cluster.

**Architecture:** One edit-session state machine in `frontend/lib/dashboard/edit-session.ts`
(pure, unit-tested), driving a toolbar and a palette rendered by `DashboardGrid`.
The destructive paths reuse `ConfirmDialog`; success uses `showToast`; the
palette's keyboard behavior is copied from `CommandPalette`, the only correct
prior art in the repo.

**Tech Stack:** Deno 2.x, Fresh 2.x (Preact), `@preact/signals`, Playwright.

**Spec:** `docs/plans/2026-09-13-dashboard-builder-design.md` — decisions D-5,
D-6, D-7, D-3.

## Global Constraints

- One PR per unit, **at most 5 touched files including tests** (G2).
- **Logic that needs a unit test lives in `frontend/lib/`** (D-10).
- **Keyboard: copy `CommandPalette.tsx:267-291`.** `SavedViews.tsx` has
  `role="menu"` with *no* Escape handling, no outside-click close and no focus
  trap — do not copy it. The palette in D16 is a dialog and must behave like one.
- Destructive actions go through `@/components/ui/ConfirmDialog.tsx`; success
  through `showToast(msg, "success")` from `@/islands/ToastProvider.tsx`. Both
  are the established patterns in `SavedViews.tsx`.
- Never `localStorage` for layout state (`pin-store.ts:9-13` — it is captured
  into Playwright's `storageState` and leaks across the suite).
- Repo-wide verification: `cd frontend && deno task check`.

---

### Task D15: Edit mode, dirty state, save and cancel

**Files:**
- Create: `frontend/lib/dashboard/edit-session.ts`
- Create: `frontend/lib/dashboard/edit-session_test.ts`
- Create: `frontend/components/dashboard/EditToolbar.tsx`
- Modify: `frontend/islands/DashboardGrid.tsx`

**Interfaces:**
- Consumes: `DashboardLayoutConfig`, `LayoutItem` from `types.ts`; `layout-store.ts`
  from P3.
- Produces: `EditSession`, `beginEdit`, `applyChange`, `isDirty`, `discard`,
  `commit`. D16 and D17 call `applyChange`.

**Why a state machine rather than a boolean.** Edit mode carries four things at
once — whether editing is active, the layout as loaded, the working copy, and the
revision the save must pin. Scattering those across four signals is how you get a
Save button that writes a stale revision.

- [ ] **Step 1: Write the failing test**

```ts
import { assertEquals } from "jsr:@std/assert@1";
import { applyChange, beginEdit, commit, discard, isDirty } from "./edit-session.ts";
import type { DashboardLayoutConfig } from "./types.ts";

const BASE: DashboardLayoutConfig = {
  schemaVersion: 1,
  scope: "overview",
  columns: 12,
  items: [{ instanceId: "a", id: "cluster-health", x: 0, y: 0, w: 4, h: 4 }],
};

Deno.test("beginEdit: the working copy starts equal to the baseline and is not dirty", () => {
  const s = beginEdit(BASE, 3);
  assertEquals(isDirty(s), false);
  assertEquals(s.revision, 3);
});

Deno.test("beginEdit: the working copy is a deep copy", () => {
  // A shallow copy would let a drag mutate the baseline, and Cancel would then
  // "restore" the edited layout.
  const s = beginEdit(BASE, 1);
  s.working.items[0].x = 9;
  assertEquals(BASE.items[0].x, 0);
});

Deno.test("applyChange: a real change marks the session dirty", () => {
  const s = applyChange(beginEdit(BASE, 1), [
    { instanceId: "a", id: "cluster-health", x: 4, y: 0, w: 4, h: 4 },
  ]);
  assertEquals(isDirty(s), true);
});

Deno.test("applyChange: a change that restores the baseline is not dirty", () => {
  // Drag a widget away and back again and Save must not be offered -- an
  // enabled Save that writes nothing trains people to ignore it.
  const moved = applyChange(beginEdit(BASE, 1), [
    { instanceId: "a", id: "cluster-health", x: 4, y: 0, w: 4, h: 4 },
  ]);
  const back = applyChange(moved, [
    { instanceId: "a", id: "cluster-health", x: 0, y: 0, w: 4, h: 4 },
  ]);
  assertEquals(isDirty(back), false);
});

Deno.test("applyChange: item order does not affect dirtiness", () => {
  // The grid re-sorts during compaction, so a reordered-but-identical array is
  // not a change.
});

Deno.test("discard: returns the baseline, not the working copy", () => {
  const s = applyChange(beginEdit(BASE, 1), [
    { instanceId: "a", id: "cluster-health", x: 7, y: 2, w: 4, h: 4 },
  ]);
  assertEquals(discard(s).items[0].x, 0);
});

Deno.test("commit: produces the working copy and the revision it was loaded at", () => {
  const s = applyChange(beginEdit(BASE, 5), [
    { instanceId: "a", id: "cluster-health", x: 2, y: 0, w: 4, h: 4 },
  ]);
  const { config, revision } = commit(s);
  assertEquals(config.items[0].x, 2);
  assertEquals(revision, 5);
});

Deno.test("commit: preserves schemaVersion, scope and columns from the baseline", () => {
  // These are not the editor's to change, and a dropped scope would write a
  // layout addressed to the wrong dashboard.
  const { config } = commit(beginEdit(BASE, 1));
  assertEquals(config.schemaVersion, 1);
  assertEquals(config.scope, "overview");
  assertEquals(config.columns, 12);
});
```

- [ ] **Step 2: Run it and verify it fails, then write `edit-session.ts`**

The dirty check compares canonical forms — items sorted by `instanceId`, keys in
a fixed order — so reordering during compaction is not mistaken for a change.

- [ ] **Step 3: Write `EditToolbar.tsx`**

Buttons: `Edit layout` when idle; `Add widget`, `Reset`, `Cancel`, `Save` when
editing. `Save` is `disabled={!isDirty(session)}`. Test ids:
`edit-layout`, `add-widget`, `reset-layout`, `cancel-edit`, `save-layout`.

Guard the unsaved-changes case, matching the care `SavedViews` takes with
in-flight state:

```tsx
  // Leaving edit mode with unsaved work is the one way to lose a layout
  // silently, and a dashboard is easy to click away from.
  const cancel = () => {
    if (!isDirty(session.value)) {
      session.value = null;
      return;
    }
    confirmDiscard.value = true;
  };
```

- [ ] **Step 4: Handle the save conflict**

`revision_conflict` means the layout changed elsewhere — another tab, another
device. Do **not** silently overwrite and do **not** silently discard. Surface it
and offer the two honest options: reload theirs (losing this session's edits) or
keep editing. Use `preferenceReason(err)` from `frontend/lib/preferences.ts` for
the classification, which is how every other preferences caller does it.

- [ ] **Step 5: E2E and commit**

```ts
test("drag is inert until edit mode is entered", async ({ page }) => {
  await page.goto("/");
  const item = page.locator('[data-instance-id="d-active-alerts"]');
  const before = await item.boundingBox();
  // Attempt the same gesture D8 uses; nothing should move.
  await item.hover();
  await page.mouse.down();
  await page.mouse.move(200, 200, { steps: 10 });
  await page.mouse.up();
  expect((await item.boundingBox())!.x).toBe(before!.x);
});

test("a saved layout survives a reload", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("edit-layout").click();
  // ...move a widget...
  await page.getByTestId("save-layout").click();
  await page.reload();
  // ...assert the moved position persisted...
});

test("cancel restores the layout as loaded", async ({ page }) => { /* ... */ });
```

Branch: `feat/d15-dashboard-edit-mode`
PR title: `feat(dashboard): edit mode with save and cancel`

**Done means** — drag and resize are inert outside edit mode; Save is disabled
until something actually changed and stays disabled if a change is undone;
Cancel restores the loaded layout and confirms first when there is unsaved work;
a save conflict is reported with both options rather than resolved silently; a
saved layout survives reload.

---

### Task D16: Catalog palette

**Files:**
- Create: `frontend/islands/WidgetPalette.tsx`
- Create: `frontend/lib/dashboard/placement.ts`
- Create: `frontend/lib/dashboard/placement_test.ts`
- Modify: `frontend/islands/DashboardGrid.tsx`

**Interfaces:**
- Consumes: `widgetsForScope` from `registry.ts`; `compact`, `layoutHeight` from
  `grid.ts`.
- Produces: `placeNewWidget(items, def, columns)` and `<WidgetPalette scope onAdd />`.

- [ ] **Step 1: Write the failing placement test**

Where a newly added widget lands is pure logic and therefore testable:

```ts
Deno.test("placeNewWidget: the first widget goes to the origin", () => {
  const out = placeNewWidget([], def("cluster-health", 4, 4));
  assertEquals({ x: out.x, y: out.y }, { x: 0, y: 0 });
});

Deno.test("placeNewWidget: fills a gap in an existing row before starting a new one", () => {
  // A new widget dropped at the very bottom of a long dashboard is invisible;
  // the user clicks Add and appears to get nothing.
});

Deno.test("placeNewWidget: falls to a new row when no gap is wide enough", () => { /* ... */ });

Deno.test("placeNewWidget: uses the widget's default size", () => { /* ... */ });

Deno.test("placeNewWidget: never overlaps and never exceeds the grid width", () => { /* ... */ });

Deno.test("placeNewWidget: a widget wider than the grid is clamped, not dropped", () => { /* ... */ });
```

- [ ] **Step 2: Write the palette**

A modal dialog, not a menu. Copy the structure from `CommandPalette.tsx`:
`role="dialog" aria-modal="true"` on the scrim, scrim click-to-close via
`e.target === e.currentTarget`, `role="listbox"` on the results, `role="option"
aria-selected` per entry, and the full `handleInputKeyDown` switch —
ArrowDown/ArrowUp/Enter/Escape, each with `e.preventDefault()`.

Group entries by `WidgetFamily` with the family as a section header, and filter
with `fuzzySearch` from `@/lib/fuzzy-search.ts`, which already exists and is
already used by the command palette.

Show, but disable, a widget already on the layout **unless** it declares
`params` — a parameterized widget can legitimately appear twice (prod beside
staging), which is the reason `instanceId` exists. Disabled entries carry a
`title` saying why, rather than vanishing; `SavedViews.tsx:537` takes the same
approach with other-cluster views for the same reason (R3).

- [ ] **Step 3: Generate the instanceId**

```ts
// Not the widget id, and not an index: an index is reused after a removal, so
// a stale render could key a new widget to a removed one's state. crypto is
// available in every browser this app supports.
const instanceId = `${def.id}-${crypto.randomUUID().slice(0, 8)}`;
```

- [ ] **Step 4: E2E and commit**

Assert: the palette opens from `add-widget`, closes on Escape, filters on typing,
adds the chosen widget to the grid, and that the added widget is visible without
scrolling.

Branch: `feat/d16-widget-palette`
PR title: `feat(dashboard): add widgets from a searchable catalog`

**Done means** — every widget for the scope is reachable, grouped by family and
searchable; the palette is fully keyboard-operable and closes on Escape and on
scrim click; an added widget lands somewhere visible; a duplicate is prevented
for unparameterized widgets with a stated reason.

---

### Task D17: Remove, reset, copy from another cluster

**Files:**
- Modify: `frontend/components/dashboard/GridItem.tsx`
- Modify: `frontend/components/dashboard/EditToolbar.tsx`
- Modify: `frontend/islands/DashboardGrid.tsx`
- Modify: `frontend/lib/dashboard/layout-store.ts`

- [ ] **Step 1: Remove**

An `x` control in the widget header, edit-mode only,
`aria-label={`Remove ${def.title}`}`, `data-testid="remove-widget"`. It must
`preventDefault()` and `stopPropagation()` — the header is also the drag handle,
exactly as `PinnedResources.tsx:167-186` has to do because its row is an anchor.

No confirm dialog: removal is one undo away (Cancel) and is not destructive
until Save. Gating it behind a modal would make arranging a dashboard tedious.

- [ ] **Step 2: Reset**

Restores `DEFAULT_OVERVIEW_LAYOUT` into the working copy — **not** the user's
previously saved layout (D-6). Behind one `ConfirmDialog` titled
`"Reset dashboard layout"`, whose body says plainly that the current arrangement
will be replaced by the default and that nothing is written until Save.

Reset marks the session dirty; it does not save. That keeps one rule — nothing
is written except by Save — rather than two.

- [ ] **Step 3: Copy from another cluster**

The affordance that makes D-3's per-cluster scoping tolerable. It needs the list
of clusters the user's layouts exist on, which the existing list endpoint already
returns — `preferencesApi.listViews` shows the shape, and layouts come back from
the same table with `clusterId` on each record.

Add `listLayouts()` to the client, offer the other clusters' layouts by name, and
on selection load that config into the working copy. It is a create under the
current `cluster_id`; no new endpoint. Mirrors the "Saved on another cluster"
section at `SavedViews.tsx:537`.

Drop unknown widget ids on copy exactly as on load, with the same warning: the
source cluster may have been saved by a different build.

- [ ] **Step 4: E2E and commit**

Branch: `feat/d17-remove-reset-copy`
PR title: `feat(dashboard): remove, reset and copy layouts`

**Done means** — a widget can be removed without a modal and restored by Cancel;
Reset restores the shipped default behind one confirm and writes nothing until
Save; a layout can be copied from another cluster, with unknown widgets dropped
and named.

---

### Task D18: Acceptance specs

**Files:**
- Modify: `e2e/tests/dashboard-grid.spec.ts`
- Create: `e2e/tests/dashboard-layout.spec.ts`

Follow the house style in `pins.spec.ts`: a docstring stating what is under test
**and what is deliberately not covered here, naming the test that owns it
instead**.

- [ ] **Step 1: The spec scenarios**

The five the spec's §9 names, plus the three the review of this feature will ask
about:

1. Reorder persists across reload.
2. Reset restores the default.
3. Unknown widget ids fall back safely — seed a layout containing
   `widget-from-the-future` through the API, then assert the page renders the
   rest and names the dropped one.
4. The layout is operable by keyboard alone: Tab to a widget, arrow-move it,
   Save, reload, assert it moved.
5. The layout is usable at 400px width — single column, no horizontal scroll.
6. Two tabs, one layout: save in tab A, save stale in tab B, assert B is told
   rather than silently winning.
7. A no-database deployment renders the default and says layouts are
   unavailable. **Note:** the E2E harness always runs PostgreSQL
   (`playwright.config.ts` webServer env), so this is unreachable from a browser
   — `pins.spec.ts:29-33` records exactly this limitation for its own 503 case.
   Assert it in Go (`TestHandler_NoDatabase_Returns503`, extended in P3/D13) and
   say so in the docstring rather than writing a spec that cannot run.
8. A widget whose data source fails shows its own error and leaves the rest of
   the dashboard rendered (already added in P1/D5b; assert it still holds with a
   customized layout).

- [ ] **Step 2: Clean up after each spec**

`pins.spec.ts` tracks created records in a module-level array and deletes them in
`afterEach`. Layouts need the same, or a spec that saves a layout changes what
every later spec sees — the admin user is shared across the suite.

- [ ] **Step 3: Full verification**

Run: `cd frontend && deno task check`
Run: `cd backend && go vet ./... && go test ./...`
Run: `cd e2e && npx playwright test`

Branch: `feat/d18-dashboard-acceptance`
PR title: `test(e2e): dashboard builder acceptance specs`

**Done means** — all eight scenarios are covered by a spec or by a named Go test
with the reason recorded; the suite is green; no spec leaves a layout behind.

---

## Self-review

**Spec coverage.** D-5 edit mode → D15. D-6 reset semantics → D17 Step 2. D-7
unknown ids on the read path → D17 Step 3 and D18 scenario 3 (the write path is
P3/D12). D-3 copy-from-cluster → D17 Step 3. §9's five E2E scenarios → D18,
extended to eight.

**Not covered:** P5's 29 remaining widgets, and P6's per-category dashboards.
Both are additive against the contract P1–P4 establish.

**Placeholders.** D15 Step 2's canonicalization body, D16's palette markup, and
several E2E bodies are described by the pattern and the file to copy rather than
written out. Each is conventional code whose shape is fixed by the named prior
art (`CommandPalette.tsx:267-291` for the keyboard switch, `pins.spec.ts` for the
spec skeleton). The parts where a wrong choice would be invisible — dirty-state
semantics, revision pinning, instanceId generation, the confirm-on-discard gate
— are written out or specified exactly.

**Type consistency.** `beginEdit(config, revision)`, `applyChange(session,
items)`, `isDirty(session)`, `discard(session) → config`, `commit(session) →
{config, revision}` are used with those exact signatures in D15's toolbar and in
D16/D17. `placeNewWidget(items, def, columns?)` returns a `LayoutItem`, matching
P1's type. `preferenceReason(err)` returns `PreferenceReason | undefined`, which
is what `preferences.ts:96-127` exports.

**Risk.** The most likely defect is Save writing a revision that is not the one
the session loaded at — which is exactly why `commit` returns the revision from
the session rather than from a signal read at save time, and why D15's test
asserts it.
