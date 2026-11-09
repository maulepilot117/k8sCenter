import { useEffect } from "react";
import { useLatest } from "@/src/lib/use-latest.ts";

/** Warns the user before leaving the page while `dirty` is true. */
export function useDirtyGuard(dirty: boolean) {
  // The listener is registered once and reads the latest value through a
  // ref, so a change of `dirty` does not remove and re-add it.
  const dirtyRef = useLatest(dirty);
  useEffect(() => {
    // Checked when the effect runs, not once at module load: a module first
    // imported where there is no DOM (a server render, a test) would
    // otherwise keep answering "no browser" for the rest of the process.
    if (typeof document === "undefined") return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirtyRef.current) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);
}
