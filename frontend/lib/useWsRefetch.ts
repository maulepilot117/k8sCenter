/**
 * Hook that subscribes to WebSocket CRD events and triggers a debounced
 * REST re-fetch when any event arrives. Handles cleanup on unmount.
 *
 * Local cluster only. The resource WebSocket is fed by the LOCAL cluster's
 * informers whatever cluster is selected (remote clusters have no informers
 * and emit no events), so under a remote selection every event would be
 * local churn triggering a refetch of remote data. The hook subscribes to
 * nothing there, matching ResourceTable, and the page updates on Refresh.
 * A cluster switch reloads the page, so reading the selection once at mount
 * is enough.
 *
 * @param fetchFn - The async function to call for re-fetching data
 * @param subscriptions - Array of [id, kind, namespace] tuples to subscribe to
 * @param debounceMs - Debounce delay in milliseconds before re-fetching
 */
import { useEffect, useRef } from "preact/hooks";
import { subscribe } from "@/lib/ws.ts";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";

export function useWsRefetch(
  fetchFn: () => Promise<void>,
  subscriptions: Array<[string, string, string]>,
  debounceMs: number,
): void {
  const refetchTimer = useRef<number | null>(null);

  useEffect(() => {
    if (selectedCluster.value !== LOCAL_CLUSTER_ID) return;

    const onEvent = () => {
      if (refetchTimer.current !== null) clearTimeout(refetchTimer.current);
      refetchTimer.current = globalThis.setTimeout(() => {
        refetchTimer.current = null;
        fetchFn();
      }, debounceMs) as unknown as number;
    };

    const unsubs = subscriptions.map(([id, kind, ns]) =>
      subscribe(id, kind, ns, onEvent),
    );

    return () => {
      // Statement body, not an expression body: forEach discards whatever the
      // callback returns, so returning each unsubscribe's result reads as a
      // value someone intended to use.
      unsubs.forEach((fn) => {
        fn();
      });
      if (refetchTimer.current !== null) clearTimeout(refetchTimer.current);
    };
  }, []);
}
