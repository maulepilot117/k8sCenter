import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";

type Theme = "light" | "dark" | "system";

/**
 * Dark mode toggle button for the TopBar.
 * Persists choice in localStorage. Defaults to OS preference.
 */
export default function ThemeToggle() {
  const theme = useSignal<Theme>("system");

  useEffect(() => {
    if (!IS_BROWSER) return;
    const stored = localStorage.getItem("theme") as Theme | null;
    theme.value = stored ?? "system";
    applyTheme(theme.value);

    // Listen for OS preference changes so "system" stays in sync
    const mq = globalThis.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      if (theme.value === "system") applyTheme("system");
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  function cycle() {
    const order: Theme[] = ["system", "light", "dark"];
    const next = order[(order.indexOf(theme.value) + 1) % order.length];
    theme.value = next;
    localStorage.setItem("theme", next);
    applyTheme(next);
  }

  return (
    <button
      type="button"
      onClick={cycle}
      title={`Theme: ${theme.value}`}
      class="rounded-md p-1.5 text-slate-500 hover:bg-slate-100 hover:text-slate-700 dark:text-slate-400 dark:hover:bg-slate-700 dark:hover:text-slate-200"
    >
      {theme.value === "dark"
        ? (
          <svg class="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
            <path d="M17.293 13.293A8 8 0 016.707 2.707a8.001 8.001 0 1010.586 10.586z" />
          </svg>
        )
        : theme.value === "light"
        ? (
          <svg
            class="h-5 w-5"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <circle cx="10" cy="10" r="3" />
            <path d="M10 2v2m0 12v2m8-8h-2M4 10H2m13.66-5.66l-1.42 1.42M5.76 14.24l-1.42 1.42m11.32 0l-1.42-1.42M5.76 5.76L4.34 4.34" />
          </svg>
        )
        : (
          <svg
            class="h-5 w-5"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <rect x="3" y="4" width="14" height="10" rx="1" />
            <path d="M7 17h6" />
          </svg>
        )}
    </button>
  );
}

/** Apply theme to <html> element. */
function applyTheme(theme: Theme) {
  if (!IS_BROWSER) return;
  const root = document.documentElement;
  if (theme === "dark") {
    root.classList.add("dark");
  } else if (theme === "light") {
    root.classList.remove("dark");
  } else {
    // System preference
    if (globalThis.matchMedia("(prefers-color-scheme: dark)").matches) {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
  }
}
