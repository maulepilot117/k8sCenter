import type { ReadonlySignal } from "@preact/signals-core";
import { useCallback, useSyncExternalStore } from "react";

/**
 * Reads a shared store signal in a component and re-renders it when the
 * signal changes. Every store module (cluster, namespace, auth, pins, counts,
 * layout, toasts) keeps its state in @preact/signals-core signals; this is
 * the one way a component on React APIs reads them.
 *
 * - The snapshot is read with `peek()`, so reading here never also subscribes
 *   the component through @preact/signals' own render tracking. One write
 *   causes one render.
 * - The server snapshot is the signal's current value, not a fixed default.
 *   With a default, the hydration render would run its effects against it:
 *   fetching all namespaces, or the local cluster while a remote one is
 *   selected. Markup that depends on a value the browser restores from
 *   storage still differs between server and client, so it belongs behind
 *   `useIsClient()`.
 */
export function useSignalValue<T>(source: ReadonlySignal<T>): T {
  const subscribe = useCallback(
    (onChange: () => void) => {
      // `subscribe` calls back once immediately with the current value.
      // That is not a change, so skip it.
      let initial = true;
      return source.subscribe(() => {
        if (initial) {
          initial = false;
          return;
        }
        onChange();
      });
    },
    [source],
  );
  const read = () => source.peek();
  return useSyncExternalStore(subscribe, read, read);
}
