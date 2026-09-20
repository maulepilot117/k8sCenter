import { useLayoutEffect, useRef } from "preact/hooks";
import ModalDialogShell from "@/components/dashboard/ModalDialogShell.tsx";
// Which layouts are offered, and what taking one would lose, is decided in
// lib/ rather than here: this is a component, and the repo has no component
// test harness (D-10). `copyableLayouts` is unit tested; this renders its
// answer. Do not move those rules back into this file.
import type { CopyableLayout } from "@/lib/dashboard/layout-store.ts";

/**
 * The layouts this user has arranged on their other clusters, offered for the
 * dashboard being edited.
 *
 * The affordance that makes D-3's per-cluster scoping bearable: a layout is
 * stored per (user, cluster, scope) so production can differ from a sandbox,
 * and without this the price of that is arranging the same dashboard by hand
 * on every cluster. It mirrors the "Saved on another cluster" section in
 * `SavedViews.tsx`, with the difference that section could not offer: there
 * the other cluster's view is shown disabled, because applying it means
 * switching clusters. A layout is just placements, so it can come across.
 *
 * A modal dialog for the same reasons `WidgetPalette.tsx` is one, and it wears
 * the same shell -- `ModalDialogShell` carries the scrim, the panel, the
 * header and the key contract both dialogs owe: Escape closes, Tab stays
 * inside, Ctrl/Cmd+K cannot stack the command palette on top of it. Focus goes
 * to the first row on open, which is this dialog's own job; the caller puts it
 * back on the button that opened this.
 *
 * Taking a layout writes nothing. It replaces the working copy, and Save is
 * still the only thing that reaches the server -- which is why there is no
 * confirmation here and why the copy is undone by Cancel, exactly like a drag.
 */

export interface LayoutCopyDialogProps {
  /** The rows to offer, in the order they should be read. */
  layouts: readonly CopyableLayout[];
  /** Takes one. The caller merges it into the session and closes this. */
  onCopy: (layout: CopyableLayout) => void;
  onClose: () => void;
}

/**
 * When a layout was last saved, as a date the reader can place.
 *
 * Two clusters' layouts are otherwise told apart only by a cluster id, and a
 * user who arranged one of them this morning knows which that was. Locale
 * formatting, because this is the user's own timestamp rather than a value
 * anything compares. An unparseable date renders as nothing rather than
 * "Invalid Date": the row is still usable without it.
 */
function savedOn(iso: string): string | null {
  const at = new Date(iso);
  return Number.isNaN(at.getTime()) ? null : at.toLocaleDateString();
}

export default function LayoutCopyDialog({
  layouts,
  onCopy,
  onClose,
}: LayoutCopyDialogProps) {
  const firstRow = useRef<HTMLButtonElement>(null);

  // On the first row rather than on Close: every row does something and Close
  // is the way out, so opening on the exit is opening on the one control the
  // user did not press a button to reach.
  //
  // A layout effect, not an effect, for the reason WidgetPalette records: this
  // dialog is mounted from the click on the button that opens it, and a
  // passive effect runs after that click has finished -- late enough for the
  // button to take focus back, leaving the dialog open with the keyboard
  // outside it and Escape unreachable.
  useLayoutEffect(() => {
    firstRow.current?.focus();
  }, []);

  return (
    <ModalDialogShell
      titleId="layout-copy-title"
      title="Copy a layout from another cluster"
      testId="layout-copy-dialog"
      closeTestId="close-layout-copy"
      onClose={onClose}
    >
      <p class="border-b border-border-subtle px-4 py-2 text-xs text-text-muted">
        This replaces the arrangement you are editing. Nothing is saved until
        you press Save.
      </p>

      {/* A plain list of buttons, not a listbox: each row is an action that
            closes the dialog, not a selection the user then confirms. The
            arrow keys therefore stay the browser's, and Tab is the way
            through -- which is also why every row is a real tab stop here and
            the palette's option rows are not. */}
      <div class="flex-1 overflow-y-auto py-1">
        {layouts.map((entry, index) => {
          const saved = savedOn(entry.updatedAt);
          const count = entry.config.items.length;
          return (
            <button
              key={entry.id}
              ref={index === 0 ? firstRow : undefined}
              type="button"
              data-testid="copy-layout-option"
              data-cluster-id={entry.clusterId}
              onClick={() => onCopy(entry)}
              class="flex w-full flex-col items-start gap-0.5 border-l-2 border-transparent px-4 py-2 text-left hover:border-accent hover:bg-accent-dim focus-visible:border-accent focus-visible:bg-accent-dim focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-brand"
            >
              <span class="text-sm font-medium text-text-primary">
                {entry.clusterLabel}
              </span>
              <span class="text-xs text-text-muted">
                {[
                  `${count} widget${count === 1 ? "" : "s"}`,
                  saved === null ? null : `saved ${saved}`,
                ]
                  .filter((part) => part !== null)
                  .join(" · ")}
              </span>
              {/* Said before the copy, not after. A warning shown once the
                    layout is already on screen reads as an error the user
                    caused; here it is part of what they are choosing. */}
              {entry.warnings.map((warning) => (
                <span key={warning} class="text-xs text-warning">
                  {warning}
                </span>
              ))}
            </button>
          );
        })}
      </div>
    </ModalDialogShell>
  );
}
