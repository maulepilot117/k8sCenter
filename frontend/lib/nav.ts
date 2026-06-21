// Shared signal for the collapsible secondary nav panel.
// Mirrors the pattern used by lib/namespace.ts (selectedNamespace).
import { signal } from "@preact/signals";

const STORAGE_KEY = "kc.navCollapsed";

function initial(): boolean {
  if (typeof localStorage === "undefined") return false;
  return localStorage.getItem(STORAGE_KEY) === "1";
}

/** true = secondary nav panel hidden (rail-only). Persisted across reloads. */
export const navCollapsed = signal<boolean>(initial());

export function toggleNav(): void {
  navCollapsed.value = !navCollapsed.value;
  if (typeof localStorage !== "undefined") {
    localStorage.setItem(STORAGE_KEY, navCollapsed.value ? "1" : "0");
  }
}

/** CSS width for the secondary nav grid column. */
export function panelWidth(): string {
  return navCollapsed.value ? "0px" : "var(--panel-width, 250px)";
}
