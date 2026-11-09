import { useEffect } from "react";
import { hasDom } from "@/src/lib/has-dom.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

/** Warns the user before leaving the page while `dirty` is true. */
export function useDirtyGuard(dirty: boolean) {
  const dirtyRef = useLatest(dirty);
  useEffect(() => {
    if (!hasDom()) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirtyRef.current) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);
}
