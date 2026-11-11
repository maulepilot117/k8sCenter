import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

/**
 * The Gateway API overview opens a kind's list from its card, narrows the
 * rows with the search box and the selected namespace, and goes back to the
 * overview. The detail pages fetch one object and show it, or say it failed.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: GatewayAPIDashboard } = await import(
  "./GatewayAPIDashboard.tsx"
);
const { default: GatewayClassDetail } = await import(
  "./GatewayClassDetail.tsx"
);
const { default: GatewayGRPCRouteDetail } = await import(
  "./GatewayGRPCRouteDetail.tsx"
);
const { default: GatewayHTTPRouteDetail } = await import(
  "./GatewayHTTPRouteDetail.tsx"
);
const { default: GatewaySimpleRouteDetail } = await import(
  "./GatewaySimpleRouteDetail.tsx"
);

const requested: string[] = [];

beforeEach(() => {
  setAccessToken("t");
  selectedNamespace.value = "all";
  requested.length = 0;
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  selectedNamespace.value = "all";
  globalThis.history.replaceState(null, "", "/");
});

type Route = [fragment: string, status: number, body: unknown];

function serve(routes: Route[]) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input instanceof Request ? input.url : input);
    requested.push(url);
    const match = routes.find(([fragment]) => url.includes(fragment));
    if (!match) throw new Error(`unexpected request: ${url}`);
    const [, status, data] = match;
    return new Response(JSON.stringify(status < 400 ? { data } : data), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
}

const KIND = { total: 2, healthy: 1, degraded: 1 };
const STATUS: Route = [
  "/v1/gateway/status",
  200,
  { available: true, installedKinds: ["gateways", "httproutes"] },
];
const SUMMARY: Route = [
  "/v1/gateway/summary",
  200,
  { gateways: KIND, httpRoutes: KIND },
];
const GATEWAYS: Route = [
  "/v1/gateway/gateways",
  200,
  [
    { name: "edge", namespace: "web", gatewayClassName: "istio" },
    { name: "internal", namespace: "ops", gatewayClassName: "istio" },
    { name: "public", namespace: "web", gatewayClassName: "istio" },
  ],
];

async function openGateways() {
  serve([STATUS, SUMMARY, GATEWAYS]);
  render(<GatewayAPIDashboard />);
  const card = await waitFor(() => body().getByText("Gateways"));
  fireEvent.click(card);
  await waitFor(() => body().getByText("internal"));
}

test("a kind's card opens its list with every row", async () => {
  await openGateways();
  expect(requested.some((u) => u.includes("/v1/gateway/gateways"))).toBe(true);
  expect(body().getByText("edge")).toBeTruthy();
  expect(body().getByText("public")).toBeTruthy();
  expect(body().getByText("3 total")).toBeTruthy();
  expect(globalThis.location.search).toBe("?kind=gateways");
});

test("a row's name link is drawn in the accent colour", async () => {
  await openGateways();
  const link = body().getByText("edge").closest("a") as HTMLAnchorElement;
  expect(link.style.color).toBe("var(--accent)");
});

test("the search box narrows the rows by name or namespace", async () => {
  await openGateways();
  const box = body().getByPlaceholderText("Filter by name or namespace...");
  fireEvent.change(box, { target: { value: "ops" } });
  await waitFor(() => expect(body().queryByText("edge")).toBeNull());
  expect(body().getByText("internal")).toBeTruthy();
  fireEvent.change(box, { target: { value: "PUB" } });
  await waitFor(() => expect(body().queryByText("internal")).toBeNull());
  expect(body().getByText("public")).toBeTruthy();
});

test("a namespace change filters the open list and the count", async () => {
  await openGateways();
  act(() => {
    selectedNamespace.value = "web";
  });
  await waitFor(() => expect(body().queryByText("internal")).toBeNull());
  expect(body().getByText("2 of 3 total")).toBeTruthy();
  expect(body().getByText("edge")).toBeTruthy();
});

test("back to overview clears the list and the kind from the address", async () => {
  await openGateways();
  fireEvent.click(body().getByText("Back to overview"));
  await waitFor(() => body().getByText("HTTP Routes"));
  expect(body().queryByText("internal")).toBeNull();
  expect(globalThis.location.search).toBe("");
});

test("a deep-linked kind opens its list once the status is known", async () => {
  globalThis.history.replaceState(null, "", "/?kind=gateways");
  serve([STATUS, SUMMARY, GATEWAYS]);
  render(<GatewayAPIDashboard />);
  await waitFor(() => body().getByText("internal"));
  expect(globalThis.location.search).toBe("?kind=gateways");
});

test("a cluster without the CRDs says Gateway API is not detected", async () => {
  serve([["/v1/gateway/status", 200, { available: false }], SUMMARY]);
  render(<GatewayAPIDashboard />);
  await waitFor(() => body().getByText("Gateway API Not Detected"));
});

test("a failed status load says so", async () => {
  serve([["/v1/gateway/status", 500, { error: { code: 500, message: "x" } }]]);
  render(<GatewayAPIDashboard />);
  await waitFor(() => body().getByText("Failed to load Gateway API status"));
});

test("a GatewayClass detail shows its controller and a failure says so", async () => {
  serve([
    [
      "/v1/gateway/gatewayclasses/istio",
      200,
      { name: "istio", controllerName: "istio.io/gateway-controller" },
    ],
  ]);
  render(<GatewayClassDetail name="istio" />);
  await waitFor(() => body().getByText("istio.io/gateway-controller"));

  serve([["/v1/gateway/gatewayclasses/", 500, { error: { code: 500 } }]]);
  render(<GatewayClassDetail name="broken" />);
  await waitFor(() => body().getByText("Failed to load GatewayClass details"));
});

test("route details fetch their kind and report failures by name", async () => {
  serve([
    ["/v1/gateway/routes/grpcroutes/web/rpc", 200, { name: "rpc" }],
    ["/v1/gateway/httproutes/web/site", 200, { name: "site" }],
    ["/v1/gateway/routes/tcproutes/web/raw", 500, { error: { code: 500 } }],
  ]);
  render(<GatewayGRPCRouteDetail namespace="web" name="rpc" />);
  await waitFor(() => body().getByText("rpc"));
  render(<GatewayHTTPRouteDetail namespace="web" name="site" />);
  await waitFor(() => body().getByText("site"));
  render(
    <GatewaySimpleRouteDetail kind="tcproutes" namespace="web" name="raw" />,
  );
  await waitFor(() => body().getByText("Failed to load TCP Route details"));
  await settle();
});

test("a detail refetches when its name changes", async () => {
  serve([
    ["/gatewayclasses/one", 200, { name: "one" }],
    ["/gatewayclasses/two", 200, { name: "two" }],
  ]);
  const view = render(<GatewayClassDetail name="one" />);
  await waitFor(() => body().getByText("one"));
  view.rerender(<GatewayClassDetail name="two" />);
  await waitFor(() => body().getByText("two"));
});
