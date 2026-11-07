import { signal } from "@preact/signals-core";

/**
 * The app-wide toast queue. ToastProvider (mounted once in ChromeLayout)
 * renders it; any island calls `showToast`.
 *
 * Like every signal store, this is a module-level singleton: on the server
 * it would be shared by every request, so it must only be reached through
 * an island (check-no-signal-store-in-ssr.ts enforces that).
 */

export interface Toast {
  id: number;
  message: string;
  type: "success" | "error" | "info";
}

let nextId = 0;

export const toasts = signal<Toast[]>([]);

/** Show a toast notification. Auto-dismisses after 5 seconds. */
export function showToast(message: string, type: Toast["type"] = "info"): void {
  const id = nextId++;
  toasts.value = [...toasts.value, { id, message, type }];
  setTimeout(() => dismissToast(id), 5000);
}

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}
