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

beforeAll(() => GlobalRegistrator.register());
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
