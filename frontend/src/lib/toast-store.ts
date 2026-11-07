import { signal } from "@preact/signals-core";

export interface Toast {
  id: number;
  message: string;
  type: "success" | "error" | "info";
}

let nextId = 0;

export const toasts = signal<Toast[]>([]);

export function showToast(message: string, type: Toast["type"] = "info"): void {
  const id = nextId++;
  toasts.value = [...toasts.value, { id, message, type }];
  setTimeout(() => dismissToast(id), 5000);
}

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}
