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
import { ApiError, api } from "@/lib/api.ts";
import type { DataSourceKey } from "./types.ts";
import { FAMILY_STATUS_KEYS } from "./types.ts";

/**
 * Why a source failed, as far as it can be told apart from the response.
 *
 * "permission" is a 403 and nothing else: a standing fact about the account
 * that no amount of waiting changes, which is why the shell renders it without
 * a retry affordance (R2). Everything else -- a 500, a dropped connection, a
 * malformed body -- is "failure", which is the behavior that shipped, kept
 * under a name so the two are distinguishable at the call site.
 */
export type SourceErrorKind = "permission" | "failure";

export interface SourceState<T = unknown> {
  data: T | null;
  error: string | null;
  /** The classification of `error`, and null exactly when `error` is null. */
  errorKind: SourceErrorKind | null;
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
  errorKind: null,
  loading: false,
  range: null,
};

/** Sources whose response depends on the selected time range. Re-ensuring one
 * of these under a new range refetches; the others do not. */
const RANGE_SENSITIVE: ReadonlySet<string> = new Set(["dashboard-trends"]);

/**
 * Sources the periodic refresh leaves alone once they have answered.
 *
 * The six discovery routes say whether an operator is installed on the
 * cluster, which changes when somebody installs one -- not on the timescale of
 * a 60s tick. Re-asking costs more than the answer is worth: the dashboard
 * requests all six on mount whether or not a widget reads them, because the
 * palette has to mark an un-added widget as unavailable before it is added,
 * and three of them (policies, gitops, mesh) share the backend's
 * 30-request-per-minute YAML bucket with `/yaml/*` and `/wizards/*`. Polling
 * them would spend a tenth of that shared budget, per IP, for as long as a
 * dashboard tab is open.
 *
 * "Once it has answered", not "once it has been asked": a status that failed
 * has nothing on screen to protect, and a widget left in the error state until
 * the page is reloaded because one discovery call hit a transient 500 is a
 * worse trade than one extra request a minute. See `refresh` below.
 */
const REFRESH_ONCE_SETTLED: ReadonlySet<string> = new Set(FAMILY_STATUS_KEYS);

/** Matches the pre-registry dashboard's 60s interval. */
export const DASHBOARD_REFRESH_MS = 60_000;

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * A forbidden response, and only a forbidden response, is a permission
 * outcome.
 *
 * Read off the HTTP status rather than the message, because the message is
 * whatever the handler wrote and a widget must not branch on prose. 401 is
 * deliberately not here: `api()` refreshes and retries on 401 and surfaces a
 * session expiry, which is an authentication problem the whole page shares,
 * not this widget's own.
 */
function classify(err: unknown): SourceErrorKind {
  return err instanceof ApiError && err.status === 403
    ? "permission"
    : "failure";
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
  key: DataSourceKey;
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
  /**
   * Requests that were cancelled -- superseded by a range change, or torn down
   * by abort() -- but have not settled yet. They no longer own their key's
   * state, but they are still on the wire: a fetcher that is slow to honour
   * its signal (api() waiting on a shared token refresh, say) is a live
   * request, and forgetting it would let settled() return early and a
   * refresh tick stack a second request on top of it.
   */
  const retired = new Set<InFlight>();
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
      key,
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
        s.value = { data, error: null, errorKind: null, loading: false, range };
      })
      .catch((err) => {
        if (!current()) return;
        if (isAbort(err) || controller.signal.aborted) {
          // Teardown, not failure: an abort banner would be noise on every
          // navigation. abort() normally retires the entry before this runs,
          // so reaching here means the fetcher aborted on its own.
          settleAborted(key);
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
        s.value = {
          ...s.value,
          error: messageOf(err),
          errorKind: classify(err),
          loading: false,
        };
      })
      .finally(() => {
        if (current()) inFlight.delete(key);
        retired.delete(entry);
      });

    inFlight.set(key, entry);
  }

  /**
   * Cancels a request and takes it off its key, so it can no longer write the
   * key's state. It stays tracked in `retired` until it actually settles.
   */
  function retire(entry: InFlight): void {
    inFlight.delete(entry.key);
    retired.add(entry);
    entry.controller.abort();
  }

  /**
   * After an abort, the recorded range must describe the data on screen, not
   * the request that never finished. fetchedRange is written before the
   * await, so leaving the aborted range in place breaks both ways:
   *
   * - With no data, the next ensure() thinks the key is already fetched and
   *   skips it, and refresh() has nothing to re-request, so the widget stays
   *   blank for good. That is an unmount/remount faster than the first
   *   response.
   * - With data from another range, the next ensure() for the aborted range
   *   is skipped, and the tab names a window the chart does not show until a
   *   refresh tick happens to fetch it.
   */
  function settleAborted(key: DataSourceKey): void {
    const s = sig(key);
    if (s.value.range === null) {
      fetchedRange.delete(key);
    } else {
      fetchedRange.set(key, s.value.range);
    }
    s.value = { ...s.value, loading: false };
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
          retire(pending);
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
      const busy = new Set<string>(inFlight.keys());
      for (const entry of retired) busy.add(entry.key);
      for (const [key, range] of [...fetchedRange.entries()]) {
        if (busy.has(key)) continue;
        // A discovery status that already answered stays answered until the
        // next page load -- see REFRESH_ONCE_SETTLED. One that failed is
        // retried like anything else.
        if (
          REFRESH_ONCE_SETTLED.has(key) &&
          sig(key as DataSourceKey).value.data !== null
        ) {
          continue;
        }
        run(key as DataSourceKey, range);
      }
    },

    abort(): void {
      // Settled here, synchronously, rather than in each request's rejection
      // handler. The caller's very next line is often an ensure() -- an effect
      // cleanup followed by the next effect -- and a request that is merely
      // signalled still looks in flight to it, so that ensure() would skip
      // the key and the rejection would then forget its range: a widget left
      // blank that no refresh ever retries.
      for (const entry of [...inFlight.values()]) {
        retire(entry);
        settleAborted(entry.key);
      }
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
      while (inFlight.size > 0 || retired.size > 0) {
        await Promise.allSettled(
          [...inFlight.values(), ...retired].map((e) => e.promise),
        );
      }
    },
  };

  return cache;
}

/** GET an endpoint and hand back the `data` envelope every handler writes. */
async function read(path: string, signal: AbortSignal): Promise<unknown> {
  return (await api<unknown>(path, { method: "GET", signal })).data;
}

/**
 * The real fetchers. Endpoints and shapes match what DashboardV2 fetched
 * before the extraction; cluster-info and recent-events gain the 60s refresh
 * the other two always had, because a silently ageing event list is a defect.
 *
 * Exported so `data_test.ts` can assert that every key in the union has one:
 * a key with no entry here is invisible at runtime -- `ensure` returns without
 * issuing anything and a widget declaring it sits in the skeleton forever.
 */
export const DASHBOARD_FETCHERS: Record<DataSourceKey, SourceFetcher> = {
  "dashboard-summary": (signal) =>
    read("/v1/cluster/dashboard-summary", signal),
  "dashboard-trends": (signal, range) =>
    read(`/v1/cluster/dashboard-trends?range=${range}`, signal),
  "cluster-info": (signal) => read("/v1/cluster/info", signal),
  "recent-events": (signal) => read("/v1/resources/events?limit=10", signal),

  // The six discovery routes. Each answers "is this feature installed", which
  // is the question its own list endpoint cannot answer -- see
  // FAMILY_STATUS_KEYS in types.ts. They are range-insensitive, so the 60s
  // refresh keeps them current and a time-range change does not refetch them.
  "policies-status": (signal) => read("/v1/policies/status", signal),
  "gitops-status": (signal) => read("/v1/gitops/status", signal),
  "certificates-status": (signal) => read("/v1/certificates/status", signal),
  "mesh-status": async (signal) => {
    // The one route of the six that wraps its payload -- `{ status: {...} }`
    // -- kept symmetric with the rest of /mesh/*. Unwrapped here so that
    // `featurePresent` reads one shape rather than six. A body without the
    // wrapper yields null, which reads as absent: saying "not installed" about
    // a response we cannot parse is the safe direction, because the other one
    // renders an absent mesh as a healthy one.
    const body = await read("/v1/mesh/status", signal);
    return (body as { status?: unknown } | null)?.status ?? null;
  },
  "external-secrets-status": (signal) =>
    read("/v1/externalsecrets/status", signal),
  "velero-status": (signal) => read("/v1/velero/status", signal),
};

export const dashboardData: SourceCache = createSourceCache(DASHBOARD_FETCHERS);
