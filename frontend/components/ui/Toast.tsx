import { signal } from "@preact/signals";

type ToastType = "success" | "error" | "info" | "warning";

interface ToastMessage {
  id: number;
  type: ToastType;
  message: string;
}

let nextId = 0;
export const toasts = signal<ToastMessage[]>([]);

export function showToast(type: ToastType, message: string, durationMs = 5000) {
  const id = nextId++;
  toasts.value = [...toasts.value, { id, type, message }];
  setTimeout(() => {
    toasts.value = toasts.value.filter((t) => t.id !== id);
  }, durationMs);
}

const typeClasses: Record<ToastType, string> = {
  success:
    "bg-green-50 text-green-800 border-green-200 dark:bg-green-900/30 dark:text-green-400 dark:border-green-800",
  error:
    "bg-red-50 text-red-800 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-800",
  info:
    "bg-blue-50 text-blue-800 border-blue-200 dark:bg-blue-900/30 dark:text-blue-400 dark:border-blue-800",
  warning:
    "bg-amber-50 text-amber-800 border-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:border-amber-800",
};

const typeIcons: Record<ToastType, string> = {
  success: "\u2713",
  error: "\u2717",
  info: "\u2139",
  warning: "\u26A0",
};

export function ToastContainer() {
  return (
    <div class="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
      {toasts.value.map((toast) => (
        <div
          key={toast.id}
          class={`flex items-center gap-2 rounded-lg border px-4 py-3 text-sm shadow-lg transition-all ${
            typeClasses[toast.type]
          }`}
        >
          <span class="text-base">{typeIcons[toast.type]}</span>
          <span>{toast.message}</span>
          <button
            type="button"
            class="ml-2 opacity-60 hover:opacity-100"
            onClick={() => {
              toasts.value = toasts.value.filter((t) => t.id !== toast.id);
            }}
          >
            \u2715
          </button>
        </div>
      ))}
    </div>
  );
}
