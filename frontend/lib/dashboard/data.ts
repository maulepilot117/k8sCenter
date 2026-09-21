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
import { HUBBLE_FLOW_BATCH } from "./networking.ts";
import { decodeSourceKey } from "./params.ts";
import { COMPLIANCE_HISTORY_DAYS } from "./severity.ts";
import type { DataSourceKey } from "./types.ts";
import {
  ABSENT_STATUSES,
  FAMILY_STATUS_KEYS,
  NOT_FOUND_IS_REFUSAL,
  RANGE_SENSITIVE_KEYS,
  sourceCost,
} from "./types.ts";
// Safe despite the apparent cycle: widget-state's only import from this
// file is `import type`, which is erased, so there is no runtime edge back.
import { featurePresent } from "./widget-state.ts";
import type { ResourceListPage } from "./wire-types.ts";

/**
 * Why a source failed, as far as it can be told apart from the response.
 *
 * "permission" is a standing fact about the account that no amount of waiting
 * changes, which is why the shell renders it without a retry affordance (R2).
 * It is a 403, plus the 404 the slug-query route uses in place of one so its
 * catalog cannot be enumerated -- see `classify` below and
 * `NOT_FOUND_IS_REFUSAL` in types.ts.
 *
 * "absent" is a standing fact about the DEPLOYMENT: the route answered a
 * status that means this build does not serve the read here at all, which for
 * the platform family is a 503 (no database) or the 404 of a route that is
 * never registered without one. `ABSENT_STATUSES` in types.ts names the
 * sources and their codes, and `resolveWidgetState` turns this into the same
 * `unavailable` outcome a missing operator produces -- not an error, because
 * nothing is wrong, and not a delay, because nothing is coming (R1).
 *
 * Everything else -- a 500, a dropped connection, a malformed body -- is
 * "failure", which is the behavior that shipped, kept under a name so the
 * three are distinguishable at the call site.
 */
export type SourceErrorKind = "permission" | "failure" | "absent";

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

/**
 * A fetcher receives the abort signal, the active time range and the
 * parameters its key was resolved under. Sources that ignore either simply do
 * not read it, which is every source that shipped before parameters existed.
 */
export type SourceFetcher = (
  signal: AbortSignal,
  range: string,
  params: Readonly<Record<string, string>>,
) => Promise<unknown>;

const IDLE: SourceState = {
  data: null,
  error: null,
  errorKind: null,
  loading: false,
  range: null,
};

/** Declared in types.ts beside the source keys themselves, so that adding a
 * range-backed source is one edit in one file. */
const RANGE_SENSITIVE = RANGE_SENSITIVE_KEYS;

/**
 * Sources the periodic refresh leaves alone once they have answered.
 *
 * The ten discovery routes say whether a feature is installed on the
 * cluster, which changes when somebody installs one -- not on the timescale of
 * a 60s tick. Re-asking costs more than the answer is worth: the dashboard
 * requests all ten on mount whether or not a widget reads them, because the
 * palette has to mark an un-added widget as unavailable before it is added,
 * and four of them (policies, gitops, mesh, hubble) share the backend's
 * 30-request-per-minute YAML bucket with `/yaml/*` and `/wizards/*`. Polling
 * them would spend a tenth of that shared budget, per IP, for as long as a
 * dashboard tab is open.
 *
 * "Once it has answered", not "once it has been asked": a status that failed
 * has nothing on screen to protect, and a widget left in the error state until
 * the page is reloaded because one discovery call hit a transient 500 is a
 * worse trade than one extra request a minute.
 *
 * And only once it has answered YES. A negative verdict is re-asked on a
 * long multiple of the interval, because the backend's discovery check
 * reports "absent" when the check itself failed -- see `isDue`.
 */
const REFRESH_ONCE_SETTLED: ReadonlySet<string> = new Set(FAMILY_STATUS_KEYS);

/**
 * How many refresh cycles pass before a NEGATIVE discovery verdict is
 * re-asked. An affirmative one is never re-asked at all.
 *
 * Ten, against a 60s interval, is a ten-minute worst case for a cluster
 * whose operator was there all along and whose discovery check happened to
 * fail once -- slow enough that an absent family costs six requests an hour
 * rather than sixty, fast enough that nobody has to know a page reload is
 * the cure.
 */
const NEGATIVE_RECHECK_TICKS = 10;

/** Matches the pre-registry dashboard's 60s interval. */
export const DASHBOARD_REFRESH_MS = 60_000;

/**
 * How long a single request may run before it is aborted.
 *
 * Half the refresh interval, so a hung read becomes a visible error within
 * one cycle and its concurrency slot comes back, rather than being lost for
 * the life of the tab. See the deadline in `issue` for why that matters.
 */
export const REQUEST_TIMEOUT_MS = DASHBOARD_REFRESH_MS / 2;

/**
 * How many non-cheap reads may be on the wire at once. The rest queue.
 *
 * Six because that is the per-origin connection ceiling a browser enforces on
 * HTTP/1.1 anyway: past it the requests queue regardless, and queueing them
 * here instead means the cache decides the order and can drop one whose
 * widget went away. Raising it would move the queue back into the browser
 * without issuing anything sooner.
 *
 * The catalog has grown past the point where every non-cheap source fits in
 * the bound at once, which is what the bound is for: a dashboard holding the
 * workload widgets alongside the metric tiles wants the trends series, the
 * counts route, four list reads and eight discovery routes, and the last of
 * them waits for a slot rather than joining a twelve-request burst. The
 * refresh offsets below spread the same set across the interval on every tick
 * after the first.
 *
 * Cheap reads are not counted or bounded. They are informer lookups; making
 * one wait behind a Prometheus range query would trade a cost that does not
 * exist for latency that does.
 */
export const MAX_CONCURRENT_EXPENSIVE_FETCHES = 6;

/**
 * The share of the refresh interval that offsets are spread across.
 *
 * Half, not all of it. An offset near the end of the interval would still be
 * counting down when the next tick arrives, and a key holding a pending timer
 * reads as busy -- so a source offset to 59s would skip its own next cycle
 * and effectively refresh every other interval. Half leaves every expensive
 * read a full half-interval to fire and finish in before the next tick looks,
 * which is also the deadline a single request gets (REQUEST_TIMEOUT_MS).
 *
 * This used to say the tick "skips entirely while anything is outstanding".
 * It no longer does: that guard froze every card whenever one read was slow,
 * and due-ness is per key now.
 */
const REFRESH_OFFSET_FRACTION = 0.5;

/** FNV-1a, 32-bit. Any stable string hash would do; this one is four lines
 * and has no dependency. */
function hash32(value: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < value.length; i++) {
    h ^= value.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/**
 * Where inside the refresh interval a source is issued.
 *
 * Cheap sources go on the tick itself, which is what every source did before
 * this existed. Everything else gets an offset derived from its key, so a
 * dashboard holding many expensive widgets spreads its reads across the
 * interval instead of issuing them all at once (R6).
 *
 * Derived from the key rather than drawn at random, because a source that
 * wandered between ticks would produce a request pattern nobody could read in
 * a network trace and could drift into lockstep with another source. The
 * consequence is that two viewers of the same dashboard offset the same
 * source identically and still align with each other; separating them needs a
 * per-session seed, which is a larger change than the stampede justifies.
 *
 * Pure, and exported for that reason: the scheduling around it is timers.
 */
export function sourceRefreshOffsetMs(key: string, intervalMs: number): number {
  // The class comes from the source, the offset from the whole key: two
  // parameterized reads of one source are two requests and want two slots in
  // the interval, but they are the same kind of request and cost the same.
  if (sourceCost(decodeSourceKey(key).base) === "cheap") return 0;
  const window = Math.floor(intervalMs * REFRESH_OFFSET_FRACTION);
  if (window <= 0) return 0;
  return hash32(key) % window;
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * A forbidden response is a permission outcome -- and so is a not-found from
 * the handful of sources that have no other way to say forbidden. A status
 * that means "this deployment does not serve this read" is an absence, which
 * is neither.
 *
 * Read off the HTTP status rather than the message, because the message is
 * whatever the handler wrote and a widget must not branch on prose. 401 is
 * deliberately not here: `api()` refreshes and retries on 401 and surfaces a
 * session expiry, which is an authentication problem the whole page shares,
 * not this widget's own.
 *
 * The 404 branch is narrow on purpose and takes the source key to keep it
 * that way. An ordinary resource route's 404 means the object is gone, which
 * is not a permission problem and must never render as one; only the
 * slug-query route deliberately collapses "forbidden" into "not found" so its
 * catalog cannot be enumerated. `NOT_FOUND_IS_REFUSAL` in types.ts names the
 * sources that read it, and carries the full reasoning.
 */
function classify(err: unknown, base: string): SourceErrorKind {
  if (!(err instanceof ApiError)) return "failure";
  if (err.status === 403) return "permission";
  if (err.status === 404 && NOT_FOUND_IS_REFUSAL.has(base)) return "permission";
  // Checked after the refusal branch, so a source that ever appeared in both
  // declarations still reads as refused: that set exists to keep a
  // deliberately opaque refusal readable as one, and reporting it as an
  // absence would tell the user the feature is gone rather than shut.
  if (ABSENT_STATUSES[base]?.includes(err.status)) return "absent";
  return "failure";
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

/**
 * Every method below takes a RESOLVED key, not a source name.
 *
 * For an unparameterized source the two are the same string, which is why
 * every caller that predates parameters still reads correctly. For a
 * parameterized one the key carries the values as well (`sourceKeyFor` in
 * params.ts), so diagnostics-for-prod and diagnostics-for-staging are two
 * entries with two states and two requests, and two widgets pointed at one
 * namespace still dedupe onto a single fetch.
 */
export interface SourceCache {
  state<T = unknown>(key: string): SourceState<T>;
  signalFor(key: string): Signal<SourceState>;
  ensure(keys: readonly string[], range: string): void;
  /**
   * Forgets every key outside `keys`: cancels anything in flight for one,
   * clears its state and stops the refresh loop re-requesting it.
   *
   * Only parameterized keys make this necessary, which is why it did not
   * exist before them. The unparameterized key set is the source union --
   * eleven keys, all of them belonging to a widget that might come back -- so
   * "everything ever fetched" was a bounded and correct set for the refresh
   * loop to re-request. A parameterized key is not: re-pointing one widget
   * from prod to staging leaves prod recorded as fetched, and without this it
   * would go on being polled every 60s, in the backend's shared
   * 30-request-per-minute bucket, on behalf of a card that no longer exists.
   *
   * Deliberately separate from `ensure` rather than folded into it. `ensure`
   * is called with whatever subset a caller needs and says "these are
   * wanted"; this says "these are ALL that are wanted", which only the
   * consumer holding the whole layout can truthfully claim.
   */
  retain(keys: readonly string[]): void;
  /** Re-requests every source already fetched that is due again, immediately.
   * The periodic loop spreads instead; see `startRefresh`. */
  refresh(): void;
  abort(): void;
  /**
   * Starts the periodic refresh and returns its stop function. Starting twice
   * replaces the first interval rather than stacking a second one, because two
   * live intervals would silently double the request rate against every
   * dashboard endpoint.
   *
   * Cheap sources go out on the tick. Each expensive one waits out its own
   * offset inside the interval, so a dashboard of many expensive widgets does
   * not issue all of them at once (R6). Stopping cancels an offset that has
   * not fired yet.
   */
  startRefresh(intervalMs?: number): () => void;
  /** Resolves when nothing is outstanding -- on the wire, queued for a slot,
   * or retired but not yet settled. A test seam, and only that: the refresh
   * loop does not consult it. Cycles are kept from stacking per key, by
   * due-ness. */
  settled(): Promise<void>;
}

/** One live request. Each carries its own controller so a single superseded
 * request can be cancelled without tearing down its siblings. */
interface InFlight {
  key: string;
  /** The source the key resolves to, and the values it was resolved under.
   * Decoded once here so neither the fetcher lookup nor the cost lookup has
   * to parse the key again on every state transition. */
  base: string;
  params: Record<string, string>;
  promise: Promise<void>;
  range: string;
  controller: AbortController;
  /**
   * True while the request is waiting for an expensive slot.
   *
   * A queued request owns its key exactly as a running one does -- it is in
   * `inFlight`, so a second widget asking for the same key still dedupes onto
   * it -- but it has not touched the network, which is why tearing one down
   * drops it outright rather than aborting it.
   */
  queued: boolean;
  /** Set only while queued. `true` starts the fetch, `false` drops it. */
  admit?: (start: boolean) => void;
}

export function createSourceCache(
  fetchers: Partial<Record<DataSourceKey, SourceFetcher>>,
  /**
   * How long a single request may take before it is abandoned. Injectable for
   * the same reason `startRefresh` takes its interval: the shipped value is
   * half a refresh cycle, and a test that had to wait it out in real time
   * would not be written -- which is exactly why the deadline's failure
   * behaviour went uncovered until it was found by review.
   */
  requestTimeoutMs: number = REQUEST_TIMEOUT_MS,
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
  /** Non-cheap requests waiting for a slot, oldest first. */
  const waiting: InFlight[] = [];
  /** How many non-cheap requests are on the wire. Cheap ones are not counted:
   * they are not bounded, so counting them would only shrink the bound. */
  let activeExpensive = 0;
  let refreshTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  /** Per-key timers for expensive sources waiting out their offset inside the
   * current refresh interval. */
  const offsetTimers = new Map<
    string,
    ReturnType<typeof globalThis.setTimeout>
  >();

  function sig(key: string): Signal<SourceState> {
    let s = states.get(key);
    if (!s) {
      s = signal<SourceState>({ ...IDLE });
      states.set(key, s);
    }
    return s;
  }

  /** Takes an expensive slot if one is free. Cheap keys never ask. */
  function takeSlot(): boolean {
    if (activeExpensive >= MAX_CONCURRENT_EXPENSIVE_FETCHES) return false;
    activeExpensive++;
    return true;
  }

  /**
   * Gives back the slot a settled request held, handing it straight to the
   * next waiter rather than releasing and letting it be re-taken -- otherwise
   * a fresh `ensure` could slip in ahead of something that has been queued
   * since the last tick.
   */
  function releaseSlot(entry: InFlight): void {
    if (sourceCost(entry.base) === "cheap") return;
    const next = waiting.shift();
    if (next) next.admit?.(true);
    else activeExpensive--;
  }

  /** Takes a settled -- or dropped -- request off the books. */
  function finish(entry: InFlight): void {
    if (inFlight.get(entry.key) === entry) inFlight.delete(entry.key);
    retired.delete(entry);
  }

  /**
   * Drops a request that never got a slot.
   *
   * It holds no slot and has nothing on the wire, so there is nothing to
   * abort and nothing for `settled()` to wait on: its promise resolves here.
   */
  function dropQueued(entry: InFlight): void {
    const at = waiting.indexOf(entry);
    if (at >= 0) waiting.splice(at, 1);
    entry.queued = false;
    finish(entry);
    entry.admit?.(false);
  }

  function run(key: string, range: string): void {
    // The key is what the cache is keyed by; the source inside it is what
    // decides which fetcher serves it and what that read costs. A key naming
    // a source with no fetcher is invisible by design -- see the union
    // coverage test in data_test.ts.
    const { base, params } = decodeSourceKey(key);
    const fetcher = fetchers[base as DataSourceKey];
    if (!fetcher) return;

    const s = sig(key);
    s.value = { ...s.value, loading: true };
    fetchedRange.set(key, range);

    const controller = new AbortController();
    const entry: InFlight = {
      key,
      base,
      params,
      promise: Promise.resolve(),
      range,
      controller,
      queued: false,
    };

    if (sourceCost(base) === "cheap" || takeSlot()) {
      entry.promise = issue(entry, fetcher);
    } else {
      // Over the bound. The request is recorded against its key -- so it
      // still dedupes and still reads as loading -- but no fetch is made
      // until a slot frees. `settled()` waits on this promise either way.
      entry.queued = true;
      entry.promise = new Promise<void>((resolve) => {
        entry.admit = (start: boolean) => {
          entry.queued = false;
          entry.admit = undefined;
          resolve(start ? issue(entry, fetcher) : undefined);
        };
      });
      waiting.push(entry);
    }

    inFlight.set(key, entry);
  }

  /** Puts a request on the wire. Called either straight from `run` or later,
   * when a slot frees. */
  function issue(entry: InFlight, fetcher: SourceFetcher): Promise<void> {
    const { key, base, range, params, controller } = entry;
    const s = sig(key);

    // Nothing below this layer bounds a request. The server-side proxy times
    // out its own hop to the backend, but the browser's fetch carries no
    // deadline, so a wedged proxy or a dead connection leaves a promise that
    // never settles -- and a promise that never settles never reaches the
    // `finally` below, so its slot is never returned.
    //
    // That is load-bearing now that the refresh tick no longer stops when
    // something is outstanding. Six hung requests would retire the whole
    // expensive budget permanently, and every other expensive key would then
    // sit queued behind them while the four cheap sources kept ticking -- a
    // dashboard that looks alive while most of it is frozen, with no error
    // anywhere, because a request that never settles never records one.
    //
    // Half the interval: long enough that a slow-but-working backend still
    // lands, short enough that a hung one is a visible error inside one
    // cycle rather than a slot lost for the session.
    // Tracked separately because the deadline and a teardown reach the catch
    // below identically: `controller.signal.aborted` is true for both, and
    // the reason we pass is a DOMException just like a real abort. Reading
    // the deadline as a teardown is what made the timeout silent -- the
    // widget recorded no error, and on a first fetch `settleAborted` dropped
    // the key out of `fetchedRange` and so out of the refresh set, leaving
    // the card in its skeleton for the life of the tab. A hung source has to
    // be a visible, retried failure, which is the whole point of the bound.
    let timedOut = false;
    const deadline = globalThis.setTimeout(() => {
      timedOut = true;
      controller.abort(new DOMException("timed out", "TimeoutError"));
    }, requestTimeoutMs);
    // Only the newest request for a key may write its state. A superseded
    // request that resolves anyway -- a fetcher that ignores its signal, or a
    // response already on the wire when abort fired -- would otherwise land
    // after the newer one and put the old range's data back on screen.
    const current = () => inFlight.get(key) === entry;

    return fetcher(controller.signal, range, params)
      .then((data) => {
        if (!current()) return;
        s.value = { data, error: null, errorKind: null, loading: false, range };
      })
      .catch((err) => {
        if (!current()) return;
        if (!timedOut && (isAbort(err) || controller.signal.aborted)) {
          // Teardown, not failure: an abort banner would be noise on every
          // navigation. abort() normally retires the entry before this runs,
          // so reaching here means the fetcher aborted on its own.
          //
          // The deadline is excluded above: it aborts the same controller,
          // but it means the source did not answer, which is a failure the
          // reader has to see.
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
          errorKind: classify(err, base),
          loading: false,
        };
      })
      .finally(() => {
        globalThis.clearTimeout(deadline);
        // The slot goes back before the bookkeeping, so the next waiter
        // starts on the same turn this one finished.
        releaseSlot(entry);
        finish(entry);
      });
  }

  /**
   * Cancels a request and takes it off its key, so it can no longer write the
   * key's state. It stays tracked in `retired` until it actually settles.
   */
  function retire(entry: InFlight): void {
    inFlight.delete(entry.key);
    retired.add(entry);
    entry.controller.abort();
    // A request still waiting for a slot has issued nothing, so there is no
    // response to ignore and nothing to wait for: drop it instead of letting
    // it start later on behalf of a widget that is gone.
    if (entry.queued) dropQueued(entry);
  }

  function clearOffsetTimers(): void {
    for (const timer of offsetTimers.values()) globalThis.clearTimeout(timer);
    offsetTimers.clear();
  }

  /** Keys with a request outstanding: on the wire, queued for a slot, or
   * retired but not yet settled. */
  function busyKeys(): Set<string> {
    const busy = new Set<string>(inFlight.keys());
    for (const entry of retired) busy.add(entry.key);
    // A key with an offset timer pending from an earlier tick is spoken for
    // too. Counting it here is what lets the tick below skip that one key
    // rather than abandoning the whole cycle.
    for (const key of offsetTimers.keys()) busy.add(key);
    return busy;
  }

  /**
   * Whether a key already fetched once is due to be fetched again.
   *
   * Due-ness and timing are deliberately separate: this answers *whether*,
   * and `sourceRefreshOffsetMs` answers *when*. A discovery status that has
   * already answered is never due, so its offset never fires -- the two
   * compose without either needing to know about the other.
   */
  function isDue(key: string, busy: Set<string>, tick = 0): boolean {
    if (busy.has(key)) return false;
    const data = sig(key).value.data;
    if (REFRESH_ONCE_SETTLED.has(key) && data !== null) {
      // A discovery status that says the feature IS here is settled: it will
      // not stop being here while the tab is open, and re-asking spends a
      // shared rate-limit budget for an answer that cannot change.
      //
      // A status that says the feature is NOT here is a different claim, and
      // one the backend can get wrong in a way nothing recovers from. Its
      // discovery check answers "absent" when the check itself failed -- a
      // transient API-server blip reads identically to an uninstalled
      // operator -- and once that lands, the widget is unavailable, the
      // palette refuses to re-add it, and no refresh revisits it. A cluster
      // that does run the operator shows "not installed" until someone
      // reloads the page, with nothing anywhere saying why.
      //
      // So a negative verdict is re-asked, but slowly: once every
      // NEGATIVE_RECHECK_TICKS cycles rather than every one. That self-heals
      // within minutes, costs a handful of requests an hour per absent
      // family, and leaves the expensive case -- the affirmative answer --
      // exactly as settled as it was.
      if (featurePresent(data)) return false;
      if (tick % NEGATIVE_RECHECK_TICKS !== 0) return false;
    }
    return true;
  }

  /** The keys a refresh would re-request right now, with their ranges.
   * Snapshotted, because running one mutates `fetchedRange`. */
  function dueEntries(tick = 0): Array<[string, string]> {
    const busy = busyKeys();
    const due: Array<[string, string]> = [];
    for (const [key, range] of fetchedRange) {
      if (isDue(key, busy, tick)) {
        due.push([key, range]);
      }
    }
    return due;
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
  function settleAborted(key: string): void {
    const s = sig(key);
    if (s.value.range === null) {
      fetchedRange.delete(key);
    } else {
      fetchedRange.set(key, s.value.range);
    }
    s.value = { ...s.value, loading: false };
  }

  const cache: SourceCache = {
    state<T>(key: string): SourceState<T> {
      return sig(key).value as SourceState<T>;
    },

    signalFor(key: string): Signal<SourceState> {
      return sig(key);
    },

    ensure(keys: readonly string[], range: string): void {
      for (const key of keys) {
        // Range-sensitivity is a property of the source, not of the values it
        // was resolved under, so it is asked of the base rather than the key.
        const sensitive = RANGE_SENSITIVE.has(decodeSourceKey(key).base);
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

    /**
     * Re-requests everything due, now.
     *
     * Deliberately immediate: this is "refresh the dashboard", and a caller
     * asking for that wants the requests issued, not scheduled. The offsets
     * belong to the periodic loop below, which is the only path that repeats
     * and therefore the only one that can stampede.
     */
    refresh(): void {
      // The schedule this supersedes is cancelled first. Pending offset
      // timers count as busy, so without this every expensive key would be
      // reported not-due and skipped -- turning "refresh the dashboard" into
      // a call that re-issues the four cheap sources and silently defers the
      // rest by up to half an interval. Clearing first also means the keys
      // cannot double-fetch: the timer is gone before due-ness is computed.
      clearOffsetTimers();
      for (const [key, range] of dueEntries()) run(key, range);
    },

    retain(keys: readonly string[]): void {
      const wanted = new Set(keys);
      for (const entry of [...inFlight.values()]) {
        if (wanted.has(entry.key)) continue;
        // A request on behalf of a card that is gone. Retired rather than
        // merely forgotten: left alone it would land and re-fill a key this
        // call just cleared, and `settled()` has to keep waiting on it
        // either way until it actually settles.
        retire(entry);
      }
      for (const key of [...fetchedRange.keys()]) {
        if (wanted.has(key)) continue;
        fetchedRange.delete(key);
        const timer = offsetTimers.get(key);
        if (timer !== undefined) {
          globalThis.clearTimeout(timer);
          offsetTimers.delete(key);
        }
        // The state goes too, not just the schedule. Keeping it would make a
        // namespace re-added later render whatever was on screen when its
        // card was removed, with no fetch behind it -- `ensure` would see a
        // key it has no record of, run it, and the stale value would be
        // visible until the response landed.
        states.delete(key);
      }
    },

    abort(): void {
      // Settled here, synchronously, rather than in each request's rejection
      // handler. The caller's very next line is often an ensure() -- an effect
      // cleanup followed by the next effect -- and a request that is merely
      // signalled still looks in flight to it, so that ensure() would skip
      // the key and the rejection would then forget its range: a widget left
      // blank that no refresh ever retries.
      //
      // A fetch waiting out its refresh offset is a fetch about to be on the
      // wire, so a teardown cancels it as well -- otherwise the timer fires
      // after the island is gone and re-fills a key nobody is reading.
      clearOffsetTimers();
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
        clearOffsetTimers();
      };
      // Replace rather than stack: a second caller must not double the rate.
      stop();
      // Counts cycles, so a negative discovery verdict can be re-asked on a
      // multiple of the interval rather than on every one. Starts at 1 so the
      // first tick is not a recheck tick: the verdict landed moments ago.
      let tick = 0;
      refreshTimer = globalThis.setInterval(() => {
        // Matches the pre-registry behavior: a hidden tab does not poll.
        if (typeof document !== "undefined" && document.hidden) return;
        tick++;
        // Deliberately not gated on the cache being quiet.
        //
        // This used to return early whenever anything anywhere was
        // outstanding, which is a far stronger claim than it needed. Stacking
        // is a per-key hazard -- the same key fetched twice -- and `isDue`
        // already refuses a key that is on the wire, queued, retired, or
        // holding an offset timer. Bailing out of the whole tick instead
        // meant one slow read froze refresh for every other card, silently:
        // a request that never settles never records an error, so nothing
        // goes stale and nothing says why. With up to forty cards over
        // separate backends, and no timeout below this layer, that is a
        // dashboard quietly serving yesterday's numbers.
        for (const [key, range] of dueEntries(tick)) {
          const offset = sourceRefreshOffsetMs(key, intervalMs);
          if (offset === 0) {
            run(key, range);
            continue;
          }
          offsetTimers.set(
            key,
            globalThis.setTimeout(() => {
              offsetTimers.delete(key);
              // Re-checked rather than assumed. Half an interval is long
              // enough for an ensure(), an abort() or a range change to have
              // covered this key already.
              const current = fetchedRange.get(key);
              if (current === undefined) return;
              if (!isDue(key, busyKeys())) return;
              run(key, current);
            }, offset),
          );
        }
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
 * The largest page the generic list route will serve. Requests above it are
 * clamped server-side (`parseListParams`), so asking for more would silently
 * get this and leave the client believing it had the whole population.
 */
const LIST_PAGE_LIMIT = 500;

/**
 * How many audit entries and unread notifications the platform cards ask for.
 *
 * Small on purpose. Both routes page against PostgreSQL and both cards show a
 * handful of rows; asking for the route's own default (25 notifications) or
 * anything larger would pay for rows nothing renders, once per refresh, per
 * open dashboard. The audit route's `pageSize` is clamped server-side and the
 * notification route's `limit` is capped at 200, so neither number can grow
 * past what the handler will serve.
 *
 * The notification cap does NOT bound the count beside the rows: that comes
 * from `metadata.total`, which the handler computes over the whole unread
 * population before it pages.
 */
const AUDIT_PAGE_SIZE = 10;
const UNREAD_FEED_LIMIT = 10;

/**
 * GET a list endpoint and keep BOTH halves of its envelope.
 *
 * `read` above drops `metadata`, which is right for the handlers that answer
 * with a whole object and wrong for the list route: the route caps a page at
 * `LIST_PAGE_LIMIT` items while `metadata.total` reports the whole
 * population, so dropping the total would leave a consumer unable to tell a
 * complete list from the first 500 of three thousand. Every widget that ranks
 * a list has to say which of the two it is showing.
 */
async function readList(
  path: string,
  signal: AbortSignal,
): Promise<ResourceListPage> {
  const res = await api<unknown>(`${path}?limit=${LIST_PAGE_LIMIT}`, {
    method: "GET",
    signal,
  });
  const items = Array.isArray(res.data) ? res.data : [];
  return { items, total: res.metadata?.total ?? items.length };
}

/**
 * A route envelope with one nil-slice field normalised to an empty array.
 *
 * Go marshals a nil slice as `null`, so a handler that builds its result with
 * `var out []T` answers `"field": null` for an empty result. On a BARE list
 * response that is caught by a plain `?? []`, and catching it matters: a
 * source whose data is null never renders, because `resolveWidgetState` reads
 * `data !== null` as "has landed".
 *
 * An envelope hides the same defect somewhere worse. The body is an object, so
 * the card renders -- and renders a fleet it could not read as a fleet with
 * nothing in it. This normalises the one field and leaves everything else
 * alone, including a body that is not an object at all: the view functions
 * distinguish an unreadable body from an empty one, and flattening the two
 * here would take that distinction away from them.
 */
function withList(body: unknown, field: string): unknown {
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    return body;
  }
  const value = (body as Record<string, unknown>)[field];
  if (value !== null && value !== undefined) return body;
  return { ...(body as Record<string, unknown>), [field]: [] };
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

  // The one parameterized fetcher. Its key carries the namespace, so this is
  // called once per distinct namespace on the layout and the cache holds one
  // state per namespace.
  //
  // The namespace is percent-encoded into the path rather than interpolated
  // raw. It has already passed `paramValueError` on the way in and the server
  // re-validates it on save, but this is the one place a stored value becomes
  // a URL, and a value with a slash in it would otherwise address a different
  // route entirely. Encoding is the narrow fix; the broad one is that the
  // value is never user-authored text in the first place (D-8).
  "diagnostics-summary": (signal, _range, params) =>
    read(
      `/v1/diagnostics/${encodeURIComponent(params.namespace ?? "")}/summary`,
      signal,
    ),

  // Batch counts across every informer-tracked kind. Deliberately requested
  // without a namespace: the dashboard is a cluster-wide surface, and the
  // namespace picker scopes the list pages rather than this one.
  "resource-counts": (signal) => read("/v1/resources/counts", signal),

  // The four list reads. `readList`, not `read`: what these widgets render is
  // a ranking, and a ranking over a capped page has to be labelled as one.
  "deployments-list": (signal) => readList("/v1/resources/deployments", signal),
  "statefulsets-list": (signal) =>
    readList("/v1/resources/statefulsets", signal),
  "daemonsets-list": (signal) => readList("/v1/resources/daemonsets", signal),
  "pods-list": (signal) => readList("/v1/resources/pods", signal),

  // `hpas` and `pdbs`, not `horizontalpodautoscalers` and
  // `poddisruptionbudgets`. The generic route dispatches on the resource
  // adapter's `Kind()`, which is the short form; the long form is what the
  // counts route keys on and what the RBAC check names, and asking for it
  // here returns a 404 for a kind that is present on every cluster.
  "hpas-list": (signal) => readList("/v1/resources/hpas", signal),
  "pdbs-list": (signal) => readList("/v1/resources/pdbs", signal),

  // `nodes` is both the adapter's `Kind()` and the long resource name, so
  // unlike the two above there is no short/long form to get wrong.
  "nodes-list": (signal) => readList("/v1/resources/nodes", signal),

  // Two family routes rather than the generic one, and neither is a list page
  // -- both answer with a whole roll-up, so `read` and not `readList`.
  //
  // `/limits/namespaces` returns one row per namespace that has a
  // ResourceQuota OR a LimitRange, already RBAC-filtered and already sorted by
  // utilization. The rows with no quota are the reason `quotaPressureView`
  // cannot simply render the payload: they are in it, and a pressure ranking
  // must not carry them. Deliberately requested without a namespace, like
  // `resource-counts`: the dashboard is a cluster-wide surface.
  "limits-namespaces": (signal) => read("/v1/limits/namespaces", signal),
  // `/storage/classes` is the storage family's class inventory. There is no
  // bare `/storage` overview route to ask instead -- the family mounts
  // drivers, classes, snapshots, snapshot-classes and presets under its own
  // prefix and nothing that spans them -- which is why the card composes this
  // with the Prometheus slug below rather than reading one endpoint.
  "storage-classes": (signal) => read("/v1/storage/classes", signal),

  // The two slug reads. The whole query lives on the server; the client
  // names it and nothing else (D-8, R15). No namespace and no name: these
  // slugs are `ClusterWide`, which also pins their RBAC check to a
  // cluster-scoped grant, so supplying either would be ignored by the
  // handler and misleading here.
  //
  // A caller without that grant gets 404, not 403 -- the handler answers
  // refusals and unknown slugs identically so the catalog cannot be
  // enumerated. `NOT_FOUND_IS_REFUSAL` is what turns that back into the
  // permission state these two cards need.
  "top-consumers-cpu": (signal) =>
    read("/v1/monitoring/queries/cluster/top-consumers-cpu", signal),
  "top-consumers-memory": (signal) =>
    read("/v1/monitoring/queries/cluster/top-consumers-memory", signal),
  // The third slug. Its RBAC check is a cluster-scoped `list` on
  // persistentvolumeclaims rather than on pods, and it refuses the same
  // opaque way -- hence its entry in `NOT_FOUND_IS_REFUSAL` too.
  "volume-capacity": (signal) =>
    read("/v1/monitoring/queries/cluster/storage-capacity", signal),

  // The security family's four reads. The prefixes are the two the project's
  // own CLAUDE.md API summary gets wrong: policy routes are mounted under
  // `/v1/policies/...` (plural) and vulnerabilities under `/v1/scanning/...`
  // rather than a `/v1/security/...` that does not exist. Both confirmed
  // against `registerPolicyRoutes` and `registerScanningRoutes` in
  // backend/internal/server/routes.go.
  "policy-compliance-score": (signal) =>
    read("/v1/policies/compliance", signal),
  // `days=30` is `COMPLIANCE_HISTORY_DAYS` in severity.ts, which is what the
  // card's copy says the trend covers. A window changed in one place and not
  // the other puts a truthful number under a false label.
  "policy-compliance-history": async (signal) =>
    (await read(
      `/v1/policies/compliance/history?days=${COMPLIANCE_HISTORY_DAYS}`,
      signal,
    )) ?? [],
  // `?? []`, and this is load-bearing rather than defensive. The handler
  // builds its result with `var filtered []NormalizedViolation`, so an engine
  // with nothing to report serialises as `"data": null` -- and a source whose
  // data is null NEVER RENDERS: `resolveWidgetState` reads `data !== null` as
  // "has landed", so the card would sit in the skeleton for as long as the
  // cluster stayed clean. Normalising here is what makes "zero violations"
  // reachable at all, which is one of this release's two acceptance examples.
  "policy-violations-list": async (signal) =>
    (await read("/v1/policies/violations", signal)) ?? [],
  // The second parameterized fetcher, and the first whose parameter is
  // mandatory rather than a scoping choice: the handler answers 400 without
  // `?namespace=`. Percent-encoded for the same reason the diagnostics
  // fetcher encodes its own -- this is where a stored value becomes a URL --
  // though here it lands in the query string rather than the path.
  "vulnerability-reports": (signal, _range, params) =>
    read(
      `/v1/scanning/vulnerabilities?namespace=${encodeURIComponent(
        params.namespace ?? "",
      )}`,
      signal,
    ),

  // The data-protection family's four reads.
  //
  // `read`, not `readList`: none of these is the generic list route, so none
  // of them caps a page or reports a population total, and every one answers
  // with the whole (already RBAC-filtered) set. There is therefore no
  // truncation for a card to disclose -- the coverage problem `page-coverage.ts`
  // exists for does not arise here.
  //
  // `?? []` on all four, and it is a guard rather than an observation. Every
  // one of these handlers builds its result with `make(..., 0, n)` or returns
  // a literal empty slice on its not-installed and RBAC-refused paths, so all
  // four serialise an empty result as `[]` today -- checked against
  // `HandleListCertificates`, `HandleListExternalSecrets`, `HandleListBackups`
  // and `HandleListSnapshots`. The guard is here because the cost of being
  // wrong is invisible and permanent: `resolveWidgetState` reads `data !==
  // null` as "has landed", so a handler that ever regressed to `var out
  // []T` would leave the card in its skeleton forever rather than failing.
  // `policy-violations-list` above is that regression, already shipped.
  //
  // The certificates read is the full inventory rather than
  // `/v1/certificates/expiring`: see `certificates-list` in types.ts.
  "certificates-list": async (signal) =>
    (await read("/v1/certificates/certificates", signal)) ?? [],
  // Nested under its own segment. A bare `/v1/externalsecrets` is the chi
  // router group, not a route, and would 404.
  "external-secrets-list": async (signal) =>
    (await read("/v1/externalsecrets/externalsecrets", signal)) ?? [],
  "velero-backups-list": async (signal) =>
    (await read("/v1/velero/backups", signal)) ?? [],
  "snapshots-list": async (signal) =>
    (await read("/v1/storage/snapshots", signal)) ?? [],

  // The delivery and networking reads. Both answer with an ENVELOPE rather
  // than a bare list -- `{ applications, summary }` and
  // `{ status, workloads, errors }` -- so `read` keeps the whole body and the
  // `?? []` guard lands on the nested field instead of on the response.
  //
  // The guard on `applications` is load-bearing, not defensive.
  // `HandleListApplications` builds its result through `filterApps`, which
  // returns `var out []NormalizedApp` -- a nil slice -- so a caller whose RBAC
  // filter or query parameters drop everything gets `"applications": null`.
  // The same defect that shipped on `policy-violations-list`. It does not
  // strand the card the way a null BODY would (the envelope is still an
  // object, so `resolveWidgetState` sees data), which is precisely why it
  // would have gone unnoticed: the card would render, and quietly report a
  // fleet it could not read as a fleet with nothing wrong.
  //
  // `mesh-mtls` does NOT have it today -- `aggregateWorkloads` returns
  // `make([]WorkloadMTLS, 0, n)` and the handler seeds the field with a
  // literal empty slice on its no-mesh path, both checked -- and carries the
  // same guard anyway, for the reason the data-protection four do: the cost of
  // a later regression is invisible and permanent.
  //
  // Neither is spelled `?? []` inline, because the field sits inside an object
  // the rest of the card reads. `withList` rebuilds the envelope with the one
  // field normalised, and leaves a body that is not an envelope at all
  // untouched so the view functions can still report it as unreadable rather
  // than as empty.
  "gitops-applications": async (signal) =>
    withList(await read("/v1/gitops/applications", signal), "applications"),
  // No `?namespace=`: the route reads an absent namespace as cluster-scoped,
  // and the cluster-wide posture is what an overview card is for (KTD4). That
  // is also what keeps the widget parameterless on both sides of the catalog.
  "mesh-mtls": async (signal) =>
    withList(await read("/v1/mesh/mtls", signal), "workloads"),

  // The networking family's other four.
  //
  // The third parameterized fetcher and the first that needs TWO values. Both
  // are mandatory -- the handler answers 400 with either missing -- and both
  // are percent-encoded for the reason the two above it are: this is where a
  // stored value becomes a URL. `URLSearchParams` rather than hand-built
  // interpolation, because two values doubles the number of places a missed
  // encode can hide.
  //
  // No `?mesh=`. The route resolves the mesh from its own discovery when the
  // parameter is absent, which is right on every cluster running one mesh;
  // it answers 400 on a cluster running BOTH, asking to be told which. That
  // is a third parameter, and a third parameter is a third stored value on
  // every placement and a third field in the dialog, to disambiguate a
  // configuration the mesh pages themselves treat as unusual. The card takes
  // the 400 and says what it means instead.
  "mesh-golden-signals": (signal, _range, params) =>
    read(
      `/v1/mesh/golden-signals?${new URLSearchParams({
        namespace: params.namespace ?? "",
        service: params.service ?? "",
      }).toString()}`,
      signal,
    ),

  // The REST flow route, NOT `/ws/flows`. See `hubble-flows` in types.ts.
  //
  // `?? []` and it is load-bearing, not defensive: `GetFlows` accumulates into
  // `var flows []FlowRecord` and hands back a NIL slice when the namespace is
  // quiet, which Go marshals as `"data": null`. A source whose data is null
  // never renders -- `resolveWidgetState` reads `data !== null` as "has
  // landed" -- so without this the card would sit in its skeleton forever on
  // exactly the cluster it has the best news for. The same defect
  // `policy-violations-list` shipped with.
  //
  // `limit` is the card's own cap rather than the route's 100 default: the
  // percentages are a share of the batch, and a batch of 100 out of a busy
  // namespace makes that share noise.
  "hubble-flows": async (signal, _range, params) =>
    (await read(
      `/v1/networking/hubble/flows?${new URLSearchParams({
        namespace: params.namespace ?? "",
        limit: String(HUBBLE_FLOW_BATCH),
      }).toString()}`,
      signal,
    )) ?? [],

  // The Gateway API pair, under `/v1/gateway/...` -- its own top-level prefix,
  // not the networking one. `?? []` on both: today each handler returns
  // `make([]T, 0, n)` through `filterByRBAC` and a literal empty slice on its
  // not-installed path, both checked, so neither can answer null now. The
  // guard is here because the cost of a later regression is invisible and
  // permanent, which is the reasoning the data-protection four carry.
  "gateway-gateways": async (signal) =>
    (await read("/v1/gateway/gateways", signal)) ?? [],
  // HTTPRoutes have their own route; `/v1/gateway/routes` serves one NON-HTTP
  // kind per call behind a required `?kind=` and 400s without it, so it is not
  // the route to ask for "all routes".
  "gateway-httproutes": async (signal) =>
    (await read("/v1/gateway/httproutes", signal)) ?? [],

  // The platform family's five. None of them is CRD-discovered and none of
  // them declares a family status: what can be missing is the deployment's
  // database, and `ABSENT_STATUSES` in types.ts maps the status each route
  // says so with onto the unavailable state.
  //
  // `?? []` on the four bare lists below, and on three of them it is
  // LOAD-BEARING rather than defensive. `ClusterStore.List` accumulates into
  // `var clusters []ClusterRecord`, `PostgresStore.Query` into
  // `var entries []Entry`, and `Store.ListNotifications` into
  // `var notifications []Notification` -- three separate nil slices, which Go
  // marshals as `"data": null`. A source whose data is null NEVER RENDERS:
  // `resolveWidgetState` reads `data !== null` as "has landed", so without
  // these guards an empty audit log, an empty registry and -- the common case
  // -- an account with nothing unread would each leave their card in the
  // skeleton forever. That is the fifth, sixth and seventh instance of this
  // shape found in this phase.
  //
  // The preferences pair is the exception and its guard IS defensive:
  // `writeList` in backend/internal/preferences/handler.go normalises a nil
  // slice to `[]` before it writes, and its docstring says so. Carried anyway,
  // because the cost of a later regression is invisible and permanent.
  "clusters-list": async (signal) => (await read("/v1/clusters", signal)) ?? [],
  // `pageSize`, not `limit`: this route's paging parameters are its own
  // (`audit.QueryParams`), not the generic list route's. Asking for `limit`
  // would silently get the handler's default page instead.
  "audit-log": async (signal) =>
    (await read(`/v1/audit/logs?pageSize=${AUDIT_PAGE_SIZE}`, signal)) ?? [],
  // The one platform fetcher that keeps its envelope, because the count the
  // card shows has to be the server's rather than this page's: `metadata.total`
  // is the whole unread population under the same filter, where `data` is one
  // capped page of it. `withList` normalises the nil slice inside the envelope
  // and leaves a body that is not an envelope at all untouched, so
  // `notificationsFeedView` can still report it as unreadable rather than as
  // empty.
  //
  // `read=unread` is `ListOpts.ReadFilter`, whose only two recognised values
  // are "read" and "unread"; anything else is no filter at all, which would
  // make this card report the whole feed as unread.
  "unread-notifications": async (signal) => {
    const res = await api<unknown>(
      `/v1/notifications?read=unread&limit=${UNREAD_FEED_LIMIT}`,
      { method: "GET", signal },
    );
    return withList(
      { items: res.data, total: res.metadata?.total ?? null },
      "items",
    );
  },
  "preference-views": async (signal) =>
    (await read("/v1/preferences/views", signal)) ?? [],
  "preference-pins": async (signal) =>
    (await read("/v1/preferences/pins", signal)) ?? [],

  // The ten discovery routes. Each answers "is this feature installed", which
  // is the question its own list endpoint cannot answer -- see
  // FAMILY_STATUS_KEYS in types.ts. They are range-insensitive, so the 60s
  // refresh keeps them current and a time-range change does not refetch them.
  "policies-status": (signal) => read("/v1/policies/status", signal),
  "gitops-status": (signal) => read("/v1/gitops/status", signal),
  "certificates-status": (signal) => read("/v1/certificates/status", signal),
  "mesh-status": async (signal) => {
    // The one route of the eight that wraps its payload -- `{ status: {...} }`
    // -- kept symmetric with the rest of /mesh/*. Unwrapped here so that
    // `featurePresent` reads one shape rather than a different one per
    // family.
    //
    // A body without the wrapper normalises to an explicit absent verdict
    // rather than to null. Null would have been the wrong safe direction: the
    // shell reads a null source as "has not landed yet", so an unparseable
    // status would have held every mesh card in its skeleton for good instead
    // of saying the mesh is not installed. The sibling normalisers below and
    // beside this one all answer with a verdict for the same reason.
    const body = await read("/v1/mesh/status", signal);
    return (body as { status?: unknown } | null)?.status ?? { detected: false };
  },
  "external-secrets-status": (signal) =>
    read("/v1/externalsecrets/status", signal),
  "velero-status": (signal) => read("/v1/velero/status", signal),
  // The seventh. Same string-valued `detected` the policy, GitOps and mesh
  // statuses use -- `""`, `"trivy"`, `"kubescape"` or `"both"` -- so
  // `featurePresent` reads it without a special case.
  "scanning-status": (signal) => read("/v1/scanning/status", signal),
  // The eighth, and the only one that reads a flag out of `metadata` rather
  // than a body out of `data`.
  //
  // The storage family mounts no `/status` route. What it has instead is the
  // `available` flag every snapshot route attaches to its metadata, which is
  // `checkSnapshotCRDs()` verbatim -- the discovery client asked for
  // `snapshot.storage.k8s.io/v1` behind a 5-minute cache. `read` drops
  // metadata, so this one calls `api` directly and normalises the flag into
  // the `{ detected }` shape `featurePresent` reads, exactly as the mesh
  // status above unwraps its own odd envelope so that function sees one shape
  // rather than eight.
  //
  // Anything other than a literal `true` reads as ABSENT, which is the
  // deliberate direction: a build that cannot parse this route saying "not
  // installed" is a visible bug someone fixes, where one rendering an empty
  // green snapshot card on a cluster with no CSI snapshotter is a bug nobody
  // sees (R1).
  "snapshots-status": async (signal) => {
    const res = await api<unknown>("/v1/storage/snapshot-classes", {
      method: "GET",
      signal,
    });
    const available = (res.metadata as { available?: unknown } | undefined)
      ?.available;
    return { detected: available === true };
  },
  // The ninth. Gateway API DOES mount a `/status` route, and it reports
  // presence as `available: boolean` rather than as `detected` -- so this
  // normalises the flag into the shape `featurePresent` reads, exactly as the
  // mesh status above unwraps its own envelope and the snapshot status above
  // lifts its flag out of metadata. Anything other than a literal `true` reads
  // as ABSENT, which is the deliberate direction for the reason those two
  // carry: a build that cannot parse this route saying "not installed" is a
  // visible bug someone fixes, where one rendering an empty green gateway card
  // on a cluster with no Gateway API is a bug nobody sees (R1).
  "gateway-status": async (signal) => {
    const body = await read("/v1/gateway/status", signal);
    return {
      detected: (body as { available?: unknown } | null)?.available === true,
    };
  },
  // The tenth, and the only family status that is not a CRD check. Hubble is a
  // Cilium feature flag plus a discovered Relay Service, and
  // `/v1/networking/cni` is where the detector publishes both.
  //
  // `features.hubble` and not `features.hubbleRelayAddr`: the flag is what the
  // Cilium ConfigMap says is enabled, while the address is what the detector
  // managed to find, and a Relay the detector missed is a broken lookup rather
  // than an absent feature. Reading the address would report Hubble as
  // uninstalled on a cluster running it, which is the error in the direction
  // that costs an operator a real signal.
  //
  // Always an object, even for a null body: a family status whose data is null
  // leaves the widget in its skeleton for good, because `resolveWidgetState`
  // reads `data !== null` as "has landed" before it ever asks whether the
  // feature is present.
  "hubble-status": async (signal) => {
    const body = await read("/v1/networking/cni", signal);
    const features = (body as { features?: unknown } | null)?.features;
    return {
      detected: (features as { hubble?: unknown } | undefined)?.hubble === true,
    };
  },
};

export const dashboardData: SourceCache = createSourceCache(DASHBOARD_FETCHERS);
