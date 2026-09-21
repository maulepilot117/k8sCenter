import { useSignal } from "@preact/signals";
import { useLayoutEffect, useRef } from "preact/hooks";
import ModalDialogShell from "@/components/dashboard/ModalDialogShell.tsx";
// Every rule this dialog enforces lives in lib/, under test, because this repo
// has no component test harness (D-10) and each of them mirrors a rule the
// server enforces on save. Do not move them back into this file: a drift
// between the two is a dialog that green-lights a value the server then
// rejects, and a rejected save costs the user their whole arrangement.
import {
  duplicatePlacementOf,
  missingParamKeys,
  paramValueError,
} from "@/lib/dashboard/params.ts";
import type { LayoutItem, WidgetDef } from "@/lib/dashboard/types.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";

/**
 * The values a parameterized widget needs, collected before it is placed and
 * changeable afterwards.
 *
 * It exists because the palette used to place a widget the instant it was
 * chosen, and the placement helper never wrote a `params` field -- so the
 * server's parameter validation, which has been there since P3, had nothing
 * to validate. This is the step between the two.
 *
 * Two jobs, one surface:
 *
 *  - **Collect.** Opened from the palette with no values, it places nothing
 *    until it is confirmed and nothing at all if it is cancelled. That is the
 *    whole reason it is a separate step rather than a form on the card: a
 *    widget placed first and configured second is a widget that exists in a
 *    half-state the layout can be saved in.
 *  - **Re-point.** Opened from a placed widget in edit mode, pre-filled with
 *    its current values. Without this the only repair for a widget pointed at
 *    a namespace that has been deleted, or that the user has lost access to,
 *    is to remove it and add it again -- which loses its position and size on
 *    a dashboard the user arranged deliberately.
 *
 * A modal dialog wearing `ModalDialogShell`, the same shell the palette and
 * the copy dialog wear, so it owes the same contract: Escape closes, the scrim
 * closes, Tab stays inside, and Ctrl/Cmd+K cannot stack the command palette on
 * top of it. `CommandPalette.tsx` is the prior art the shell was drawn from;
 * `SavedViews.tsx` deliberately is not -- it is a `role="menu"` with no Escape
 * handling, no outside-click close and no focus management.
 *
 * Confirming returns the values. Cancelling returns nothing, and the caller
 * treats that as "place no widget" rather than "place it with defaults".
 */

/** What the caller gets back. A cancel calls `onCancel` instead. */
export type WidgetParamValues = Record<string, string>;

export interface WidgetParamDialogProps {
  /** The widget whose `params` declaration decides which fields appear. */
  def: WidgetDef;
  /**
   * The layout as it stands, which is what a confirmed value is checked
   * against for a duplicate. The server refuses two placements of one widget
   * with identical parameters, so offering that here would produce an
   * arrangement that cannot be saved.
   */
  placed: readonly LayoutItem[];
  /**
   * The placement being re-pointed, or undefined when adding a new one.
   *
   * It is excluded from the duplicate check -- confirming a widget's own
   * current namespace unchanged must not report it as a collision with
   * itself -- and it is what pre-fills the fields.
   */
  editing?: LayoutItem;
  onConfirm: (values: WidgetParamValues) => void;
  onCancel: () => void;
}

export default function WidgetParamDialog({
  def,
  placed,
  editing,
  onConfirm,
  onCancel,
}: WidgetParamDialogProps) {
  const declared = def.params ?? {};
  const keys = Object.keys(declared);

  const values = useSignal<WidgetParamValues>({
    ...Object.fromEntries(keys.map((k) => [k, ""])),
    ...(editing?.params ?? {}),
  });
  /** The refusal under the fields, or null. Cleared by any edit, because a
   * message about a value the user has already changed is noise. */
  const problem = useSignal<string | null>(null);
  const firstField = useRef<HTMLSelectElement>(null);

  // The namespace picker's options. Fetched here rather than passed in: this
  // dialog is mounted from a click and unmounted on the way out, so the read
  // is made by someone who is about to use it, and a failure leaves the list
  // at its "default" fallback rather than blocking the dialog.
  const namespaces = useNamespaces();

  // On the first field, not on Cancel: every field is something to fill in and
  // Cancel is the way out, so opening on the exit would open on the one
  // control the user did not press a button to reach.
  //
  // A layout effect, not an effect, for the reason `WidgetPalette` records:
  // this dialog is mounted from the click that opens it, and a passive effect
  // runs after that click has finished -- late enough for the opener to take
  // focus back, leaving the dialog open with the keyboard outside it and
  // Escape unreachable.
  useLayoutEffect(() => {
    firstField.current?.focus();
  }, []);

  const title = editing ? `Change what ${def.title} reads` : `Add ${def.title}`;

  /**
   * Checks every declared value and hands them back, or says why not.
   *
   * Both checks mirror the server, and both have to happen HERE rather than
   * on the way back from a rejected save: the layout is not written until the
   * user presses Save, so a refusal that arrives from the server takes the
   * whole arrangement with it, while a refusal here costs one field.
   */
  function confirm() {
    const next: WidgetParamValues = Object.fromEntries(
      keys.map((k) => [k, (values.value[k] ?? "").trim()]),
    );

    // Unfilled fields first, and named. One field could report this through
    // `paramValueError`'s own empty-value message, but a widget taking two --
    // the namespace-then-service case a later unit adds -- would then say
    // "choose a value" without saying which, on a dialog where the user can
    // see two empty selects.
    const missing = missingParamKeys(declared, next);
    if (missing.length > 0) {
      problem.value = `Choose a ${missing.join(" and a ")}.`;
      return;
    }

    for (const key of keys) {
      const error = paramValueError(next[key], declared[key] ?? []);
      if (error !== null) {
        problem.value = error;
        return;
      }
    }

    const clash = duplicatePlacementOf(
      placed,
      def.id,
      next,
      editing?.instanceId,
    );
    if (clash !== null) {
      problem.value =
        `${def.title} is already on this dashboard with the same values. ` +
        "Choose different ones, or change the card you already have.";
      return;
    }

    onConfirm(next);
  }

  return (
    <ModalDialogShell
      titleId="widget-param-title"
      title={title}
      testId="widget-param-dialog"
      closeTestId="close-widget-params"
      onClose={onCancel}
    >
      <div class="flex flex-col gap-3 px-4 py-3">
        {keys.map((key, index) => {
          const allowed = declared[key] ?? [];
          // An open-valued key is a namespace today, and the cluster's own
          // namespace list is the only sane source of options for one -- so
          // both branches render a select and neither renders a free-text
          // field. That is D-8 made structural rather than promised: there is
          // no control here into which a query or a URL can be typed.
          const options = allowed.length > 0 ? [...allowed] : namespaces.value;
          const current = values.value[key] ?? "";
          return (
            <label key={key} class="flex flex-col gap-1">
              <span class="text-xs font-medium capitalize text-text-secondary">
                {key}
              </span>
              <select
                ref={index === 0 ? firstField : undefined}
                data-testid={`widget-param-${key}`}
                value={current}
                onChange={(e) => {
                  values.value = {
                    ...values.value,
                    [key]: (e.target as HTMLSelectElement).value,
                  };
                  problem.value = null;
                }}
                class="w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
              >
                {/* A placeholder row, so an unfilled field reads as unfilled
                    rather than as the first namespace in the list. It carries
                    an empty value, which `paramValueError` refuses. */}
                <option value="">Choose a {key}...</option>
                {/* A value the widget already carries that is not in the list
                    -- the namespace was deleted, or the list came back short
                    -- is offered anyway, so re-opening this dialog on a broken
                    widget shows what it is pointed at instead of silently
                    resetting the field. */}
                {(options.includes(current) || current === ""
                  ? options
                  : [current, ...options]
                ).map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </label>
          );
        })}

        {problem.value !== null && (
          <p
            data-testid="widget-param-error"
            role="alert"
            class="text-xs text-danger"
          >
            {problem.value}
          </p>
        )}
      </div>

      <div class="flex justify-end gap-2 border-t border-border-subtle px-4 py-3">
        <button
          type="button"
          data-testid="cancel-widget-params"
          onClick={onCancel}
          class="rounded-lg px-3 py-1.5 text-xs font-medium text-text-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
        >
          Cancel
        </button>
        <button
          type="button"
          data-testid="confirm-widget-params"
          onClick={confirm}
          class="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
          // The same exception EditToolbar.tsx and ConfirmDialog.tsx already
          // make for this one colour: the theme token is `--bg-base`, whose
          // utility would be `text-base` -- which Tailwind already owns as a
          // font size. There is no colour utility to spell it with.
          style={{ color: "var(--bg-base)" }}
        >
          {editing ? "Update" : "Add widget"}
        </button>
      </div>
    </ModalDialogShell>
  );
}
