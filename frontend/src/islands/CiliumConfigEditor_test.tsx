/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";

/**
 * The Cilium config editor reads a cluster's cilium-config and writes one key
 * at a time (#608 U7). A write is pinned to the cluster and registration the
 * config was read from (KTD6): the PUT echoes the pin and addresses that
 * cluster, whatever is selected by then. Failures render from the response's
 * reason (KTD5, KTD14), so "not permitted", "reload" and "changed by someone
 * else" stay distinct.
 *
 * As in NamespaceTopology_test.tsx, IS_BROWSER is stubbed to true for this
 * island and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: CiliumConfigEditor } = await import(
  "./CiliumConfigEditor.tsx"
);

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

interface Call {
  method: string;
  cluster: string | null;
  body: Record<string, unknown> | null;
}

type Reply = { status: number; body: unknown };

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;
let calls: Call[] = [];
// Replies are consumed in order per method; the last one repeats.
let getReplies: Reply[] = [];
let putReplies: Reply[] = [];

const CONFIG = {
  cniType: "cilium",
  configSource: "configmap",
  configMapName: "cilium-config",
  configMapNamespace: "kube-system",
  editable: true,
  config: { "enable-hubble": "false", debug: "false" },
  targetCluster: "c1",
  targetGeneration: "2026-10-01T00:00:00Z",
};

function ok(data: unknown): Reply {
  return { status: 200, body: { data } };
}

function fail(status: number, reason?: string): Reply {
  return {
    status,
    body: { error: { code: status, message: "server wording", reason } },
  };
}

beforeEach(() => {
  calls = [];
  getReplies = [ok(CONFIG)];
  putReplies = [ok(CONFIG)];
  selectedCluster.value = "c1";
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    calls.push({
      method,
      cluster: headers.get("X-Cluster-ID"),
      body: init?.body ? JSON.parse(String(init.body)) : null,
    });
    const queue = method === "PUT" ? putReplies : getReplies;
    const reply = (queue.length > 1 ? queue.shift() : queue[0]) as Reply;
    return new Response(JSON.stringify(reply.body), {
      status: reply.status,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof globalThis.fetch;
  setAccessToken("test-token");
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
  selectedCluster.value = "local";
});

/** Lets fetches, body reads and state updates run (bounded). */
async function settle() {
  for (let i = 0; i < 20; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

async function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<CiliumConfigEditor />, host as HTMLElement));
  await settle();
  return host;
}

function buttons(root: HTMLElement, label: string) {
  return [...root.querySelectorAll("button")].filter(
    (b) => b.textContent?.trim() === label,
  );
}

/** Opens the edit row for enable-hubble, types a value and saves. */
async function editAndSave(root: HTMLElement, value: string) {
  const row = [...root.querySelectorAll("tr")].find((tr) =>
    tr.textContent?.includes("enable-hubble"),
  ) as HTMLElement;
  act(() => (buttons(row, "Edit")[0] as HTMLButtonElement).click());
  const input = root.querySelector("input") as HTMLInputElement;
  act(() => {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  act(() => (buttons(root, "Save")[0] as HTMLButtonElement).click());
  await settle();
}

test("a 403 read says not permitted, not 'not available', with no retry", async () => {
  getReplies = [fail(403, "forbidden")];
  const root = await mount();
  expect(root.textContent).toContain("You do not have permission");
  expect(root.textContent).not.toContain("not available");
  expect(buttons(root, "Try again")).toHaveLength(0);
  expect(buttons(root, "Reload")).toHaveLength(0);
});

test("a save carries the pin and addresses the pinned cluster", async () => {
  const root = await mount();
  // The selection moving after the read must not retarget the write.
  selectedCluster.value = "c2";
  await editAndSave(root, "true");

  const put = calls.find((c) => c.method === "PUT") as Call;
  expect(put.cluster).toBe("c1");
  expect(put.body).toEqual({
    changes: { "enable-hubble": "true" },
    confirmed: true,
    targetCluster: "c1",
    targetGeneration: "2026-10-01T00:00:00Z",
  });
});

for (const [status, reason] of [
  [409, "cluster_generation_mismatch"],
  [404, "cluster_unknown"],
] as const) {
  test(`a save refused with ${reason} asks for a reload and keeps no success state`, async () => {
    putReplies = [fail(status, reason)];
    const root = await mount();
    await editAndSave(root, "true");

    expect(root.textContent).toContain("registration changed");
    expect(buttons(root, "Reload")).toHaveLength(1);
    expect(root.textContent).not.toContain("Saved");
    // The row leaves edit mode: saving again against the stale pin cannot work.
    expect(root.querySelector("input")).toBeNull();
  });
}

test("a save conflict refetches and keeps the pending value", async () => {
  putReplies = [fail(409, "conflict")];
  getReplies = [
    ok(CONFIG),
    ok({ ...CONFIG, config: { ...CONFIG.config, debug: "true" } }),
  ];
  const root = await mount();
  await editAndSave(root, "true");

  expect(calls.filter((c) => c.method === "GET")).toHaveLength(2);
  expect(root.textContent).toContain("changed on the cluster");
  const input = root.querySelector("input") as HTMLInputElement;
  expect(input.value).toBe("true");
  // The other rows show the refetched values.
  const debugRow = [...root.querySelectorAll("tr")].find((tr) =>
    tr.textContent?.includes("debug"),
  ) as HTMLElement;
  expect(debugRow.textContent).toContain("true");
});

test("an unknown save outcome asks for a reload to check", async () => {
  putReplies = [fail(502, "outcome_unknown")];
  const root = await mount();
  await editAndSave(root, "true");

  expect(root.textContent).toContain("Reload to check");
  expect(buttons(root, "Reload")).toHaveLength(1);
});

test("a refused save says not permitted to change, with no retry", async () => {
  putReplies = [fail(403, "forbidden")];
  const root = await mount();
  await editAndSave(root, "true");

  expect(root.textContent).toContain("permission to change");
  expect(buttons(root, "Try again")).toHaveLength(0);
});

test("a save whose refresh fails is shown as saved, out of edit mode", async () => {
  getReplies = [ok(CONFIG), fail(502, "unreachable")];
  const root = await mount();
  await editAndSave(root, "true");

  expect(root.textContent).toContain("Saved");
  expect(root.querySelector("input")).toBeNull();
  // The saved value is shown even though the refresh failed.
  const row = [...root.querySelectorAll("tr")].find((tr) =>
    tr.textContent?.includes("enable-hubble"),
  ) as HTMLElement;
  expect(row.textContent).toContain("true");
  expect(buttons(root, "Try again")).toHaveLength(1);
});
