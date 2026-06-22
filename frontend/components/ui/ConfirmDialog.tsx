import type { ComponentChildren } from "preact";
import { useEffect, useRef } from "preact/hooks";
import { useSignal } from "@preact/signals";

export interface ConfirmDialogProps {
  title: string;
  message?: ComponentChildren;
  confirmLabel: string;
  danger?: boolean;
  /** If provided, user must type this string to enable the confirm button. */
  typeToConfirm?: string;
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

let dialogIdCounter = 0;

export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  danger = false,
  typeToConfirm,
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const input = useSignal("");
  const dialogRef = useRef<HTMLDivElement>(null);
  const idPrefix = useRef(`confirm-dialog-${++dialogIdCounter}`);
  const titleId = `${idPrefix.current}-title`;
  const descId = `${idPrefix.current}-desc`;

  const canConfirm = !typeToConfirm || input.value === typeToConfirm;

  // Escape key handler
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    globalThis.addEventListener("keydown", handler);

    // Auto-focus the input or the confirm button
    const el = dialogRef.current;
    if (el) {
      const focusTarget = el.querySelector<HTMLElement>(
        "[data-autofocus], input",
      );
      (focusTarget ?? el.querySelector<HTMLElement>("button:last-of-type"))
        ?.focus();
    }

    return () => globalThis.removeEventListener("keydown", handler);
  }, [onCancel]);

  return (
    <div
      class="glass-scrim fixed inset-0 z-50 flex items-center justify-center"
      onClick={onCancel}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={message ? descId : undefined}
        class="glass-elevated w-full max-w-md rounded-2xl p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id={titleId}
          class="text-lg font-semibold text-text-primary"
        >
          {title}
        </h3>
        {message && (
          <p
            id={descId}
            class="mt-2 text-sm text-text-secondary"
          >
            {message}
          </p>
        )}
        {typeToConfirm && (
          <div class="mt-4">
            <label class="block text-sm text-text-secondary">
              Type <strong>{typeToConfirm}</strong> to confirm
            </label>
            <input
              data-autofocus
              type="text"
              value={input.value}
              onInput={(e) =>
                input.value = (e.target as HTMLInputElement).value}
              class="mt-1 w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
              placeholder={typeToConfirm}
            />
          </div>
        )}
        <div class="mt-6 flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            class="rounded-md border border-border-primary px-4 py-2 text-sm font-medium text-text-secondary hover:bg-hover"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={!canConfirm || loading}
            onClick={onConfirm}
            class={`cursor-pointer rounded-md px-4 py-2 text-sm font-medium disabled:opacity-50 ${
              danger
                ? "bg-error hover:bg-error/90"
                : "bg-brand hover:bg-brand/90"
            }`}
            style={{ color: "var(--bg-base)" }}
          >
            {loading ? "..." : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
