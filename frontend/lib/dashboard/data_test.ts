import { expect, test } from "bun:test";
import { ApiError } from "@/lib/api.ts";
import type { SourceFetcher } from "./data.ts";
import { createSourceCache, DASHBOARD_FETCHERS } from "./data.ts";
import { DATA_SOURCE_KEYS } from "./types.ts";

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
