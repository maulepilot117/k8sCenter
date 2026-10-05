/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import type { Capability, ReasonCode } from "@/lib/capability-types.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";

/**
 * Render-level coverage for the YAML Apply page: what the operator sees for
 * each capability state, and how the Apply button and pinned-target label
 * follow the preview. The page runs against the real hook, capability client
 * and api() with fetch routed by URL, so the assertions cover the wiring the
 * hook-level tests cannot see.
 *
 * The editor island loads Monaco from a CDN, which a test cannot do; it is
 * replaced by a plain textarea that honours the same value/onChange contract.
 */
mock.module("./YamlEditor.tsx", () => ({
  default: (props: { value: string; onChange?: (v: string) => void }) => (
    <textarea
      data-testid="yaml"
      value={props.value}
      onInput={(e) =>
        props.onChange?.((e.currentTarget as HTMLTextAreaElement).value)
      }
    />
  ),
}));

const { default: YamlApplyPage } = await import("./YamlApplyPage.tsx");

// A real origin, so the page can read ?repairOf= and rewrite its own URL.
const PAGE_URL = "http://localhost/tools/yaml-apply";
beforeAll(() => GlobalRegistrator.register({ url: PAGE_URL }));
afterAll(() => GlobalRegistrator.unregister());

interface Call {
  url: string;
  clusterHeader: string | null;
}

let calls: Call[] = [];
let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
});

function row(operation: string, reasonCode: ReasonCode): Capability {
  return {
    operation: operation as Capability["operation"],
    label: operation === "yaml.apply" ? "Apply YAML" : "Validate YAML",
    platformSupported: reasonCode !== "unsupported_platform",
    discoveryPresent: null,
    reachable: reasonCode !== "unreachable",
    authorized: reasonCode !== "forbidden",
    observedAt: "2026-09-27T00:00:00Z",
    reasonCode,
  };
}

/**
 * Routes each request by URL. Validate echoes the X-Cluster-ID it was sent to,
 * which is exactly what the backend does with the resolved target.
 */
function stubFetch(applyReason: ReasonCode = "ok") {
  calls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    const clusterHeader = new Headers(init?.headers).get("X-Cluster-ID");
    calls.push({ url, clusterHeader });
    let data: unknown = {};
    const capMatch = url.match(/\/api\/v1\/capabilities\/(.+)$/);
    const clusterMatch = url.match(/\/api\/v1\/clusters\/(.+)$/);
    if (capMatch) {
      data = {
        clusterId: decodeURIComponent(capMatch[1]),
        capabilities: [
          row("yaml.validate", "ok"),
          row("yaml.apply", applyReason),
        ],
      };
    } else if (clusterMatch) {
      const id = decodeURIComponent(clusterMatch[1]);
      data = { id, name: `name-of-${id}` };
    } else if (url.startsWith("/api/v1/yaml/validate")) {
      data = {
        documents: [{ index: 0, kind: "ConfigMap", name: "cm", valid: true }],
        valid: true,
        targetCluster: clusterHeader,
        targetGeneration: clusterHeader === LOCAL_CLUSTER_ID ? "local" : "gen",
      };
    } else if (url.startsWith("/api/v1/yaml/apply")) {
      data = {
        results: [
          { index: 0, kind: "ConfigMap", name: "cm", action: "created" },
        ],
        summary: {
          total: 1,
          created: 1,
          configured: 0,
          unchanged: 0,
          failed: 0,
        },
      };
    }
    return Promise.resolve(
      new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

async function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<YamlApplyPage />, host as HTMLElement));
  await flush();
  return host;
}

function button(root: HTMLElement, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === label,
  );
  if (!found) throw new Error(`no "${label}" button`);
  return found;
}

async function click(el: HTMLElement) {
  await act(async () => {
    el.click();
    await new Promise((r) => setTimeout(r, 0));
  });
  // A preview starts follow-up requests from an effect (GitOps ownership);
  // let those settle too.
  await flush();
  await flush();
}

async function type(root: HTMLElement, text: string) {
  const editor = root.querySelector<HTMLTextAreaElement>("[data-testid=yaml]");
  if (!editor) throw new Error("editor stub not rendered");
  await act(async () => {
    editor.value = text;
    editor.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

/** Unique per test, so no earlier test's capability cache entry can answer. */
let n = 0;
function freshCluster() {
  const id = `cluster-${++n}`;
  switchCluster(id, `gen-${n}`);
  return id;
}

test("blocked, unsupported and unknown render as three different notices", async () => {
  const seen: Array<{ tone: string | null; text: string }> = [];
  for (const reason of [
    "forbidden",
    "unsupported_platform",
    "authz_namespace_scoped",
  ] as const) {
    freshCluster();
    stubFetch(reason);
    const root = await mount();
    const notice = root.querySelector("[data-tone]");
    seen.push({
      tone: notice?.getAttribute("data-tone") ?? null,
      text: notice?.textContent ?? "",
    });
    act(() => render(null, root));
    root.remove();
    host = null;
    if (originalFetch) globalThis.fetch = originalFetch;
  }

  expect(seen.map((s) => s.tone)).toEqual([
    "blocked",
    "unsupported",
    "unknown",
  ]);
  expect(seen[0].text).toContain("Blocked right now");
  expect(seen[1].text).toContain("Not supported");
  expect(seen[2].text).toContain("Could not confirm");
  // An outage or missing RBAC is never worded as something k8sCenter cannot do.
  expect(seen[0].text).not.toContain("Not supported");
});

test("an all-ok cluster shows no capability notice", async () => {
  freshCluster();
  stubFetch("ok");
  const root = await mount();

  expect(root.querySelector("[data-tone]")).toBeNull();
  expect(calls[0].url).toMatch(/\/api\/v1\/capabilities\/cluster-\d+$/);
});

test("Apply stays disabled until a preview pins a target, and editing unpins it", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubFetch();
  const root = await mount();

  await type(root, "kind: ConfigMap");
  expect(button(root, "Apply").disabled).toBe(true);
  expect(root.textContent).toContain("Validate to choose the cluster");

  await click(button(root, "Validate"));
  expect(button(root, "Apply").disabled).toBe(false);
  expect(root.textContent).toContain("Applies to the local cluster");
  expect(root.textContent).toContain("1 resource validated: all valid");

  await type(root, "kind: ConfigMap\n# edited");
  expect(button(root, "Apply").disabled).toBe(true);
  expect(root.textContent).not.toContain("Applies to");
  expect(root.textContent).not.toContain("all valid");
});

test("after a switch the page names both clusters and Apply stays pinned", async () => {
  const a = freshCluster();
  stubFetch();
  const root = await mount();
  await type(root, "kind: ConfigMap");
  await click(button(root, "Validate"));

  const b = `cluster-${++n}`;
  await act(async () => {
    switchCluster(b, `gen-${n}`);
    await new Promise((r) => setTimeout(r, 0));
  });
  await flush();

  expect(root.textContent).toContain(`You are now viewing name-of-${b}`);
  expect(root.textContent).toContain(`Apply still targets name-of-${a}`);

  await click(button(root, "Apply"));
  const apply = calls.find((c) => c.url.startsWith("/api/v1/yaml/apply"));
  expect(apply?.clusterHeader).toBe(a);
  expect(
    new URL(apply?.url ?? "", "http://x").searchParams.get("targetCluster"),
  ).toBe(a);
});

test("Re-preview moves the pin to the cluster now on screen", async () => {
  freshCluster();
  stubFetch();
  const root = await mount();
  await type(root, "kind: ConfigMap");
  await click(button(root, "Validate"));

  const b = `cluster-${++n}`;
  await act(async () => {
    switchCluster(b, `gen-${n}`);
    await new Promise((r) => setTimeout(r, 0));
  });
  await flush();

  await click(button(root, `Re-preview on name-of-${b}`));

  expect(root.textContent).not.toContain("You are now viewing");
  expect(root.textContent).toContain(`Applies to name-of-${b}`);
  const validates = calls.filter((c) =>
    c.url.startsWith("/api/v1/yaml/validate"),
  );
  expect(validates.at(-1)?.clusterHeader).toBe(b);
});

// --- Tracked apply, ownership and repair (Release E U31) ---------------------

const OP_ID = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";

interface Reply {
  status?: number;
  body: unknown;
}

interface Sent {
  url: string;
  method: string;
  body: string | null;
}

const ok = (data: unknown): Reply => ({ body: { data } });
const refused = (
  status: number,
  reason?: string,
  extra?: Record<string, unknown>,
): Reply => ({
  status,
  body: { error: { code: status, message: "refused", reason, extra } },
});

function tracking(over: Record<string, unknown> = {}) {
  return {
    operationId: OP_ID,
    receiptUrl: `/v1/changes/${OP_ID}`,
    state: "applied",
    clusterId: "local",
    clusterGeneration: "local",
    contentDigest: "sha256:abc",
    recordedThrough: 1,
    notAttempted: 0,
    unrecorded: 0,
    replayed: false,
    containsSecret: false,
    objects: [],
    verification: {
      state: "pending",
      url: `/v1/changes/${OP_ID}/verification`,
    },
    warnings: [],
    ...over,
  };
}

function applied(trackingBlock?: Record<string, unknown>): Reply {
  return ok({
    results: [{ index: 0, kind: "ConfigMap", name: "cm", action: "created" }],
    summary: { total: 1, created: 1, configured: 0, unchanged: 0, failed: 0 },
    ...(trackingBlock ? { tracking: trackingBlock } : {}),
  });
}

function confirmedOwnership() {
  return {
    object: {
      clusterId: "local",
      kind: "ConfigMap",
      namespace: "default",
      name: "cm",
    },
    controller: "argocd",
    confidence: "confirmed",
    reason: "confirmed-argo-status",
    apps: [
      {
        appId: "argo:argocd:shop",
        tool: "argocd",
        kind: "Application",
        namespace: "argocd",
        name: "shop",
        source: { repoURL: "https://git.example.com/shop.git" },
        suspended: false,
      },
    ],
    identityBasis: "group-kind-namespace-name",
    uidConfirmed: false,
    writableGitSource: false,
    observedAt: "2026-10-01T00:00:00Z",
  };
}

let sent: Sent[] = [];

/** Like stubFetch, plus the /v1/changes probe, ownership and a chosen apply reply. */
function stubTracked(
  opts: { changes?: Reply; ownership?: Reply; apply?: Reply } = {},
) {
  sent = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    const clusterHeader = new Headers(init?.headers).get("X-Cluster-ID");
    sent.push({
      url,
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? init.body : null,
    });
    let reply: Reply = ok({});
    if (url.startsWith("/api/v1/capabilities/")) {
      reply = ok({
        clusterId: "local",
        capabilities: [row("yaml.validate", "ok"), row("yaml.apply", "ok")],
      });
    } else if (url.startsWith("/api/v1/changes/ownership")) {
      reply = opts.ownership ?? ok({ clusterId: "local", results: [] });
    } else if (url.startsWith("/api/v1/changes")) {
      reply = opts.changes ?? { body: { data: [], metadata: { total: 0 } } };
    } else if (url.startsWith("/api/v1/yaml/validate")) {
      reply = ok({
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
        targetCluster: clusterHeader,
        targetGeneration: "local",
      });
    } else if (url.startsWith("/api/v1/yaml/apply")) {
      reply = opts.apply ?? applied(tracking());
    }
    return Promise.resolve(
      new Response(JSON.stringify(reply.body), {
        status: reply.status ?? 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

const CM_YAML =
  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n  namespace: default\n";

function trackingBox(root: HTMLElement): HTMLInputElement {
  const label = [...root.querySelectorAll("label")].find((l) =>
    l.textContent?.includes("Keep a record of this change"),
  );
  const box = label?.querySelector("input");
  if (!box) throw new Error("no tracking checkbox");
  return box;
}

const applyUrl = () =>
  new URL(
    sent.find((s) => s.url.startsWith("/api/v1/yaml/apply"))?.url ?? "/",
    "http://x",
  );

async function validateAndApply(root: HTMLElement) {
  await type(root, CM_YAML);
  await click(button(root, "Validate"));
  await click(button(root, "Apply"));
}

test("tracking defaults on when change records are reachable and the apply is recorded", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked();
  const root = await mount();
  expect(trackingBox(root).checked).toBe(true);
  expect(trackingBox(root).disabled).toBe(false);

  await validateAndApply(root);

  expect(applyUrl().searchParams.get("trackedOperationId")).toMatch(
    /^[0-9a-f-]{36}$/,
  );
  const link = [...root.querySelectorAll("a")].find((a) =>
    a.textContent?.startsWith("View change receipt"),
  );
  expect(link?.getAttribute("href")).toBe(`/changes/${OP_ID}`);
  const panel = root.querySelector('[aria-label="Change record"]');
  expect(panel?.textContent).toContain("Applied");
  expect(panel?.textContent).toContain("Verification pending");
});

test("a 503 from change records disables tracking, explains it, and applies untracked", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({ changes: refused(503), apply: applied() });
  const root = await mount();
  const box = trackingBox(root);
  expect(box.checked).toBe(false);
  expect(box.disabled).toBe(true);
  const note = root.querySelector(`#${box.getAttribute("aria-describedby")}`);
  expect(note?.textContent).toContain("Change records are unavailable");

  await validateAndApply(root);
  expect(applyUrl().searchParams.has("trackedOperationId")).toBe(false);
  expect(root.querySelector('[aria-label="Change record"]')).toBeNull();
});

test("a 404 from change records says the server does not keep them", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({ changes: refused(404) });
  const root = await mount();
  expect(trackingBox(root).disabled).toBe(true);
  expect(root.textContent).toContain(
    "This server does not keep change records.",
  );
});

test("a tracked apply without a tracking block says it was not recorded", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({ apply: applied() });
  const root = await mount();
  await validateAndApply(root);
  expect(root.textContent).toContain(
    "This apply was not recorded: the server returned no change record.",
  );
});

test("a confirmed controller is warned about before apply, with no Git write", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({
    ownership: ok({ clusterId: "local", results: [confirmedOwnership()] }),
  });
  const root = await mount();
  await type(root, CM_YAML);
  await click(button(root, "Validate"));

  const ownershipCall = sent.find((s) => s.url === "/api/v1/changes/ownership");
  expect(JSON.parse(ownershipCall?.body ?? "{}")).toEqual({
    clusterId: "local",
    objects: [
      { kind: "ConfigMap", name: "cm", namespace: "default", version: "v1" },
    ],
  });
  const text = root.textContent ?? "";
  expect(text).toContain(
    "1 of these objects is managed by a GitOps controller",
  );
  expect(text).toContain(
    "Managed by Argo CD application argo:argocd:shop. Applying here changes the live object; the controller may revert it on its next sync.",
  );
  expect(text.toLowerCase()).not.toContain("pull request");
  expect(text).not.toContain("git.example.com");
});

test("a failed ownership check is never shown as unmanaged", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({ ownership: refused(504) });
  const root = await mount();
  await type(root, CM_YAML);
  await click(button(root, "Validate"));
  expect(root.textContent).toContain(
    "Checking GitOps ownership timed out, so it is unknown.",
  );
  expect(root.textContent).not.toContain("managed by a GitOps controller");
});

test("ownership from a different cluster than the preview is discarded", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({
    ownership: ok({ clusterId: "elsewhere", results: [confirmedOwnership()] }),
  });
  const root = await mount();
  await type(root, CM_YAML);
  await click(button(root, "Validate"));
  expect(root.textContent).toContain("different cluster than the preview");
  expect(root.textContent).not.toContain("argo:argocd:shop");
});

test("an unknown outcome explains itself and offers no retry button", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({
    apply: applied(
      tracking({ state: "unknown", unrecorded: 2, notAttempted: 1 }),
    ),
  });
  const root = await mount();
  await type(root, CM_YAML);
  await click(button(root, "Validate"));
  const before = root.querySelectorAll("button").length;
  await click(button(root, "Apply"));

  const panel = root.querySelector('[aria-label="Change record"]');
  expect(panel?.textContent).toContain("Outcome unknown");
  expect(panel?.textContent).toContain("no one-click retry");
  expect(panel?.textContent).toContain("2 documents have no recorded outcome");
  expect(panel?.textContent).toContain("1 document was not attempted");
  expect(panel?.querySelectorAll("button")).toHaveLength(0);
  expect(root.querySelectorAll("button").length).toBe(before);
});

test("a replayed response says nothing was applied twice", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({ apply: applied(tracking({ replayed: true })) });
  const root = await mount();
  await validateAndApply(root);
  expect(root.textContent).toContain("Nothing was applied a second time.");
});

test("an in-flight refusal links to the running change's receipt", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  stubTracked({
    apply: refused(409, "operation_in_flight", { receiptId: OP_ID }),
  });
  const root = await mount();
  await validateAndApply(root);
  expect(root.querySelector('[role="alert"]')?.textContent).toContain(
    "This apply is still running",
  );
  const link = [...root.querySelectorAll("a")].find((a) =>
    a.textContent?.includes("still running"),
  );
  expect(link?.getAttribute("href")).toBe(`/changes/${OP_ID}`);
});

test("a repair link never prefills content and travels with the tracked apply", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  history.replaceState(null, "", `/tools/yaml-apply?repairOf=${OP_ID}`);
  try {
    stubTracked();
    const root = await mount();
    expect(root.textContent).toContain("Repairing change 6f1d3c52.");
    expect(root.textContent).toContain(
      "k8sCenter does not store applied content",
    );
    // Nothing was loaded from the receipt: no receipt read, editor untouched.
    expect(sent.some((s) => s.url.startsWith(`/api/v1/changes/${OP_ID}`))).toBe(
      false,
    );
    const editor =
      root.querySelector<HTMLTextAreaElement>("[data-testid=yaml]");
    expect(editor?.value.startsWith("# Paste or type")).toBe(true);

    await validateAndApply(root);
    expect(applyUrl().searchParams.get("repairOf")).toBe(OP_ID);

    await click(button(root, "Stop repairing"));
    expect(root.textContent).not.toContain("Repairing change");
    expect(location.search).toBe("");
  } finally {
    history.replaceState(null, "", "/tools/yaml-apply");
  }
});

test("a malformed repair link is refused rather than sent", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  history.replaceState(null, "", "/tools/yaml-apply?repairOf=not-a-uuid");
  try {
    stubTracked();
    const root = await mount();
    expect(root.textContent).toContain("does not name a valid change");
    await validateAndApply(root);
    expect(applyUrl().searchParams.has("repairOf")).toBe(false);
  } finally {
    history.replaceState(null, "", "/tools/yaml-apply");
  }
});
