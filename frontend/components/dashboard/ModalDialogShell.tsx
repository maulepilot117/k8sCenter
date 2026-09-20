import type { ComponentChildren } from "preact";
import { useModalDialogKeys } from "@/lib/hooks/use-modal-dialog-keys.ts";

/**
 * The scrim, panel and header every modal dialog in the dashboard editor
 * wears, plus the keyboard contract that makes it modal.
 *
 * Extracted after D17, for the reason D17 itself extracted
 * `use-modal-dialog-keys.ts`: the editor grew a second dialog, and a second
 * hand-written copy of anything is a second one to get wrong. That change
 * shared the two dialogs' key handling and left the markup around it
 * duplicated byte for byte -- the same scrim utilities, the same
 * `glass-elevated` panel, the same header row with the same Close button, once
 * in `WidgetPalette.tsx` and once in `LayoutCopyDialog.tsx`. Two copies of a
 * modal's chrome drift the same way two copies of its Tab trap would, except
 * that the drift is visual and nothing fails when it happens.
 *
 * It owns the key contract rather than taking it as props, because a caller
 * that could render this panel without the hook would be a modal with no
 * Escape, no Tab trap and a command palette able to stack on top of it -- the
 * exact failure the hook exists to prevent, reachable by forgetting one line.
 *
 * What it deliberately does NOT own: moving focus into the dialog on open.
 * Each dialog knows which of its own controls should have it -- the palette
 * its search input, the copy dialog its first row -- and the hook's docstring
 * gives the same reason for leaving it out.
 */
export interface ModalDialogShellProps {
  /**
   * The id the heading carries and `aria-labelledby` points at. Supplied by
   * the caller rather than generated, so a caller's own test or label can
   * reference it.
   */
  titleId: string;
  /** The heading text. */
  title: string;
  /** `data-testid` for the panel, which is what the E2E suite scopes to. */
  testId: string;
  /** `data-testid` for the Close button. */
  closeTestId: string;
  /**
   * The way out: Escape, the Close button, and a click on the scrim all call
   * it. The caller closes itself and puts focus back on whatever opened it.
   */
  onClose: () => void;
  /** The dialog's body, below the header. */
  children: ComponentChildren;
}

export default function ModalDialogShell({
  titleId,
  title,
  testId,
  closeTestId,
  onClose,
  children,
}: ModalDialogShellProps) {
  const { dialogRef, onKeyDown } = useModalDialogKeys(onClose);

  return (
    // The scrim's click is the pointer-only way out; every keyboard path out
    // is handled by the key handler on the panel below.
    <div
      class="glass-scrim fixed inset-0 z-50 flex items-start justify-center px-4 pt-[min(15vh,96px)]"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        data-testid={testId}
        class="glass-elevated flex max-h-[70vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl"
        onKeyDown={onKeyDown}
      >
        <div class="flex items-center justify-between gap-3 border-b border-border-subtle px-4 py-3">
          <h2 id={titleId} class="text-sm font-semibold text-text-primary">
            {title}
          </h2>
          <button
            type="button"
            data-testid={closeTestId}
            onClick={onClose}
            class="rounded-lg px-2 py-1 text-xs font-medium text-text-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
          >
            Close
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
