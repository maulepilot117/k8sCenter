import { expect, test } from "bun:test";
import type { SourceFetcher } from "./data.ts";
import { createSourceCache } from "./data.ts";

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
