import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import {
  fetchCounts,
  formatCount,
  getCount,
  REMOTE_COUNT_CAP,
  resetCountsForTest,
  resourceCounts,
  resourceCountsTruncated,
} from "./resource-counts.ts";

/**
 * The counts store's fetch outcome handling.
 *
 * `fetchCounts` is driven directly rather than through the module's debounced
 * effect: that effect is wired only when `IS_BROWSER` was true at import, and
 * `bun test` shares one module registry across files, so whether it exists
 * depends on which test file happened to load the store first. `fetchCounts`
 * is the function the effect calls, so this is the same code path.
 */

interface Call {
  url: string;
  clusterHeader: string | null;
}

let calls: Call[] = [];
const originalFetch = globalThis.fetch;

/** Every request answers with `status` and `payload`. */
function answer(status: number, payload: unknown) {
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    calls.push({
      url: String(input),
      clusterHeader: new Headers(init?.headers).get("X-Cluster-ID"),
    });
    return Promise.resolve(
      new Response(JSON.stringify(payload), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
  resetCountsForTest();
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  resetCountsForTest();
});

describe("fetchCounts", () => {
  test("a 400 on a remote cluster keeps that cluster's counts", async () => {
    answer(200, { data: { pods: 7 } });
    await fetchCounts("all", "abc123");
    answer(400, { error: { code: 400, message: "bad request" } });
    await fetchCounts("all", "abc123");
    expect(calls[1].clusterHeader).toBe("abc123");
    expect(resourceCounts.value).toEqual({ pods: 7 });
  });

  test("a cluster switch clears the previous cluster's counts before the read lands", async () => {
    answer(200, { data: { pods: 7 } });
    await fetchCounts("all", "local");
    expect(resourceCounts.value).toEqual({ pods: 7 });
    let release: (r: Response) => void = () => {};
    globalThis.fetch = (() =>
      new Promise<Response>((r) => {
        release = r;
      })) as unknown as typeof globalThis.fetch;
    const pending = fetchCounts("all", "abc123");
    expect(resourceCounts.value).toBeNull();
    release(
      new Response(JSON.stringify({ data: { pods: 2 } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await pending;
    expect(resourceCounts.value).toEqual({ pods: 2 });
  });

  test("truncated metadata sets the signal and a plain response resets it", async () => {
    answer(200, {
      data: { pods: 5000 },
      metadata: { total: 5000, truncated: true },
    });
    await fetchCounts("all", "abc123");
    expect(resourceCountsTruncated.value).toBe(true);
    expect(formatCount("pods")).toBe("5000+");
    answer(200, { data: { pods: 5000 }, metadata: { total: 5000 } });
    await fetchCounts("all", "abc123");
    expect(resourceCountsTruncated.value).toBe(false);
    expect(formatCount("pods")).toBe("5000");
    expect(REMOTE_COUNT_CAP).toBe(5000);
  });

  test("formatCount marks only kinds at the cap", async () => {
    answer(200, {
      data: { pods: 5000, services: 3 },
      metadata: { total: 5003, truncated: true },
    });
    await fetchCounts("all", "abc123");
    expect(formatCount("services")).toBe("3");
  });

  test("a failed read for another cluster clears the previous cluster's counts", async () => {
    // Counts from the local cluster are wrong data for the one just selected,
    // not stale data for it.
    answer(200, { data: { pods: 7 } });
    await fetchCounts("all", "local");
    expect(resourceCounts.value).toEqual({ pods: 7 });
    answer(500, { error: { code: 500, message: "boom" } });
    await fetchCounts("all", "abc123");
    expect(resourceCounts.value).toBeNull();
  });

  test("a failed read for the same cluster keeps its counts", async () => {
    answer(200, { data: { pods: 7 } });
    await fetchCounts("all", "abc123");
    answer(500, { error: { code: 500, message: "boom" } });
    await fetchCounts("default", "abc123");
    expect(resourceCounts.value).toEqual({ pods: 7 });
  });

  test("a later successful fetch sets counts", async () => {
    answer(200, { data: { pods: 3 } });
    await fetchCounts("default", "local");
    expect(calls[0].url).toContain("/v1/resources/counts?namespace=default");
    expect(calls[0].clusterHeader).toBe("local");
    expect(resourceCounts.value).toEqual({ pods: 3 });
  });
});

describe("getCount", () => {
  // The counts map is keyed by API resource name, but several list pages
  // name their kind by the backend adapter's short kind.
  test("a short adapter kind reads its API resource's count", () => {
    resourceCounts.value = {
      persistentvolumeclaims: 16,
      persistentvolumes: 18,
      horizontalpodautoscalers: 2,
      poddisruptionbudgets: 1,
    };
    expect(getCount("pvcs")).toBe(16);
    expect(getCount("pvs")).toBe(18);
    expect(getCount("hpas")).toBe(2);
    expect(getCount("pdbs")).toBe(1);
    expect(getCount("persistentvolumeclaims")).toBe(16);
  });

  // Secrets are never counted, and a kind the user may not list is omitted:
  // either way the count is unknown, which is not the same as zero.
  test("a kind the backend did not count is unknown, not zero", () => {
    resourceCounts.value = { configmaps: 4 };
    expect(getCount("secrets")).toBeNull();
    expect(formatCount("secrets")).toBeNull();
    expect(getCount("configmaps")).toBe(4);
  });
});
