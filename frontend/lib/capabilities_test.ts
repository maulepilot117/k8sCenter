import { afterEach, expect, setSystemTime, test } from "bun:test";
import { setAccessToken } from "./api.ts";
import {
  CAPABILITY_TTL_MS,
  capabilityFor,
  explain,
  fetchCapabilities,
} from "./capabilities.ts";
import type {
  CapabilitiesResponse,
  Capability,
  ReasonCode,
} from "./capability-types.ts";
import { type ClusterTarget, currentTarget, switchCluster } from "./cluster.ts";

// Drives fetchCapabilities through the real api() client with only fetch
// stubbed, so the header/path agreement is asserted on the request that would
// actually leave the browser. The cache is reset between tests the way the
// product resets it: a cluster switch (epoch bump) drops every entry.

interface Recorded {
  url: string;
  clusterHeader: string | null;
}

function stubFetch(
  answer: (url: string) => CapabilitiesResponse = (url) =>
    body(url.slice(url.lastIndexOf("/") + 1)),
): { calls: Recorded[]; restore: () => void } {
  const calls: Recorded[] = [];
  const original = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    calls.push({
      url,
      clusterHeader: new Headers(init?.headers).get("X-Cluster-ID"),
    });
    return Promise.resolve(
      new Response(JSON.stringify({ data: answer(url) }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
  return {
    calls,
    restore: () => {
      globalThis.fetch = original;
    },
  };
}

function row(reasonCode: ReasonCode, overrides: Partial<Capability> = {}) {
  return {
    operation: "yaml.apply",
    label: "Apply YAML",
    platformSupported: reasonCode !== "unsupported_platform",
    discoveryPresent: null,
    reachable: reasonCode !== "unreachable",
    authorized: reasonCode !== "forbidden",
    observedAt: "2026-09-27T00:00:00Z",
    reasonCode,
    ...overrides,
  } satisfies Capability;
}

function body(clusterId: string): CapabilitiesResponse {
  return { clusterId, capabilities: [row("ok")] };
}

/** Switches to a fresh target so no earlier test's cache entry can answer. */
let n = 0;
function freshTarget(id = `cluster-${++n}`, generation = "gen-1") {
  switchCluster(id, generation);
  return currentTarget();
}

afterEach(() => {
  setSystemTime();
  setAccessToken(null);
});

test("sends explicit clusterId so header and path agree", async () => {
  const target = freshTarget("cluster-path");
  // The ambient selection has moved on by the time the read is issued: the
  // header must still follow the target, not the live signal, or U8 409s.
  switchCluster("cluster-elsewhere", "gen-x");
  const { calls, restore } = stubFetch();
  try {
    await fetchCapabilities(target);
    expect(calls.length).toBe(1);
    expect(calls[0].url).toBe("/api/v1/capabilities/cluster-path");
    expect(calls[0].clusterHeader).toBe("cluster-path");
  } finally {
    restore();
  }
});

test("caches by cluster and generation", async () => {
  const target = freshTarget("cluster-cache", "gen-1");
  const { calls, restore } = stubFetch();
  try {
    await fetchCapabilities(target);
    await fetchCapabilities(target);
    expect(calls.length).toBe(1);

    // Same id, different registration: must not be served the old answers.
    const reRegistered: ClusterTarget = { ...target, generation: "gen-2" };
    await fetchCapabilities(reRegistered);
    expect(calls.length).toBe(2);
  } finally {
    restore();
  }
});

test("expiry forces a refetch", async () => {
  const target = freshTarget();
  const { calls, restore } = stubFetch();
  try {
    const start = Date.now();
    setSystemTime(new Date(start));
    await fetchCapabilities(target);

    setSystemTime(new Date(start + CAPABILITY_TTL_MS - 1));
    await fetchCapabilities(target);
    expect(calls.length).toBe(1);

    setSystemTime(new Date(start + CAPABILITY_TTL_MS + 1));
    await fetchCapabilities(target);
    expect(calls.length).toBe(2);
  } finally {
    restore();
  }
});

test("epoch change drops the cache", async () => {
  const a = freshTarget("cluster-epoch-a");
  const { calls, restore } = stubFetch();
  try {
    await fetchCapabilities(a);
    // A -> B -> A: the id and generation are back where they were, but the
    // operator moved in between, so the answer must be asked for again.
    switchCluster("cluster-epoch-b", "gen-1");
    switchCluster("cluster-epoch-a", "gen-1");
    await fetchCapabilities(currentTarget());
    expect(calls.length).toBe(2);
  } finally {
    restore();
  }
});

test("a response for a different cluster is rejected, not cached", async () => {
  const target = freshTarget("cluster-asked");
  const { calls, restore } = stubFetch(() => body("cluster-other"));
  try {
    await expect(fetchCapabilities(target)).rejects.toThrow();
    await expect(fetchCapabilities(target)).rejects.toThrow();
    expect(calls.length).toBe(2);
  } finally {
    restore();
  }
});

test("capabilityFor finds the row for an operation", () => {
  const caps: CapabilitiesResponse = {
    clusterId: "local",
    capabilities: [
      row("ok", { operation: "yaml.validate" }),
      row("forbidden", { operation: "yaml.apply" }),
    ],
  };
  expect(capabilityFor(caps, "yaml.apply")?.reasonCode).toBe("forbidden");
  expect(capabilityFor(caps, "pod.exec")).toBeUndefined();
});

test("unreachable renders as blocked, not unsupported", () => {
  expect(explain(row("unreachable")).tone).toBe("blocked");
});

test("forbidden renders as blocked, not unsupported", () => {
  expect(explain(row("forbidden")).tone).toBe("blocked");
});

test("unsupported_platform renders as unsupported", () => {
  expect(explain(row("unsupported_platform")).tone).toBe("unsupported");
});

test("namespace-scoped denial is unknown, never blocked or ok", () => {
  // authorized is null here: indeterminate, and must not read as a denial.
  const cap = row("authz_namespace_scoped", { authorized: null });
  const { tone, message } = explain(cap);
  expect(tone).toBe("unknown");
  expect(message).toContain("namespaces");
});

test("only unsupported_platform is ever unsupported", () => {
  // The whole point of D3: a recoverable condition must never be worded as
  // something k8sCenter cannot do.
  const codes: ReasonCode[] = [
    "ok",
    "unsupported_platform",
    "discovery_missing",
    "discovery_unavailable",
    "unreachable",
    "stale_observation",
    "forbidden",
    "authz_unknown",
    "authz_namespace_scoped",
    "cluster_unknown",
    "credentials_invalid",
    "db_unavailable",
  ];
  for (const code of codes) {
    const { tone, message } = explain(row(code));
    expect(message.length).toBeGreaterThan(0);
    expect(tone === "unsupported").toBe(code === "unsupported_platform");
    expect(tone === "ok").toBe(code === "ok");
  }
});

test("unknown reason code does not throw", () => {
  const cap = row("ok", {
    reasonCode: "added_by_a_newer_backend" as ReasonCode,
  });
  const result = explain(cap);
  expect(result.tone).toBe("unknown");
  expect(result.message.length).toBeGreaterThan(0);
});
