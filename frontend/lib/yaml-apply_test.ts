import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { signal } from "@preact/signals";
import { h, render } from "preact";
import { act } from "preact/test-utils";
import { ApiError, setAccessToken } from "./api.ts";
import type {
  ApplyTracking,
  TrackedApplyRefusalReason,
} from "./change-types.ts";
import { switchCluster, UNKNOWN_GENERATION } from "./cluster.ts";
import {
  type ApplyResponse,
  buildApplyQuery,
  isIndeterminateApplyFailure,
  PENDING_ATTEMPT_TTL_MS,
  trackedApplyRefusal,
  type UseYamlApplyOptions,
  type UseYamlApplyReturn,
  useYamlApply,
  type ValidateResponse,
} from "./yaml-apply.ts";

/**
 * Drives the real hook, mounted in a happy-dom document, through the real
 * api() client with only fetch replaced. Every response is a deferred the test
 * settles by hand, so "a cluster switch lands while a preview is in flight"
 * happens in a deterministic order rather than by timing.
 *
 * The DOM is registered for this file only and removed afterwards, so the rest
 * of the suite keeps running without browser globals.
 */

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

interface Call {
  url: string;
  clusterHeader: string | null;
  signal: AbortSignal | null | undefined;
  respond: (status: number, payload: unknown) => void;
  /** Fails the request the way a dropped connection does. */
  fail: () => void;
}

let calls: Call[] = [];
let originalFetch: typeof globalThis.fetch | undefined;

function stubFetch() {
  calls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) =>
    new Promise<Response>((resolve, reject) => {
      calls.push({
        fail: () => reject(new TypeError("Failed to fetch")),
        url: String(input),
        clusterHeader: new Headers(init?.headers).get("X-Cluster-ID"),
        signal: init?.signal,
        respond: (status, payload) =>
          resolve(
            new Response(JSON.stringify(payload), {
              status,
              headers: { "Content-Type": "application/json" },
            }),
          ),
      });
    })) as typeof globalThis.fetch;
}

let container: HTMLElement | null = null;

afterEach(() => {
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  if (container) {
    act(() => render(null, container as HTMLElement));
    container.remove();
    container = null;
  }
  setAccessToken(null);
  // Unknown-outcome ids persist in sessionStorage; never let one leak into the
  // next test.
  globalThis.sessionStorage.clear();
});

/** Unmounts the mounted hook, as a navigation or remount would. */
function unmount() {
  if (!container) return;
  act(() => render(null, container as HTMLElement));
  container.remove();
  container = null;
}

/** Mounts the hook and returns what it returned on its first render. */
function mount(
  initialYaml = "kind: ConfigMap",
  options?: UseYamlApplyOptions,
): UseYamlApplyReturn {
  let captured: UseYamlApplyReturn | undefined;
  function Harness() {
    captured =
      options === undefined
        ? useYamlApply(initialYaml)
        : useYamlApply(initialYaml, options);
    return null;
  }
  container = document.createElement("div");
  document.body.appendChild(container);
  act(() => render(h(Harness, null), container as HTMLElement));
  if (!captured) throw new Error("hook did not render");
  return captured;
}

function preview(targetCluster: string, targetGeneration: string) {
  return {
    data: {
      documents: [
        {
          index: 0,
          kind: "ConfigMap",
          name: "cm",
          namespace: "default",
          valid: true,
        },
      ],
      valid: true,
      targetCluster,
      targetGeneration,
    } satisfies ValidateResponse,
  };
}

function invalidPreview(targetCluster: string, targetGeneration: string) {
  return {
    data: {
      documents: [
        {
          index: 0,
          kind: "ConfigMap",
          name: "cm",
          namespace: "default",
          valid: false,
          errors: [{ field: "data.foo", message: "must be a string" }],
        },
      ],
      valid: false,
      targetCluster,
      targetGeneration,
    } satisfies ValidateResponse,
  };
}

const applied: { data: ApplyResponse } = {
  data: {
    results: [{ index: 0, kind: "ConfigMap", name: "cm", action: "created" }],
    summary: { total: 1, created: 1, configured: 0, unchanged: 0, failed: 0 },
  },
};

/** Lets the api() client finish parsing a settled response. */
const flush = () => new Promise((r) => setTimeout(r, 0));

/** Runs a handler to completion, answering its single request. */
async function run(
  handler: () => Promise<void>,
  status: number,
  payload: unknown,
): Promise<Call> {
  const done = handler();
  await flush();
  const call = calls[calls.length - 1];
  call.respond(status, payload);
  await done;
  return call;
}

test("apply sends the pinned cluster after a switch", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  switchCluster("cluster-b", "gen-b");
  const call = await run(hook.handleApply, 200, applied);

  const url = new URL(call.url, "http://x");
  expect(url.pathname).toBe("/api/v1/yaml/apply");
  expect(url.searchParams.get("targetCluster")).toBe("cluster-a");
  expect(url.searchParams.get("targetGeneration")).toBe("gen-a");
  expect(call.clusterHeader).toBe("cluster-a");
  expect(hook.result.value?.summary.created).toBe(1);
});

test("apply is disabled with no pin", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await hook.handleApply();

  expect(calls.length).toBe(0);
  expect(hook.error.value).toContain("Validate");
  expect(hook.applying.value).toBe(false);
});

test("pinStale is set by an epoch change", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  const pinned = hook.pin.value;
  expect(pinned?.target.clusterId).toBe("cluster-a");
  expect(hook.pinStale.value).toBe(false);

  switchCluster("cluster-b", "gen-b");

  expect(hook.pinStale.value).toBe(true);
  expect(hook.pin.value).toBe(pinned);
});

test("a preview in flight while the restored generation resolves still pins", async () => {
  // Page-load race (U11b P3): the selection was restored under the unknown
  // sentinel and the switcher resolves it while a Validate is in flight.
  switchCluster("cluster-a", UNKNOWN_GENERATION);
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  const done = hook.handleValidate();
  await flush();
  const inFlight = calls[0];

  act(() => switchCluster("cluster-a", "gen-a"));
  expect(inFlight.signal?.aborted).toBe(false);

  inFlight.respond(200, preview("cluster-a", "gen-a"));
  await done;

  expect(hook.pin.value?.targetGeneration).toBe("gen-a");
  expect(hook.pinStale.value).toBe(false);
});

test("a pin taken before the restored generation resolves is not stale", async () => {
  switchCluster("cluster-a", UNKNOWN_GENERATION);
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  act(() => switchCluster("cluster-a", "gen-a"));

  // The server already told us which registration it validated against; the
  // client learning the same generation later does not make that pin stale.
  expect(hook.pinStale.value).toBe(false);
});

test("a pin on a known generation is stale once that generation changes", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  act(() => switchCluster("cluster-a", "gen-a-second-registration"));

  expect(hook.pinStale.value).toBe(true);
});

test("clearPin forces a fresh preview", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  hook.clearPin();
  expect(hook.pin.value).toBeNull();
  expect(hook.preview.value).toBeNull();

  await hook.handleApply();
  expect(calls.length).toBe(1); // the validate only

  switchCluster("cluster-b", "gen-b");
  await run(hook.handleValidate, 200, preview("cluster-b", "gen-b"));
  const call = await run(hook.handleApply, 200, applied);
  expect(call.clusterHeader).toBe("cluster-b");
  expect(new URL(call.url, "http://x").searchParams.get("targetCluster")).toBe(
    "cluster-b",
  );
});

test("late validate response from a prior epoch is discarded", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  const done = hook.handleValidate();
  await flush();
  const inFlight = calls[0];
  expect(inFlight.clusterHeader).toBe("cluster-a");

  act(() => switchCluster("cluster-b", "gen-b"));
  // The switch cancels the preview it made irrelevant...
  expect(inFlight.signal?.aborted).toBe(true);

  // ...and even a response that arrives anyway is not rendered or pinned.
  inFlight.respond(200, preview("cluster-a", "gen-a"));
  await done;

  expect(hook.pin.value).toBeNull();
  expect(hook.preview.value).toBeNull();
  expect(hook.error.value).toBeNull();
  expect(hook.validating.value).toBe(false);
});

test("a preview answered by a different cluster does not pin", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-z", "gen-z"));

  expect(hook.pin.value).toBeNull();
  expect(hook.error.value).toContain("cluster-z");
});

test("validate results land in preview, not result", async () => {
  // The validate endpoint answers { documents, valid }, not the apply shape.
  // Writing it into `result` handed renderers a body with no `summary`.
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount();

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));

  expect(hook.result.value).toBeNull();
  expect(hook.preview.value?.documents[0].name).toBe("cm");
});

test("a failing verdict lands in preview, not error, pinned or not", async () => {
  // Both editors render `preview`; a dry-run rejection is a verdict to show,
  // not a request failure, so `error` stays for requests that did not answer.
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const unpinned = mount();

  await run(unpinned.handleValidate, 200, invalidPreview("cluster-a", "gen-a"));

  expect(unpinned.error.value).toBeNull();
  expect(unpinned.result.value).toBeNull();
  expect(unpinned.preview.value?.valid).toBe(false);
});

test("pinned validate failure leaves error null and carries the errors in preview", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, invalidPreview("cluster-a", "gen-a"));

  expect(hook.error.value).toBeNull();
  expect(hook.preview.value?.valid).toBe(false);
  expect(hook.preview.value?.documents[0].errors?.[0].message).toBe(
    "must be a string",
  );
});

test("a 409 cluster_pin_mismatch surfaces its reason", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  await run(hook.handleApply, 409, {
    error: {
      code: 409,
      message:
        "the cluster you previewed is not the cluster this request targets",
      reason: "cluster_pin_mismatch",
      extra: { pinnedClusterId: "cluster-a", requestClusterId: "cluster-b" },
    },
  });

  expect(hook.error.value).toContain("cluster-a");
  expect(hook.error.value).toContain("cluster-b");
  // The refused pin cannot succeed on retry; a fresh preview is required.
  expect(hook.pin.value).toBeNull();
  expect(hook.result.value).toBeNull();
});

test("a 409 cluster_generation_mismatch requires a fresh preview", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { pinApplyToPreview: true });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  await run(hook.handleApply, 409, {
    error: {
      code: 409,
      message:
        "the cluster you previewed has been re-registered since the preview",
      reason: "cluster_generation_mismatch",
      extra: { pinnedGeneration: "gen-a", targetGeneration: "gen-a2" },
    },
  });

  expect(hook.error.value).toContain("re-registered");
  expect(hook.pin.value).toBeNull();
});

test("hook remains backward compatible", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const successes: ApplyResponse[] = [];
  const forceConflicts = signal(true);
  const hook = mount("kind: ConfigMap", {
    forceConflicts,
    onApplySuccess: (res) => successes.push(res),
  });

  for (const key of [
    "yamlContent",
    "applying",
    "validating",
    "error",
    "result",
    "handleValidate",
    "handleApply",
  ] as const) {
    expect(hook[key]).toBeDefined();
  }
  expect(hook.yamlContent.value).toBe("kind: ConfigMap");

  // No preview needed and no pin sent: the pre-existing unpinned apply.
  switchCluster("cluster-b", "gen-b");
  const call = await run(hook.handleApply, 200, applied);

  const url = new URL(call.url, "http://x");
  expect(url.pathname).toBe("/api/v1/yaml/apply");
  expect(url.searchParams.get("force")).toBe("true");
  expect(url.searchParams.has("targetCluster")).toBe(false);
  expect(call.clusterHeader).toBe("cluster-b");
  expect(hook.result.value).toEqual(applied.data);
  expect(successes).toEqual([applied.data]);
});

test("hook with no options at all applies unpinned", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount();

  const call = await run(hook.handleApply, 200, applied);

  expect(call.url).toBe("/api/v1/yaml/apply");
  expect(call.clusterHeader).toBe("cluster-a");
});

// --- Tracked apply (Release E U30b) ----------------------------------------

const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

const OP = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";
const OTHER_OP = "0b6a8d21-77c4-4d5e-8a30-5e1c9f2b4d07";

function trackingFor(operationId: string): ApplyTracking {
  return {
    operationId,
    receiptUrl: `/v1/changes/${operationId}`,
    state: "applied",
    clusterId: "local",
    clusterGeneration: "local",
    contentDigest: "sha256:abc123",
    recordedThrough: 1,
    notAttempted: 0,
    unrecorded: 0,
    replayed: false,
    containsSecret: false,
    objects: [
      {
        index: 0,
        group: "",
        version: "v1",
        resource: "configmaps",
        uid: "u-1",
      },
    ],
    verification: {
      state: "pending",
      url: `/v1/changes/${operationId}/verification`,
    },
    warnings: [],
  };
}

function appliedTracked(operationId: string): { data: ApplyResponse } {
  return { data: { ...applied.data, tracking: trackingFor(operationId) } };
}

function refusal(status: number, reason: string, extra?: unknown) {
  return {
    error: { code: status, message: `refused: ${reason}`, reason, extra },
  };
}

/** Runs a handler whose single request dies without an answer. */
async function runDropped(handler: () => Promise<void>): Promise<Call> {
  const done = handler();
  await flush();
  const call = calls[calls.length - 1];
  call.fail();
  await done;
  return call;
}

function operationIdOf(call: Call): string | null {
  return new URL(call.url, "http://x").searchParams.get("trackedOperationId");
}

test("buildApplyQuery: nothing set is the empty string", () => {
  expect(buildApplyQuery()).toBe("");
  expect(buildApplyQuery({})).toBe("");
  expect(
    buildApplyQuery({
      force: false,
      pin: null,
      trackedOperationId: null,
      repairOf: null,
    }),
  ).toBe("");
});

test("buildApplyQuery: force only is unchanged legacy behaviour", () => {
  expect(buildApplyQuery({ force: true })).toBe("?force=true");
});

test("buildApplyQuery: pin only carries both pin parameters", () => {
  const pin = { targetCluster: "cluster-a", targetGeneration: "gen-a" };
  expect(buildApplyQuery({ pin })).toBe(
    "?targetCluster=cluster-a&targetGeneration=gen-a",
  );
  expect(buildApplyQuery({ force: true, pin })).toBe(
    "?force=true&targetCluster=cluster-a&targetGeneration=gen-a",
  );
});

test("buildApplyQuery: tracking only is the opt-in", () => {
  expect(buildApplyQuery({ trackedOperationId: OP })).toBe(
    `?trackedOperationId=${OP}`,
  );
});

test("buildApplyQuery: every parameter appears in a fixed order", () => {
  const pin = { targetCluster: "cluster-a", targetGeneration: "gen-a" };
  const expected = `?force=true&targetCluster=cluster-a&targetGeneration=gen-a&trackedOperationId=${OP}&repairOf=${OTHER_OP}`;
  // Property order of the input must not leak into the URL.
  expect(
    buildApplyQuery({
      force: true,
      pin,
      trackedOperationId: OP,
      repairOf: OTHER_OP,
    }),
  ).toBe(expected);
  expect(
    buildApplyQuery({
      repairOf: OTHER_OP,
      trackedOperationId: OP,
      pin,
      force: true,
    }),
  ).toBe(expected);
});

test("buildApplyQuery: tracked without pin keeps force before the id", () => {
  expect(buildApplyQuery({ force: true, trackedOperationId: OP })).toBe(
    `?force=true&trackedOperationId=${OP}`,
  );
});

test("buildApplyQuery: repairOf is only sent with a tracked id", () => {
  expect(buildApplyQuery({ repairOf: OTHER_OP })).toBe("");
  expect(buildApplyQuery({ trackedOperationId: OP, repairOf: OTHER_OP })).toBe(
    `?trackedOperationId=${OP}&repairOf=${OTHER_OP}`,
  );
});

test("buildApplyQuery: values are percent-encoded", () => {
  expect(
    buildApplyQuery({
      pin: { targetCluster: "a&b", targetGeneration: "2026-01-02T03:04:05Z" },
    }),
  ).toBe("?targetCluster=a%26b&targetGeneration=2026-01-02T03%3A04%3A05Z");
});

test("ApplyResponse parses with and without tracking", () => {
  const legacy: ApplyResponse = applied.data;
  expect(legacy.tracking).toBeUndefined();

  const tracked: ApplyResponse = appliedTracked(OP).data;
  expect(tracked.tracking?.operationId).toBe(OP);
  expect(tracked.tracking?.verification.state).toBe("pending");
  expect(tracked.results).toEqual(legacy.results);
});

test("ApplyResponse tolerates keys it does not know", () => {
  const wire = {
    ...appliedTracked(OP).data,
    futureField: { anything: true },
  };
  const parsed: ApplyResponse = wire;
  expect(parsed.summary.created).toBe(1);
  expect(parsed.tracking?.state).toBe("applied");
});

test("tracked absent leaves the request identical to today", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", {
    forceConflicts: signal(true),
  });

  const call = await run(hook.handleApply, 200, applied);

  expect(call.url).toBe("/api/v1/yaml/apply?force=true");
  expect(operationIdOf(call)).toBeNull();
  expect(hook.lastOperationId.value).toBeNull();
  expect(hook.result.value?.tracking).toBeUndefined();
});

test("tracked false is the same as absent", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(false) });

  const call = await run(hook.handleApply, 200, applied);

  expect(call.url).toBe("/api/v1/yaml/apply");
  expect(hook.lastOperationId.value).toBeNull();
});

test("tracked apply sends a UUIDv4 id and surfaces the tracking block", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const done = hook.handleApply();
  await flush();
  const id = operationIdOf(calls[0]);
  expect(id).toMatch(UUID_V4);
  calls[0].respond(200, appliedTracked(id as string));
  await done;

  expect(hook.lastOperationId.value).toBe(id);
  expect(hook.result.value?.tracking?.operationId ?? null).toBe(id);
  expect(hook.error.value).toBeNull();
});

test("a retry after a dropped connection reuses the operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await runDropped(hook.handleApply);
  const id = operationIdOf(first);
  expect(id).toMatch(UUID_V4);
  expect(hook.error.value).not.toBeNull();
  expect(hook.lastOperationId.value).toBe(id);

  const retry = await run(hook.handleApply, 200, appliedTracked(id as string));
  expect(operationIdOf(retry)).toBe(id);
  expect(retry.url).toBe(first.url);
});

test("a retry after a 5xx reuses the operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await run(hook.handleApply, 502, { error: { code: 502 } });
  const retry = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(retry)).toBe(operationIdOf(first));
});

test("a new apply after a success mints a new operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await run(hook.handleApply, 200, applied);
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(first)).toMatch(UUID_V4);
  expect(operationIdOf(second)).toMatch(UUID_V4);
  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
});

test("a new apply after a definite refusal mints a new operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await run(hook.handleApply, 422, {
    error: { code: 422, message: "invalid" },
  });
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
});

test("edited content after an unknown outcome mints a new operation id", async () => {
  // The server refuses an id reused with different content; the client never
  // sends that combination.
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await runDropped(hook.handleApply);
  hook.yamlContent.value = "kind: Secret";
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
});

test("changed options after an unknown outcome mint a new operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const forceConflicts = signal(false);
  const hook = mount("kind: ConfigMap", {
    tracked: signal(true),
    forceConflicts,
  });

  const first = await runDropped(hook.handleApply);
  forceConflicts.value = true;
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
  expect(second.url).toContain("force=true");
});

test("switching tracking off discards the pending id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const tracked = signal(true);
  const hook = mount("kind: ConfigMap", { tracked });

  const first = await runDropped(hook.handleApply);
  tracked.value = false;
  const untracked = await run(hook.handleApply, 200, applied);
  tracked.value = true;
  const third = await run(hook.handleApply, 200, applied);

  expect(untracked.url).toBe("/api/v1/yaml/apply");
  expect(operationIdOf(third)).not.toBe(operationIdOf(first));
});

test("a tracked apply composes with the target pin and repairOf", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", {
    pinApplyToPreview: true,
    tracked: signal(true),
    forceConflicts: signal(true),
    repairOf: signal<string | null>(OTHER_OP),
  });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  switchCluster("cluster-b", "gen-b");
  const call = await run(hook.handleApply, 200, applied);

  const url = new URL(call.url, "http://x");
  expect([...url.searchParams.keys()]).toEqual([
    "force",
    "targetCluster",
    "targetGeneration",
    "trackedOperationId",
    "repairOf",
  ]);
  expect(url.searchParams.get("targetCluster")).toBe("cluster-a");
  expect(url.searchParams.get("targetGeneration")).toBe("gen-a");
  expect(url.searchParams.get("repairOf")).toBe(OTHER_OP);
  // X-Cluster-ID still comes from the pin, never the live selection.
  expect(call.clusterHeader).toBe("cluster-a");
});

test("repairOf without tracking is not sent", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", {
    repairOf: signal<string | null>(OTHER_OP),
  });

  const call = await run(hook.handleApply, 200, applied);

  expect(call.url).toBe("/api/v1/yaml/apply");
});

test("tracked refusals map each 409 reason and the 503 to a typed error", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const cases: Array<{
    status: number;
    reason: TrackedApplyRefusalReason;
    extra?: Record<string, unknown>;
    receiptId?: string;
    text: RegExp;
  }> = [
    { status: 409, reason: "operation_id_conflict", text: /already in use/ },
    {
      status: 409,
      reason: "operation_in_flight",
      extra: { receiptId: OP },
      receiptId: OP,
      text: /still running/,
    },
    {
      status: 409,
      reason: "operation_id_reused",
      text: /different content or a different cluster/,
    },
    {
      status: 503,
      reason: "receipt_store_unavailable",
      text: /nothing was applied/,
    },
  ];

  for (const c of cases) {
    await run(hook.handleApply, c.status, refusal(c.status, c.reason, c.extra));
    expect(hook.trackedRefusal.value?.reason).toBe(c.reason);
    expect(hook.trackedRefusal.value?.receiptId).toBe(c.receiptId);
    expect(hook.error.value).toMatch(c.text);
    expect(hook.error.value).toBe(hook.trackedRefusal.value?.message ?? null);
    expect(hook.result.value).toBeNull();
  }
});

test("a refusal is cleared by the next attempt", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  await run(hook.handleApply, 409, refusal(409, "operation_id_reused"));
  expect(hook.trackedRefusal.value).not.toBeNull();

  await run(hook.handleApply, 200, applied);
  expect(hook.trackedRefusal.value).toBeNull();
  expect(hook.error.value).toBeNull();
});

test("operation_in_flight keeps the id; the other refusals release it", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const inflight = await run(
    hook.handleApply,
    409,
    refusal(409, "operation_in_flight", { receiptId: OP }),
  );
  const again = await run(
    hook.handleApply,
    409,
    refusal(409, "operation_id_reused"),
  );
  expect(operationIdOf(again)).toBe(operationIdOf(inflight));

  const next = await run(hook.handleApply, 200, applied);
  expect(operationIdOf(next)).not.toBe(operationIdOf(inflight));
});

test("a pin refusal is still reported as a pin refusal on a tracked apply", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", {
    pinApplyToPreview: true,
    tracked: signal(true),
  });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  await run(hook.handleApply, 409, refusal(409, "cluster_generation_mismatch"));

  expect(hook.error.value).toContain("re-registered");
  expect(hook.trackedRefusal.value).toBeNull();
  expect(hook.pin.value).toBeNull();
});

test("a tracked apply still mints a v4 id where crypto.randomUUID is undefined", async () => {
  // An HTTP-only deployment is not a secure context: randomUUID is missing.
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });
  const c = globalThis.crypto as unknown as { randomUUID?: unknown };
  c.randomUUID = undefined;
  try {
    const call = await run(hook.handleApply, 200, applied);
    expect(operationIdOf(call)).toMatch(UUID_V4);
    expect(hook.error.value).toBeNull();
  } finally {
    delete c.randomUUID;
  }
});

test("a changed repairOf after an unknown outcome mints a new operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const repairOf = signal<string | null>(OTHER_OP);
  const hook = mount("kind: ConfigMap", { tracked: signal(true), repairOf });

  const first = await runDropped(hook.handleApply);
  repairOf.value = OP;
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
  expect(new URL(second.url, "http://x").searchParams.get("repairOf")).toBe(OP);
});

test("an unchanged repairOf after an unknown outcome reuses the operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", {
    tracked: signal(true),
    repairOf: signal<string | null>(OTHER_OP),
  });

  const first = await runDropped(hook.handleApply);
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).toBe(operationIdOf(first));
});

test("an unpinned retry on another cluster mints a new operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await runDropped(hook.handleApply);
  expect(first.clusterHeader).toBe("cluster-a");
  switchCluster("cluster-b", "gen-b");
  const second = await run(hook.handleApply, 200, applied);

  expect(second.clusterHeader).toBe("cluster-b");
  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
});

test("an unpinned retry on the same cluster reuses the operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await runDropped(hook.handleApply);
  const second = await run(hook.handleApply, 200, applied);

  expect(second.clusterHeader).toBe("cluster-a");
  expect(operationIdOf(second)).toBe(operationIdOf(first));
});

test("a fresh preview after an unknown outcome mints a new operation id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", {
    pinApplyToPreview: true,
    tracked: signal(true),
  });

  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a"));
  const first = await runDropped(hook.handleApply);
  await run(hook.handleValidate, 200, preview("cluster-a", "gen-a-second"));
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
});

test("a 503 receipt_store_unavailable on a fresh id releases it", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await run(
    hook.handleApply,
    503,
    refusal(503, "receipt_store_unavailable"),
  );
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).not.toBe(operationIdOf(first));
});

test("a 503 receipt_store_unavailable on a reused id keeps it", async () => {
  // The 503 may be the read step failing over an earlier attempt that did
  // apply; minting a new id then could apply twice.
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await runDropped(hook.handleApply);
  const second = await run(
    hook.handleApply,
    503,
    refusal(503, "receipt_store_unavailable"),
  );
  const third = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).toBe(operationIdOf(first));
  expect(operationIdOf(third)).toBe(operationIdOf(first));
});

test("other 5xx on a fresh id keep it", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const hook = mount("kind: ConfigMap", { tracked: signal(true) });

  const first = await run(hook.handleApply, 503, { error: { code: 503 } });
  const second = await run(hook.handleApply, 200, applied);

  expect(operationIdOf(second)).toBe(operationIdOf(first));
});

test("a remount after a dropped response continues the same attempt", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const first = mount("kind: ConfigMap", { tracked: signal(true) });
  const dropped = await runDropped(first.handleApply);
  unmount();

  // A new hook instance, as after an Astro navigation or reload.
  const second = mount("kind: ConfigMap", { tracked: signal(true) });
  const retry = await run(second.handleApply, 200, applied);

  expect(operationIdOf(retry)).toBe(operationIdOf(dropped));
});

test("a remount does not reuse the id for different content", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const first = mount("kind: ConfigMap", { tracked: signal(true) });
  const dropped = await runDropped(first.handleApply);
  unmount();

  const second = mount("kind: Secret", { tracked: signal(true) });
  const next = await run(second.handleApply, 200, applied);

  expect(operationIdOf(next)).not.toBe(operationIdOf(dropped));
});

test("a success clears the persisted id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const first = mount("kind: ConfigMap", { tracked: signal(true) });
  const dropped = await runDropped(first.handleApply);
  await run(first.handleApply, 200, applied);
  unmount();

  const second = mount("kind: ConfigMap", { tracked: signal(true) });
  const next = await run(second.handleApply, 200, applied);

  expect(operationIdOf(next)).not.toBe(operationIdOf(dropped));
});

test("a definite refusal clears the persisted id", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const first = mount("kind: ConfigMap", { tracked: signal(true) });
  const dropped = await runDropped(first.handleApply);
  await run(first.handleApply, 422, { error: { code: 422, message: "bad" } });
  unmount();

  const second = mount("kind: ConfigMap", { tracked: signal(true) });
  const next = await run(second.handleApply, 200, applied);

  expect(operationIdOf(next)).not.toBe(operationIdOf(dropped));
});

test("an unpinned untracked apply supersedes a persisted tracked attempt", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const tracked = signal(true);
  const first = mount("kind: ConfigMap", { tracked });
  const dropped = await runDropped(first.handleApply);
  tracked.value = false;
  await run(first.handleApply, 200, applied);
  unmount();

  const second = mount("kind: ConfigMap", { tracked: signal(true) });
  const next = await run(second.handleApply, 200, applied);

  expect(operationIdOf(next)).not.toBe(operationIdOf(dropped));
});

test("a persisted id expires after the reuse window", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const realNow = Date.now;
  try {
    const first = mount("kind: ConfigMap", { tracked: signal(true) });
    const dropped = await runDropped(first.handleApply);
    unmount();

    Date.now = () => realNow() + PENDING_ATTEMPT_TTL_MS + 1;
    const second = mount("kind: ConfigMap", { tracked: signal(true) });
    const next = await run(second.handleApply, 200, applied);

    expect(operationIdOf(next)).not.toBe(operationIdOf(dropped));
  } finally {
    Date.now = realNow;
  }
});

test("a corrupt persisted entry is ignored", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  const first = mount("kind: ConfigMap", { tracked: signal(true) });
  const dropped = await runDropped(first.handleApply);
  unmount();
  for (let i = 0; i < globalThis.sessionStorage.length; i++) {
    const key = globalThis.sessionStorage.key(i);
    if (key) globalThis.sessionStorage.setItem(key, "{not json");
  }

  const second = mount("kind: ConfigMap", { tracked: signal(true) });
  const next = await run(second.handleApply, 200, applied);

  expect(operationIdOf(next)).toMatch(UUID_V4);
  expect(operationIdOf(next)).not.toBe(operationIdOf(dropped));
});

test("unavailable storage falls back to the in-memory attempt", async () => {
  switchCluster("cluster-a", "gen-a");
  stubFetch();
  // Blocked site data: merely reading `sessionStorage` throws.
  const real = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");
  Object.defineProperty(globalThis, "sessionStorage", {
    configurable: true,
    get() {
      throw new DOMException("storage blocked", "SecurityError");
    },
  });
  try {
    // Guard the stub itself: it must really make storage throw.
    expect(() => globalThis.sessionStorage).toThrow("storage blocked");
    const hook = mount("kind: ConfigMap", { tracked: signal(true) });
    const first = await runDropped(hook.handleApply);
    const retry = await run(hook.handleApply, 200, applied);
    expect(operationIdOf(first)).toMatch(UUID_V4);
    expect(operationIdOf(retry)).toBe(operationIdOf(first));
    expect(hook.error.value).toBeNull();
  } finally {
    if (real) Object.defineProperty(globalThis, "sessionStorage", real);
  }
});

test("isIndeterminateApplyFailure keeps the id for 5xx and in-flight only", () => {
  const api = (status: number, reason?: string) =>
    new ApiError(status, status, "x", { error: { reason } });

  for (const status of [500, 502, 503, 504]) {
    expect(isIndeterminateApplyFailure(api(status))).toBe(true);
  }
  expect(isIndeterminateApplyFailure(api(409, "operation_in_flight"))).toBe(
    true,
  );
  for (const reason of [
    "operation_id_reused",
    "operation_id_conflict",
    "cluster_pin_mismatch",
    undefined,
  ]) {
    expect(isIndeterminateApplyFailure(api(409, reason))).toBe(false);
  }
  for (const status of [400, 401, 403, 404, 422, 429]) {
    expect(isIndeterminateApplyFailure(api(status))).toBe(false);
  }
});

test("the refusal and retry helpers ignore errors that are not tracked refusals", () => {
  expect(trackedApplyRefusal(new Error("boom"))).toBeNull();
  expect(trackedApplyRefusal(undefined)).toBeNull();
  expect(isIndeterminateApplyFailure(new TypeError("Failed to fetch"))).toBe(
    true,
  );
});
