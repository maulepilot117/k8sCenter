/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * Module-level signal is a process-global singleton in Deno; importing
 * this server-side would leak state across SSR requests.
 *
 * Shared namespace state consumed by TopBar (writes) and all resource
 * table islands (reads).
 */
import { effect, signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

const STORAGE_KEY = "k8scenter.selectedNamespace";

const stored = IS_BROWSER ? localStorage.getItem(STORAGE_KEY) : null;

/** Currently selected namespace. "all" = all namespaces. */
export const selectedNamespace = signal<string>(stored ?? "all");

// Persist selection changes so the namespace survives page changes + reloads
// (mirrors lib/cluster.ts). `value` can be null/"" when a detail panel clears
// the selection (NamespaceLimitsDashboard), and "all" is the default — in
// those cases clear the key so a reload falls back to "all" rather than
// persisting a bogus "null"/"all" string.
if (IS_BROWSER) {
  effect(() => {
    const ns = selectedNamespace.value;
    if (ns && ns !== "all") {
      localStorage.setItem(STORAGE_KEY, ns);
    } else {
      localStorage.removeItem(STORAGE_KEY);
    }
  });
}

/** Returns the currently selected namespace, or "default" during SSR / when "all" is selected. */
export function initialNamespace(): string {
  return IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
}
