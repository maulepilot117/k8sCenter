import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The per-cluster metrics binding form (#608 PR 4a). The island only runs its
 * mount effect when `IS_BROWSER` is true, which `setupDom` sets for this file
 * and puts back afterwards.
 */
setupDom({ browser: true });

const toasts: Array<{ message: string; type: string }> = [];
// bun shares modules across test files, so the real module is kept and put
// back in afterAll; ToastProvider_test.tsx uses the real one.
const realToastStore = {
  ...(await import("@/src/lib/toast-store.ts")),
};
mock.module("@/src/lib/toast-store.ts", () => ({
  ...realToastStore,
  showToast: (message: string, type = "info") => {
    toasts.push({ message, type });
  },
}));

const { default: ClusterManager } = await import("./ClusterManager.tsx");

afterAll(() => {
  mock.module("@/src/lib/toast-store.ts", () => realToastStore);
});

interface Call {
  method: string;
  path: string;
  body: Record<string, unknown> | null;
}

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
let logsReplies: Record<string, Reply> = {};
// Per-path override (path -> method -> reply promise) so a test can hold a
// response open; and an opt-in second remote cluster.
let pathReplies: Record<string, Record<string, Promise<Reply>>> = {};
let withSecondRemote = false;
// "METHOD /path-suffix" -> reply, for the cluster list's own endpoints.
let routeReplies: Record<string, Reply> = {};

beforeEach(() => {
  toasts.length = 0;
  calls = [];
  metricsReplies = {};
  logsReplies = {};
  pathReplies = {};
  withSecondRemote = false;
  routeReplies = {};
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "");
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : null;
    calls.push({ method, path, body });
    let reply: Reply = { status: 404, body: {} };
    const routed = Object.entries(routeReplies).find(
      ([k]) => k === `${method} ${path.replace(/^\/api/, "")}`,
    )?.[1];
    if (routed) {
      reply = routed;
    } else if (path.endsWith("/v1/clusters") && method === "GET") {
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
    } else if (path.endsWith("/logs")) {
      reply = logsReplies[method] ?? { status: 500, body: {} };
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
  if (originalFetch) globalThis.fetch = originalFetch;
  setAccessToken(null);
});

async function mount() {
  const { container } = render(<ClusterManager />);
  await settle();
  return container as HTMLElement;
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
    fireEvent.input(el, { target: { value } });
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

test("only remote clusters get a Logs button", async () => {
  const root = await mount();
  const buttons = Array.from(root.querySelectorAll("button")).filter(
    (b) => b.textContent?.trim() === "Logs",
  );
  expect(buttons.length).toBe(1);
});

test("the Logs button opens the Loki binding form and badges how the cluster's Loki is found", async () => {
  logsReplies.GET = {
    status: 404,
    body: {
      error: {
        code: 404,
        message: "a Loki URL is not configured for the selected cluster",
        reason: "loki_not_configured",
      },
    },
  };
  const root = await mount();
  await click(button(root, "Logs"));
  expect(root.querySelector('[data-testid="loki-form"]')).not.toBeNull();
  expect(root.textContent).toContain("logs: in-cluster Loki");
  expect(
    calls.some(
      (c) =>
        c.method === "GET" && c.path.endsWith("/v1/clusters/c-remote/logs"),
    ),
  ).toBe(true);

  // Closing and reopening against a bound cluster flips the badge.
  await click(button(root, "Logs"));
  expect(root.querySelector('[data-testid="loki-form"]')).toBeNull();
  logsReplies.GET = {
    status: 200,
    body: {
      data: {
        clusterId: "c-remote",
        url: "https://loki.example.com",
        tenantId: "",
        hasToken: false,
        tokenStale: false,
        updatedAt: "2026-11-05T00:00:00Z",
      },
    },
  };
  await click(button(root, "Logs"));
  expect(root.textContent).toContain("logs: configured Loki URL");
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

function clearTokenBox(root: HTMLElement): HTMLInputElement {
  const label = Array.from(root.querySelectorAll("label")).find((el) =>
    el.textContent?.includes("Clear the stored Prometheus token"),
  );
  const box = label?.querySelector('input[type="checkbox"]');
  if (!box) throw new Error("no clear-token checkbox");
  return box as HTMLInputElement;
}

async function check(el: HTMLInputElement) {
  await act(async () => {
    el.click();
  });
  await settle();
}

test("the clear-token box sends an empty token, which clears the stored one", async () => {
  metricsReplies.GET = { status: 200, body: { data: BINDING } };
  metricsReplies.PUT = {
    status: 200,
    body: { data: { ...BINDING, hasToken: false } },
  };
  const root = await mount();
  await click(button(root, "Metrics"));
  await check(clearTokenBox(root));
  await click(button(root, "Save"));
  expect(metricsCalls("PUT")[0].body).toEqual({
    prometheusUrl: "https://prom.example.com",
    token: "",
  });
  // The binding no longer holds a token, so the control goes away.
  expect(root.textContent).not.toContain("A token is stored");
  expect(root.textContent).not.toContain("Clear the stored Prometheus token");
});

test("a typed token wins over the clear-token box", async () => {
  metricsReplies.GET = { status: 200, body: { data: BINDING } };
  metricsReplies.PUT = { status: 200, body: { data: BINDING } };
  const root = await mount();
  await click(button(root, "Metrics"));
  await check(clearTokenBox(root));
  await type(input(root, "leave blank"), "new-token");
  await settle();
  expect(clearTokenBox(root).disabled).toBe(true);
  await click(button(root, "Save"));
  expect(metricsCalls("PUT")[0].body).toEqual({
    prometheusUrl: "https://prom.example.com",
    token: "new-token",
  });
});

test("a 409 toasts the server's message to reload and retry", async () => {
  metricsReplies.GET = { status: 200, body: { data: BINDING } };
  metricsReplies.PUT = {
    status: 409,
    body: {
      error: {
        code: 409,
        message:
          "the metrics binding was changed by another save; reload it and try again",
      },
    },
  };
  const root = await mount();
  await click(button(root, "Metrics"));
  await click(button(root, "Save"));
  expect(toasts.at(-1)).toEqual({
    message:
      "the metrics binding was changed by another save; reload it and try again",
    type: "error",
  });
});

test("remove deletes the binding and flips the badge back", async () => {
  metricsReplies.GET = { status: 200, body: { data: BINDING } };
  metricsReplies.DELETE = { status: 204 };
  globalThis.confirm = () => true;
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

test("a save that finishes after the form moved to another cluster still flips the first cluster's badge", async () => {
  withSecondRemote = true;
  let resolvePut: (r: Reply) => void = () => {};
  pathReplies["/v1/clusters/c-remote/metrics"] = {
    PUT: new Promise<Reply>((r) => {
      resolvePut = r;
    }),
  };
  metricsReplies.GET = NOT_CONFIGURED;
  const root = await mount();
  const buttons = Array.from(root.querySelectorAll("button")).filter(
    (b) => b.textContent?.trim() === "Metrics",
  );
  await click(buttons[0]); // A
  await type(input(root, "https://prometheus"), "https://prom.example.com");
  await click(button(root, "Save")); // A's PUT held open
  await click(buttons[1]); // form moves to B
  await act(async () => {
    resolvePut({ status: 200, body: { data: BINDING } });
  });
  await settle();
  // B's open form is untouched; A's badge is configured.
  expect(input(root, "https://prometheus").value).toBe("");
  const badges = Array.from(root.querySelectorAll("*")).filter(
    (el) => el.children.length === 0 && el.textContent?.includes("metrics: "),
  );
  expect(badges.map((b) => b.textContent?.trim())).toEqual([
    "metrics: configured",
    "metrics: not configured",
  ]);
});

function field(root: HTMLElement, selector: string): HTMLInputElement {
  const el = root.querySelector(selector);
  if (!el) throw new Error(`no ${selector}`);
  return el as HTMLInputElement;
}

async function typeText(
  el: HTMLInputElement | HTMLTextAreaElement,
  value: string,
) {
  await act(async () => {
    fireEvent.input(el, { target: { value } });
  });
}

test("lists the registered clusters with the count", async () => {
  const root = await mount();
  expect(root.textContent).toContain("2 clusters registered");
  expect(root.textContent).toContain("Remote");
  expect(root.textContent).toContain("https://k8s.example.com");
});

test("Register Cluster stays disabled until a CA or the insecure opt-in is given", async () => {
  const root = await mount();
  await click(button(root, "Add Cluster"));
  const register = () => button(root, "Register Cluster");
  expect(register().disabled).toBe(true);
  await typeText(input(root, "production-us-east"), "prod");
  await typeText(input(root, "https://k8s-api"), "https://api.example.com");
  await typeText(field(root, "textarea[placeholder^='eyJ']"), "tok");
  expect(register().disabled).toBe(true);
  await typeText(
    field(root, "textarea[placeholder^='-----BEGIN']"),
    "-----BEGIN CERTIFICATE-----",
  );
  expect(register().disabled).toBe(false);
});

test("registering posts the form, returns to the list and reloads it", async () => {
  routeReplies["POST /v1/clusters"] = { status: 201, body: { data: {} } };
  const root = await mount();
  await click(button(root, "Add Cluster"));
  await typeText(input(root, "production-us-east"), "prod");
  await typeText(input(root, "Production US"), "Prod");
  await typeText(input(root, "https://k8s-api"), "https://api.example.com");
  await typeText(field(root, "textarea[placeholder^='eyJ']"), "tok");
  await typeText(field(root, "textarea[placeholder^='-----BEGIN']"), "CERT");
  const listCalls = calls.filter(
    (c) => c.method === "GET" && c.path.endsWith("/v1/clusters"),
  ).length;
  await click(button(root, "Register Cluster"));
  const posts = calls.filter((c) => c.method === "POST");
  expect(posts.length).toBe(1);
  expect(posts[0].body).toEqual({
    name: "prod",
    displayName: "Prod",
    apiServerUrl: "https://api.example.com",
    token: "tok",
    caCert: "CERT",
    allowInsecureTLS: false,
  });
  expect(root.textContent).toContain("clusters registered");
  expect(
    calls.filter((c) => c.method === "GET" && c.path.endsWith("/v1/clusters"))
      .length,
  ).toBe(listCalls + 1);
});

test("a rejected registration shows the server's message and keeps the form", async () => {
  routeReplies["POST /v1/clusters"] = {
    status: 400,
    body: { error: { code: 400, message: "apiServerUrl is not allowed" } },
  };
  const root = await mount();
  await click(button(root, "Add Cluster"));
  await typeText(input(root, "production-us-east"), "prod");
  await typeText(input(root, "https://k8s-api"), "https://api.example.com");
  await typeText(field(root, "textarea[placeholder^='eyJ']"), "tok");
  await typeText(field(root, "textarea[placeholder^='-----BEGIN']"), "CERT");
  await click(button(root, "Register Cluster"));
  expect(root.textContent).toContain("apiServerUrl is not allowed");
  expect(input(root, "production-us-east").value).toBe("prod");
});

test("Test replaces the cluster's status from the response and toasts", async () => {
  routeReplies["POST /v1/clusters/c-remote/test"] = {
    status: 200,
    body: {
      data: { ...CLUSTERS[1], status: "unreachable", statusMessage: "refused" },
    },
  };
  const root = await mount();
  await click(button(root, "Test"));
  expect(toasts.at(-1)).toEqual({
    message: "Connection failed",
    type: "error",
  });
  expect(root.textContent).toContain("refused");
  expect(root.textContent).toContain("unreachable");
});

test("Remove asks first, deletes, and reloads the list", async () => {
  routeReplies["DELETE /v1/clusters/c-remote"] = { status: 204 };
  let asked = 0;
  globalThis.confirm = () => {
    asked++;
    return false;
  };
  const root = await mount();
  await click(button(root, "Remove"));
  expect(asked).toBe(1);
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  globalThis.confirm = () => true;
  await click(button(root, "Remove"));
  expect(calls.filter((c) => c.method === "DELETE").length).toBe(1);
});
