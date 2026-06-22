// Light/dark theme signal. Single runtime owner of light/dark mode.
// `initTheme()` in lib/themes.ts calls `applyTheme()` on TopBarV2 mount
// so the persisted choice is applied at page load.
import { signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

const STORAGE_KEY = "kc.theme";
export type ThemeMode = "dark" | "light";

// IS_BROWSER (not `typeof localStorage`): Deno DEFINES localStorage during
// SSR, but touching it on a read-only rootfs throws "Read-only file system
// (os error 30)" and crashes the server on boot. IS_BROWSER is false during
// SSR, so we never open the backing store there.
function initial(): ThemeMode {
  if (!IS_BROWSER) return "dark";
  return (localStorage.getItem(STORAGE_KEY) as ThemeMode) || "dark";
}

export const theme = signal<ThemeMode>(initial());

export function applyTheme(): void {
  if (typeof document === "undefined") return;
  document.documentElement.classList.toggle(
    "theme-light",
    theme.value === "light",
  );
}

export function toggleTheme(): void {
  theme.value = theme.value === "light" ? "dark" : "light";
  if (IS_BROWSER) {
    localStorage.setItem(STORAGE_KEY, theme.value);
  }
  applyTheme();
}
