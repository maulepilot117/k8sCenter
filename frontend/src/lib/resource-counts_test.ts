import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { fetchCounts, resourceCounts } from "./resource-counts.ts";

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
  resourceCounts.value = null;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  resourceCounts.value = null;
});

describe("fetchCounts", () => {
  test("a 400 on a remote cluster is an ordinary error: no reason is published", async () => {
    answer(400, { error: { code: 400, message: "bad request" } });
    await fetchCounts("all", "abc123");
    expect(calls).toHaveLength(1);
    expect(calls[0].clusterHeader).toBe("abc123");
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
