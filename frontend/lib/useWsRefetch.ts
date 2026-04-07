/**
 * Hook that subscribes to WebSocket CRD events and triggers a debounced
 * REST re-fetch when any event arrives. Handles cleanup on unmount.
 *
 * @param fetchFn - The async function to call for re-fetching data
 * @param subscriptions - Array of [id, kind, namespace] tuples to subscribe to
 * @param debounceMs - Debounce delay in milliseconds before re-fetching
 */
import { useEffect, useRef } from "preact/hooks";
import { subscribe } from "@/lib/ws.ts";

export function useWsRefetch(
  fetchFn: () => Promise<void>,
  subscriptions: Array<[string, string, string]>,
  debounceMs: number,
): void {
  const refetchTimer = useRef<number | null>(null);

  useEffect(() => {
    const onEvent = () => {
      if (refetchTimer.current !== null) clearTimeout(refetchTimer.current);
      refetchTimer.current = globalThis.setTimeout(() => {
        refetchTimer.current = null;
        fetchFn();
      }, debounceMs) as unknown as number;
    };

    const unsubs = subscriptions.map(([id, kind, ns]) =>
      subscribe(id, kind, ns, onEvent)
    );

    return () => {
      unsubs.forEach((fn) => fn());
      if (refetchTimer.current !== null) clearTimeout(refetchTimer.current);
    };
  }, []);
}
