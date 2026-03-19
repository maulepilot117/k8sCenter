import { useEffect, useRef } from "preact/hooks";
import { useSignal } from "@preact/signals";

export interface ConfirmDialogProps {
  title: string;
  message?: string;
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
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={message ? descId : undefined}
        class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl dark:bg-slate-800"
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id={titleId}
          class="text-lg font-semibold text-slate-900 dark:text-white"
        >
          {title}
        </h3>
        {message && (
          <p
            id={descId}
            class="mt-2 text-sm text-slate-600 dark:text-slate-400"
          >
            {message}
          </p>
        )}
        {typeToConfirm && (
          <div class="mt-4">
            <label class="block text-sm text-slate-600 dark:text-slate-400">
              Type <strong>{typeToConfirm}</strong> to confirm
            </label>
            <input
              data-autofocus
              type="text"
              value={input.value}
              onInput={(e) =>
                input.value = (e.target as HTMLInputElement).value}
              class="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
              placeholder={typeToConfirm}
            />
          </div>
        )}
        <div class="mt-6 flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            class="rounded-md border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={!canConfirm || loading}
            onClick={onConfirm}
            class={`rounded-md px-4 py-2 text-sm font-medium text-white disabled:opacity-50 ${
              danger
                ? "bg-red-600 hover:bg-red-700"
                : "bg-brand hover:bg-brand/90"
            }`}
          >
            {loading ? "..." : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
