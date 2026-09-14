import type { ReadonlySignal } from "@preact/signals";
import { useEffect } from "preact/hooks";

// Inlined rather than imported from "fresh/runtime" — see lib/nav.ts for why:
// this file is shared by both the Fresh tree and the Astro/Bun port, and the
// bare specifier would otherwise resolve to the always-false test shim under
// Astro. Value-neutral for Fresh: identical to @fresh/core's own IS_BROWSER.
const IS_BROWSER = typeof document !== "undefined";

/** Warns the user before leaving the page when dirty is true. */
export function useDirtyGuard(dirty: ReadonlySignal<boolean>) {
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty.value) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);
}
