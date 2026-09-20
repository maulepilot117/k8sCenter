import type { Ref } from "preact";

/**
 * The dashboard's edit controls: one button to enter edit mode, and the two
 * ways out of it.
 *
 * Presentation only. It decides nothing -- not whether Save is offered, not
 * what Cancel confirms -- because all of that is the edit session's, which is
 * unit tested and this is not (D-10). What it owns is which buttons exist in
 * which mode, and that a user cannot reach a control that would do nothing.
 *
 * `Add widget` (D16) and `Reset` (D17) join it here. They are deliberately
 * absent rather than disabled: a button that is visible in every screenshot
 * and never does anything is a worse promise than one that has not shipped.
 */

/** Matches the pill styling the time-range buttons beside it already use. */
const BUTTON_BASE = {
  padding: "7px 14px",
  borderRadius: "8px",
  fontSize: "12px",
  fontWeight: 500,
  transition: "background 0.15s, color 0.15s, opacity 0.15s",
} as const;

export interface EditToolbarProps {
  /** Whether an edit session is open. */
  editing: boolean;
  /** Whether that session has anything worth saving. */
  dirty: boolean;
  /** A save is in flight: both exits are held so neither races it. */
  saving: boolean;
  /**
   * Why this layout cannot be written at all, when it cannot be.
   *
   * Set after a save the server refused: the client has to re-read before it
   * may claim a revision again, so a second attempt would fail differently and
   * less clearly than the first. Save stays visible -- the user's arrangement
   * is still on screen and still theirs -- and stops being clickable, with
   * this as its title.
   */
  saveBlockedReason?: string;
  /**
   * Editing cannot be offered at all -- storage is down, or the stored layout
   * has not landed yet. `disabledReason` says which, as the button's title.
   */
  disabled: boolean;
  disabledReason?: string;
  /**
   * The "Edit layout" button, so the caller can put focus back on it when
   * Escape ends a session: the widget that had focus stops being a tab stop
   * the moment editing ends, and focus would otherwise fall to the document.
   */
  editButtonRef?: Ref<HTMLButtonElement>;
  /**
   * The Cancel button, so the caller can move focus here when editing starts.
   * The Edit button it replaces is gone by then, and focus would otherwise
   * fall to the document.
   */
  cancelButtonRef?: Ref<HTMLButtonElement>;
  onEdit: () => void;
  onCancel: () => void;
  onSave: () => void;
}

export default function EditToolbar({
  editing,
  dirty,
  saving,
  saveBlockedReason,
  disabled,
  disabledReason,
  editButtonRef,
  cancelButtonRef,
  onEdit,
  onCancel,
  onSave,
}: EditToolbarProps) {
  if (!editing) {
    return (
      <button
        ref={editButtonRef}
        type="button"
        data-testid="edit-layout"
        // No aria-pressed. It was a toggle while "Done" was the way out; now
        // the button is replaced by Cancel and Save, so it can never be in the
        // pressed state -- and a toggle that is always off is a lie told to
        // assistive technology rather than a state it can rely on.
        disabled={disabled}
        title={disabledReason}
        onClick={onEdit}
        style={{
          ...BUTTON_BASE,
          border: "1px solid var(--glass-border)",
          cursor: disabled ? "not-allowed" : "pointer",
          opacity: disabled ? 0.5 : 1,
          background: "var(--glass-surface)",
          color: "var(--text-muted)",
        }}
      >
        Edit layout
      </button>
    );
  }

  const saveBlocked = saveBlockedReason !== undefined;
  const saveOff = !dirty || saving || saveBlocked;

  return (
    <div
      data-testid="edit-toolbar"
      // A named group, so a screen reader user arriving on Save by keyboard is
      // told what these two buttons belong to rather than meeting them bare.
      role="group"
      aria-label="Dashboard layout editing"
      style={{ display: "flex", alignItems: "center", gap: "8px" }}
    >
      <button
        ref={cancelButtonRef}
        type="button"
        data-testid="cancel-edit"
        // Held during a save so the two cannot race: cancelling mid-flight
        // would restore the layout underneath a write that still lands.
        disabled={saving}
        onClick={onCancel}
        style={{
          ...BUTTON_BASE,
          border: "1px solid var(--glass-border)",
          cursor: saving ? "not-allowed" : "pointer",
          opacity: saving ? 0.5 : 1,
          background: "var(--glass-surface)",
          color: "var(--text-muted)",
        }}
      >
        Cancel
      </button>
      <button
        type="button"
        data-testid="save-layout"
        // Nothing to write, a write already in flight, or a write the client
        // is no longer in a position to make. An enabled Save that cannot
        // succeed trains people to ignore the one that can.
        disabled={!dirty || saving || saveBlocked}
        title={
          saveBlocked
            ? saveBlockedReason
            : !dirty && !saving
              ? "Move or resize a widget to save a layout"
              : undefined
        }
        onClick={onSave}
        style={{
          ...BUTTON_BASE,
          border: "1px solid transparent",
          cursor: saveOff ? "not-allowed" : "pointer",
          opacity: saveOff ? 0.5 : 1,
          background: "var(--accent)",
          color: "var(--bg-base)",
        }}
      >
        {saving ? "Saving..." : "Save"}
      </button>
    </div>
  );
}
