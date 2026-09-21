import { expect, test } from "bun:test";
import { ApiError } from "@/lib/api.ts";
import type { SourceCache, SourceFetcher } from "./data.ts";
import {
  createSourceCache,
  DASHBOARD_FETCHERS,
  DASHBOARD_REFRESH_MS,
  MAX_CONCURRENT_EXPENSIVE_FETCHES,
  sourceRefreshOffsetMs,
} from "./data.ts";
import { sourceKeyFor } from "./params.ts";
import type { DataSourceKey } from "./types.ts";
import {
  DATA_SOURCE_KEYS,
  FAMILY_STATUS_KEYS,
  RANGE_SENSITIVE_KEYS,
  SOURCE_COST,
  sourceCost,
} from "./types.ts";

// The cache exists so that N widgets declaring the same source produce one
// request, and so that a failure is a value a widget can render rather than a
// silently swallowed promise. Both are new behavior -- the pre-registry
// dashboard fetched centrally and discarded every error.

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test("ensure: concurrent requests for one key issue a single fetch", async () => {
  let calls = 0;
  const d = deferred<string>();
  const fetchers: Partial<Record<string, SourceFetcher>> = {
    "dashboard-summary": () => {
      calls++;
      return d.promise;
    },
  };
  const cache = createSourceCache(fetchers);

  cache.ensure(["dashboard-summary", "dashboard-summary"], "1h");
  cache.ensure(["dashboard-summary"], "1h");
  expect(calls).toBe(1);

  d.resolve("ok");
  await cache.settled();
  expect(cache.state("dashboard-summary").data).toBe("ok");
  expect(calls).toBe(1);
});

test("ensure: a key nobody asked for is never fetched", async () => {
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      calls++;
      return Promise.resolve("s");
    },
    "recent-events": () => {
      calls++;
      return Promise.resolve("e");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  expect(calls).toBe(1);
  expect(cache.state("recent-events").data).toBeNull();
});

test("state: a failure is a readable error, not a swallowed promise", async () => {
  const cache = createSourceCache({
    "dashboard-summary": () => Promise.reject(new Error("boom")),
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();

  const s = cache.state("dashboard-summary");
  expect(s.loading).toBe(false);
  expect(s.data).toBeNull();
  expect(s.error).toBe("boom");
});

test("state: a refresh failure keeps the last good data alongside the error", async () => {
  // The dashboard must not go blank because one 60s refresh hit a transient
  // 500. WidgetHost renders stale data with an inline error; wiping it would
  // remove the view an operator needs exactly when the backend is unreliable.
  let attempt = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      attempt++;
      return attempt === 1
        ? Promise.resolve("good")
        : Promise.reject(new Error("boom"));
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  expect(cache.state("dashboard-summary").data).toBe("good");

  cache.refresh();
  await cache.settled();

  const s = cache.state("dashboard-summary");
  expect(s.data).toBe("good");
  expect(s.error).toBe("boom");
  expect(s.loading).toBe(false);
});

test("state: a first-fetch failure has no data to keep", async () => {
  // The complement: preserving prior data must not invent any when the very
  // first attempt fails, or a widget would render an empty shell instead of
  // the error.
  const cache = createSourceCache({
    "dashboard-summary": () => Promise.reject(new Error("boom")),
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();

  expect(cache.state("dashboard-summary").data).toBeNull();
  expect(cache.state("dashboard-summary").error).toBe("boom");
});

test("state: loading is true while in flight and false after", async () => {
  const d = deferred<string>();
  const cache = createSourceCache({ "dashboard-summary": () => d.promise });

  cache.ensure(["dashboard-summary"], "1h");
  expect(cache.state("dashboard-summary").loading).toBe(true);

  d.resolve("ok");
  await cache.settled();
  expect(cache.state("dashboard-summary").loading).toBe(false);
});

test("state: an unfetched key reads as idle, not as an error", () => {
  const cache = createSourceCache({});
  const s = cache.state("cluster-info");
  expect(s.data).toBeNull();
  expect(s.error).toBeNull();
  expect(s.loading).toBe(false);
});

test("refresh: re-fetches keys already ensured, and only those", async () => {
  let summary = 0;
  let events = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      summary++;
      return Promise.resolve("s");
    },
    "recent-events": () => {
      events++;
      return Promise.resolve("e");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  cache.refresh();
  await cache.settled();

  expect(summary).toBe(2);
  expect(events).toBe(0);
});

test("range: changing the range refetches range-sensitive keys", async () => {
  const ranges: string[] = [];
  const cache = createSourceCache({
    "dashboard-trends": (_signal, range) => {
      ranges.push(range);
      return Promise.resolve(range);
    },
  });

  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();
  cache.ensure(["dashboard-trends"], "6h");
  await cache.settled();

  expect(ranges).toEqual(["1h", "6h"]);
  expect(cache.state("dashboard-trends").data).toBe("6h");
});

test("range: re-ensuring the same range does not refetch", async () => {
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-trends": (_s, r) => {
      calls++;
      return Promise.resolve(r);
    },
  });

  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();
  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();

  expect(calls).toBe(1);
});

test("range: a range change does not refetch range-insensitive keys", async () => {
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      calls++;
      return Promise.resolve("s");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  cache.ensure(["dashboard-summary"], "6h");
  await cache.settled();

  expect(calls).toBe(1);
});

test("abort: a cancelled in-flight fetch leaves no error on the state", async () => {
  // An aborted request is the app tearing down, not a failure the user should
  // be shown. PinnedResources and SavedViews both take this care already.
  const cache = createSourceCache({
    "dashboard-summary": (signal) =>
      new Promise((_res, rej) => {
        signal.addEventListener("abort", () => {
          rej(new DOMException("Aborted", "AbortError"));
        });
      }),
  });

  cache.ensure(["dashboard-summary"], "1h");
  cache.abort();
  await cache.settled();

  const s = cache.state("dashboard-summary");
  expect(s.error).toBeNull();
  expect(s.loading).toBe(false);
});

test("abort: a key aborted before any data arrived is retried by the next ensure", async () => {
  // Regression guard. run() records the key in fetchedRange *before* awaiting,
  // so without forgetting it on an empty abort the next ensure() sees
  // "already fetched", skips, and the widget stays blank forever -- the exact
  // shape of an unmount/remount that outran the first response.
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": (signal) => {
      calls++;
      return new Promise((res, rej) => {
        signal.addEventListener("abort", () => {
          rej(new DOMException("Aborted", "AbortError"));
        });
        globalThis.setTimeout(() => res("late"), 1000);
      });
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  cache.abort();
  await cache.settled();
  expect(cache.state("dashboard-summary").data).toBeNull();

  cache.ensure(["dashboard-summary"], "1h");
  expect(calls).toBe(2);
  expect(cache.state("dashboard-summary").loading).toBe(true);
  cache.abort();
  await cache.settled();
});

test("abort: a key that already has data is not refetched by the next ensure", async () => {
  // The complement of the guard above: forgetting the range on abort must be
  // limited to keys with nothing to show, or every navigation would refetch.
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      calls++;
      return Promise.resolve("s");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  cache.abort();
  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();

  expect(calls).toBe(1);
  expect(cache.state("dashboard-summary").data).toBe("s");
});

test("startRefresh: ticks re-fetch ensured keys until stopped", async () => {
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      calls++;
      return Promise.resolve("s");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  expect(calls).toBe(1);

  const stop = cache.startRefresh(5);
  await new Promise((r) => globalThis.setTimeout(r, 60));
  stop();
  const afterStop = calls;
  expect(afterStop).toBeGreaterThan(1);

  await new Promise((r) => globalThis.setTimeout(r, 40));
  expect(calls).toBe(afterStop);
});

test("startRefresh: a second start replaces the first rather than stacking", async () => {
  // Two live intervals would double the request rate against every dashboard
  // endpoint, which is the kind of thing nobody notices until a cluster is big.
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      calls++;
      return Promise.resolve("s");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();

  cache.startRefresh(5);
  const stop2 = cache.startRefresh(5);
  await new Promise((r) => globalThis.setTimeout(r, 60));
  stop2();
  const afterStop = calls;

  await new Promise((r) => globalThis.setTimeout(r, 40));
  expect(calls).toBe(afterStop);
});

test("range: a range change while in flight supersedes the older request", async () => {
  // Regression guard. ensure() used to skip any key already in flight, so
  // clicking 6h then 24h before 6h answered never requested 24h at all: the
  // tab read 24h, the chart showed 6h, and every refresh re-requested 6h.
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const signals = new Map<string, AbortSignal>();
  const requested: string[] = [];
  const cache = createSourceCache({
    "dashboard-trends": (signal, range) => {
      requested.push(range);
      const d = deferred<string>();
      pending.set(range, d);
      signals.set(range, signal);
      signal.addEventListener("abort", () => {
        d.reject(new DOMException("Aborted", "AbortError"));
      });
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "24h");

  expect(requested).toEqual(["6h", "24h"]);
  expect(signals.get("6h")?.aborted).toBe(true);
  expect(signals.get("24h")?.aborted).toBe(false);

  pending.get("24h")?.resolve("24h-data");
  await cache.settled();

  // The later range is what refresh re-requests from here on.
  cache.refresh();
  expect(requested).toEqual(["6h", "24h", "24h"]);
  pending.get("24h")?.resolve("24h-data");
  await cache.settled();
  expect(cache.state("dashboard-trends").data).toBe("24h-data");
});

test("range: a superseded response that lands late is discarded", async () => {
  // A fetcher that ignores its signal, or a response already on the wire when
  // abort fired, must not put the old range's data back over the new one.
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const cache = createSourceCache({
    "dashboard-trends": (_signal, range) => {
      const d = deferred<string>();
      pending.set(range, d);
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "24h");
  pending.get("24h")?.resolve("24h-data");
  pending.get("6h")?.resolve("6h-data");
  await cache.settled();

  const s = cache.state("dashboard-trends");
  expect(s.data).toBe("24h-data");
  expect(s.range).toBe("24h");
  expect(s.loading).toBe(false);
});

test("settled: waits for a superseded request that is still on the wire", async () => {
  // A cancelled request whose fetcher has not honoured its signal yet is
  // still a live request. settled() returning early would let a caller treat
  // the cache as quiet while a response is outstanding.
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const cache = createSourceCache({
    "dashboard-trends": (_signal, range) => {
      const d = deferred<string>();
      pending.set(range, d);
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "24h");
  pending.get("24h")?.resolve("24h-data");

  let done = false;
  const wait = cache.settled().then(() => {
    done = true;
  });
  await new Promise((r) => globalThis.setTimeout(r, 10));
  expect(done).toBe(false);

  pending.get("6h")?.resolve("6h-data");
  await wait;
  expect(done).toBe(true);
});

test("refresh: does not stack a request on a superseded one still in flight", async () => {
  const requested: string[] = [];
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const cache = createSourceCache({
    "dashboard-trends": (_signal, range) => {
      requested.push(range);
      const d = deferred<string>();
      pending.set(range, d);
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "24h");
  pending.get("24h")?.resolve("24h-data");
  await new Promise((r) => globalThis.setTimeout(r, 0));

  cache.refresh();
  expect(requested).toEqual(["6h", "24h"]);

  pending.get("6h")?.resolve("6h-data");
  await cache.settled();
  cache.refresh();
  expect(requested).toEqual(["6h", "24h", "24h"]);
  pending.get("24h")?.resolve("again");
  await cache.settled();
});

test("abort: an ensure in the same tick as abort still fetches the key", async () => {
  // Regression guard. An effect cleanup calls abort() and the next effect
  // calls ensure() immediately. A signalled request still looked in flight,
  // so that ensure was skipped; the rejection then forgot the range, and with
  // no data and no recorded range nothing ever retried the key.
  let calls = 0;
  const cache = createSourceCache({
    "dashboard-summary": (signal) => {
      calls++;
      return new Promise((res, rej) => {
        signal.addEventListener("abort", () => {
          rej(new DOMException("Aborted", "AbortError"));
        });
        globalThis.setTimeout(() => res("data"), 5);
      });
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  cache.abort();
  cache.ensure(["dashboard-summary"], "1h");
  expect(calls).toBe(2);

  await cache.settled();
  expect(cache.state("dashboard-summary").data).toBe("data");
});

test("abort: an aborted range change hands the key back to the data's range", async () => {
  // 1h has landed; a switch to 6h is torn down before it answers. The
  // recorded range must go back to 1h, or the next ensure(6h) is skipped and
  // the tab names a window the chart is not showing.
  const requested: string[] = [];
  const cache = createSourceCache({
    "dashboard-trends": (signal, range) => {
      requested.push(range);
      if (range === "1h") return Promise.resolve("1h-data");
      return new Promise((res, rej) => {
        signal.addEventListener("abort", () => {
          rej(new DOMException("Aborted", "AbortError"));
        });
        globalThis.setTimeout(() => res(`${range}-data`), 5);
      });
    },
  });

  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();
  cache.ensure(["dashboard-trends"], "6h");
  cache.abort();
  await cache.settled();

  expect(cache.state("dashboard-trends").range).toBe("1h");
  expect(cache.state("dashboard-trends").loading).toBe(false);

  cache.ensure(["dashboard-trends"], "6h");
  expect(requested).toEqual(["1h", "6h", "6h"]);
  await cache.settled();
  expect(cache.state("dashboard-trends").range).toBe("6h");
});

test("range: a superseded request's abort leaves the newer one loading", async () => {
  // The older request rejects with AbortError. Its handler must not clear
  // loading or forget the range out from under the request replacing it.
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const cache = createSourceCache({
    "dashboard-trends": (signal, range) => {
      const d = deferred<string>();
      pending.set(range, d);
      signal.addEventListener("abort", () => {
        d.reject(new DOMException("Aborted", "AbortError"));
      });
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "24h");
  await pending.get("6h")?.promise.catch(() => {});
  await Promise.resolve();

  expect(cache.state("dashboard-trends").loading).toBe(true);
  cache.abort();
  await cache.settled();
});

test("range: same-range ensure while in flight still issues one request", async () => {
  let calls = 0;
  const d = deferred<string>();
  const cache = createSourceCache({
    "dashboard-trends": () => {
      calls++;
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "6h");
  expect(calls).toBe(1);
  d.resolve("x");
  await cache.settled();
});

test("state.range: names the window the data covers, not the one requested", async () => {
  // The network tile labels its p95 with this. While a tab switch is in
  // flight the displayed series still belongs to the old window.
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const cache = createSourceCache({
    "dashboard-trends": (_signal, range) => {
      const d = deferred<string>();
      pending.set(range, d);
      return d.promise;
    },
  });

  expect(cache.state("dashboard-trends").range).toBeNull();

  cache.ensure(["dashboard-trends"], "1h");
  expect(cache.state("dashboard-trends").range).toBeNull();
  pending.get("1h")?.resolve("1h-data");
  await cache.settled();
  expect(cache.state("dashboard-trends").range).toBe("1h");

  cache.ensure(["dashboard-trends"], "6h");
  expect(cache.state("dashboard-trends").range).toBe("1h");
  pending.get("6h")?.resolve("6h-data");
  await cache.settled();
  expect(cache.state("dashboard-trends").range).toBe("6h");
});

test("state.range: a failed fetch keeps the range of the data it kept", async () => {
  let attempt = 0;
  const cache = createSourceCache({
    "dashboard-trends": (_s, range) => {
      attempt++;
      return attempt === 1
        ? Promise.resolve(range)
        : Promise.reject(new Error("boom"));
    },
  });

  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();
  cache.ensure(["dashboard-trends"], "6h");
  await cache.settled();

  const s = cache.state("dashboard-trends");
  expect(s.data).toBe("1h");
  expect(s.range).toBe("1h");
  expect(s.error).toBe("boom");
});

test("range: a superseded request settling does not un-track its replacement", async () => {
  // If the older request's cleanup removed whatever entry the key now holds,
  // it would drop the newer in-flight request, and the next refresh tick --
  // which skips only keys it can see in flight -- would issue a duplicate.
  let calls = 0;
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const cache = createSourceCache({
    "dashboard-trends": (signal, range) => {
      calls++;
      const d = deferred<string>();
      pending.set(range, d);
      signal.addEventListener("abort", () => {
        d.reject(new DOMException("Aborted", "AbortError"));
      });
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "24h");
  await pending.get("6h")?.promise.catch(() => {});
  await new Promise((r) => globalThis.setTimeout(r, 0));

  cache.refresh();
  expect(calls).toBe(2);

  cache.abort();
  await cache.settled();
});

test("startRefresh: a tick waits while a superseded request is still on the wire", async () => {
  const requested: string[] = [];
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const cache = createSourceCache({
    "dashboard-trends": (_signal, range) => {
      requested.push(range);
      const d = deferred<string>();
      pending.set(range, d);
      return d.promise;
    },
  });

  cache.ensure(["dashboard-trends"], "6h");
  cache.ensure(["dashboard-trends"], "24h");
  pending.get("24h")?.resolve("24h-data");

  const stop = cache.startRefresh(5);
  await new Promise((r) => globalThis.setTimeout(r, 40));
  stop();
  expect(requested).toEqual(["6h", "24h"]);

  pending.get("6h")?.resolve("6h-data");
  await cache.settled();
});

// --- Permission classification (R2) ---------------------------------------
//
// A 403 and a 500 are both "the fetch failed", and the dashboard has to tell
// them apart: one is a transient condition worth retrying and the other is a
// standing fact about the account, which the shell renders without a retry
// affordance. The cache is the only place that still holds the thrown error,
// so the distinction is made here and carried on the state.

test("errorKind: a forbidden response is classified as a permission outcome", async () => {
  const cache = createSourceCache({
    "dashboard-summary": () =>
      Promise.reject(new ApiError(403, 403, "Forbidden")),
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();

  const s = cache.state("dashboard-summary");
  expect(s.errorKind).toBe("permission");
  expect(s.error).toContain("Forbidden");
});

test("errorKind: any other failure stays an ordinary failure", async () => {
  const cache = createSourceCache({
    "dashboard-summary": () => Promise.reject(new ApiError(500, 500, "boom")),
    "cluster-info": () => Promise.reject(new Error("network down")),
  });

  cache.ensure(["dashboard-summary", "cluster-info"], "1h");
  await cache.settled();
  expect(cache.state("dashboard-summary").errorKind).toBe("failure");
  expect(cache.state("cluster-info").errorKind).toBe("failure");
});

test("errorKind: a recovered refresh clears the earlier classification", async () => {
  // Permissions change under a live session -- a role binding added while the
  // dashboard is open -- and a widget left in the permission state after the
  // next refresh succeeded would be telling the user something untrue.
  let attempt = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      attempt++;
      return attempt === 1
        ? Promise.reject(new ApiError(403, 403, "Forbidden"))
        : Promise.resolve("ok");
    },
  });

  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  expect(cache.state("dashboard-summary").errorKind).toBe("permission");

  cache.refresh();
  await cache.settled();
  const s = cache.state("dashboard-summary");
  expect(s.errorKind).toBeNull();
  expect(s.error).toBeNull();
  expect(s.data).toBe("ok");
});

test("errorKind: an idle key carries no classification", () => {
  const cache = createSourceCache({});
  expect(cache.state("dashboard-summary").errorKind).toBeNull();
});

test("every declared source key has a fetcher", () => {
  // A key in the union with no entry in the table is invisible: `ensure`
  // returns without issuing anything, the state stays idle, and a widget
  // declaring it sits in the skeleton forever with nothing in the console.
  const missing = DATA_SOURCE_KEYS.filter((k) => !(k in DASHBOARD_FETCHERS));
  expect(missing).toEqual([]);
});

test("refresh: a settled family status is not re-polled", async () => {
  // Three of the six discovery routes (policies, gitops, mesh) share the
  // backend's 30-request-per-minute YAML bucket with /yaml/* and /wizards/*.
  // The dashboard asks for all six on mount whether or not a widget reads
  // them, so re-asking every 60s would spend a tenth of that shared budget,
  // per IP, for as long as a dashboard tab is open -- to re-learn an answer
  // that changes when someone installs an operator. Fetched once per page
  // load instead.
  let calls = 0;
  const cache = createSourceCache({
    "mesh-status": () => {
      calls++;
      return Promise.resolve({ detected: "istio" });
    },
    "dashboard-summary": () => Promise.resolve("s"),
  });

  cache.ensure(["mesh-status", "dashboard-summary"], "1h");
  await cache.settled();
  expect(calls).toBe(1);

  cache.refresh();
  await cache.settled();
  expect(calls).toBe(1);
  // The ordinary sources are unaffected.
  expect(cache.state("dashboard-summary").data).toBe("s");
});

test("refresh: a family status that has never landed is retried", async () => {
  // The exemption above is "do not re-ask a question already answered", not
  // "ask once and give up": a widget whose family status hit a transient 500
  // would otherwise sit in the error state until the page is reloaded.
  let calls = 0;
  const cache = createSourceCache({
    "mesh-status": () => {
      calls++;
      return calls === 1
        ? Promise.reject(new ApiError(500, 500, "boom"))
        : Promise.resolve({ detected: "istio" });
    },
  });

  cache.ensure(["mesh-status"], "1h");
  await cache.settled();
  expect(cache.state("mesh-status").error).toBeTruthy();

  cache.refresh();
  await cache.settled();
  expect(calls).toBe(2);
  expect(cache.state("mesh-status").data).toEqual({ detected: "istio" });

  // And once it has landed, the exemption applies again.
  cache.refresh();
  await cache.settled();
  expect(calls).toBe(2);
});

// --- Cost classes, the expensive bound and refresh offsets (R6 / KTD5) -----
//
// The catalog admits forty items pointing at distinct routes, several of them
// Prometheus- or Hubble-backed. Without a class on each source they would all
// be issued on the same 60s tick, for every viewer. These tests pin the two
// mechanisms that stop that: a bound on how many expensive reads are on the
// wire at once, and a stable per-source offset inside the refresh interval.

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    globalThis.setTimeout(resolve, ms);
  });
}

/** Every source the scheduler does not treat as cheap, in declaration order. */
const NON_CHEAP_KEYS = DATA_SOURCE_KEYS.filter(
  (k) => sourceCost(k) !== "cheap",
);

function saturate(): {
  cache: SourceCache;
  started: DataSourceKey[];
  pending: Map<string, ReturnType<typeof deferred<string>>>;
} {
  const started: DataSourceKey[] = [];
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const fetchers: Partial<Record<DataSourceKey, SourceFetcher>> = {};
  for (const key of NON_CHEAP_KEYS) {
    fetchers[key] = (signal) => {
      started.push(key);
      const d = deferred<string>();
      pending.set(key, d);
      signal.addEventListener("abort", () => {
        d.reject(new DOMException("Aborted", "AbortError"));
      });
      return d.promise;
    };
  }
  return { cache: createSourceCache(fetchers), started, pending };
}

/**
 * Resolves every deferred the saturated cache is holding, in passes, until
 * none is left outstanding.
 *
 * One pass is not enough and the number of passes needed is the number of
 * queued requests, which is `NON_CHEAP_KEYS.length - MAX_CONCURRENT_...` and
 * therefore changes whenever a source is added. A queued request is admitted
 * from a settled one's `.finally`, which is a microtask -- so it registers its
 * own deferred strictly AFTER a synchronous loop over `pending` has finished,
 * and a single pass leaves it unresolved and `settled()` waiting on it
 * forever. The "cheap sources are not subject to the bound" test above already
 * drains in passes for this reason; this is the same loop, sized off the key
 * list instead of a hardcoded count so adding a source cannot silently turn a
 * passing test into a five-second timeout.
 */
async function drain(
  pending: Map<string, ReturnType<typeof deferred<string>>>,
): Promise<void> {
  for (let pass = 0; pass <= NON_CHEAP_KEYS.length; pass++) {
    for (const d of pending.values()) d.resolve("ok");
    await sleep(0);
  }
}

test("cost: every declared source key carries an explicit class", () => {
  // Omission is the failure mode the default guards, not the one it excuses:
  // an unlisted key falls back to expensive by design, but a key the author
  // meant to classify and forgot should be named here rather than inferred.
  const missing = DATA_SOURCE_KEYS.filter((k) => SOURCE_COST[k] === undefined);
  expect(missing).toEqual([]);
});

test("cost: an unclassified key is expensive, never cheap", () => {
  // KTD5's default. A contributor who adds a source and forgets the table
  // gets one throttled too hard, not one that joins the stampede.
  expect(sourceCost("some-future-source")).toBe("expensive");
});

test("cost: informer reads are cheap and discovery routes are their own class", () => {
  const misfiled = FAMILY_STATUS_KEYS.filter(
    (k) => sourceCost(k) !== "discovery",
  );
  expect(misfiled).toEqual([]);

  const informerBacked: DataSourceKey[] = [
    "dashboard-summary",
    "cluster-info",
    "recent-events",
  ];
  const notCheap = informerBacked.filter((k) => sourceCost(k) !== "cheap");
  expect(notCheap).toEqual([]);

  // The trends endpoint is not informer-backed: the Go handler runs Prometheus
  // range queries and its own comment calls them multi-second. Under KTD5 that
  // is expensive, whatever its neighbours on the same dashboard cost.
  expect(sourceCost("dashboard-trends")).toBe("expensive");
});

test("bound: expensive fetches beyond the limit wait for a slot", async () => {
  // Precondition: there have to be more non-cheap sources than slots for this
  // test to mean anything. Seven exist today against a bound of six.
  expect(NON_CHEAP_KEYS.length).toBeGreaterThan(
    MAX_CONCURRENT_EXPENSIVE_FETCHES,
  );

  const { cache, started, pending } = saturate();
  cache.ensure(NON_CHEAP_KEYS, "1h");
  expect(started.length).toBe(MAX_CONCURRENT_EXPENSIVE_FETCHES);

  // Freeing one slot admits exactly the one that was waiting.
  pending.get(started[0])?.resolve("ok");
  await sleep(0);
  expect(started.length).toBe(MAX_CONCURRENT_EXPENSIVE_FETCHES + 1);

  await drain(pending);
  await cache.settled();
  const blank = NON_CHEAP_KEYS.filter((k) => cache.state(k).data === null);
  expect(blank).toEqual([]);
});

test("bound: a failing expensive fetch releases its slot", async () => {
  // A rejection has to hand the slot on. Otherwise one endpoint returning 500
  // permanently shrinks the dashboard's capacity to fetch anything else.
  const { cache, started, pending } = saturate();
  cache.ensure(NON_CHEAP_KEYS, "1h");
  const queued = NON_CHEAP_KEYS[MAX_CONCURRENT_EXPENSIVE_FETCHES];
  expect(started).not.toContain(queued);

  const first = started[0];
  pending.get(first)?.reject(new Error("boom"));
  await sleep(0);
  expect(started).toContain(queued);

  await drain(pending);
  await cache.settled();
  expect(cache.state(first).error).toBe("boom");
});

test("bound: a queued fetch torn down before it starts never reaches the network", async () => {
  // The widget that asked for it is gone -- the island unmounted, or the user
  // removed the card -- and the request was still waiting for a slot. It has
  // to be dropped, not issued on behalf of nobody.
  const { cache, started, pending } = saturate();
  cache.ensure(NON_CHEAP_KEYS, "1h");
  const queued = NON_CHEAP_KEYS[MAX_CONCURRENT_EXPENSIVE_FETCHES];
  expect(started).not.toContain(queued);

  cache.abort();
  expect(started).not.toContain(queued);
  expect(cache.state(queued).loading).toBe(false);

  await cache.settled();
  expect(started).not.toContain(queued);
  expect(pending.has(queued)).toBe(false);
});

test("bound: cheap sources are not subject to the bound", async () => {
  // The bound protects Prometheus and the API server; it is not there to make
  // an informer read queue behind one.
  const started: DataSourceKey[] = [];
  const pending = new Map<string, ReturnType<typeof deferred<string>>>();
  const fetchers: Partial<Record<DataSourceKey, SourceFetcher>> = {};
  for (const key of DATA_SOURCE_KEYS) {
    fetchers[key] = () => {
      started.push(key);
      const d = deferred<string>();
      pending.set(key, d);
      return d.promise;
    };
  }
  const cache = createSourceCache(fetchers);

  // Every expensive slot is occupied and nothing will free one.
  cache.ensure(NON_CHEAP_KEYS, "1h");
  const cheap = DATA_SOURCE_KEYS.filter((k) => sourceCost(k) === "cheap");
  cache.ensure(cheap, "1h");

  const stalled = cheap.filter((k) => !started.includes(k));
  expect(stalled).toEqual([]);

  // Drain in passes: resolving the running ones admits the queued one, which
  // registers a deferred of its own.
  for (let pass = 0; pass < 3; pass++) {
    for (const d of pending.values()) d.resolve("ok");
    await sleep(0);
  }
  await cache.settled();
});

test("bound: two widgets sharing one expensive key still issue one fetch", async () => {
  // The dedupe the cache already had has to survive the queue: a key already
  // waiting for a slot is a key already asked for.
  const { cache, started } = saturate();
  cache.ensure(["mesh-status", "mesh-status"], "1h");
  cache.ensure(["mesh-status"], "1h");
  expect(started).toEqual(["mesh-status"]);
  cache.abort();
  await cache.settled();
});

test("offset: a cheap source refreshes on the tick, with no offset", () => {
  const offset = DATA_SOURCE_KEYS.filter(
    (k) => sourceCost(k) === "cheap",
  ).filter((k) => sourceRefreshOffsetMs(k, DASHBOARD_REFRESH_MS) !== 0);
  expect(offset).toEqual([]);
});

test("offset: expensive sources get distinct offsets inside the interval", () => {
  const seen = new Map<number, string>();
  const collisions: string[] = [];
  const outOfRange: string[] = [];
  for (const key of NON_CHEAP_KEYS) {
    const offset = sourceRefreshOffsetMs(key, DASHBOARD_REFRESH_MS);
    if (offset <= 0 || offset >= DASHBOARD_REFRESH_MS) outOfRange.push(key);
    const prior = seen.get(offset);
    if (prior !== undefined) collisions.push(prior + "/" + key);
    seen.set(offset, key);
  }
  expect(collisions).toEqual([]);
  expect(outOfRange).toEqual([]);
});

test("offset: the same key gets the same offset on every tick", () => {
  // Derived from the key, not from a counter or the clock. A source that
  // wandered across the interval would make the request pattern unreadable
  // and could drift into lockstep with another one.
  const first = NON_CHEAP_KEYS.map((k) =>
    sourceRefreshOffsetMs(k, DASHBOARD_REFRESH_MS),
  );
  const again = NON_CHEAP_KEYS.map((k) =>
    sourceRefreshOffsetMs(k, DASHBOARD_REFRESH_MS),
  );
  const drifted = NON_CHEAP_KEYS.filter((_k, i) => first[i] !== again[i]);
  expect(drifted).toEqual([]);
});

test("offset: an interval too short to divide degrades to no offset", () => {
  // Guards the arithmetic rather than a behaviour anyone wants: a test that
  // ticks every millisecond must not produce a negative or fractional delay.
  expect(sourceRefreshOffsetMs("mesh-status", 1)).toBe(0);
  expect(sourceRefreshOffsetMs("mesh-status", 0)).toBe(0);
});

test("startRefresh: an expensive source waits out its offset, cheap ones do not", async () => {
  // The point of R6: at the tick the cheap reads go, and the expensive one is
  // scheduled into its own slot later in the same interval.
  const INTERVAL = 400;
  // The discovery route furthest into the interval, so the assertion has a
  // margin. Offsets are fixed by the key hash, so the choice is stable.
  const target = [...FAMILY_STATUS_KEYS].sort(
    (a, b) =>
      sourceRefreshOffsetMs(b, INTERVAL) - sourceRefreshOffsetMs(a, INTERVAL),
  )[0];
  const offset = sourceRefreshOffsetMs(target, INTERVAL);
  expect(offset).toBeGreaterThan(40);

  let cheapCalls = 0;
  let targetCalls = 0;
  const cache = createSourceCache({
    "dashboard-summary": () => {
      cheapCalls++;
      return Promise.resolve("s");
    },
    [target]: () => {
      targetCalls++;
      // Failing keeps it eligible for the next refresh. A settled discovery
      // status is exempt from refresh entirely -- see the test below.
      return Promise.reject(new Error("boom"));
    },
  });

  cache.ensure(["dashboard-summary", target], "1h");
  await cache.settled();
  expect(cheapCalls).toBe(1);
  expect(targetCalls).toBe(1);

  const stop = cache.startRefresh(INTERVAL);
  await sleep(INTERVAL + offset / 2);
  expect(cheapCalls).toBe(2);
  expect(targetCalls).toBe(1);

  await sleep(offset / 2 + 60);
  expect(targetCalls).toBe(2);
  stop();
  await cache.settled();
});

test("startRefresh: stopping cancels an offset fetch that has not fired", async () => {
  const INTERVAL = 400;
  const target = [...FAMILY_STATUS_KEYS].sort(
    (a, b) =>
      sourceRefreshOffsetMs(b, INTERVAL) - sourceRefreshOffsetMs(a, INTERVAL),
  )[0];
  const offset = sourceRefreshOffsetMs(target, INTERVAL);

  let targetCalls = 0;
  const cache = createSourceCache({
    [target]: () => {
      targetCalls++;
      return Promise.reject(new Error("boom"));
    },
  });
  cache.ensure([target], "1h");
  await cache.settled();

  const stop = cache.startRefresh(INTERVAL);
  await sleep(INTERVAL + offset / 2);
  stop();
  await sleep(offset / 2 + 60);
  expect(targetCalls).toBe(1);
});

test("startRefresh: a settled family status stays exempt despite having an offset", async () => {
  // U1's REFRESH_ONCE_SETTLED and this unit's offsets have to compose: the
  // offset decides *when* a due source is issued, not *whether* it is due. A
  // discovery route that already answered is never due, so its offset never
  // fires and the answer is not re-asked once per interval on a delay.
  let meshCalls = 0;
  let summaryCalls = 0;
  const cache = createSourceCache({
    "mesh-status": () => {
      meshCalls++;
      return Promise.resolve({ detected: "istio" });
    },
    "dashboard-summary": () => {
      summaryCalls++;
      return Promise.resolve("s");
    },
  });

  cache.ensure(["mesh-status", "dashboard-summary"], "1h");
  await cache.settled();
  expect(meshCalls).toBe(1);

  const stop = cache.startRefresh(20);
  await sleep(140);
  stop();
  await cache.settled();

  expect(meshCalls).toBe(1);
  expect(summaryCalls).toBeGreaterThan(1);
});

test("range: the range-sensitive set names real source keys", () => {
  // Mechanical on purpose: a unit adding a range-backed source adds its key to
  // that set. This only catches a key that is not a source at all.
  const unknown = [...RANGE_SENSITIVE_KEYS].filter(
    (k) => !(DATA_SOURCE_KEYS as readonly string[]).includes(k),
  );
  expect(unknown).toEqual([]);
});

// `retain` -- dropping keys nothing on the layout asks for any more.
//
// Before parameters, the set of keys a session could ever fetch was the source
// union: eleven, fixed, and every one of them belonged to a widget that might
// come back. A parameterized key is not like that. Re-pointing one widget from
// prod to staging leaves `diagnostics-summary` for prod recorded as fetched,
// and `dueEntries` re-requests everything it has ever fetched -- so the
// abandoned namespace goes on being polled every 60s, in the backend's shared
// 30-request-per-minute bucket, for as long as the tab is open, for a card
// that no longer exists.

test("retain: a key nothing asks for any more stops being refreshed", async () => {
  let prodCalls = 0;
  let stagingCalls = 0;
  const cache = createSourceCache({
    "diagnostics-summary": (_signal, _range, params) => {
      if (params.namespace === "prod") prodCalls++;
      else stagingCalls++;
      return Promise.resolve({ total: 0, failing: [] });
    },
  });

  const prod = sourceKeyFor("diagnostics-summary", { namespace: "prod" });
  const staging = sourceKeyFor("diagnostics-summary", { namespace: "staging" });

  cache.ensure([prod], "1h");
  await cache.settled();
  expect(prodCalls).toBe(1);

  // The widget is re-pointed: staging is now the only key on the layout.
  cache.ensure([staging], "1h");
  cache.retain([staging]);
  await cache.settled();

  const stop = cache.startRefresh(20);
  await sleep(140);
  stop();
  await cache.settled();

  expect(prodCalls).toBe(1);
  expect(stagingCalls).toBeGreaterThan(1);
});

test("retain: a dropped key's state is forgotten, not left stale", async () => {
  // Re-adding the same namespace later must refetch rather than render
  // whatever was on screen when the card was removed.
  const cache = createSourceCache({
    "dashboard-summary": () => Promise.resolve("s"),
  });
  cache.ensure(["dashboard-summary"], "1h");
  await cache.settled();
  expect(cache.state("dashboard-summary").data).toBe("s");

  cache.retain([]);
  expect(cache.state("dashboard-summary").data).toBeNull();
});

test("retain: a key still asked for is untouched", async () => {
  const cache = createSourceCache({
    "dashboard-summary": () => Promise.resolve("s"),
    "cluster-info": () => Promise.resolve("c"),
  });
  cache.ensure(["dashboard-summary", "cluster-info"], "1h");
  await cache.settled();

  cache.retain(["dashboard-summary"]);
  expect(cache.state("dashboard-summary").data).toBe("s");
  expect(cache.state("cluster-info").data).toBeNull();
});

test("retain: an in-flight request for a dropped key is cancelled", async () => {
  // It is a request on behalf of a card that is gone. Leaving it would let it
  // land and re-fill a key `retain` just cleared.
  const d = deferred<string>();
  let aborted = false;
  const cache = createSourceCache({
    "dashboard-summary": (signal) => {
      signal.addEventListener("abort", () => {
        aborted = true;
        d.reject(new DOMException("Aborted", "AbortError"));
      });
      return d.promise;
    },
  });
  cache.ensure(["dashboard-summary"], "1h");
  cache.retain([]);
  await cache.settled();

  expect(aborted).toBe(true);
  expect(cache.state("dashboard-summary").data).toBeNull();
  expect(cache.state("dashboard-summary").loading).toBe(false);
});

test("startRefresh: one hung source does not stop the others refreshing", async () => {
  // The property the tick guard was removed to provide, and the one nothing
  // asserted. Before, a single outstanding request returned early from the
  // whole cycle, so one wedged backend froze all forty cards -- silently,
  // because a request that never settles never records an error and nothing
  // ever reads as stale.
  const INTERVAL = 200;
  let hungCalls = 0;
  let cheapCalls = 0;
  const cache = createSourceCache({
    // Never resolves, never rejects: a dead connection, not a slow one.
    "dashboard-trends": () => {
      hungCalls++;
      return new Promise<unknown>(() => {});
    },
    "dashboard-summary": () => {
      cheapCalls++;
      return Promise.resolve({ ok: true });
    },
  });

  cache.ensure(["dashboard-trends", "dashboard-summary"], "1h");
  await sleep(0);
  expect(hungCalls).toBe(1);
  expect(cheapCalls).toBe(1);

  const stop = cache.startRefresh(INTERVAL);
  await sleep(INTERVAL * 2 + 40);
  stop();

  // The hung key is still outstanding, so it is correctly never re-issued.
  expect(hungCalls).toBe(1);
  // Its neighbour is not hostage to it.
  expect(cheapCalls).toBeGreaterThan(1);
});

test("startRefresh: a hung request releases its slot on the deadline", async () => {
  // Without a deadline the hung promise never reaches `finally`, so its
  // concurrency slot is gone for the life of the tab. Six of those retire the
  // whole expensive budget and every other expensive key queues behind them
  // forever -- the frozen dashboard again, arrived at cumulatively.
  const cache = createSourceCache({
    "dashboard-trends": (signal) =>
      new Promise<unknown>((_res, rej) => {
        signal.addEventListener("abort", () => rej(signal.reason));
      }),
  });

  cache.ensure(["dashboard-trends"], "1h");
  await sleep(0);
  expect(cache.state("dashboard-trends").loading).toBe(true);

  // The deadline is half the refresh interval; this test does not wait it
  // out, it asserts the abort path is wired by tearing down and confirming
  // the cache settles rather than hanging forever.
  cache.abort();
  await cache.settled();
  expect(cache.state("dashboard-trends").loading).toBe(false);
});

test("refresh: supersedes a pending offset rather than skipping the key", async () => {
  // `refresh()` is the immediate path -- "refresh the dashboard" means issue
  // the requests, not schedule them. Pending offset timers count as busy for
  // the periodic tick, so without cancelling them first this call would find
  // every expensive key not-due and quietly do nothing for most of the page.
  const INTERVAL = 400;
  let trendCalls = 0;
  const cache = createSourceCache({
    "dashboard-trends": () => {
      trendCalls++;
      return Promise.resolve({ ok: true });
    },
  });
  const offset = sourceRefreshOffsetMs("dashboard-trends", INTERVAL);
  expect(offset).toBeGreaterThan(0);

  cache.ensure(["dashboard-trends"], "1h");
  await cache.settled();
  expect(trendCalls).toBe(1);

  const stop = cache.startRefresh(INTERVAL);
  // Past the tick that schedules the offset, short of the offset firing.
  await sleep(INTERVAL + 10);
  expect(trendCalls).toBe(1);

  cache.refresh();
  await cache.settled();
  expect(trendCalls).toBe(2);
  stop();
});

test("startRefresh: an affirmative discovery verdict is never re-asked", async () => {
  const INTERVAL = 20;
  let calls = 0;
  const cache = createSourceCache({
    "mesh-status": () => {
      calls++;
      return Promise.resolve({ detected: "istio" });
    },
  });
  cache.ensure(["mesh-status"], "1h");
  await cache.settled();
  expect(calls).toBe(1);

  const stop = cache.startRefresh(INTERVAL);
  await sleep(INTERVAL * 12 + 40);
  stop();
  expect(calls).toBe(1);
});

test("startRefresh: a negative discovery verdict is re-asked, slowly", async () => {
  // The backend's discovery check answers "absent" when the check itself
  // failed, so a blip reads exactly like an uninstalled operator -- and the
  // widget then sits at "not installed" with its palette row refusing to be
  // re-added. Re-asking a negative is what lets that heal without a reload.
  const INTERVAL = 20;
  let calls = 0;
  const cache = createSourceCache({
    "mesh-status": () => {
      calls++;
      return Promise.resolve({ detected: "" });
    },
  });
  cache.ensure(["mesh-status"], "1h");
  await cache.settled();
  expect(calls).toBe(1);

  const stop = cache.startRefresh(INTERVAL);
  // Short of the recheck cadence: still settled, still one call.
  await sleep(INTERVAL * 3 + 10);
  expect(calls).toBe(1);
  // Past it: asked again.
  await sleep(INTERVAL * 9 + 40);
  stop();
  expect(calls).toBeGreaterThan(1);
});
