import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { signal } from "@preact/signals";
import { h, render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "./api.ts";
import { switchCluster } from "./cluster.ts";
import {
  type ApplyResponse,
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
}

let calls: Call[] = [];
let originalFetch: typeof globalThis.fetch | undefined;

function stubFetch() {
  calls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) =>
    new Promise<Response>((resolve) => {
      calls.push({
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
});

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
