/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The Applications and ApplicationSets lists: the search box narrows the
 * rows, the selected namespace narrows the Applications rows, and Refresh
 * reloads the data once.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: GitOpsApplications } = await import(
  "./GitOpsApplications.tsx"
);
const { default: GitOpsAppSets } = await import("./GitOpsAppSets.tsx");

const urls: string[] = [];

beforeEach(() => {
  // A remote selection keeps the live-update socket closed.
  switchCluster("remote-1", "gen-1");
  setAccessToken("t");
  selectedNamespace.value = "all";
  urls.length = 0;
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  selectedNamespace.value = "all";
  switchCluster(LOCAL_CLUSTER_ID, "local");
});

const SUMMARY = {
  total: 2,
  synced: 2,
  outOfSync: 0,
  degraded: 0,
  progressing: 0,
  suspended: 0,
};

function app(name: string, namespace: string) {
  return {
    id: `argo:${namespace}:${name}`,
    name,
    namespace,
    tool: "argocd",
    kind: "Application",
    syncStatus: "synced",
    healthStatus: "healthy",
    source: { repoURL: `https://example.test/${name}.git` },
    managedResourceCount: 1,
    suspended: false,
  };
}

function appSet(name: string, generator: string) {
  return {
    id: `argocd:argocd:${name}`,
    name,
    namespace: "argocd",
    tool: "argocd",
    generatorTypes: [generator],
    templateSource: {},
    templateDestination: "in-cluster",
    status: "Healthy",
    generatedAppCount: 0,
    preserveOnDeletion: false,
    summary: SUMMARY,
    createdAt: "2026-10-01T00:00:00Z",
  };
}

function serve(routes: Record<string, unknown>) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input instanceof Request ? input.url : input);
    urls.push(url);
    const key = Object.keys(routes).find((k) => url.includes(k));
    if (!key) throw new Error(`unexpected request: ${url}`);
    return new Response(JSON.stringify({ data: routes[key] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
}

function serveApplications() {
  serve({
    "/v1/gitops/status": {
      detected: "argocd",
      lastChecked: "2026-10-01T00:00:00Z",
    },
    "/v1/gitops/applications": {
      applications: [app("guestbook", "argocd"), app("billing", "payments")],
      summary: SUMMARY,
    },
  });
}

test("the Applications search box narrows the rows and the counter", async () => {
  serveApplications();
  render(<GitOpsApplications />);
  await settle();

  expect(body().getByText("guestbook")).toBeTruthy();
  expect(body().getByText("billing")).toBeTruthy();
  expect(document.body.textContent).toContain("2 of 2 applications");

  const search = body().getByPlaceholderText(
    "Filter by name, namespace, repo...",
  );
  fireEvent.input(search, { target: { value: "guest" } });
  await settle();

  expect(body().getByText("guestbook")).toBeTruthy();
  expect(body().queryByText("billing")).toBeNull();
  expect(document.body.textContent).toContain("1 of 2 applications");
});

test("changing the selected namespace narrows the Applications rows", async () => {
  serveApplications();
  render(<GitOpsApplications />);
  await settle();
  expect(body().getByText("billing")).toBeTruthy();

  await act(async () => {
    selectedNamespace.value = "payments";
  });
  await settle();

  expect(body().getByText("billing")).toBeTruthy();
  expect(body().queryByText("guestbook")).toBeNull();
});

test("Refresh on the Applications list reloads status and applications once", async () => {
  serveApplications();
  render(<GitOpsApplications />);
  await settle();
  expect(urls).toHaveLength(2);

  fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  await settle();

  expect(urls).toHaveLength(4);
  expect(
    urls.filter((u) => u.includes("/v1/gitops/applications")),
  ).toHaveLength(2);
});

test("the ApplicationSets search box narrows the rows by generator", async () => {
  serve({
    "/v1/gitops/applicationsets": {
      applicationSets: [appSet("team-apps", "list"), appSet("envs", "git")],
    },
  });
  render(<GitOpsAppSets />);
  await settle();

  expect(body().getByText("team-apps")).toBeTruthy();
  expect(body().getByText("envs")).toBeTruthy();

  const search = body().getByPlaceholderText(
    "Filter by name, namespace, generator...",
  );
  fireEvent.input(search, { target: { value: "git" } });
  await settle();

  expect(body().getByText("envs")).toBeTruthy();
  expect(body().queryByText("team-apps")).toBeNull();
  expect(document.body.textContent).toContain("1 of 2 applicationsets");

  fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  await settle();
  expect(urls).toHaveLength(2);
});
