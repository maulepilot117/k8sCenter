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
  /**
   * The range `data` was fetched under, or null before any data has landed.
   *
   * Not the range most recently requested: the two differ while a tab-switch
   * fetch is in flight, and a widget that labels its data with a window (the
   * network tile's "p95 over 6h") must name the window the numbers actually
   * cover, not the one the user just clicked.
   */
  range: string | null;
}

/** A fetcher receives the abort signal and the active time range. Sources that
 * ignore the range simply do not read it. */
export type SourceFetcher = (
  signal: AbortSignal,
  range: string,
) => Promise<unknown>;

const IDLE: SourceState = {
  data: null,
  error: null,
  loading: false,
  range: null,
};

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

/** One live request. Each carries its own controller so a single superseded
 * request can be cancelled without tearing down its siblings. */
interface InFlight {
  promise: Promise<void>;
  range: string;
  controller: AbortController;
}

export function createSourceCache(
  fetchers: Partial<Record<DataSourceKey, SourceFetcher>>,
): SourceCache {
  const states = new Map<string, Signal<SourceState>>();
  const inFlight = new Map<string, InFlight>();
  /** The range each key was last requested under, so a range change is
   * detectable without refetching range-insensitive sources. */
  const fetchedRange = new Map<string, string>();
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

    const controller = new AbortController();
    const entry: InFlight = {
      promise: Promise.resolve(),
      range,
      controller,
    };
    // Only the newest request for a key may write its state. A superseded
    // request that resolves anyway -- a fetcher that ignores its signal, or a
    // response already on the wire when abort fired -- would otherwise land
    // after the newer one and put the old range's data back on screen.
    const current = () => inFlight.get(key) === entry;

    entry.promise = fetcher(controller.signal, range)
      .then((data) => {
        if (!current()) return;
        s.value = { data, error: null, loading: false, range };
      })
      .catch((err) => {
        if (!current()) return;
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
        // the widget shows the error on its own. `range` is kept too: it
        // describes the data, and the data did not change.
        s.value = { ...s.value, error: messageOf(err), loading: false };
      })
      .finally(() => {
        if (current()) inFlight.delete(key);
      });

    inFlight.set(key, entry);
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
        const sensitive = RANGE_SENSITIVE.has(key);
        const pending = inFlight.get(key);
        if (pending) {
          // A request under the range being asked for already covers this.
          if (!sensitive || pending.range === range) continue;
          // One under a different range is superseded, not waited on.
          // Skipping it would leave the new range never fetched: the tab
          // reads 24h, the chart shows 6h, and every refresh re-requests 6h
          // because that is the range recorded. The pre-registry island
          // cancelled the earlier tab fetch for the same reason.
          inFlight.delete(key);
          pending.controller.abort();
          run(key, range);
          continue;
        }
        const previous = fetchedRange.get(key);
        const stale = previous !== undefined && sensitive && previous !== range;
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
      for (const entry of inFlight.values()) entry.controller.abort();
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
        await Promise.allSettled([...inFlight.values()].map((e) => e.promise));
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
