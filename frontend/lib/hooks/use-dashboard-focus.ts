import { useSignal } from "@preact/signals";
import type { MutableRef } from "preact/hooks";
import { useLayoutEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * Where the keyboard goes when the dashboard editor changes shape.
 *
 * Every gesture in the editor takes a control out of the document: entering
 * edit mode replaces "Edit layout" with Cancel and Save, leaving it takes
 * Cancel and Save away again, adding a widget re-mounts the grid and unmounts
 * the palette that had focus, removing one takes the focused cell away. A
 * browser's answer to all of them is the same one -- drop focus to the body --
 * and the body is the top of the page, which for a keyboard user means
 * starting the whole journey over.
 *
 * This hook is the one owner of that. It holds the button refs the toolbar
 * attaches to, the pieces of "what the next render owes the keyboard", and the
 * three layout effects that pay them. The island calls a named method once per
 * gesture and never reasons about effect ordering: the ordering rules that
 * matter are written down here, next to the effects that depend on them.
 *
 * One owner is the whole point, and it was nearly lost. D17 added three
 * gestures and routed two of them here; removal grew its own ref, its own
 * counter and its own layout effect inside `DashboardGrid` instead, and the
 * review that caught it found the predictable consequence -- a grid that owns
 * half the choreography cannot reach the toolbar, so emptying the dashboard
 * left focus on the body with nowhere for the component to send it. The
 * removal mover moved here; the grid reports the vacated position and nothing
 * else.
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
   * A widget was just taken off: focus whatever moves up into the gap once the
   * re-compacted grid has rendered, and fall back to "Add widget" when nothing
   * does.
   *
   * Call it before the layout write, in the same gesture that removed the
   * widget, the way `focusOnInsert` is called before the re-mount.
   *
   * Position rather than identity, because what the user wants next is
   * whatever took the vacated place, which is a different widget every time
   * and has no id worth recording. The last cell when the widget that went was
   * the last one; the toolbar when it was the only one.
   */
  focusAfterRemoval: (vacatedIndex: number) => void;
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
   * Put focus on a placed widget's cell now, falling back to "Add widget".
   *
   * The immediate counterpart of `focusOnInsert`, for a dialog that opened
   * FROM a cell and closed without changing the layout -- cancelling the
   * parameter dialog on a placed widget. Nothing is re-mounted in that case,
   * so there is no render to arm against and the cell is already on screen.
   *
   * It lives here rather than as a `querySelector` in the island for the
   * reason this hook exists at all: focus has one owner, and the last time a
   * second one appeared the editor lost the keyboard on an empty grid.
   */
  focusPlacement: (instanceId: string) => void;
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
  /**
   * The reading-order position a removal just vacated, until the render
   * without that widget has happened.
   *
   * Removing the focused widget takes the focused element out of the document,
   * and the browser's answer to that is the body -- the top of the page. Where
   * focus should go instead is not knowable until the layout has re-compacted,
   * so the position is recorded here and spent by the effect below.
   */
  const removalPending = useRef<number | null>(null);
  /**
   * Counts removals, and exists only to be that effect's dependency.
   *
   * The effect cannot key on the layout: that changes on every cell a drag
   * crosses, so a pre-paint effect for a once-per-removal job would be
   * scheduled and run on every frame of every gesture. Nor can it key on
   * `removalPending` itself -- removing the first widget and then the widget
   * that took its place records index 0 twice, and a dependency that did not
   * change is an effect that does not run.
   *
   * A signal rather than a ref for the same reason `focusOnInsert` leans on
   * `gridEpoch`: an effect needs a value that differs between renders, and a
   * ref never does.
   */
  const removals = useSignal(0);

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

  // Puts the keyboard back after a removal took it off the grid.
  //
  // This lived inside DashboardGrid until the D17 review. It belongs here for
  // the reason the D16 post-mortem gave when it made this hook the one owner
  // of focus: two owners is how the editor lost the keyboard twice already,
  // and a grid that owns half the choreography cannot reach the toolbar for
  // the half it does not own -- which is exactly the empty-grid case below.
  //
  // Running it from the island rather than from the grid also fixes the
  // ordering for free. Preact flushes a child's layout effects before its
  // parent's, so by the time this runs the grid has already patched in the
  // re-compacted cells; the version inside the grid had to reason about that
  // sequencing itself.
  //
  // A layout effect, so focus lands before the browser paints and the page
  // never shows a frame with nothing focused.
  useLayoutEffect(() => {
    if (!IS_BROWSER) return;
    const vacated = removalPending.current;
    if (vacated === null) return;
    removalPending.current = null;
    const cells = document.querySelectorAll<HTMLElement>("[data-instance-id]");
    if (cells.length === 0) {
      // Nothing left to focus, so the keyboard goes to the toolbar. "Add
      // widget" rather than Reset or Cancel: an empty dashboard is one the
      // user has to put something back on, and it is the control that is
      // unconditionally mounted for the whole session.
      //
      // The grid could not do this -- it has no toolbar to reach -- so it
      // returned here and left focus on the body, which is the top of the
      // page with the whole dashboard having just gone.
      addButton.current?.focus();
      return;
    }
    // Position, not identity: what the user wants next is whatever moved up
    // into the gap. The last cell when the widget that went was the last one.
    (cells[vacated] ?? cells[cells.length - 1]).focus();
  }, [removals.value]);

  // A keyboard move can reorder the same grid cell in the DOM, which blurs
  // it even though the widget still exists. Capture before the child patch;
  // restore afterwards only if that patch dropped focus to the document.
  const activePlacement =
    IS_BROWSER &&
    editing &&
    document.activeElement instanceof HTMLElement &&
    document.activeElement.matches("[data-instance-id]")
      ? document.activeElement
      : null;
  useLayoutEffect(() => {
    if (
      activePlacement?.isConnected &&
      activePlacement.tabIndex >= 0 &&
      document.activeElement === document.body
    ) {
      // Explicit toolbar/dialog/removal focus wins, and keeping the cell's
      // focus must not scroll the dashboard while the user arranges it.
      activePlacement.focus({ preventScroll: true });
    }
  });

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
    focusAfterRemoval(vacatedIndex: number) {
      removalPending.current = vacatedIndex;
      // Before the caller's layout write, so both land in one render: Preact
      // batches signal writes made in the same turn, and the effect that
      // spends this counter then runs against the DOM the new layout made.
      removals.value += 1;
    },
    focusAddButton() {
      addButton.current?.focus();
    },
    focusCopyButton() {
      copyButton.current?.focus();
    },
    focusPlacement(instanceId: string) {
      const el = document.querySelector<HTMLElement>(
        `[data-instance-id="${instanceId}"]`,
      );
      el?.focus();
      // Below the narrow breakpoint a cell is not a tab stop at all, and the
      // widget may also simply be gone -- removed from another gesture while
      // the dialog was open. Same fallback `focusOnInsert` uses, for the same
      // reason: "Add widget" is mounted for the whole session.
      if (document.activeElement !== el) addButton.current?.focus();
    },
    focusResetButton() {
      resetButton.current?.focus();
    },
  };
}
