import type { ReadonlySignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";

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
