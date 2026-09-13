---
title: "DX — Discoverability of Release A surfaces"
parent_plan: docs/plans/2026-09-10-EXECUTION-ORDER.md
spec: docs/plans/2026-09-13-dashboard-builder-design.md
date: 2026-09-13
baseline_revision: 78d9881e
status: ready
---

# DX Implementation Plan — Discoverability of saved views and pins

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan
> task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the saved-views and pins features that Release A already shipped
findable by a user who has never been told they exist.

**Architecture:** Three copy-and-affordance edits to two existing islands, plus
one Playwright spec. No new modules, no backend change, no new dependency.

**Tech Stack:** Deno 2.x, Fresh 2.x (Preact), Playwright.

**Spec:** `docs/plans/2026-09-13-dashboard-builder-design.md` (this unit is the
DX row in that document's execution-order amendment, not part of Release G
proper).

## Global Constraints

- One PR, one branch, **at most 5 touched files including tests** (G2).
- Repo-wide verification only (Agent Directive 4): `cd frontend && deno task check`.
  Scoped checks do not satisfy it.
- Tailwind utility-only is the stated convention, but **both islands under edit
  use inline `style` objects exclusively**. Match the file you are in; do not
  introduce classes into a file that has none.
- No unit tests are possible here. This repo has **zero component tests** — all
  nine unit tests are `frontend/lib/*_test.ts` over pure modules, and there is no
  testing-library or `preact-render-to-string`. Island behavior is verified by
  Playwright only, so the TDD cycle in this plan is spec-first Playwright.
- Every assertion targets a `data-testid`, never visible copy. The existing specs
  do this (`saved-views.spec.ts` uses `getByTestId("saved-views-toggle")` at
  eight call sites and never asserts the button's text), which is what makes the
  copy changes below safe.

---

## The problem, stated precisely

Measured against the shipped code:

| Surface | What a first-time user sees | Why it dead-ends |
|---|---|---|
| Pinned nav section | The header `Pinned`, then the string `Nothing pinned yet.` (`PinnedResources.tsx:92`) | Proves pins exist, then gives no path to creating one — no link, no instruction, no mention of where the Pin control lives. |
| Saved views trigger | A text button reading `Views (0)` (`SavedViews.tsx:302`, rendered `:335`) | No `title` and no `aria-label`, so the accessible name is literally `Views (0)`. Nothing says the control can *save* anything. |
| Saved views menu, empty | `No saved views for this table yet.` (`SavedViews.tsx:405`) | Names the absence, not the action. The only creation verb, `Save current view` (`:666`), sits below the list and a divider. |

**Out of scope, with reason:** the command palette. `CommandPalette.tsx:205`
builds its index once into a `useRef(buildSearchIndex())`, so it is static for
the island's lifetime and cannot react to signals — a user's actual pins can
never appear there without a different mechanism. Static "Save current view"
entries would be worse than nothing, because that action is contextual to
whichever resource table is on screen and a palette entry would navigate
somewhere it does not apply. Leave the palette alone.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `frontend/islands/PinnedResources.tsx` | Pinned list in the secondary nav | Empty state gains a second line naming the action and where to find it |
| `frontend/islands/SavedViews.tsx` | Saved-views control in the table toolbar | Trigger gains an accessible name; empty-menu copy names the create action |
| `e2e/tests/discoverability.spec.ts` | **New.** Proves the hints render | — |

Three files. Two under the cap.

---

### Task 1: The pinned empty state names the action

**Files:**
- Modify: `frontend/islands/PinnedResources.tsx:78-96`
- Test: `e2e/tests/discoverability.spec.ts` (create)

**Interfaces:**
- Consumes: `pinsLoaded`, `pinsUnavailable`, `pinsForActiveCluster` from
  `frontend/lib/pin-store.ts`; `HEADER_STYLE` from the island's own module scope
  (`PinnedResources.tsx:21-28`).
- Produces: a new `data-testid="pinned-empty-hint"` element. Task 3's spec asserts
  on it.

- [ ] **Step 1: Write the failing spec**

Create `e2e/tests/discoverability.spec.ts`:

```ts
import { expect, test } from "../fixtures/base.ts";
import { deleteAllPins } from "../helpers.ts";

/**
 * Discoverability of the Release A surfaces (DX).
 *
 * Release A shipped saved views and pins, and then advertised them on two
 * surfaces that contained no path to using them: a "Pinned" header above the
 * words "Nothing pinned yet.", and a "Views (0)" button with no accessible
 * name. Both told the user a feature existed and neither told them how to
 * reach it.
 *
 * These specs pin the hints, not the phrasing: each asserts a testid exists
 * and is non-empty, plus the one substring that carries the instruction. Copy
 * can be reworded without breaking them; deleting the hint cannot.
 *
 * NOT covered here, and deliberately: the command palette. Its search index is
 * built once into a useRef (CommandPalette.tsx:205) and cannot react to
 * signals, so a user's pins can never appear in it without a different
 * mechanism. Nothing to assert.
 */

test.describe("discoverability", () => {
  test("the empty pinned section tells the user how to create a pin", async ({ page }) => {
    await deleteAllPins(page);
    await page.goto("/");

    const empty = page.getByTestId("pinned-empty");
    await expect(empty).toBeVisible();

    const hint = page.getByTestId("pinned-empty-hint");
    await expect(hint).toBeVisible();
    // The instruction must name the control by the word on the button.
    await expect(hint).toContainText("Pin");
  });
});
```

- [ ] **Step 2: Run it and verify it fails**

Run: `cd e2e && npx playwright test tests/discoverability.spec.ts -g "how to create a pin"`

Expected: FAIL. `pinned-empty-hint` does not exist, so the locator times out
with `Error: expect(locator).toBeVisible() failed`.

- [ ] **Step 3: Add the hint**

In `frontend/islands/PinnedResources.tsx`, replace the empty-state block at
`:78-96`. The existing comment on that branch explains only why the header
still renders; extend it to cover the hint.

```tsx
  // Three different situations, three different renderings. Collapsing any
  // pair of them would tell the user something untrue (R3).
  const unavailable = pinsUnavailable.value;
  const loading = !pinsLoaded.value && !unavailable;

  if (!unavailable && pinsLoaded.value && mine.length === 0) {
    // The header renders even when empty so the section reads as empty rather
    // than vanishing as if unsupported -- and the hint exists because saying
    // "nothing is pinned" to someone who has never seen a pin control is a
    // dead end. Name the button, and name the page it is on.
    return (
      <div style={{ marginTop: "14px" }} data-testid="pinned-resources">
        <div style={HEADER_STYLE}>Pinned</div>
        <div
          data-testid="pinned-empty"
          style={{
            fontSize: "12px",
            color: "var(--text-muted)",
            padding: "0 9px 4px",
          }}
        >
          Nothing pinned yet.
        </div>
        <div
          data-testid="pinned-empty-hint"
          style={{
            fontSize: "11px",
            lineHeight: 1.45,
            color: "var(--text-muted)",
            opacity: 0.8,
            padding: "0 9px 4px",
          }}
        >
          Open any resource and choose <strong>Pin</strong> to keep it here.
        </div>
      </div>
    );
  }
```

- [ ] **Step 4: Run the spec and verify it passes**

Run: `cd e2e && npx playwright test tests/discoverability.spec.ts -g "how to create a pin"`

Expected: PASS (1 passed).

- [ ] **Step 5: Commit**

```bash
git add frontend/islands/PinnedResources.tsx e2e/tests/discoverability.spec.ts
git commit -m "feat(ui): tell users how to create their first pin"
```

---

### Task 2: The saved-views control says what it does

**Files:**
- Modify: `frontend/islands/SavedViews.tsx:302` (label), `:335` (trigger attrs), `:405` (empty copy)
- Test: `e2e/tests/discoverability.spec.ts` (extend)

**Interfaces:**
- Consumes: `mine` (`SavedViews.tsx:124`), `loading`, `open` signals already
  declared at `:67-84`.
- Produces: an `aria-label` and `title` on `data-testid="saved-views-toggle"`,
  and a `data-testid="saved-views-empty-hint"` inside the menu.

- [ ] **Step 1: Write the failing specs**

Append to `e2e/tests/discoverability.spec.ts`, inside the same `test.describe`:

```ts
  test("the saved-views trigger has an accessible name that says it saves", async ({ page }) => {
    await page.goto("/workloads/pods");

    const toggle = page.getByTestId("saved-views-toggle");
    await expect(toggle).toBeVisible();

    // The visible label stays short; the accessible name carries the meaning.
    const label = await toggle.getAttribute("aria-label");
    expect(label, "saved-views-toggle must have an aria-label").not.toBeNull();
    expect(label!.toLowerCase()).toContain("save");
  });

  test("the empty saved-views menu names the create action", async ({ page }) => {
    await page.goto("/workloads/pods");
    await page.getByTestId("saved-views-toggle").click();

    const menu = page.getByTestId("saved-views-menu");
    await expect(menu).toBeVisible();

    // Only meaningful when this table has no saved views; skip otherwise
    // rather than deleting another spec's fixtures.
    const empty = page.getByTestId("saved-views-empty");
    if (await empty.count() === 0) {
      test.skip(true, "table already has saved views");
    }

    const hint = page.getByTestId("saved-views-empty-hint");
    await expect(hint).toBeVisible();
    await expect(hint).toContainText("Save current view");
  });
```

- [ ] **Step 2: Run them and verify they fail**

Run: `cd e2e && npx playwright test tests/discoverability.spec.ts`

Expected: 1 passed (Task 1), 2 failed. First failure
`expect(received).not.toBeNull()` because `aria-label` is absent; second times
out on `saved-views-empty-hint`.

- [ ] **Step 3: Add the accessible name**

In `frontend/islands/SavedViews.tsx`, the label at `:302` stays as it is — it is
short on purpose and eight existing specs locate this control by testid, not by
text. Add the meaning to the accessible name instead. Replace the opening of the
trigger button at `:335`:

```tsx
      <button
        type="button"
        data-testid="saved-views-toggle"
        aria-expanded={open.value}
        aria-haspopup="menu"
        // "Views (0)" is short enough to fit the toolbar and says nothing about
        // what the control is for. The accessible name and the tooltip carry
        // the verb, so the affordance is discoverable without widening the
        // button.
        aria-label="Saved views — save the current filters as a view"
        title="Saved views — save the current filters as a view"
```

- [ ] **Step 4: Point the empty menu at the create action**

Replace the empty-state copy at `SavedViews.tsx:405`. The existing element keeps
its `data-testid="saved-views-empty"` so no existing spec breaks; the hint is a
sibling.

```tsx
            <div
              data-testid="saved-views-empty"
              style={{
                fontSize: "12px",
                color: "var(--text-muted)",
                padding: "8px 6px",
              }}
            >
              No saved views for this table yet.
              <div
                data-testid="saved-views-empty-hint"
                style={{ marginTop: "4px", opacity: 0.8 }}
              >
                Set the filters you want, then choose{" "}
                <strong>Save current view</strong> below.
              </div>
            </div>
```

- [ ] **Step 5: Run the specs and verify they pass**

Run: `cd e2e && npx playwright test tests/discoverability.spec.ts`

Expected: 3 passed.

- [ ] **Step 6: Commit**

```bash
git add frontend/islands/SavedViews.tsx e2e/tests/discoverability.spec.ts
git commit -m "feat(ui): make the saved-views control say what it does"
```

---

### Task 3: Verify repo-wide and open the PR

**Files:** none modified.

- [ ] **Step 1: Run the repo-wide frontend check**

Run: `cd frontend && deno task check`

Expected: exit 0. This runs `deno fmt --check . && deno lint . && deno check`
across the whole tree, identical to CI.

**Known hazard:** `deno task check` currently short-circuits on a pre-existing
`deno fmt` disagreement in `frontend/assets/styles.css` under deno 2.7.14,
recorded as an open item in the 2026-09-13 session log. It reproduces on a clean
checkout and is unrelated to this change. If it fires, run
`deno fmt frontend/assets/styles.css` as a **separate commit** in this PR and say
so in the PR body — do not silently fold an unrelated formatting fix into a
feature commit.

- [ ] **Step 2: Run the full e2e suite, not just the new spec**

Run: `cd e2e && npx playwright test`

Expected: no new failures against the branch baseline. Pay attention to
`saved-views.spec.ts` and `pins.spec.ts` — they are the two suites touching the
edited islands. Baseline at `78d9881e` is 126 passed / 0 failing locally.

- [ ] **Step 3: Open the PR**

```bash
git push -u origin feat/dx-discoverability
gh pr create --title "feat(ui): make saved views and pins discoverable" --body "..."
```

Branch: `feat/dx-discoverability`
PR title: `feat(ui): make saved views and pins discoverable`

**Done means** — a user who has never used either feature can, from the pinned
nav section, learn that pins come from a resource detail page; and from the
saved-views trigger, learn that the control saves the current filters. Three
Playwright specs assert the hints exist. `deno task check` is exit 0 repo-wide,
and neither `saved-views.spec.ts` nor `pins.spec.ts` regressed.

---

## Self-review

- **Spec coverage:** the three surfaces named in the problem table each have a
  task and an assertion. The command palette is explicitly out of scope with the
  reason recorded (static `useRef` index).
- **Placeholders:** none. Every step carries the literal code to write.
- **Type consistency:** no new types. `data-testid` values used in the specs
  (`pinned-empty`, `pinned-empty-hint`, `saved-views-toggle`, `saved-views-menu`,
  `saved-views-empty`, `saved-views-empty-hint`) match the JSX exactly; the first,
  third, fourth and fifth already exist in the shipped islands.
- **Risk:** the only behavioral risk is a spec elsewhere asserting on the
  now-changed copy. Checked: `saved-views.spec.ts` locates this control by
  `getByTestId` at all eight call sites and asserts no button text.
