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
