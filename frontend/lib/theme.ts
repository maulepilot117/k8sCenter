// Light/dark theme signal. Single runtime owner of light/dark mode.
// `initTheme()` in lib/themes.ts calls `applyTheme()` on TopBarV2 mount
// so the persisted choice is applied at page load.
import { signal } from "@preact/signals";

const STORAGE_KEY = "kc.theme";
export type ThemeMode = "dark" | "light";

function initial(): ThemeMode {
  if (typeof localStorage === "undefined") return "dark";
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
  if (typeof localStorage !== "undefined") {
    localStorage.setItem(STORAGE_KEY, theme.value);
  }
  applyTheme();
}
