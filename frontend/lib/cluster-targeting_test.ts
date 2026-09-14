import { assert, assertEquals, assertStrictEquals } from "jsr:@std/assert@1";
import { effect } from "@preact/signals";
import {
  api,
  apiGet,
  apiPost,
  apiPostRaw,
  onForbidden,
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
  UNKNOWN_GENERATION,
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
  body: string | null;
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
      body: typeof init?.body === "string" ? init.body : null,
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

  // The subscriber must track ALL THREE signals, not just the epoch. Reading
  // the other two with .peek() creates no subscription, so the effect would
  // fire exactly once whether or not the writes were batched -- the test would
  // pass with batch() deleted. Tracking all three makes the notification COUNT
  // the thing under test: batched writes wake the subscriber once, unbatched
  // writes wake it once per signal.
  const seen: Array<{ epoch: number; id: string; generation: string }> = [];
  const dispose = effect(() => {
    seen.push({
      epoch: clusterEpoch.value,
      id: selectedCluster.value,
      generation: selectedClusterGeneration.value,
    });
  });

  const before = clusterEpoch.value;
  const notificationsBefore = seen.length;
  switchCluster("cluster-b", "gen-b");
  dispose();

  assertEquals(clusterEpoch.value, before + 1);
  assertEquals(
    seen.length - notificationsBefore,
    1,
    "a switch must wake a subscriber exactly once; more than one means the three writes were not batched",
  );
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

Deno.test("a legacy id with no generation is restored under the unknown sentinel", () => {
  // What a build from before generations were written leaves behind. Dropping
  // the id silently moved the operator to a different cluster on upgrade --
  // the retargeting this module exists to prevent. The sentinel keeps the
  // selection while still forcing a cache miss, because it can never equal a
  // real generation ("local", or an RFC3339 timestamp).
  const store = new Map<string, string>([[
    "k8scenter.selectedCluster",
    "cluster-a",
  ]]);
  const storage = { getItem: (k: string) => store.get(k) ?? null };

  assertEquals(readPersistedTarget(storage), {
    clusterId: "cluster-a",
    generation: UNKNOWN_GENERATION,
  });
});

Deno.test("the persisted pair is one value, so it cannot be half-written", () => {
  // Two setItem calls could be interrupted between them -- storage full, the
  // accessor throwing -- leaving an id beside a stale generation. One key
  // means there is no intermediate state to observe.
  const store = new Map<string, string>();
  const storage = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => {
      store.set(k, v);
    },
  };

  persistTarget(storage, "cluster-a", "gen-a");
  assertEquals(store.size, 1);
  assertEquals(readPersistedTarget(storage), {
    clusterId: "cluster-a",
    generation: "gen-a",
  });
});

Deno.test("a hand-edited unparseable target falls back to local", () => {
  const store = new Map<string, string>([["k8scenter.clusterTarget", "{oops"]]);
  const storage = { getItem: (k: string) => store.get(k) ?? null };

  assertEquals(readPersistedTarget(storage), {
    clusterId: LOCAL_CLUSTER_ID,
    generation: LOCAL_GENERATION,
  });
});

Deno.test("switchCluster does not pair a remote id with the local generation", () => {
  // Defaulting the id and the generation independently produced a pair that
  // describes no real registration, which then persisted and passed the
  // reload guard.
  resetTarget("cluster-a", "gen-a");
  switchCluster("cluster-remote", "");

  assertEquals(selectedCluster.value, "cluster-remote");
  assertEquals(selectedClusterGeneration.value, UNKNOWN_GENERATION);

  // The local cluster still normalizes to the local generation, whatever it
  // was handed.
  switchCluster(LOCAL_CLUSTER_ID, "not-a-real-generation");
  assertEquals(selectedClusterGeneration.value, LOCAL_GENERATION);
});

Deno.test("apiGet accepts a bare signal, a targeting object, and neither", async () => {
  resetTarget("cluster-a", "gen-a");
  const controller = new AbortController();
  const { calls, restore } = stubFetch([ok, ok, ok]);

  try {
    await apiGet("/v1/resources/pods");
    await apiGet("/v1/resources/nodes", controller.signal);
    await apiGet("/v1/resources/services", { clusterId: "cluster-pinned" });

    assertEquals(calls[0].clusterHeader, "cluster-a");
    assertEquals(calls[1].clusterHeader, "cluster-a");
    assertStrictEquals(calls[1].signal, controller.signal);
    assertEquals(calls[2].clusterHeader, "cluster-pinned");
  } finally {
    restore();
  }
});

Deno.test("apiPost forwards a targeting object and still serializes its body", async () => {
  resetTarget("cluster-a", "gen-a");
  const { calls, restore } = stubFetch([ok, ok]);

  try {
    await apiPost("/v1/resources/pods", { a: 1 });
    await apiPost("/v1/resources/nodes", { b: 2 }, {
      clusterId: "cluster-pinned",
    });

    assertEquals(calls[0].clusterHeader, "cluster-a");
    assertEquals(calls[0].body, JSON.stringify({ a: 1 }));
    assertEquals(calls[1].clusterHeader, "cluster-pinned");
    assertEquals(calls[1].body, JSON.stringify({ b: 2 }));
  } finally {
    restore();
  }
});

Deno.test("a 403 on an auth endpoint does not re-enter the forbidden hook", async () => {
  // The hook re-issues GET /v1/auth/me, which is inside the backend's
  // cluster-gated group and 403s for a non-admin on a remote cluster. Without
  // the guard that is one unthrottled request per round-trip, forever.
  resetTarget("cluster-remote", "gen-remote");
  let hookCalls = 0;
  onForbidden(() => {
    hookCalls += 1;
  });
  const forbidden = () => new Response("{}", { status: 403 });
  const { restore } = stubFetch([forbidden, forbidden]);

  try {
    await api("/v1/auth/me?namespace=default").catch(() => {});
    assertEquals(hookCalls, 0);

    // A 403 anywhere else still refreshes permissions.
    await api("/v1/resources/pods").catch(() => {});
    assertEquals(hookCalls, 1);
  } finally {
    restore();
    onForbidden(() => {});
  }
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
