import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: MetricsRail } = await import("./MetricsRail.tsx");
const { default: RelatedPods } = await import("./RelatedPods.tsx");

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(status === 200 ? { data } : { error: {} }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

test("MetricsRail shows each KPI latest value from the metrics response", async () => {
  setAccessToken("t");
  const requested: string[] = [];
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    requested.push(url);
    if (url.includes("/monitoring/status")) {
      return json({ prometheus: { available: true } });
    }
    const slug = url.split("/monitoring/queries/")[1]?.split("?")[0];
    const last =
      slug === "pods/cpu" ? "0.25" : slug === "pods/restarts" ? "3" : "2048";
    return json({
      result: [
        {
          values: [
            [1, "1"],
            [2, last],
          ],
        },
      ],
    });
  }) as unknown as typeof globalThis.fetch;

  const view = render(
    <MetricsRail kind="pods" name="web-1" namespace="shop" />,
  );
  await settle();

  expect(view.container.textContent).toContain("CPU");
  expect(view.container.textContent).toContain("250m");
  expect(view.container.textContent).toContain("Restarts");
  expect(view.container.textContent).toContain("2.0Gi");
  const cpu = requested.find((u) => u.includes("queries/pods/cpu")) ?? "";
  expect(cpu).toContain("name=web-1");
  expect(cpu).toContain("namespace=shop");
  expect(cpu).toContain("step=60s");
});

test("MetricsRail shows the unavailable state when monitoring is down", async () => {
  setAccessToken("t");
  globalThis.fetch = (async () =>
    json({}, 502)) as unknown as typeof globalThis.fetch;

  const view = render(
    <MetricsRail kind="pods" name="web-1" namespace="shop" />,
  );
  await settle();

  expect(view.container.textContent).toContain("Monitoring unavailable");
  expect(view.container.textContent).not.toContain("Restarts");
});

function pod(name: string, nodeName: string, phase = "Running") {
  return {
    metadata: {
      name,
      namespace: "shop",
      creationTimestamp: new Date(Date.now() - 3 * 86400_000).toISOString(),
    },
    spec: {
      nodeName,
      containers: [
        {
          name: "c",
          resources: {
            requests: { cpu: "100m", memory: "128Mi" },
            limits: { cpu: "200m", memory: "256Mi" },
          },
        },
      ],
    },
    status: {
      phase,
      containerStatuses: [{ name: "c", ready: true, restartCount: 2 }],
    },
  };
}

test("RelatedPods lists the pods with links, status and resources", async () => {
  setAccessToken("t");
  let requested = "";
  globalThis.fetch = (async (input: string | URL | Request) => {
    requested = String(input);
    return json([pod("web-1", "node-a"), pod("web-2", "node-b", "Pending")]);
  }) as unknown as typeof globalThis.fetch;

  render(
    <RelatedPods namespace="shop" labelSelector="app=web" parentName="web" />,
  );
  await waitFor(() => body().getByText("web-1"));

  expect(requested).toContain(
    "/v1/resources/pods/shop?labelSelector=app%3Dweb",
  );
  expect(body().getByText("2 pods")).toBeTruthy();
  expect(body().getByText("node-a")).toBeTruthy();
  expect(body().getByText("Running")).toBeTruthy();
  expect(body().getByText("Pending")).toBeTruthy();
  expect(body().getAllByText("100m / 200m").length).toBe(2);
  const link = body().getByText("web-2").closest("a");
  expect(link?.getAttribute("href")).toBe("/workloads/pods/shop/web-2");
});

test("RelatedPods says so when there are no pods", async () => {
  setAccessToken("t");
  globalThis.fetch = (async () =>
    json([])) as unknown as typeof globalThis.fetch;

  render(
    <RelatedPods namespace="shop" labelSelector="app=web" parentName="web" />,
  );
  await waitFor(() => body().getByText("No pods found for web"));
});
