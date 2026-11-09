/** @jsxImportSource react */
import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: MeshDashboard } = await import("./MeshDashboard.tsx");
const { default: MeshRouteDetail } = await import("./MeshRouteDetail.tsx");
const { default: MeshRoutingList } = await import("./MeshRoutingList.tsx");
const { default: MTLSPosture } = await import("./MTLSPosture.tsx");
const { selectedNamespace } = await import("@/src/lib/namespace.ts");

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  selectedNamespace.value = "all";
  globalThis.history.replaceState({}, "", "/");
});

const wait = (ms: number) =>
  act(async () => {
    await new Promise((r) => setTimeout(r, ms));
  });

const meshStatus = {
  detected: "istio",
  istio: { installed: true, namespace: "istio-system", version: "1.22.0" },
  lastChecked: "2026-01-01T00:00:00Z",
};

function route(id: string, mesh: string, ns: string, name = id) {
  return {
    id,
    mesh,
    kind: "VirtualService",
    name,
    namespace: ns,
    hosts: [`${name}.example.com`],
  };
}

test("MeshDashboard shows the detected mesh and Refresh fetches again", async () => {
  setAccessToken("t");
  const calls: string[] = [];
  globalThis.fetch = (async (input: string | URL | Request) => {
    calls.push(String(input));
    return json({ status: meshStatus });
  }) as unknown as typeof globalThis.fetch;

  const view = render(<MeshDashboard />);
  await settle();
  expect(view.container.textContent).toContain("istio-system");
  expect(view.container.textContent).toContain("1.22.0");
  expect(calls.length).toBe(1);

  fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  await settle();
  expect(calls.length).toBe(2);
});

test("MeshDashboard shows the empty state when no mesh is installed", async () => {
  setAccessToken("t");
  globalThis.fetch = (async () =>
    json({
      status: { detected: "none", lastChecked: "t" },
    })) as unknown as typeof globalThis.fetch;
  const view = render(<MeshDashboard />);
  await settle();
  expect(view.container.textContent).toContain("No service mesh detected");
});

test("MeshRouteDetail encodes the id, shows the route and a 404 as not found", async () => {
  setAccessToken("t");
  const calls: string[] = [];
  globalThis.fetch = (async (input: string | URL | Request) => {
    calls.push(String(input));
    return json({
      ...route("istio:default:VirtualService:a", "istio", "default", "a"),
      hosts: ["a.example.com"],
    });
  }) as unknown as typeof globalThis.fetch;

  const view = render(<MeshRouteDetail id="istio:default:VirtualService:a" />);
  await settle();
  expect(calls[0]).toContain(
    encodeURIComponent("istio:default:VirtualService:a"),
  );
  expect(view.container.textContent).toContain("a.example.com");

  view.unmount();
  globalThis.fetch = (async () =>
    json({ message: "nope" }, 404)) as unknown as typeof globalThis.fetch;
  const missing = render(<MeshRouteDetail id="x" />);
  await settle();
  expect(missing.container.textContent).toContain("Resource not found.");
});

test("MeshRouteDetail without raw yaml has no YAML section and Refresh refetches", async () => {
  setAccessToken("t");
  let n = 0;
  globalThis.fetch = (async () => {
    n++;
    return json(route("r1", "linkerd", "ns1", "route-one"));
  }) as unknown as typeof globalThis.fetch;
  const view = render(<MeshRouteDetail id="r1" />);
  await settle();
  expect(view.container.textContent).toContain("route-one");
  expect(view.container.textContent).not.toContain("Raw YAML");
  fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  await settle();
  expect(n).toBe(2);
});

test("MeshRoutingList search, mesh and namespace filters and the global namespace narrow rows", async () => {
  setAccessToken("t");
  globalThis.fetch = (async () =>
    json({
      status: meshStatus,
      routes: [
        route("1", "istio", "alpha", "web"),
        route("2", "linkerd", "beta", "api"),
        route("3", "istio", "beta", "db"),
      ],
    })) as unknown as typeof globalThis.fetch;

  const view = render(<MeshRoutingList />);
  await settle();
  const text = () => view.container.textContent ?? "";
  expect(text()).toContain("3 of 3 routes");

  fireEvent.change(
    body().getByPlaceholderText("Filter by name, namespace, kind..."),
    { target: { value: "api" } },
  );
  await settle();
  expect(text()).toContain("1 of 3 routes");

  fireEvent.change(
    body().getByPlaceholderText("Filter by name, namespace, kind..."),
    { target: { value: "" } },
  );
  fireEvent.change(body().getByRole("combobox"), {
    target: { value: "istio" },
  });
  await settle();
  expect(text()).toContain("2 of 3 routes");

  fireEvent.change(body().getByPlaceholderText("Namespace..."), {
    target: { value: "BET" },
  });
  await settle();
  expect(text()).toContain("1 of 3 routes");

  fireEvent.change(body().getByPlaceholderText("Namespace..."), {
    target: { value: "" },
  });
  await act(async () => {
    selectedNamespace.value = "alpha";
  });
  await settle();
  expect(text()).toContain("1 of 3 routes");
});

test("MeshRoutingList paginates 100 rows a page", async () => {
  setAccessToken("t");
  const routes = Array.from({ length: 130 }, (_, i) =>
    route(`id${i}`, "istio", "ns", `r${String(i).padStart(3, "0")}`),
  );
  globalThis.fetch = (async () =>
    json({ status: meshStatus, routes })) as unknown as typeof globalThis.fetch;
  const view = render(<MeshRoutingList />);
  await settle();
  expect(view.container.textContent).toContain("Page 1 of 2");
  expect(view.container.textContent).toContain("r099");
  expect(view.container.textContent).not.toContain("r100");
  fireEvent.click(body().getByRole("button", { name: "Next" }));
  await settle();
  expect(view.container.textContent).toContain("Page 2 of 2");
  expect(view.container.textContent).toContain("r100");
});

const workload = (ns: string, name: string, state: string) => ({
  namespace: ns,
  workload: name,
  workloadKind: "Deployment",
  mesh: "istio",
  state,
  source: "policy",
  workloadKindConfident: true,
});

test("MTLSPosture reads the namespace from the URL, debounces typing and drops a stale response", async () => {
  setAccessToken("t");
  globalThis.history.replaceState({}, "", "/mtls?namespace=first");
  const calls: string[] = [];
  const releases: Record<string, () => void> = {};
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    calls.push(url);
    const ns = new URL(url, "http://localhost").searchParams.get("namespace");
    if (ns === "slow") {
      await new Promise<void>((r) => {
        releases.slow = r;
      });
    }
    return json({
      status: meshStatus,
      workloads: [workload(ns ?? "all-ns", "wl", "active")],
    });
  }) as unknown as typeof globalThis.fetch;

  const view = render(<MTLSPosture />);
  await settle();
  expect(calls.length).toBe(1);
  expect(calls[0]).toContain("namespace=first");
  expect(view.container.textContent).toContain("first");

  const input = body().getByPlaceholderText("All namespaces");
  fireEvent.change(input, { target: { value: "slow" } });
  await wait(400);
  expect(calls.length).toBe(2);
  expect(calls[1]).toContain("namespace=slow");
  expect(globalThis.location.search).toContain("namespace=slow");

  fireEvent.change(input, { target: { value: "fast" } });
  await wait(400);
  expect(calls.length).toBe(3);
  await settle();
  expect(view.container.textContent).toContain("fast");

  await act(async () => {
    releases.slow?.();
  });
  await settle();
  expect(view.container.textContent).toContain("fast");
  expect(view.container.textContent).not.toContain("slow");

  fireEvent.click(body().getByRole("button", { name: "Clear" }));
  await wait(400);
  expect(calls[3]).not.toContain("namespace=");
});

test("MTLSPosture Refresh cancels a pending debounce and cards expand", async () => {
  setAccessToken("t");
  const calls: string[] = [];
  globalThis.fetch = (async (input: string | URL | Request) => {
    calls.push(String(input));
    return json({
      status: meshStatus,
      workloads: [
        workload("prod", "checkout", "active"),
        workload("prod", "cart", "inactive"),
      ],
    });
  }) as unknown as typeof globalThis.fetch;
  const view = render(<MTLSPosture />);
  await settle();
  expect(view.container.textContent).toContain("1 of 2 active");
  expect(view.container.textContent).not.toContain("Deployment/checkout");
  fireEvent.click(body().getByRole("button", { name: /prod/ }));
  await settle();
  expect(view.container.textContent).toContain("Deployment/checkout");

  fireEvent.change(body().getByPlaceholderText("All namespaces"), {
    target: { value: "x" },
  });
  fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  await wait(400);
  expect(calls.length).toBe(2);
});
