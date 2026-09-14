import { assert, assertEquals, assertStrictEquals } from "jsr:@std/assert@1";
import { effect } from "@preact/signals";
import {
  api,
  apiPostRaw,
  type RequestTargeting,
  setAccessToken,
} from "./api.ts";
import {
  clusterEpoch,
  type ClusterTarget,
  currentTarget,
  LOCAL_CLUSTER_ID,
  LOCAL_GENERATION,
  persistTarget,
  readPersistedTarget,
  selectedCluster,
  selectedClusterGeneration,
  switchCluster,
} from "./cluster.ts";

// Covers both lib/cluster.ts and lib/api.ts, because the two halves are one
// contract: cluster.ts decides what the current target is, api.ts decides
// which target a given request is addressed to, and the interesting behaviour
// (a request surviving a switch that lands mid-flight) only exists where they
// meet.
//
// Both modules carry a client-only banner. It is about SSR request handling —
// lib/api.ts's module-level access token would be shared across requests — and
// does not apply here: `deno test` serves no requests, and lib/cluster.ts gates
// its localStorage side effect behind IS_BROWSER, which is false off-DOM.

/** A recorded outbound request: everything the assertions care about. */
interface Recorded {
  url: string;
  clusterHeader: string | null;
  signal: AbortSignal | null | undefined;
}

/**
 * Installs a fetch stub that records every call and answers from `responses`
 * in order, falling back to 200 {} once exhausted. Returns the recording and a
 * restore function.
 *
 * `beforeRespond` runs after the request is recorded and before its response
 * resolves, which is the only seam where "a cluster switch lands while a
 * request is in flight" can be simulated deterministically.
 */
function stubFetch(
  responses: Array<() => Response>,
  beforeRespond?: (call: Recorded, index: number) => void,
): { calls: Recorded[]; restore: () => void } {
  const calls: Recorded[] = [];
  const original = globalThis.fetch;
  let i = 0;

  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    const call: Recorded = {
      url: String(input),
      clusterHeader: headers.get("X-Cluster-ID"),
      signal: init?.signal,
    };
    calls.push(call);
    const index = i++;
    beforeRespond?.(call, index);
    if (init?.signal?.aborted) {
      return Promise.reject(new DOMException("Aborted", "AbortError"));
    }
    const make = responses[index];
    return Promise.resolve(
      make ? make() : new Response("{}", { status: 200 }),
    );
  }) as typeof globalThis.fetch;

  return {
    calls,
    restore: () => {
      globalThis.fetch = original;
    },
  };
}

const ok = () =>
  new Response(JSON.stringify({ data: {} }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
const unauthorized = () => new Response("{}", { status: 401 });
const refreshed = () =>
  new Response(JSON.stringify({ data: { accessToken: "new-token" } }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

/** Returns the signals to a known starting point between tests. */
function resetTarget(id: string, generation: string) {
  switchCluster(id, generation);
  setAccessToken(null);
}

Deno.test("switchCluster bumps epoch after writing id and generation", () => {
  resetTarget("cluster-a", "gen-a");

  // An epoch subscriber is the consumer this ordering exists for: it wakes on
  // the counter and immediately reads the other two signals. If the epoch were
  // visible before they settled, it would read a torn pair.
  const seen: Array<{ epoch: number; id: string; generation: string }> = [];
  const dispose = effect(() => {
    seen.push({
      epoch: clusterEpoch.value,
      id: selectedCluster.peek(),
      generation: selectedClusterGeneration.peek(),
    });
  });

  const before = clusterEpoch.value;
  switchCluster("cluster-b", "gen-b");
  dispose();

  assertEquals(clusterEpoch.value, before + 1);
  const last = seen[seen.length - 1];
  assertEquals(last.epoch, before + 1);
  assertEquals(last.id, "cluster-b");
  assertEquals(last.generation, "gen-b");
});

Deno.test("switchCluster to the already-active target does not bump the epoch", () => {
  resetTarget("cluster-a", "gen-a");
  const before = clusterEpoch.value;

  switchCluster("cluster-a", "gen-a");

  // A spurious tick would invalidate live caches and mark valid pins stale.
  assertEquals(clusterEpoch.value, before);
});

Deno.test("switchCluster treats a new generation of the same id as a switch", () => {
  resetTarget("cluster-a", "gen-a");
  const before = clusterEpoch.value;

  // Delete-and-re-register under a recycled id: same cluster id, different
  // registration. Anything cached against the old generation must be dropped.
  switchCluster("cluster-a", "gen-a-second-registration");

  assertEquals(clusterEpoch.value, before + 1);
  assertEquals(selectedClusterGeneration.value, "gen-a-second-registration");
});

Deno.test("api pins X-Cluster-ID at call entry", async () => {
  resetTarget("cluster-a", "gen-a");
  const { calls, restore } = stubFetch([ok]);

  try {
    await api("/v1/resources/pods");
    assertEquals(calls.length, 1);
    assertEquals(calls[0].clusterHeader, "cluster-a");
  } finally {
    restore();
  }
});

Deno.test("api retry after 401 reuses the original cluster", async () => {
  resetTarget("cluster-a", "gen-a");

  // The concrete defect this unit fixes. The header used to be read from the
  // signal inside doFetch, which runs a second time on the refresh path — so a
  // switch landing during the token refresh silently retargeted the retry, and
  // a request the operator issued against cluster-a completed against
  // cluster-b.
  const { calls, restore } = stubFetch(
    [unauthorized, refreshed, ok],
    (call) => {
      if (call.url.includes("/auth/refresh")) {
        switchCluster("cluster-b", "gen-b");
      }
    },
  );

  try {
    await api("/v1/resources/pods");

    assertEquals(calls.length, 3);
    assertEquals(calls[0].url, "/api/v1/resources/pods");
    assertEquals(calls[1].url, "/api/v1/auth/refresh");
    assertEquals(calls[2].url, "/api/v1/resources/pods");

    // The switch really did land between the two attempts...
    assertEquals(selectedCluster.value, "cluster-b");
    // ...and both attempts still went to the cluster the call was issued for.
    assertEquals(calls[0].clusterHeader, "cluster-a");
    assertEquals(calls[2].clusterHeader, "cluster-a");
  } finally {
    restore();
  }
});

Deno.test("api honours an explicit clusterId over the signal", async () => {
  resetTarget("cluster-a", "gen-a");
  const { calls, restore } = stubFetch([ok]);

  try {
    await api("/v1/capabilities/cluster-pinned", {
      clusterId: "cluster-pinned",
    });
    assertEquals(calls[0].clusterHeader, "cluster-pinned");
  } finally {
    restore();
  }
});

Deno.test("api does not leak clusterId into the fetch init", async () => {
  resetTarget("cluster-a", "gen-a");
  let sawClusterId = false;
  const original = globalThis.fetch;
  globalThis.fetch = ((_input: string | URL | Request, init?: RequestInit) => {
    sawClusterId = init !== undefined && "clusterId" in init;
    return Promise.resolve(ok());
  }) as typeof globalThis.fetch;

  try {
    await api("/v1/resources/pods", { clusterId: "cluster-b" });
    // clusterId is ours, not RequestInit's. Passing it through would be inert
    // today but is exactly the kind of thing a future Request constructor
    // rejects.
    assert(!sawClusterId);
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("apiPostRaw forwards signal and clusterId", async () => {
  resetTarget("cluster-a", "gen-a");
  const controller = new AbortController();
  const { calls, restore } = stubFetch([ok], (_call, index) => {
    if (index === 0) controller.abort();
  });

  try {
    let aborted = false;
    try {
      await apiPostRaw("/v1/yaml/apply", "kind: ConfigMap\n", "text/yaml", {
        signal: controller.signal,
        clusterId: "cluster-pinned",
      });
    } catch (e) {
      aborted = e instanceof DOMException && e.name === "AbortError";
    }

    assert(aborted, "expected the aborted request to reject with AbortError");
    assertEquals(calls[0].clusterHeader, "cluster-pinned");
    assertStrictEquals(calls[0].signal, controller.signal);
  } finally {
    restore();
  }
});

Deno.test("apiPostRaw still accepts a bare AbortSignal", async () => {
  resetTarget("cluster-a", "gen-a");
  const controller = new AbortController();
  const { calls, restore } = stubFetch([ok]);

  try {
    await apiPostRaw(
      "/v1/yaml/validate",
      "kind: ConfigMap\n",
      "text/yaml",
      controller.signal,
    );
    // The convenience wrappers accept either shape so pre-existing positional
    // call sites keep working; the bare signal must not be mistaken for a
    // targeting object with no signal.
    assertStrictEquals(calls[0].signal, controller.signal);
    assertEquals(calls[0].clusterHeader, "cluster-a");
  } finally {
    restore();
  }
});

Deno.test("currentTarget is a snapshot, not a live view", () => {
  resetTarget("cluster-a", "gen-a");
  const pin: ClusterTarget = currentTarget();

  switchCluster("cluster-b", "gen-b");

  assertEquals(pin.clusterId, "cluster-a");
  assertEquals(pin.generation, "gen-a");
  assert(pin.epoch < clusterEpoch.value);
  assertEquals(selectedCluster.value, "cluster-b");
});

Deno.test("selectedCluster survives a reload via localStorage", () => {
  // The module's own signals are seeded from storage at import time, which has
  // already happened, so the round-trip is exercised through the two helpers
  // that seeding uses. A fake Storage keeps this honest off-DOM.
  const store = new Map<string, string>();
  const storage = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => {
      store.set(k, v);
    },
  };

  persistTarget(storage, "cluster-a", "gen-a");
  assertEquals(readPersistedTarget(storage), {
    clusterId: "cluster-a",
    generation: "gen-a",
  });
});

Deno.test("a persisted cluster with no persisted generation falls back to local", () => {
  // What a build from before generations were written leaves behind. Pairing
  // the old id with the "local" generation would produce a cache key that
  // describes no real registration.
  const store = new Map<string, string>([[
    "k8scenter.selectedCluster",
    "cluster-a",
  ]]);
  const storage = { getItem: (k: string) => store.get(k) ?? null };

  assertEquals(readPersistedTarget(storage), {
    clusterId: LOCAL_CLUSTER_ID,
    generation: LOCAL_GENERATION,
  });
});

Deno.test("unreadable storage yields the local cluster instead of throwing", () => {
  const hostile = {
    getItem: () => {
      throw new Error("The operation is insecure.");
    },
  };

  assertEquals(readPersistedTarget(hostile), {
    clusterId: LOCAL_CLUSTER_ID,
    generation: LOCAL_GENERATION,
  });
  assertEquals(readPersistedTarget(null), {
    clusterId: LOCAL_CLUSTER_ID,
    generation: LOCAL_GENERATION,
  });

  // A write that throws must not escape either: failing to remember the
  // selection cannot be allowed to break making one.
  const writeHostile: Pick<Storage, "setItem"> = {
    setItem: () => {
      throw new Error("QuotaExceededError");
    },
  };
  persistTarget(writeHostile, "cluster-a", "gen-a");
});

Deno.test("RequestTargeting is assignable from a bare object literal", () => {
  // Type-level guard: the option bag U11b passes must stay structural, not a
  // branded/class type, or every call site needs an import just to build one.
  const t: RequestTargeting = { clusterId: "cluster-a" };
  assertEquals(t.clusterId, "cluster-a");
});
