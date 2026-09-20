import type { MutableRef } from "preact/hooks";
import { useLayoutEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * Where the keyboard goes when the dashboard editor changes shape.
 *
 * Every gesture in the editor takes a control out of the document: entering
 * edit mode replaces "Edit layout" with Cancel and Save, leaving it takes
 * Cancel and Save away again, adding a widget re-mounts the grid and unmounts
 * the palette that had focus. A browser's answer to all three is the same one
 * -- drop focus to the body -- and the body is the top of the page, which for
 * a keyboard user means starting the whole journey over.
 *
 * This hook is the one owner of that. It holds the button refs the toolbar
 * attaches to, the three pieces of "what the next render owes the keyboard",
 * and the two layout effects that pay them. The island calls a
 * named method once per gesture and never reasons about effect ordering: the
 * ordering rules that matter are written down here, next to the effects that
 * depend on them.
 *
 * It lives in `frontend/lib/hooks/` because that is this repo's one home for
 * hooks (`use-poll`, `use-dirty-guard`, `use-split-pane`, ...), all of which
 * touch the DOM or the network and none of which is unit tested. D-10 sends
 * anything that *needs* a unit test to `frontend/lib/` as a *pure* module, and
 * nothing here is pure or testable without a DOM: what this hook does is move
 * focus, and the only gate that can observe that is the Playwright dashboard
 * suite. So it is deliberately not in `lib/dashboard/` beside the geometry and
 * the edit session, which are pure and are unit tested.
 *
 * A dialog's own autofocus is NOT here. A dialog focusing itself on open is
 * the dialog's job and lives in `WidgetPalette.tsx` and
 * `LayoutCopyDialog.tsx`; what this hook owns is where focus goes when one of
 * them closes.
 */
export interface DashboardFocus {
  /**
   * The "Edit layout" button.
   *
   * Escape on a focused widget leaves edit mode, which takes that widget out
   * of the tab order under the focus that is on it. Focus has to land
   * somewhere deliberate, and where editing started is the only place the user
   * asked for.
   */
  editButton: MutableRef<HTMLButtonElement | null>;
  /** The Cancel button. It unmounts the moment the session closes. */
  cancelButton: MutableRef<HTMLButtonElement | null>;
  /** The palette's opener, so closing the dialog puts focus back on it. */
  addButton: MutableRef<HTMLButtonElement | null>;
  /** The copy dialog's opener. Same contract as `addButton`. */
  copyButton: MutableRef<HTMLButtonElement | null>;
  /**
   * The Reset button, so its confirmation can hand the keyboard back.
   *
   * Reset is the one destructive gesture that leaves the session open: Cancel
   * and the conflict dialog both close it and are covered by
   * `armReturnToEdit`, but confirming a reset unmounts the dialog the user was
   * standing in and leaves editing exactly where it was. Without somewhere to
   * go, focus falls to the body and the keyboard restarts at the top of the
   * page -- with the whole dashboard having just changed underneath it.
   */
  resetButton: MutableRef<HTMLButtonElement | null>;
  /**
   * The user asked to start editing: move focus to Cancel on the render that
   * has it.
   *
   * "Edit layout" is replaced by Cancel and Save rather than relabelled, so
   * the button the user just pressed leaves the document and the browser drops
   * focus to the body. Moving it to Cancel keeps the keyboard where the
   * controls now are, and makes the first Tab land inside the editor instead
   * of at the top of the page.
   */
  armToolbarFocus: () => void;
  /**
   * This gesture is closing the session: put focus back on "Edit layout" on
   * the render that has it.
   *
   * Armed by every exit that has a user behind it -- Cancel, a successful
   * save, taking the server's layout after a conflict -- rather than by the
   * effect noticing the session went away, because a session closed by
   * something other than a gesture has no keyboard to move.
   */
  armReturnToEdit: () => void;
  /**
   * A widget was just placed: focus it once the re-mounted grid has rendered
   * it, and fall back to "Add widget" if it cannot take focus.
   *
   * Call it before the re-mount, in the same gesture that inserted the widget.
   */
  focusOnInsert: (instanceId: string) => void;
  /**
   * Put focus on "Add widget" now.
   *
   * The palette's close path: a dialog that returns focus to the document
   * leaves a keyboard user at the top of the page. Immediate rather than armed
   * because nothing is being re-rendered -- the button is already on screen
   * and is where the user came from.
   */
  focusAddButton: () => void;
  /** Put focus on "Copy from cluster" now. Same case as `focusAddButton`. */
  focusCopyButton: () => void;
  /**
   * Put focus on "Reset" now.
   *
   * Immediate, like the two above, and safe for the same reason: the toolbar
   * is not what a reset re-mounts. The grid is, and this button is not in it.
   */
  focusResetButton: () => void;
}

/**
 * Owns the dashboard editor's focus choreography.
 *
 * `editing` and `gridEpoch` are the two things the effects key on, and both
 * belong to the island: edit mode is the island's session, and the epoch is
 * the counter that re-mounts its grid. They are passed in rather than derived
 * so this hook stays the mover of focus and not a second owner of the editor's
 * state.
 */
export function useDashboardFocus(
  editing: boolean,
  gridEpoch: number,
): DashboardFocus {
  const editButton = useRef<HTMLButtonElement | null>(null);
  const cancelButton = useRef<HTMLButtonElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);
  const copyButton = useRef<HTMLButtonElement | null>(null);
  const resetButton = useRef<HTMLButtonElement | null>(null);

  /** Set by `armToolbarFocus`, read by the edit-mode effect below. */
  const toolbarPending = useRef(false);
  /** Set by `armReturnToEdit`, read by the edit-mode effect below. */
  const returnPending = useRef(false);
  /**
   * The widget an insertion just placed, until the grid it re-mounted has
   * rendered it.
   *
   * Adding re-mounts the grid, so the new cell does not exist during the click
   * that asked for it. The insertion effect picks this up on the render that
   * does have it and moves focus there -- which is also what scrolls it into
   * view, and what makes the grid announce where it landed.
   */
  const insertPending = useRef<string | null>(null);

  // After the render that changed edit mode, not during the key press that
  // asked for it. Focusing first leaves the browser about to run its own focus
  // fix-up on the widget it is removing from the tab order, and that lands on
  // the document rather than on the button we just moved to.
  useLayoutEffect(() => {
    if (!IS_BROWSER) return;
    if (editing) {
      if (!toolbarPending.current) return;
      toolbarPending.current = false;
      cancelButton.current?.focus();
      return;
    }
    if (!returnPending.current) return;
    returnPending.current = false;
    editButton.current?.focus();
  }, [editing]);

  // A widget added from the palette, on the render that has it.
  //
  // Focus rather than a scroll alone: the new cell is the thing the user just
  // asked for, it announces where it landed through the grid's own accessible
  // name, and moving the keyboard there is also what brings it into view --
  // a widget added to the bottom of a long dashboard is otherwise off screen,
  // which reads as an Add button that did nothing. Below the grid's narrow
  // breakpoint a widget is not a tab stop at all (DashboardGrid withholds
  // `tabIndex` there on purpose -- see its comment on `arrangeable`), so
  // focus falls back to the Add widget button in that mode; the explicit
  // scroll below still carries the visibility half of the promise either way.
  useLayoutEffect(() => {
    if (!IS_BROWSER) return;
    const instanceId = insertPending.current;
    if (instanceId === null) return;
    insertPending.current = null;
    const el = document.querySelector<HTMLElement>(
      `[data-instance-id="${instanceId}"]`,
    );
    el?.focus();
    el?.scrollIntoView({ block: "nearest" });
    // Two ways the keyboard ends up nowhere, and neither is hypothetical.
    //
    // The cell may not be there at all, in which case `focus()` above did
    // nothing and the palette that had focus has already unmounted.
    if (document.activeElement !== el) {
      addButton.current?.focus();
      return;
    }
    // Or the cell may take focus and then lose it, which is what the microtask
    // below is for. The ordering it depends on, in full, because a shorter
    // story about it was wrong once already:
    //
    //   1. A re-mounted grid always renders wide. It corrects to one column
    //      from its own layout effect, which measures the grid and writes its
    //      `narrow` signal -- and which runs before this one, because Preact
    //      flushes a child's layout effects before its parent's.
    //   2. That write does not patch anything itself. Preact re-renders from a
    //      signal write on the microtask queue, so at this moment the DOM is
    //      still the wide one and the new cell still carries the tab stop that
    //      one-column mode is about to take away -- which is exactly why the
    //      `focus()` above succeeds and a check made right here cannot see the
    //      problem.
    //   3. When that re-render runs it patches the DOM synchronously, and
    //      removing `tabindex` from the focused element blurs it to the body
    //      then and there -- measured in Chromium, the one engine the E2E gate
    //      runs, where `document.activeElement` is already the body on the
    //      statement after `removeAttribute("tabindex")`.
    //
    // So a microtask queued here runs after the blur. Preact either flushes
    // that re-render inside the flush already in progress -- synchronously,
    // before any microtask at all -- or schedules its own microtask for it
    // from step 1, which was queued before this one and therefore runs first.
    // Both orderings land the patch ahead of this callback.
    //
    // This was a `requestAnimationFrame` until R5. A frame also sees it, one
    // frame later, but it is the weaker thing to depend on: a backgrounded or
    // hidden tab throttles frames to a crawl or stops them entirely, so the
    // fallback could be delayed indefinitely -- in precisely the situation
    // where nothing else is going to put the keyboard anywhere.
    //
    // A microtask cannot be cancelled, so the cleanup flips a flag instead of
    // dropping a callback. Without it an unmount, or the next insertion's
    // re-run of this effect, leaves a callback that can still move focus to a
    // button that is no longer on the page.
    let cancelled = false;
    queueMicrotask(() => {
      if (cancelled) return;
      const active = document.activeElement;
      if (active === null || active === document.body) {
        addButton.current?.focus();
      }
    });
    return () => {
      cancelled = true;
    };
  }, [gridEpoch]);

  return {
    editButton,
    cancelButton,
    addButton,
    copyButton,
    resetButton,
    armToolbarFocus() {
      toolbarPending.current = true;
    },
    armReturnToEdit() {
      returnPending.current = true;
    },
    focusOnInsert(instanceId: string) {
      insertPending.current = instanceId;
    },
    focusAddButton() {
      addButton.current?.focus();
    },
    focusCopyButton() {
      copyButton.current?.focus();
    },
    focusResetButton() {
      resetButton.current?.focus();
    },
  };
}
