import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { ApiError } from "@/lib/api.ts";
import {
  countsPendingText,
  countsUnavailableReason,
  fetchCounts,
  resourceCounts,
  resourceCountsUnavailable,
} from "./resource-counts.ts";

/**
 * The counts store's refusal handling.
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
  resourceCountsUnavailable.value = null;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  resourceCounts.value = null;
  resourceCountsUnavailable.value = null;
});

describe("countsUnavailableReason", () => {
  test("a 400 on a remote cluster reports the known refusal", () => {
    const err = new ApiError(
      400,
      400,
      "resource counts are only available for the local cluster",
    );
    expect(countsUnavailableReason(err, "abc123")).toBe(
      "Resource counts are only available for the local cluster.",
    );
  });

  test("a 400 on the local cluster is still an error", () => {
    expect(
      countsUnavailableReason(new ApiError(400, 400, "bad namespace"), "local"),
    ).toBeNull();
  });

  test("a remote 500 is still an error, not the known refusal", () => {
    expect(
      countsUnavailableReason(new ApiError(500, 500, "boom"), "abc123"),
    ).toBeNull();
  });

  test("a plain Error is not the known refusal", () => {
    expect(countsUnavailableReason(new Error("offline"), "abc123")).toBeNull();
  });
});

describe("countsPendingText", () => {
  test("falls back to the caller's loading copy when nothing is unavailable", () => {
    expect(countsPendingText("Loading pods…")).toBe("Loading pods…");
  });

  test("prefers the unavailability reason over the loading copy", () => {
    resourceCountsUnavailable.value =
      "Resource counts are only available for the local cluster.";
    expect(countsPendingText("Loading pods…")).toBe(
      "Resource counts are only available for the local cluster.",
    );
  });
});

describe("fetchCounts", () => {
  const refusal = {
    error: {
      code: 400,
      message: "resource counts are only available for the local cluster",
    },
  };

  test("a 400 on a remote cluster clears counts and sets the reason", async () => {
    // Counts left over from the local cluster: wrong data for this one.
    resourceCounts.value = { pods: 7 };
    answer(400, refusal);
    await fetchCounts("all", "abc123");
    expect(calls).toHaveLength(1);
    expect(calls[0].clusterHeader).toBe("abc123");
    expect(resourceCounts.value).toBeNull();
    expect(resourceCountsUnavailable.value).toBe(
      "Resource counts are only available for the local cluster.",
    );
  });

  test("a later successful fetch clears the reason and sets counts", async () => {
    resourceCountsUnavailable.value = "stale reason";
    answer(200, { data: { pods: 3 } });
    await fetchCounts("default", "local");
    expect(calls[0].url).toContain("/v1/resources/counts?namespace=default");
    expect(calls[0].clusterHeader).toBe("local");
    expect(resourceCounts.value).toEqual({ pods: 3 });
    expect(resourceCountsUnavailable.value).toBeNull();
  });

  test("a non-400 error keeps prior counts and sets no reason", async () => {
    resourceCounts.value = { pods: 7 };
    answer(500, { error: { code: 500, message: "boom" } });
    await fetchCounts("all", "abc123");
    expect(resourceCounts.value).toEqual({ pods: 7 });
    expect(resourceCountsUnavailable.value).toBeNull();
  });

  test("a 400 on the local cluster keeps prior counts and sets no reason", async () => {
    resourceCounts.value = { pods: 7 };
    answer(400, { error: { code: 400, message: "bad namespace" } });
    await fetchCounts("all", "local");
    expect(resourceCounts.value).toEqual({ pods: 7 });
    expect(resourceCountsUnavailable.value).toBeNull();
  });
});
