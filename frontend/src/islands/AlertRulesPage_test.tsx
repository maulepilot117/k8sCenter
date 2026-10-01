/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";

/**
 * Under a remote selection the rule list is the remote cluster's
 * PrometheusRule objects, but the firing and history feeds stay on the local
 * Alertmanager (R-8 KTD13, R14). The page must say so, or an operator reads
 * a quiet local feed as proof the remote rules are not firing.
 */
const { default: AlertRulesPage } = await import("./AlertRulesPage.tsx");

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

async function mount() {
  originalFetch = globalThis.fetch;
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(JSON.stringify({ data: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    )) as unknown as typeof globalThis.fetch;
  setAccessToken("test-token");
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<AlertRulesPage />, host as HTMLElement));
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  return host;
}

const NOTICE = "local cluster's Alertmanager";

test("a remote selection says firing alerts come from the local Alertmanager", async () => {
  switchCluster("remote-1", "gen-1");
  const root = await mount();
  expect(root.textContent).toContain(NOTICE);
});

test("the local cluster shows no remote notice", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  const root = await mount();
  expect(root.textContent).not.toContain(NOTICE);
});
