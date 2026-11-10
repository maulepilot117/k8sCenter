/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The active and history alert feeds come from the local Alertmanager on
 * every cluster selection (R-8 KTD13): /alerts and /alerts/history ignore
 * X-Cluster-ID. The page must say so under a remote selection, or a quiet
 * local feed reads as a quiet remote cluster.
 *
 * The page also lists the firing alerts, switches to the history tab (which
 * pages with a continue token and ignores a response that a newer request has
 * overtaken), expands a row for its annotations and labels, and refreshes the
 * tab it is on.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: AlertsPage } = await import("./AlertsPage.tsx");

interface Call {
  url: string;
  aborted: () => boolean;
}
let calls: Call[] = [];
let active: unknown[] = [];
/** Per-request hook so a test can hold a history request. */
let hold: (url: URL) => Promise<Response> | null = () => null;

const alertEvent = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  alertName: `Alert-${id}`,
  severity: "critical",
  namespace: "prod",
  status: "firing",
  startsAt: "2026-11-05T10:00:00Z",
  labels: { team: "core" },
  annotations: { summary: `summary ${id}`, description: `desc ${id}` },
  ...over,
});

const reply = (data: unknown) =>
  new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  calls = [];
  active = [];
  hold = () => null;
  setAccessToken("test-token");
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost/");
    calls.push({
      url: `${url.pathname}${url.search}`,
      aborted: () => init?.signal?.aborted ?? false,
    });
    if (url.pathname === "/api/v1/alerts/history") {
      return hold(url) ?? Promise.resolve(reply({ items: [] }));
    }
    return Promise.resolve(reply(active));
  }) as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  switchCluster(LOCAL_CLUSTER_ID, "local");
});

async function mount() {
  const view = render(<AlertsPage />);
  await settle();
  return view.container as HTMLElement;
}

const click = async (el: Element) => {
  await act(async () => {
    fireEvent.click(el);
  });
  await settle();
};
const historyCalls = () =>
  calls.filter((c) => c.url.startsWith("/api/v1/alerts/history"));

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

test("no firing alerts shows the all-clear", async () => {
  const root = await mount();
  expect(root.textContent).toContain("No active alerts");
});

test("lists the firing alerts with a count on the Active tab", async () => {
  active = [alertEvent("a"), alertEvent("b", { severity: "warning" })];
  const root = await mount();
  const rows = [...root.querySelectorAll("tbody tr")];
  expect(rows).toHaveLength(2);
  expect(rows[0].textContent).toContain("Alert-a");
  expect(rows[0].textContent).toContain("critical");
  expect(rows[0].textContent).toContain("prod");
  expect(rows[1].textContent).toContain("warning");
  expect(body().getByRole("button", { name: /Active/ }).textContent).toContain(
    "2",
  );
  expect(root.textContent).not.toContain("Resolved");
});

test("clicking a row shows its annotations and labels, and clicking again hides them", async () => {
  active = [alertEvent("a"), alertEvent("b")];
  const root = await mount();
  await click(body().getByText("Alert-a"));
  expect(root.textContent).toContain("Summary:summary a");
  expect(root.textContent).toContain("Description:desc a");
  expect(root.textContent).toContain("team=core");
  await click(body().getByText("Alert-b"));
  expect(root.textContent).not.toContain("summary a");
  expect(root.textContent).toContain("summary b");
  await click(body().getByText("Alert-b"));
  expect(root.textContent).not.toContain("summary b");
});

test("the History tab loads the history with a resolved column", async () => {
  const root = await mount();
  expect(historyCalls()).toHaveLength(0);
  await click(body().getByRole("button", { name: "History" }));
  expect(historyCalls()).toHaveLength(1);
  expect(historyCalls()[0].url).toContain("limit=50");
  expect(root.textContent).not.toContain("No active alerts");
});

test("Load More passes the continue token and replaces the list", async () => {
  hold = (url) =>
    Promise.resolve(
      reply(
        url.searchParams.get("continue")
          ? { items: [alertEvent("h2", { status: "resolved" })] }
          : { items: [alertEvent("h1")], continue: "tok-1" },
      ),
    );
  const root = await mount();
  await click(body().getByRole("button", { name: "History" }));
  expect(root.textContent).toContain("Alert-h1");
  await click(body().getByRole("button", { name: "Load More" }));
  expect(historyCalls()[1].url).toContain("continue=tok-1");
  expect(root.textContent).toContain("Alert-h2");
  expect(root.textContent).not.toContain("Alert-h1");
  expect(body().queryByRole("button", { name: "Load More" })).toBeNull();
});

test("Refresh reloads the tab it is on", async () => {
  const root = await mount();
  const before = calls.filter((c) => c.url === "/api/v1/alerts").length;
  active = [alertEvent("fresh")];
  await click(body().getByRole("button", { name: "Refresh" }));
  expect(calls.filter((c) => c.url === "/api/v1/alerts")).toHaveLength(
    before + 1,
  );
  expect(root.textContent).toContain("Alert-fresh");

  await click(body().getByRole("button", { name: "History" }));
  await click(body().getByRole("button", { name: "Refresh" }));
  expect(historyCalls()).toHaveLength(2);
});

test("a slow history response that a newer request overtook is ignored", async () => {
  const release: Array<(r: Response) => void> = [];
  hold = () =>
    new Promise<Response>((resolve) => {
      release.push(resolve);
    });
  const root = await mount();
  await click(body().getByRole("button", { name: "History" }));
  await click(body().getByRole("button", { name: "Refresh" }));
  expect(release).toHaveLength(2);
  expect(historyCalls()[0].aborted()).toBe(true);
  await act(async () => {
    release[1](reply({ items: [alertEvent("new")] }));
    await new Promise((r) => setTimeout(r, 0));
  });
  await act(async () => {
    release[0](reply({ items: [alertEvent("old")] }));
    await new Promise((r) => setTimeout(r, 0));
  });
  expect(root.textContent).toContain("Alert-new");
  expect(root.textContent).not.toContain("Alert-old");
});

test("leaving the History tab aborts a history request still in flight", async () => {
  hold = () => new Promise<Response>(() => {});
  await mount();
  await click(body().getByRole("button", { name: "History" }));
  expect(historyCalls()[0].aborted()).toBe(false);
  await click(body().getByRole("button", { name: /Active/ }));
  expect(historyCalls()[0].aborted()).toBe(true);
});
