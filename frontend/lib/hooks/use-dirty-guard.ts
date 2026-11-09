import type { ReadonlySignal } from "@preact/signals-core";
import { useEffect } from "react";

/** Warns the user before leaving the page when dirty is true. */
export function useDirtyGuard(dirty: ReadonlySignal<boolean>) {
  useEffect(() => {
    // Checked when the effect runs, not once at module load: a module first
    // imported where there is no DOM (a server render, a test) would
    // otherwise keep answering "no browser" for the rest of the process.
    if (typeof document === "undefined") return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty.value) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, [dirty]);
}
