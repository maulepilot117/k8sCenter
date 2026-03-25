import { signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

interface Toast {
  id: number;
  message: string;
  type: "success" | "error" | "info";
}

let nextId = 0;
const toasts = signal<Toast[]>([]);

/** Show a toast notification. Auto-dismisses after 5 seconds. */
export function showToast(
  message: string,
  type: "success" | "error" | "info" = "info",
) {
  const id = nextId++;
  toasts.value = [...toasts.value, { id, message, type }];
  setTimeout(() => {
    toasts.value = toasts.value.filter((t) => t.id !== id);
  }, 5000);
}

function dismiss(id: number) {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

const typeStyles = {
  success:
    "border-green-400 bg-green-50 text-green-800 dark:border-green-600 dark:bg-green-900/30 dark:text-green-300",
  error:
    "border-red-400 bg-red-50 text-red-800 dark:border-red-600 dark:bg-red-900/30 dark:text-red-300",
  info:
    "border-blue-400 bg-blue-50 text-blue-800 dark:border-blue-600 dark:bg-blue-900/30 dark:text-blue-300",
};

/**
 * Toast container — renders active toasts in the bottom-right corner.
 * Mount once in _layout.tsx.
 */
export default function ToastProvider() {
  if (!IS_BROWSER) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      class="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2"
    >
      {toasts.value.map((toast) => (
        <div
          key={toast.id}
          class={`pointer-events-auto flex items-center gap-3 rounded-lg border px-4 py-3 text-sm shadow-lg ${
            typeStyles[toast.type]
          }`}
        >
          <span class="flex-1">{toast.message}</span>
          <button
            type="button"
            onClick={() => dismiss(toast.id)}
            class="ml-2 opacity-60 hover:opacity-100"
          >
            <svg class="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
              <path d="M4.646 4.646a.5.5 0 01.708 0L8 7.293l2.646-2.647a.5.5 0 01.708.708L8.707 8l2.647 2.646a.5.5 0 01-.708.708L8 8.707l-2.646 2.647a.5.5 0 01-.708-.708L7.293 8 4.646 5.354a.5.5 0 010-.708z" />
            </svg>
          </button>
        </div>
      ))}
    </div>
  );
}
