/**
 * The dashboard's data layer.
 *
 * Central fetching was correct for six fixed widgets and is wrong for a
 * catalog of thirty-nine optional ones: the page would issue every request
 * whether or not the widget is on the layout. Here a widget declares the
 * source keys it needs, the cache fetches each key at most once per cycle, and
 * a key nobody asked for is never requested.
 *
 * The cache is built by a factory over injected fetchers so the dedupe and
 * failure behavior can be unit-tested. lib/api.ts has no injectable fetch, and
 * lib/preferences_test.ts shows what testing around that looks like.
 */
import type { Signal } from "@preact/signals";
import { signal } from "@preact/signals";
import { api } from "@/lib/api.ts";
import type { DataSourceKey } from "./types.ts";

export interface SourceState<T = unknown> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

/** A fetcher receives the abort signal and the active time range. Sources that
 * ignore the range simply do not read it. */
export type SourceFetcher = (
  signal: AbortSignal,
  range: string,
) => Promise<unknown>;

const IDLE: SourceState = { data: null, error: null, loading: false };

/** Sources whose response depends on the selected time range. Re-ensuring one
 * of these under a new range refetches; the others do not. */
const RANGE_SENSITIVE: ReadonlySet<string> = new Set(["dashboard-trends"]);

/** Matches the pre-registry dashboard's 60s interval. */
export const DASHBOARD_REFRESH_MS = 60_000;

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

export interface SourceCache {
  state<T = unknown>(key: DataSourceKey): SourceState<T>;
  signalFor(key: DataSourceKey): Signal<SourceState>;
  ensure(keys: readonly DataSourceKey[], range: string): void;
  refresh(): void;
  abort(): void;
  /**
   * Starts the periodic refresh and returns its stop function. Starting twice
   * replaces the first interval rather than stacking a second one, because two
   * live intervals would silently double the request rate against every
   * dashboard endpoint.
   */
  startRefresh(intervalMs?: number): () => void;
  /** Resolves when nothing is in flight. Test seam; also used by the refresh
   * loop to avoid stacking cycles. */
  settled(): Promise<void>;
}

export function createSourceCache(
  fetchers: Partial<Record<DataSourceKey, SourceFetcher>>,
): SourceCache {
  const states = new Map<string, Signal<SourceState>>();
  const inFlight = new Map<string, Promise<void>>();
  /** The range each key was last fetched under, so a range change is
   * detectable without refetching range-insensitive sources. */
  const fetchedRange = new Map<string, string>();
  let controller = new AbortController();
  let refreshTimer: ReturnType<typeof globalThis.setInterval> | null = null;

  function sig(key: DataSourceKey): Signal<SourceState> {
    let s = states.get(key);
    if (!s) {
      s = signal<SourceState>({ ...IDLE });
      states.set(key, s);
    }
    return s;
  }

  function run(key: DataSourceKey, range: string): void {
    const fetcher = fetchers[key];
    if (!fetcher) return;

    const s = sig(key);
    s.value = { ...s.value, loading: true };
    fetchedRange.set(key, range);

    const p = fetcher(controller.signal, range)
      .then((data) => {
        s.value = { data, error: null, loading: false };
      })
      .catch((err) => {
        if (isAbort(err) || controller.signal.aborted) {
          // Teardown, not failure. Leave the previous value in place and say
          // nothing -- an abort banner would be noise on every navigation.
          //
          // But if the abort landed before any data ever did, forget the range
          // too. fetchedRange is written before the await, so leaving it set
          // would make the next ensure() treat this key as already fetched,
          // skip it, and leave the widget blank permanently. That is what an
          // unmount/remount faster than the first response looks like.
          if (s.value.data === null) {
            fetchedRange.delete(key);
          }
          s.value = { ...s.value, loading: false };
          return;
        }
        // Keep whatever we last had. A 60s refresh that hits a transient 500
        // must not discard a working widget: an operator watching a cluster
        // degrade loses the dashboard at exactly the moment the backend gets
        // unreliable. The error rides alongside the stale data, and WidgetHost
        // renders the data with an inline error rather than an error page.
        // On a first fetch there is nothing to keep, so this is still null and
        // the widget shows the error on its own.
        s.value = { data: s.value.data, error: messageOf(err), loading: false };
      })
      .finally(() => {
        inFlight.delete(key);
      });

    inFlight.set(key, p);
  }

  const cache: SourceCache = {
    state<T>(key: DataSourceKey): SourceState<T> {
      return sig(key).value as SourceState<T>;
    },

    signalFor(key: DataSourceKey): Signal<SourceState> {
      return sig(key);
    },

    ensure(keys: readonly DataSourceKey[], range: string): void {
      for (const key of keys) {
        if (inFlight.has(key)) continue;
        const previous = fetchedRange.get(key);
        const stale =
          previous !== undefined &&
          RANGE_SENSITIVE.has(key) &&
          previous !== range;
        if (previous !== undefined && !stale) continue;
        run(key, range);
      }
    },

    refresh(): void {
      for (const [key, range] of [...fetchedRange.entries()]) {
        if (inFlight.has(key)) continue;
        run(key as DataSourceKey, range);
      }
    },

    abort(): void {
      controller.abort();
      controller = new AbortController();
    },

    startRefresh(intervalMs: number = DASHBOARD_REFRESH_MS): () => void {
      const stop = () => {
        if (refreshTimer !== null) {
          globalThis.clearInterval(refreshTimer);
          refreshTimer = null;
        }
      };
      // Replace rather than stack: a second caller must not double the rate.
      stop();
      refreshTimer = globalThis.setInterval(() => {
        // Matches the pre-registry behavior: a hidden tab does not poll.
        if (typeof document !== "undefined" && document.hidden) return;
        if (inFlight.size > 0) return;
        cache.refresh();
      }, intervalMs);
      return stop;
    },

    async settled(): Promise<void> {
      while (inFlight.size > 0) {
        await Promise.allSettled([...inFlight.values()]);
      }
    },
  };

  return cache;
}

/** The real fetchers. Endpoints and shapes match what DashboardV2 fetched
 * before the extraction; cluster-info and recent-events gain the 60s refresh
 * the other two always had, because a silently ageing event list is a defect. */
export const dashboardData: SourceCache = createSourceCache({
  "dashboard-summary": async (signal) =>
    (
      await api<unknown>("/v1/cluster/dashboard-summary", {
        method: "GET",
        signal,
      })
    ).data,
  "dashboard-trends": async (signal, range) =>
    (
      await api<unknown>(`/v1/cluster/dashboard-trends?range=${range}`, {
        method: "GET",
        signal,
      })
    ).data,
  "cluster-info": async (signal) =>
    (await api<unknown>("/v1/cluster/info", { method: "GET", signal })).data,
  "recent-events": async (signal) =>
    (
      await api<unknown>("/v1/resources/events?limit=10", {
        method: "GET",
        signal,
      })
    ).data,
});
