import { dismissToast, type Toast, toasts } from "@/src/lib/toast-store.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

export { showToast } from "@/src/lib/toast-store.ts";

// Toast chrome is glass (.glass-elevated); type identity comes from the
// text color and a solid left accent edge instead of a tinted fill.
const typeStyles: Record<Toast["type"], Record<string, string>> = {
  success: {
    color: "var(--success)",
    borderLeft: "3px solid var(--success)",
  },
  error: {
    color: "var(--error)",
    borderLeft: "3px solid var(--error)",
  },
  info: {
    color: "var(--accent)",
    borderLeft: "3px solid var(--accent)",
  },
};

/**
 * Toast container — renders active toasts in the bottom-right corner.
 * Mount once in _layout.tsx.
 */
export default function ToastProvider() {
  // Deliberately NOT `if (!IS_BROWSER) return null`. Astro renders an island
  // on the server and hydrates that markup in place; a provider that returned
  // null server-side left an <astro-island> with no children, which never
  // hydrated -- so the container was absent and every showToast in the app was
  // a no-op. Rendering the empty container on both sides costs one hidden div
  // and is what makes hydration possible at all. `toasts` is empty during SSR
  // by construction, so the two renders agree.
  //
  // Read through useSignalValue rather than `toasts.value`: the bare read
  // only re-renders if @preact/signals has patched Preact on this page,
  // which nothing guarantees once this island stops importing it.
  const active = useSignalValue(toasts);
  return (
    <div
      role="status"
      aria-live="polite"
      class="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2"
    >
      {active.map((toast) => (
        <div
          key={toast.id}
          class="glass-elevated pointer-events-auto flex items-center gap-3 rounded-xl px-4 py-3 text-sm"
          style={typeStyles[toast.type]}
        >
          <span class="flex-1">{toast.message}</span>
          <button
            type="button"
            onClick={() => dismissToast(toast.id)}
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
