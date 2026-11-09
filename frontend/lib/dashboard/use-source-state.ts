import { useCallback, useMemo, useRef, useSyncExternalStore } from "react";
import { dashboardData, type SourceState } from "@/lib/dashboard/data.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

/**
 * Reads one dashboard data source in a component and re-renders it when that
 * source's state changes, and only then: a widget reading the summary does not
 * re-render when the trends land.
 *
 * `dashboardData.state(key)` reads the same value but does not subscribe a
 * component on React APIs, so in render use this; `state()` stays right for
 * event handlers, effects and pure helpers.
 */
export function useSourceState<T = unknown>(key: string): SourceState<T> {
  return useSignalValue(dashboardData.signalFor(key)) as SourceState<T>;
}

/**
 * The state of every key in `keys`, re-rendering the component when any of
 * them changes: for a component whose key list varies with its input (the
 * widget host reads a widget's sources under its params), where one
 * `useSourceState` per key would call a varying number of hooks.
 *
 * Repeated keys are read once. The returned map keeps its identity until a
 * state in it changes, as `useSyncExternalStore` requires of a snapshot; a
 * repeated key once broke that and the host re-rendered forever.
 */
export function useSourceStates(
  keys: readonly string[],
): ReadonlyMap<string, SourceState> {
  const keyId = JSON.stringify([...new Set(keys)]);
  const unique = useMemo(() => JSON.parse(keyId) as string[], [keyId]);
  const subscribe = useCallback(
    (onChange: () => void) => {
      const unsubscribers = unique.map((key) => {
        // `subscribe` calls back once immediately with the current value,
        // which is not a change.
        let initial = true;
        return dashboardData.signalFor(key).subscribe(() => {
          if (initial) {
            initial = false;
            return;
          }
          onChange();
        });
      });
      return () => {
        for (const off of unsubscribers) off();
      };
    },
    [unique],
  );
  const cache = useRef<Map<string, SourceState> | null>(null);
  const read = () => {
    const prev = cache.current;
    if (
      prev !== null &&
      prev.size === unique.length &&
      unique.every(
        (key) => prev.get(key) === dashboardData.signalFor(key).peek(),
      )
    ) {
      return prev;
    }
    const next = new Map<string, SourceState>();
    for (const key of unique)
      next.set(key, dashboardData.signalFor(key).peek());
    cache.current = next;
    return next;
  };
  return useSyncExternalStore(subscribe, read, read);
}
