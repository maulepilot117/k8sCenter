/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * The per-cluster metrics binding form (#608 PR 4a). The island only runs its
 * mount effect when `IS_BROWSER` is true, so this file stubs it for its own
 * island and puts the real value back afterwards (as DiagnosticWorkspace_test).
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const toasts: Array<{ message: string; type: string }> = [];
mock.module("@/src/islands/ToastProvider.tsx", () => ({
  showToast: (message: string, type = "info") => {
    toasts.push({ message, type });
  },
}));

const { default: ClusterManager } = await import("./ClusterManager.tsx");

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

interface Call {
  method: string;
  path: string;
  body: Record<string, unknown> | null;
}

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;
let calls: Call[] = [];

const CLUSTERS = [
  {
    id: "local",
    name: "local",
    displayName: "Local",
    apiServerUrl: "",
    status: "connected",
    statusMessage: "",
    k8sVersion: "v1.30.0",
    nodeCount: 1,
    isLocal: true,
  },
  {
    id: "c-remote",
    name: "remote",
    displayName: "Remote",
    apiServerUrl: "https://k8s.example.com",
    status: "connected",
    statusMessage: "",
    k8sVersion: "v1.30.0",
    nodeCount: 3,
    isLocal: false,
  },
];

const NOT_CONFIGURED = {
  status: 404,
  body: {
    error: {
      code: 404,
      message: "metrics are not configured for the selected cluster",
      reason: "metrics_not_configured",
    },
  },
};

const BINDING = {
  clusterId: "c-remote",
  prometheusUrl: "https://prom.example.com",
  hasToken: true,
  alertmanagerUrl: "",
  updatedAt: "2026-10-10T00:00:00Z",
};

type Reply = { status: number; body?: unknown };
let metricsReplies: Record<string, Reply> = {};
// Per-path override (path -> method -> reply promise) so a test can hold a
// response open; and an opt-in second remote cluster.
let pathReplies: Record<string, Record<string, Promise<Reply>>> = {};
let withSecondRemote = false;

beforeEach(() => {
  toasts.length = 0;
  calls = [];
  metricsReplies = {};
  pathReplies = {};
  withSecondRemote = false;
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "");
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : null;
    calls.push({ method, path, body });
    let reply: Reply = { status: 404, body: {} };
    if (path.endsWith("/v1/clusters")) {
      const list = withSecondRemote
        ? [...CLUSTERS, { ...CLUSTERS[1], id: "c-other", name: "other" }]
        : CLUSTERS;
      reply = { status: 200, body: { data: list } };
    } else if (path.endsWith("/metrics")) {
      const held = Object.entries(pathReplies).find(([k]) =>
        path.endsWith(k),
      )?.[1][method];
      reply = held
        ? await held
        : (metricsReplies[method] ?? { status: 500, body: {} });
    }
    if (reply.status === 204) return new Response(null, { status: 204 });
    return new Response(JSON.stringify(reply.body ?? {}), {
      status: reply.status,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
  setAccessToken("test-token");
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  setAccessToken(null);
});

async function settle() {
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

async function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<ClusterManager />, host as HTMLElement));
  await settle();
  return host;
}

function button(root: HTMLElement, label: string): HTMLButtonElement {
  const b = Array.from(root.querySelectorAll("button")).find(
    (el) => el.textContent?.trim() === label,
  );
  if (!b) throw new Error(`no button ${label}`);
  return b as HTMLButtonElement;
}

async function click(el: HTMLElement) {
  await act(async () => {
    el.click();
  });
  await settle();
}

function input(root: HTMLElement, placeholder: string): HTMLInputElement {
  const el = root.querySelector(`input[placeholder^="${placeholder}"]`);
  if (!el) throw new Error(`no input ${placeholder}`);
  return el as HTMLInputElement;
}

async function type(el: HTMLInputElement, value: string) {
  await act(async () => {
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function metricsCalls(method: string) {
  return calls.filter(
    (c) => c.method === method && c.path.endsWith("/metrics"),
  );
}

test("only remote clusters get a Metrics button", async () => {
  const root = await mount();
  const buttons = Array.from(root.querySelectorAll("button")).filter(
    (b) => b.textContent?.trim() === "Metrics",
  );
  expect(buttons.length).toBe(1);
});

test("opening the form on an unbound cluster shows the not-configured badge", async () => {
  metricsReplies.GET = NOT_CONFIGURED;
  const root = await mount();
  await click(button(root, "Metrics"));
  expect(root.textContent).toContain("metrics: not configured");
  expect(input(root, "https://prometheus").value).toBe("");
  expect(metricsCalls("GET")[0].path).toContain(
    "/v1/clusters/c-remote/metrics",
  );
});

test("opening the form on a bound cluster fills the URL and says a token is stored", async () => {
  metricsReplies.GET = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await click(button(root, "Metrics"));
  expect(root.textContent).toContain("metrics: configured");
  expect(input(root, "https://prometheus").value).toBe(
    "https://prom.example.com",
  );
  expect(root.textContent).toContain("A token is stored");
});

test("save sends the token when typed, and flips the badge to configured", async () => {
  metricsReplies.GET = NOT_CONFIGURED;
  metricsReplies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await click(button(root, "Metrics"));
  await type(input(root, "https://prometheus"), "https://prom.example.com");
  await type(input(root, "leave blank"), "s3cret");
  await click(button(root, "Save"));
  expect(metricsCalls("PUT")[0].body).toEqual({
    prometheusUrl: "https://prom.example.com",
    token: "s3cret",
  });
  expect(root.textContent).toContain("metrics: configured");
  expect(toasts.at(-1)?.type).toBe("success");
});

test("a blank token is omitted from the body so the stored one is kept", async () => {
  metricsReplies.GET = { status: 200, body: { data: BINDING } };
  metricsReplies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await click(button(root, "Metrics"));
  await type(input(root, "https://alertmanager"), "https://am.example.com");
  await click(button(root, "Save"));
  const body = metricsCalls("PUT")[0].body as Record<string, unknown>;
  expect("token" in body).toBe(false);
  expect(body).toEqual({
    prometheusUrl: "https://prom.example.com",
    alertmanagerUrl: "https://am.example.com",
  });
});

test("remove deletes the binding and flips the badge back", async () => {
  metricsReplies.GET = { status: 200, body: { data: BINDING } };
  metricsReplies.DELETE = { status: 204 };
  const root = await mount();
  await click(button(root, "Metrics"));
  await click(button(root, "Remove binding"));
  expect(metricsCalls("DELETE").length).toBe(1);
  expect(root.textContent).toContain("metrics: not configured");
});

test("a 400 toasts the message the server sent", async () => {
  metricsReplies.GET = NOT_CONFIGURED;
  metricsReplies.PUT = {
    status: 400,
    body: { error: { code: 400, message: "prometheusUrl must use https" } },
  };
  const root = await mount();
  await click(button(root, "Metrics"));
  await type(input(root, "https://prometheus"), "http://x");
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({
    message: "prometheusUrl must use https",
    type: "error",
  });
});

test("a 502 toasts a fixed sentence, not the server text", async () => {
  metricsReplies.GET = NOT_CONFIGURED;
  metricsReplies.PUT = {
    status: 502,
    body: {
      error: {
        code: 502,
        message: "could not reach Prometheus at the given URL",
        reason: "unreachable",
      },
    },
  };
  const root = await mount();
  await click(button(root, "Metrics"));
  await type(input(root, "https://prometheus"), "https://prom.example.com");
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({
    message: "Could not reach Prometheus at that URL",
    type: "error",
  });
});

test("a late GET for cluster A cannot fill the form now shown for cluster B", async () => {
  withSecondRemote = true;
  let resolveA: (r: Reply) => void = () => {};
  pathReplies["/v1/clusters/c-remote/metrics"] = {
    GET: new Promise<Reply>((r) => {
      resolveA = r;
    }),
  };
  metricsReplies.GET = NOT_CONFIGURED; // cluster B has no binding
  metricsReplies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  const buttons = Array.from(root.querySelectorAll("button")).filter(
    (b) => b.textContent?.trim() === "Metrics",
  );
  expect(buttons.length).toBe(2);
  await click(buttons[0]); // A, GET held open
  await click(buttons[1]); // B
  await act(async () => {
    resolveA({ status: 200, body: { data: BINDING } }); // A's late binding
  });
  await settle();
  expect(input(root, "https://prometheus").value).toBe("");
  expect(root.textContent).not.toContain("A token is stored");
  await type(input(root, "https://prometheus"), "https://prom-b.example.com");
  await click(button(root, "Save"));
  const puts = metricsCalls("PUT");
  expect(puts.length).toBe(1);
  expect(puts[0].path).toContain("/v1/clusters/c-other/metrics");
  expect(puts[0].body).toEqual({ prometheusUrl: "https://prom-b.example.com" });
});
