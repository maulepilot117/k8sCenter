import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";

const shortcuts = [
  { key: "?", description: "Show keyboard shortcuts" },
  { key: "/", description: "Focus search" },
  { key: "Escape", description: "Close modal / blur focus" },
];

/**
 * Global keyboard shortcut handler + help modal.
 * Mount once in _layout.tsx.
 */
export default function KeyboardShortcuts() {
  const showHelp = useSignal(false);

  useEffect(() => {
    if (!IS_BROWSER) return;

    function handler(e: KeyboardEvent) {
      // Ignore when typing in inputs
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;

      switch (e.key) {
        case "?":
          e.preventDefault();
          showHelp.value = !showHelp.value;
          break;
        case "/":
          e.preventDefault();
          {
            const search = document.querySelector<HTMLInputElement>(
              "[data-search-input]",
            );
            search?.focus();
          }
          break;
        case "Escape":
          showHelp.value = false;
          (document.activeElement as HTMLElement)?.blur?.();
          break;
      }
    }

    globalThis.addEventListener("keydown", handler);
    return () => globalThis.removeEventListener("keydown", handler);
  }, []);

  if (!showHelp.value) return null;

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={() => {
        showHelp.value = false;
      }}
    >
      <div
        class="w-full max-w-sm rounded-lg border border-slate-200 bg-white p-6 shadow-xl dark:border-slate-700 dark:bg-slate-800"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 class="mb-4 text-lg font-semibold text-slate-900 dark:text-white">
          Keyboard Shortcuts
        </h2>
        <div class="space-y-2">
          {shortcuts.map((s) => (
            <div
              key={s.key}
              class="flex items-center justify-between text-sm"
            >
              <span class="text-slate-600 dark:text-slate-300">
                {s.description}
              </span>
              <kbd class="rounded bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-700 dark:bg-slate-700 dark:text-slate-300">
                {s.key}
              </kbd>
            </div>
          ))}
        </div>
        <p class="mt-4 text-xs text-slate-400">
          Press <kbd class="font-mono">Escape</kbd> to close
        </p>
      </div>
    </div>
  );
}
