/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";
// Static imports evaluate before this file's body, so these stores see the
// real IS_BROWSER (false) and register no browser-only effects that would
// outlive the stub below.
import { selectedNamespace } from "@/src/lib/namespace.ts";
import "@/src/lib/resource-counts.ts";

/**
 * The pages that read a remote multi-source list's `coverage` field (R-8
 * KTD8): each must name the source it could not load and still show what it
 * did load (R8).
 *
 * Islands render nothing unless `IS_BROWSER` is true, and that constant is
 * fixed when `is-browser.ts` is first evaluated, which under `bun test` is
 * without a DOM. This file stubs it to true for its own islands and puts the
 * real value (false under `bun test`) back afterwards, so files that run
 * later still see what they always saw. The signal stores that act on
 * IS_BROWSER at import are imported statically above, before the stub, so
 * none of them registers a browser-only effect here.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: GitOpsApplications } = await import(
  "./GitOpsApplications.tsx"
);
const { default: GitOpsAppSets } = await import("./GitOpsAppSets.tsx");
const { default: GatewayAPIDashboard } = await import(
  "./GatewayAPIDashboard.tsx"
);
const { default: GatewayDetail } = await import("./GatewayDetail.tsx");

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
const originalFetch = globalThis.fetch;

beforeEach(() => {
  // Coverage is only ever sent for a remote cluster; a remote selection
  // also keeps useWsRefetch from opening the resource socket.
  switchCluster("remote-1", "gen-1");
  setAccessToken("test-token");
  // Another file may have left a namespace selected, which would filter
  // the rows these tests look for.
  selectedNamespace.value = "all";
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  globalThis.fetch = originalFetch;
  setAccessToken(null);
  switchCluster(LOCAL_CLUSTER_ID, "local");
  globalThis.history.replaceState(null, "", "/");
});

type Route = [fragment: string, status: number, body: unknown];

/** Answers each request with the first route whose fragment its URL holds. */
function serve(routes: Route[]) {
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    const match = routes.find(([fragment]) => url.includes(fragment));
    if (!match) throw new Error(`unexpected request: ${url}`);
    const [, status, body] = match;
    return Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as unknown as typeof globalThis.fetch;
}

/** Mounts `node` and settles until its text contains `text`, or gives up. */
async function mountUntil(
  node: preact.ComponentChild,
  text: string,
): Promise<HTMLElement> {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(node, host as HTMLElement));
  for (let i = 0; i < 50 && !host.textContent?.includes(text); i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
  return host;
}

const UNREACHABLE = { status: "unavailable", reasonCode: "unreachable" };
const SUMMARY = {
  total: 1,
  synced: 1,
  outOfSync: 0,
  degraded: 0,
  progressing: 0,
  suspended: 0,
};

test("GitOps Applications names a failed Flux list and keeps the Argo rows", async () => {
  serve([
    [
      "/v1/gitops/status",
      200,
      { data: { detected: "both", lastChecked: "2026-10-01T00:00:00Z" } },
    ],
    [
      "/v1/gitops/applications",
      200,
      {
        data: {
          applications: [
            {
              id: "argo:argocd:guestbook",
              name: "guestbook",
              namespace: "argocd",
              tool: "argocd",
              kind: "Application",
              syncStatus: "synced",
              healthStatus: "healthy",
              source: { repoURL: "https://example.test/guestbook.git" },
              managedResourceCount: 3,
              suspended: false,
            },
          ],
          summary: SUMMARY,
          coverage: [{ source: "kustomizations", ...UNREACHABLE }],
        },
      },
    ],
  ]);
  const root = await mountUntil(<GitOpsApplications />, "Could not load");
  expect(root.textContent).toContain("Could not load Flux Kustomizations");
  expect(root.textContent).toContain("guestbook");
});

test("ApplicationSets names a failed child-application list", async () => {
  serve([
    [
      "/v1/gitops/applicationsets",
      200,
      {
        data: {
          applicationSets: [
            {
              id: "argocd:argocd:team-apps",
              name: "team-apps",
              namespace: "argocd",
              tool: "argocd",
              generatorTypes: ["list"],
              templateSource: {},
              templateDestination: "in-cluster",
              status: "Healthy",
              generatedAppCount: 0,
              preserveOnDeletion: false,
              summary: SUMMARY,
              createdAt: "2026-10-01T00:00:00Z",
            },
          ],
          total: 1,
          coverage: [{ source: "applications", ...UNREACHABLE }],
        },
      },
    ],
  ]);
  const root = await mountUntil(<GitOpsAppSets />, "Could not load");
  expect(root.textContent).toContain("Could not load Argo CD Applications");
  expect(root.textContent).toContain("team-apps");
});

const KIND = { total: 1, healthy: 1, degraded: 0 };
const GATEWAY_STATUS = {
  data: {
    available: true,
    installedKinds: ["gateways", "httproutes", "grpcroutes"],
    lastChecked: "2026-10-01T00:00:00Z",
  },
};

test("the Gateway API overview names a route kind it could not count", async () => {
  serve([
    ["/v1/gateway/status", 200, GATEWAY_STATUS],
    [
      "/v1/gateway/summary",
      200,
      {
        data: {
          gatewayClasses: KIND,
          gateways: KIND,
          httpRoutes: KIND,
          grpcRoutes: { total: 0, healthy: 0, degraded: 0 },
          tcpRoutes: KIND,
          tlsRoutes: KIND,
          udpRoutes: KIND,
          coverage: [{ source: "grpcroutes", ...UNREACHABLE }],
        },
      },
    ],
  ]);
  const root = await mountUntil(<GatewayAPIDashboard />, "Could not load");
  expect(root.textContent).toContain("Could not load gRPC Routes");
  expect(root.textContent).toContain("HTTP Routes");
});

test("a Gateway API kind list that fails says so instead of listing none", async () => {
  globalThis.history.replaceState(null, "", "/?kind=grpcroutes");
  serve([
    ["/v1/gateway/status", 200, GATEWAY_STATUS],
    ["/v1/gateway/summary", 200, { data: { gatewayClasses: KIND } }],
    [
      "/v1/gateway/routes",
      502,
      {
        error: {
          code: 502,
          message: "the selected cluster is unreachable",
          reason: "unreachable",
        },
      },
    ],
  ]);
  const root = await mountUntil(<GatewayAPIDashboard />, "gRPC Routes");
  expect(root.textContent).toContain("Failed to load gRPC Routes");
  expect(root.textContent).not.toContain("No grpc routes found");
});

test("a Gateway's detail names a route kind it could not list, with none attached", async () => {
  serve([
    [
      "/v1/gateway/gateways/web/edge",
      200,
      {
        data: {
          name: "edge",
          namespace: "web",
          gatewayClassName: "istio",
          listeners: [],
          attachedRouteCount: 0,
          age: "1d",
          attachedRoutes: [],
          coverage: [{ source: "tcproutes", ...UNREACHABLE }],
        },
      },
    ],
  ]);
  const root = await mountUntil(
    <GatewayDetail namespace="web" name="edge" />,
    "Could not load",
  );
  expect(root.textContent).toContain("Could not load TCP Routes");
  expect(root.textContent).toContain("edge");
});
