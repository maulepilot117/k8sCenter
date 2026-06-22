// Shared signal for the collapsible secondary nav panel.
// Mirrors the pattern used by lib/namespace.ts (selectedNamespace).
import { signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

const STORAGE_KEY = "kc.navCollapsed";

// IS_BROWSER (not `typeof localStorage`): Deno DEFINES localStorage during
// SSR, but touching it on a read-only rootfs throws "Read-only file system
// (os error 30)" and crashes the server on boot. IS_BROWSER is false during
// SSR, so we never open the backing store there.
function initial(): boolean {
  if (!IS_BROWSER) return false;
  return localStorage.getItem(STORAGE_KEY) === "1";
}

/** true = secondary nav panel hidden (rail-only). Persisted across reloads. */
export const navCollapsed = signal<boolean>(initial());

export function toggleNav(): void {
  navCollapsed.value = !navCollapsed.value;
  if (IS_BROWSER) {
    localStorage.setItem(STORAGE_KEY, navCollapsed.value ? "1" : "0");
  }
}

/** CSS width for the secondary nav grid column. */
export function panelWidth(): string {
  return navCollapsed.value ? "0px" : "var(--panel-width, 250px)";
}
