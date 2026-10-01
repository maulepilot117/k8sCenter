/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";

/**
 * The active and history alert feeds come from the local Alertmanager on
 * every cluster selection (R-8 KTD13): /alerts and /alerts/history ignore
 * X-Cluster-ID. The page must say so under a remote selection, or a quiet
 * local feed reads as a quiet remote cluster.
 */
const { default: AlertsPage } = await import("./AlertsPage.tsx");

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

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
  switchCluster(LOCAL_CLUSTER_ID, "local");
});

function stubFetch() {
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request) => {
    const url = String(input);
    const data = url.startsWith("/api/v1/alerts/history") ? { items: [] } : [];
    return Promise.resolve(
      new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

async function mount() {
  stubFetch();
  setAccessToken("test-token");
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<AlertsPage />, host as HTMLElement));
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  return host;
}

const NOTICE = "local cluster's Alertmanager";

test("a remote selection says the alert feeds are the local cluster's", async () => {
  switchCluster("remote-1", "gen-1");
  const root = await mount();
  expect(root.textContent).toContain(NOTICE);
});

test("the local cluster shows no remote notice", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  const root = await mount();
  expect(root.textContent).not.toContain(NOTICE);
});
