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

**AMENDED 2026-09-19 — what D15 shipped.** Written by walking this section's
own list, per the lesson D12 recorded, not by reading the diff. Every step
above is discharged. The divergences are below, then what D16 and D17 inherit.

1. **Stack, again (the D14 note, still true).** This plan predates the
   Bun/Astro migration. `Deno.test` + `jsr:@std/assert` became `bun:test`,
   `deno task check` became `bun run check`, and the grid is
   `frontend/components/dashboard/DashboardGrid.tsx` while the island that owns
   edit mode is `frontend/src/islands/DashboardV2.tsx`. D16's and D17's file
   lists and verification commands carry the same rot; translate, do not
   trust.

2. **Seven files, not four** (G2 again, for the third time in this release).
   The list above omits the island — which is where edit state has lived since
   P2 — and both E2E files. Shipped: `lib/dashboard/edit-session.ts`,
   `lib/dashboard/edit-session_test.ts`,
   `components/dashboard/EditToolbar.tsx`,
   `components/dashboard/DashboardGrid.tsx`, `src/islands/DashboardV2.tsx`,
   `e2e/tests/dashboard-edit.spec.ts`, `e2e/tests/dashboard-grid.spec.ts`.

3. **No `Reset` button and no `Add widget` button.** Step 3 lists both with
   test ids. They belong to D17 and D16, and a button that is present in every
   screenshot while doing nothing is a worse promise than one that has not
   shipped. The toolbar ships `edit-layout`, `cancel-edit` and `save-layout`;
   the other two are additions, not replacements.

4. **The grid needed a way to report changes.** Nothing in the plan says how
   `applyChange` is reached, and the grid held its working copy privately with
   no way out. It grew one prop, `onChange`, fired from a single `setItems`
   helper that every write now goes through — the two pointer sessions, the
   keyboard, and a cancelled session's restore. A Save armed by three of those
   four paths would have been worse than no Save at all. **D16 and D17 add
   widgets and remove them through this same door**: call `onChange` with the
   new item list, or the session never learns about it.

5. **The session's baseline is the layout as RENDERED, not as loaded.** The
   grid runs `resolveRenderable` on the way in, which drops widgets this build
   has no definition for and re-compacts the survivors. Opening a session over
   the raw stored config would measure dirtiness against a layout that is not
   on screen, and the first nudge would read as two changes. `asRendered` in
   the island is the one-line normalizer, and it has to stay in step with
   whatever the grid does on mount.

6. **A refused save blocks the next one, and the button has to say so.** This
   is inherited from D14 and is the finding that cost the most here. Every
   failed write clears the store's `observed` (layout-store.ts, `saveLayout`'s
   catch) because the write may have committed on the way out, so until a
   fresh READ no save may claim a revision at all. Leaving edit mode and
   re-entering reads nothing, so the obvious recovery does not work. The
   editor therefore carries `saveBlocked`, set on any refusal, cleared only by
   a completed load, and surfaced as Save's disabled title
   (`SAVE_BLOCKED_REASON`). **D17's Reset and copy-from-cluster write through
   the same path and inherit the same rule.**

7. **The conflict dialog's "keep editing" is honest but terminal.** Both
   options the plan asks for are offered. Dismissing keeps the arrangement on
   screen — it is the only copy of that work — but the layout cannot then be
   written without loading the newer one, which the dialog copy and Save's
   title both say outright.

8. **Re-mounting is keyed on an epoch, not on the store's generation.** The
   grid takes its layout as a mount-time prop, so replacing it means
   re-mounting. Keying that on `layoutGeneration` is not enough: a reload
   landing on 204 hands back the very same default OBJECT, generation and all,
   while the grid is still holding the user's discarded edits. The island now
   owns `gridSource` + `gridEpoch` and bumps unconditionally, with
   `mountedGeneration` keeping the load effect and the conflict path from
   re-mounting over each other.

9. **Three existing tests in `dashboard-grid.spec.ts` had to change, all for
   the same reason: the "Edit layout" toggle no longer exists while editing.**
   - "leaving edit mode keeps the layout the drag produced" asserted the old
     "Done" button's behaviour. Deleted; Cancel (restores) and Save (stores)
     are both covered in `dashboard-edit.spec.ts`.
   - "a drag survives the handle disappearing under it" flipped edit mode off
     mid-drag by pressing Space on the still-focused toggle. That is now
     unreachable — mid-drag the pointer is captured, so neither Cancel nor
     Save can be pressed, and Escape is the drag's own cancel. Deleted; its
     sibling "collapsing to one column mid-drag" drives the identical grid
     teardown through the one trigger a user still has.
   - "a resize survives the grip disappearing under it" used the same trigger
     but had NO narrow-collapse sibling, so it was rewritten to collapse the
     viewport instead of deleted. That closes a coverage gap rather than
     leaving one.
   - "editing suspends the widget's own links and gives them back" now exits
     through `cancel-edit`.

   **Pre-existing, not D15:** "arrow keys never scroll the page out from under
   the widget" fails on a clean `main` as well (verified by stashing this
   branch and re-running it). It is untouched here and needs its own look.

10. **The new E2E mocks the preferences endpoint although a real Postgres is
    running.** A layout is stored per (user, cluster, scope) and the whole
    suite shares one login, so a single test that really saved would hand its
    arrangement to every later test that loads the dashboard — including the
    ones asserting where the DEFAULT layout puts things. **D18's acceptance
    specs have to decide this deliberately**: either keep mocking, or run
    serially and restore the default afterwards.

11. **D15 had to fix the toast provider to satisfy its own success-feedback
    constraint.** `ToastProvider` opened with `if (!IS_BROWSER) return null`,
    so its island rendered nothing during SSR; the probe that caught this
    found the `<astro-island>` still carrying `ssr=""` with zero children on a
    fully loaded page, and no `aria-live` container in the DOM at all. Every
    `showToast` call in the application — this one, CRDResourceList's deletes,
    ClusterManager's, SavedViews' — was a silent no-op, which is why no E2E
    had ever caught it: none of them assert on a toast, and saved-views.spec
    even says so out loud. Rendering the empty container on both sides fixes
    it, and is independently right: an `aria-live` region has to exist before
    its content changes or the change is not reliably announced. **Worth a
    look in its own unit:** twenty-odd other islands use the same
    `return null` SSR guard. They demonstrably do hydrate, so the pattern is
    not universally fatal, and what distinguishes this one was not chased
    down here.

    **Dev-loop trap worth remembering:** editing an island and re-running the
    E2E against the already-running `astro dev` produced TWO live copies of
    the edited module — HMR serves the new one while the old one stays
    resident — so `showToast` wrote to one `toasts` signal while the rendered
    provider subscribed to the other, and the fix looked like it had not
    worked. A probe counting module instances on `window` is what showed it;
    restarting the dev server made it one instance and the toast appear.
    Restart the dev server before trusting an E2E result about island module
    state.

12. **Review round (PR #470) found two real defects in this unit, both the
    same omission.** Seven local reviewers plus an independent cross-model
    adversarial pass ran over the branch; the cross-model peer and two local
    reviewers independently described the same defect, and an independent
    validator confirmed a second on a protected data-loss subject. Both are
    fixed on the branch:
    - **An async operation must own the editing surface while it runs.** The
      grid stayed editable for the whole `saveLayout` await, so an edit made
      after `commit()` snapshotted the payload reached the session but never
      the server — and the success path then cleared the session and reported
      "saved" over an arrangement that was never written. Fixed with
      `editable={editing && !saving.value}`. The same omission appeared a
      second time in `reloadStoredLayout`: `layoutLoaded` never returns to
      false, so the first-load gate does not cover a *second* load, and Edit
      stayed live while the replacement GET was outstanding. Fixed with a
      `reloading` signal OR'd into `editDisabled`. **D16 and D17 add more
      async paths through this island and inherit the rule: whatever owns the
      layout while a request is in flight must withdraw the editing surface
      for exactly that window.**
    - **A stub that answers every non-GET measures the mock, not the client.**
      The conflict E2E returned its canned 409 for any method or body, so a
      client that switched to POST or dropped the revision would still have
      passed. Both stubs now assert the method, the CSRF and cluster headers,
      and the revision/config body before answering.

    One reported finding was **rejected on inspection** and left alone: the
    unconditional `saveBlocked` latch after a pre-flight refusal is correct,
    because nothing in-session can clear `layoutWithheld` or the store's
    observation, so a fresh read genuinely is required.

13. **The new E2E asserts state, not toasts.** All but one of the specs prove
    a save through the closed session and the recorded write; exactly one
    asserts the toast itself, so a future regression in the notification
    cannot be mistaken for a regression in the write.

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

**AMENDED 2026-09-20 — what D16 shipped.** Written by walking this section's
own list, per the lesson D12 recorded. Every step above is discharged. The
divergences are below, then what D17 and D18 inherit.

1. **Stack rot, for the fourth time.** `Deno.test` is `bun:test`, `deno task
   check` is `bun run check`, and the palette is
   `components/dashboard/WidgetPalette.tsx`, not `islands/WidgetPalette.tsx`:
   it renders inside DashboardV2, which is already an island, and the tree
   that Astro hydrates is `src/islands/`. D17's file list carries the same rot
   — and one more: there is no `components/dashboard/GridItem.tsx`. `GridItem`
   is an exported component inside `DashboardGrid.tsx`.

2. **Nine files, not four** (G2, for the fourth time): `lib/dashboard/placement.ts`,
   `lib/dashboard/placement_test.ts`, `lib/fuzzy-search.ts`,
   `components/dashboard/WidgetPalette.tsx`,
   `components/dashboard/EditToolbar.tsx`, `src/islands/DashboardV2.tsx`,
   `e2e/tests/dashboard-layout-stub.ts`, `e2e/tests/dashboard-palette.spec.ts`,
   `e2e/tests/dashboard-edit.spec.ts`.

3. **`DashboardGrid.tsx` was NOT modified.** The plan assumed the grid owned
   edit state; since P2 the island does. The grid holds a private working copy
   it takes only at mount, so an insertion goes in through `mountGrid` — the
   same door a completed load and a cancel use — and the session is told
   separately with `applyChange`, because the grid deliberately does not report
   the layout it mounts with. **D17's Remove is the mirror image and should use
   the same door** rather than growing a second write path into the grid.

4. **The data cache had to learn about the working copy.** `dashboardData.ensure`
   was keyed on the stored layout. A widget added from the palette is in no
   stored layout until the user saves, so the source it reads was never
   fetched and the new card would have sat in its loading state forever. The
   effect now reads `session.working` while a session is open. `ensure` skips
   an already-fetched source, so this costs nothing on the drag path.

5. **The palette autofocuses in a LAYOUT effect.** A passive effect runs after
   the click that opened the dialog has finished, which is late enough for the
   browser to put focus back on the button — the dialog then opens with the
   keyboard outside it and Escape does nothing. This is D15's focus fix-up
   finding from the other direction, and it was measured (a probe reading
   `document.activeElement`), not guessed. `CommandPalette.tsx` papers over the
   same ordering with `setTimeout(..., 10)`; the plan's instruction to copy it
   would have copied the workaround. **Any dialog D17 adds inherits this.**

6. **`fuzzySearch` would have truncated the catalog.** It slices to 8 entries
   with no query and 12 with one — a screenful for a palette with hundreds of
   navigation entries, and a silent lie for a catalog whose stated contract is
   that every widget stays reachable. It is now generic over `{id, label,
   detail}` with an optional limit, so widget definitions are ranked and
   returned as themselves. The command palette's behaviour is unchanged.

7. **The selection never rests on an entry that cannot be added.** The opening
   selection, the arrows and hover all skip the already-placed rows, and the
   selection is -1 when nothing is addable — which is the state a first-time
   user meets, since the shipped default layout holds all ten widgets. A
   highlighted row that ignores Enter is a worse promise than no highlight.
   Those rows carry `aria-disabled` rather than the `disabled` attribute, so
   they are still announced with their reason; note that Playwright's
   actionability check waits `aria-disabled` out, so the spec that proves the
   refusal clicks with `force: true`.

8. **Nothing is addable from a default dashboard, which is D17's other half.**
   Every palette E2E starts from a deliberately short stored layout. This is
   not a testing artifact: until Remove ships, a user who has never edited
   anything opens the catalog and finds every row disabled. D17 closes that
   loop, and the pair only reads as a feature once both are in.

9. **The E2E stub of the layout endpoint now lives in
   `e2e/tests/dashboard-layout-stub.ts`**, imported by both dashboard specs.
   It carries D15's request assertions (method, CSRF and cluster headers,
   revision and config body), which are the reason these specs measure the
   client rather than the mock, and a second copy of them is a copy that
   drifts. `stubLayoutStore` takes an optional pre-loaded layout.
   **D15's open question is still open**: everything here mocks the endpoint,
   and D18 has to decide deliberately whether the acceptance specs keep
   mocking or run serially against the real store and restore the default.

10. **Dev-loop trap, worth remembering.** `styles.css` squashes every
    transition to 0.01ms under `prefers-reduced-motion: reduce`, which the E2E
    fixture emulates, and the app sets `transition-property: all`. A
    `getComputedStyle` read — or a screenshot — taken in the same tick as a
    class change therefore reports the *pre-transition* colour, which looks
    exactly like a Tailwind utility that was never generated. Half an hour
    went into that. Let two frames pass before trusting either.

11. **`DASHBOARD_MAX_ITEMS` is not enforced client-side.** With ten
    unparameterized widgets a layout cannot reach forty, so a guard here would
    be untestable code guarding an unreachable state; the server refuses with
    `limit_reached` and the save path already reports it. The day a
    parameterized widget ships, the palette is where that check belongs.

12. **Review round found six defects, two of them P1, and one bad fix.** Seven
    local reviewers plus an independent cross-model adversarial pass ran over
    the branch; a separate validator confirmed four findings, and two more
    were decisions the review deliberately left open. All are fixed on the
    branch. The two that mattered most:

    - **`crypto.randomUUID` is secure-context only**, so Add widget threw a
      TypeError inside the click handler and did nothing at all on an
      HTTP-only deployment -- the homelab this repo ships values for.
      `GaugeRing.tsx`, `SparklineChart.tsx` and `ResourceAreaChart.tsx` had
      each already hit this and carry a comment saying why they use
      `Math.random`; D16's docstring asserted the opposite. **The lesson is
      not about that one API.** A comment that states a platform guarantee is
      a claim, and this one was written from memory rather than from the three
      places in the same tree that had already paid for the answer.
    - **A pointer press focuses an element even at `tabIndex={-1}`**, so
      clicking a greyed-out row moved focus off the search input -- taking the
      arrow keys, Enter and Escape with it -- and left the Tab trap inert,
      because its own selector excludes option rows. The next Tab left an
      `aria-modal` dialog. `onMouseDown` preventDefault on the row is the
      standard guard, and the trap now pulls focus back whenever something
      outside the ring holds it. **D17's Remove control sits in this same
      dialog family and inherits both.**

    Also fixed: placement is now bounded by `DASHBOARD_MAX_ROWS` and returns
    `null` when nothing fits, with the palette offering such an entry disabled
    for the same reason it disables a widget already on the dashboard (a
    clamp was rejected -- a clamped cell overlaps, which the server refuses
    too); the dialog swallows Ctrl/Cmd+K so the global command palette cannot
    stack on top of it; and the palette takes the layout's own column count
    rather than the constant.

    **The narrow-mode focus fix was wrong the first time, and the E2E is what
    caught it.** A re-mounted grid renders wide before its own layout effect
    corrects it to one column, so the new cell briefly carries a tab stop:
    `focus()` succeeds, the immediate "did it take focus?" check passes, and
    the correction then blurs it to the document. The check has to be made a
    frame later, which is the first point at which the answer is stable. A
    focus trace, not reasoning, is what showed this -- **the same lesson as
    note 10, one layer down: in this island, what is on screen one tick after
    a re-mount is not what the effect sees.**

13. **Eleven files, not nine** once the review fixes landed:
    `lib/fuzzy-search_test.ts` (the shared helper had no unit test although
    this branch changed its contract) and the three further specs in
    `e2e/tests/dashboard-palette.spec.ts` -- the Tab cycle, the keyboard after
    a pointer click, and the narrow-mode insertion that found the bad fix.

14. **A second review round closed what note 14 first listed as deferred.**
    That note read "left undone, deliberately" and named three things: the
    E2E stub validating the request contract but not the layout's geometry,
    no spec proving Cancel discards an added widget, and six focus-management
    refs wanting one owner. A second review of the branch -- six reviewers
    plus an independent cross-model pass, pointed at the fix round rather
    than at the original feature -- found one P1 and fourteen smaller items.
    All of them are now applied, those three included, so the paragraph this
    one replaces is no longer true.

    **The P1 is the one to remember: the test written to guard the Tab-trap
    repair proved nothing.** It pressed Shift+Tab from the search input and
    Tab from Close -- the two adjacent, non-wrapping directions a browser
    handles by itself -- so deleting the whole wrap block left it green. A
    regression guard that passes without the mechanism it names is worse than
    no guard, because it reports coverage that does not exist. **The rule this
    leaves behind: a test written to guard a specific fix has to be run
    against a tree with that fix removed, once, before it is trusted.** Three
    other specs from that round failed the same test and were strengthened --
    the one-column insertion asserted only that the palette closed, the
    untracked-focus backstop had no test at all, and nothing proved Cancel
    discards an insertion.

    What moved, and why it matters to D17:
    - **The disabled-reason decision is now `lib/dashboard/catalog.ts`**, with
      its own tests, and it carries the `DASHBOARD_MAX_ITEMS` check the client
      never had. It was logic that needed a test sitting inside a component,
      which is the exact thing D-10 exists to prevent; the "no room" reason
      shipped untested at any level for one round. **D17's Remove changes what
      is placed, so it changes what this function answers -- extend it there,
      not in the component.**
    - **Focus management is now `lib/hooks/use-dashboard-focus.ts`.** Six refs
      and two layout effects coordinated by boolean flags had produced the two
      worst defects of the previous round. The island calls one named method
      per gesture. **D17 adds a seventh mover; it calls `armReturnToEdit` (or
      adds a method here) instead of a new ref.**
    - **The dialog's focus ring is selected by what is focusable**, not by
      tag, so a Remove control that is not an input or a button joins the Tab
      cycle instead of falling into the backstop.
    - **The narrow-mode focus recovery waits on a microtask, not a frame.** A
      backgrounded tab throttles frames, and the frame was never what the
      check needed: the grid's correction lands in a microtask-debounced
      re-render whose DOM patch blurs the cell synchronously. That last step
      was *measured* in Chromium this time. The first version of this fix was
      wrong precisely because nobody measured it.
    - The E2E layout stub now refuses the geometry the server refuses, and its
      docstring no longer claims more than it checks. The widget catalog's own
      rules stay with the Go tests; a second copy here would drift.

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
