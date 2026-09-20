import type { MutableRef } from "preact/hooks";
import { useRef } from "preact/hooks";

/**
 * The keyboard contract every `aria-modal="true"` dialog in this app owes:
 * Escape closes it, Tab stays inside it, and the global command-palette
 * shortcut cannot open a second modal on top of it.
 *
 * It lives here rather than in a component because it is DOM behaviour with no
 * rendering of its own -- the same reason `use-dashboard-focus.ts` is a hook
 * in this directory. D-10 sends logic that needs a unit test to `frontend/lib/`
 * as a *pure* module; nothing here is pure or observable without a DOM, and
 * the gate that can see it is the Playwright suite.
 *
 * Extracted from `WidgetPalette.tsx`, which is where every rule below was
 * worked out. The dashboard editor grew a second dialog in D17 ("copy from
 * another cluster"), and a second hand-written Tab trap is a second one to get
 * wrong -- these rules are individually subtle and only correct together.
 *
 * What it deliberately does NOT own: moving focus *into* the dialog on open
 * (each dialog knows which of its own controls should have it) or back to the
 * opener on close (that control belongs to the caller).
 */

/**
 * Elements the Tab trap treats as stops, chosen by what actually makes an
 * element focusable rather than by which tags a given dialog happens to use.
 * A tag list goes stale the moment a control that is not on it joins the
 * dialog -- a select, an anchor, a textarea, anything carrying an explicit
 * tabindex -- and a stop the trap does not recognise falls into the
 * untracked-focus backstop below, which yanks focus to the first stop on every
 * Tab instead of letting it join the cycle.
 *
 * `[tabindex="-1"]` is excluded because that is how a dialog opts an element
 * (a listbox's option rows) out of the Tab order without removing it from the
 * DOM, and `:disabled` is excluded because a disabled control is not a tab
 * stop -- treating it as one would make Tab appear to stick on it.
 */
const FOCUSABLE_SELECTOR = [
  "a[href]:not([tabindex='-1'])",
  "button:not([tabindex='-1']):not(:disabled)",
  "input:not([tabindex='-1']):not(:disabled)",
  "select:not([tabindex='-1']):not(:disabled)",
  "textarea:not([tabindex='-1']):not(:disabled)",
  "[tabindex]:not([tabindex='-1'])",
].join(", ");

export interface ModalDialogKeys {
  /** Attach to the dialog panel -- the element carrying `role="dialog"`. */
  dialogRef: MutableRef<HTMLDivElement | null>;
  /** Attach to that same element's `onKeyDown`. */
  onKeyDown: (e: KeyboardEvent) => void;
}

export function useModalDialogKeys(onClose: () => void): ModalDialogKeys {
  const dialogRef = useRef<HTMLDivElement | null>(null);

  /**
   * Ctrl/Cmd+K, Escape from anywhere in the dialog, and Tab held inside it.
   *
   * A modal that lets Tab walk out into the page behind it is modal only to
   * the mouse.
   *
   * The Ctrl/Cmd+K branch is the one that looks gratuitous and is not.
   * `CommandPalette.tsx` binds that combination on `window`, and its handler
   * calls `preventDefault()` but never `stopPropagation()` -- so with nothing
   * stopping it here, pressing it while a dialog is open stacks a second
   * `aria-modal` dialog on top of this one, with two Tab traps live at once
   * and this dialog's own Escape no longer reachable.
   *
   * This belongs on the dialog container rather than on whichever control has
   * focus: Preact's onKeyDown is a real DOM listener, so a keydown fired on a
   * child still bubbles through the container before it would reach `window`,
   * and this runs first and stops it there. Every branch that calls
   * `preventDefault` for a key the dialog owns calls `stopPropagation`
   * alongside it for the same reason -- nothing a dialog handles should be
   * visible to a listener outside it.
   */
  function onKeyDown(e: KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      onClose();
      return;
    }
    if (e.key !== "Tab") return;
    const stops = Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ??
        [],
    );
    if (stops.length === 0) return;
    const first = stops[0];
    const last = stops[stops.length - 1];
    const active = document.activeElement;
    if (!stops.some((stop) => stop === active)) {
      // Focus is inside the dialog but on an element the trap does not
      // recognise as a stop -- an option row focused by a pointer press is how
      // that happens in the widget palette. Neither wrap branch below would
      // match, so both would be skipped and the next Tab would walk out of an
      // `aria-modal="true"` dialog. Pull focus onto the first stop instead of
      // trusting the browser to keep it inside.
      e.preventDefault();
      e.stopPropagation();
      first.focus();
      return;
    }
    if (e.shiftKey && active === first) {
      e.preventDefault();
      e.stopPropagation();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      e.stopPropagation();
      first.focus();
    }
  }

  return { dialogRef, onKeyDown };
}
