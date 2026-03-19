import { useEffect } from "preact/hooks";
import { type Signal, useSignal } from "@preact/signals";

export interface ToastState {
  message: string;
  type: "success" | "error";
  ts: number;
}

/** Hook that manages toast state with auto-dismiss. Returns [signal, show]. */
export function useToast(duration = 4000) {
  const toast = useSignal<ToastState | null>(null);

  useEffect(() => {
    if (!toast.value) return;
    const id = setTimeout(() => {
      toast.value = null;
    }, duration);
    return () => clearTimeout(id);
  }, [toast.value]);

  const show = (message: string, type: "success" | "error") => {
    toast.value = { message, type, ts: Date.now() };
  };

  return { toast, show };
}

/** Renders a toast notification from a signal. */
export function Toast({ toast }: { toast: Signal<ToastState | null> }) {
  if (!toast.value) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      class={`fixed top-4 right-4 z-50 rounded-md px-4 py-3 text-sm shadow-lg ${
        toast.value.type === "success"
          ? "bg-green-600 text-white"
          : "bg-red-600 text-white"
      }`}
    >
      {toast.value.message}
    </div>
  );
}
