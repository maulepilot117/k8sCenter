/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
// Loads preact/compat, as ToastProvider does on every ChromeLayout page.
import "react";
import type { ClusterInfoData } from "@/lib/dashboard/wire-types.ts";
import type { LayoutResponse } from "@/lib/preferences.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's own fetch and Response, captured before happy-dom replaces them and
// restored afterwards so later server tests see the real ones.
const bunFetch = globalThis.fetch;
const BunResponse = globalThis.Response;

setupDom({ browser: true });

const { default: DashboardV2 } = await import("./DashboardV2.tsx");
const { dashboardData } = await import("@/lib/dashboard/data.ts");
const { useSourceState } = await import("@/lib/dashboard/use-source-state.ts");
const { registerWidget } = await import("@/lib/dashboard/registry.ts");
const { selectedCluster } = await import("@/src/lib/cluster.ts");
const store = await import("@/lib/dashboard/layout-store.ts");

type Config = import("@/lib/dashboard/types.ts").DashboardLayoutConfig;
type Item = import("@/lib/dashboard/types.ts").LayoutItem;

afterAll(() => {
  globalThis.fetch = bunFetch;
});

/** A small card that shows what the cluster-info source holds. */
function Fixture({ name }: { name: string }) {
  const info = useSourceState<ClusterInfoData>("cluster-info");
  return (
    <div
      data-testid={`fx-${name}`}
    >{`${name}:${info.data?.platform ?? "none"}`}</div>
  );
}

const IDS = ["fixture-dash-a", "fixture-dash-b", "fixture-dash-c"] as const;
for (const id of IDS) {
  registerWidget({
    id,
    title: id,
    family: "cluster",
    scopes: ["overview"],
    sources: ["cluster-info"],
    minW: 2,
    minH: 2,
    defaultW: 4,
    defaultH: 4,
    modes: ["normal"],
    render: () => <Fixture name={id.slice(-1)} />,
  });
}

function cfg(...ids: string[]): Config {
  return {
    schemaVersion: 1,
    scope: "overview",
    columns: 12,
    items: ids.map(
      (id, i): Item => ({
        instanceId: `inst-${id}`,
        id: `fixture-dash-${id}`,
        x: (i * 4) % 12,
        y: Math.floor((i * 4) / 12) * 4,
        w: 4,
        h: 4,
      }),
    ),
  };
}

// The stored layouts the stubbed preferences endpoint serves, per cluster.
const stored = new Map<string, { revision: number; config: Config }>();

function record(clusterId: string, revision: number, config: Config) {
  return {
    id: `id-${clusterId}`,
    kind: "dashboard_layout",
    name: "overview",
    clusterId,
    schemaVersion: 1,
    revision,
    config,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  } as unknown as LayoutResponse;
}

const json = (data: unknown, status = 200) =>
  new BunResponse(JSON.stringify({ data }), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function stubNetwork() {
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    const cluster = new Headers(init?.headers).get("X-Cluster-ID") ?? "local";
    const method = init?.method ?? "GET";
    if (url === "/api/v1/preferences/layouts/overview") {
      const have = stored.get(cluster);
      if (method === "PUT") {
        const sent = JSON.parse(String(init?.body)) as {
          revision: number;
          config: Config;
        };
        if ((have?.revision ?? 0) !== sent.revision) {
          return new BunResponse(
            JSON.stringify({ error: { code: 409, message: "conflict" } }),
            { status: 409, headers: { "Content-Type": "application/json" } },
          );
        }
        const next = { revision: sent.revision + 1, config: sent.config };
        stored.set(cluster, next);
        return json(record(cluster, next.revision, next.config));
      }
      return have
        ? json(record(cluster, have.revision, have.config))
        : new BunResponse(null, { status: 204 });
    }
    if (url === "/api/v1/preferences/layouts") {
      return json([...stored].map(([c, s]) => record(c, s.revision, s.config)));
    }
    // Every data source stays in flight, so the test owns what each holds.
    return new Promise<Response>((_, reject) => {
      init?.signal?.addEventListener("abort", () =>
        reject(new DOMException("aborted", "AbortError")),
      );
    });
  }) as unknown as typeof fetch;
}

const EMPTY = {
  data: null,
  error: null,
  errorKind: null,
  loading: false,
  range: "1h",
} as const;

function seedInfo(platform: string) {
  dashboardData.signalFor("cluster-info").value = {
    ...EMPTY,
    data: { clusterID: "local", platform, nodeCount: 3 },
    range: "1h",
  };
  // The subtitle waits for both sources to settle.
  dashboardData.signalFor("dashboard-summary").value = {
    ...EMPTY,
    data: {
      nodes: { total: 3, ready: 3 },
      pods: { total: 12, running: 12, pending: 0, failed: 0 },
    },
  };
}

const origStartRefresh = dashboardData.startRefresh;
// One record per refresh loop started in the current test.
let loops: { stopped: boolean }[] = [];

beforeEach(async () => {
  // The previous test's unmount cleanups may still be pending.
  await new Promise((resolve) => setTimeout(resolve, 20));
  stored.clear();
  loops = [];
  dashboardData.startRefresh = (ms?: number) => {
    const loop = { stopped: false };
    loops.push(loop);
    const stop = origStartRefresh.call(dashboardData, ms);
    return () => {
      loop.stopped = true;
      stop();
    };
  };
  stubNetwork();
});

afterEach(() => {
  dashboardData.startRefresh = origStartRefresh;
  dashboardData.signalFor("cluster-info").value = { ...EMPTY };
  dashboardData.signalFor("dashboard-summary").value = { ...EMPTY };
  store.layout.value = store.defaultLayoutFor("overview");
  store.layoutLoaded.value = false;
  store.layoutRevision.value = 0;
  store.layoutGeneration.value = 0;
  store.layoutWarnings.value = [];
  store.layoutWithheld.value = [];
  store.layoutUnavailable.value = undefined;
  store.copyableLayoutRecords.value = [];
  selectedCluster.value = "local";
});

const cell = (name: string) =>
  document.querySelector(`[data-testid="fx-${name}"]`);
const cells = () =>
  [...document.querySelectorAll("[data-instance-id]")].map((el) =>
    el.getAttribute("data-instance-id"),
  );

/** Mounts the island over a stored layout and waits for it to land. */
async function mount(ids: string[]) {
  stored.set(selectedCluster.value, { revision: 4, config: cfg(...ids) });
  seedInfo("kind");
  // The default layout holds the shipped widgets; start on fixtures so the
  // first paint, before the load lands, is the same cards.
  store.layout.value = cfg(...ids);
  const view = render(<DashboardV2 />);
  await waitFor(() => {
    expect(cells()).toEqual(ids.map((i) => `inst-${i}`));
    expect(
      (body().getByTestId("edit-layout") as HTMLButtonElement).disabled,
    ).toBe(false);
  });
  return view;
}

const click = (testId: string) =>
  act(() => {
    fireEvent.click(body().getByTestId(testId));
  });

test("renders the stored layout's widgets with their data", async () => {
  await mount(["a", "b"]);
  expect(cell("a")?.textContent).toBe("a:kind");
  expect(cell("b")?.textContent).toBe("b:kind");
  expect(cells()).toHaveLength(2);
  expect(body().getByText("Cluster Overview")).toBeTruthy();
});

test("a source resolving after mount updates the widget", async () => {
  await mount(["a"]);
  expect(cell("a")?.textContent).toBe("a:kind");
  expect(body().getByText(/^kind · /)).toBeTruthy();
  act(() => {
    // Only the one source changes: the subtitle has to follow it alone.
    dashboardData.signalFor("cluster-info").value = {
      ...EMPTY,
      data: { clusterID: "local", platform: "k3s", nodeCount: 3 },
    };
  });
  expect(cell("a")?.textContent).toBe("a:k3s");
  // The island's own subtitle reads the same source.
  expect(body().getByText(/^k3s · /)).toBeTruthy();
});

test("edit mode shows the toolbar, Cancel restores the layout and focus returns to Edit", async () => {
  stored.set("prod-east", { revision: 1, config: cfg("b", "c") });
  await mount(["a"]);
  expect(document.querySelector('[data-testid="edit-toolbar"]')).toBeNull();

  await click("edit-layout");
  expect(body().getByTestId("edit-toolbar")).toBeTruthy();
  expect(body().queryByTestId("edit-layout")).toBeNull();
  expect(document.activeElement).toBe(body().getByTestId("cancel-edit"));

  // Copying another cluster's layout is an edit, which makes the session
  // dirty; Cancel then asks before discarding it.
  await waitFor(() => body().getByTestId("copy-layout"));
  await click("copy-layout");
  act(() => {
    fireEvent.click(body().getByTestId("copy-layout-option"));
  });
  await waitFor(() => expect(cells()).toEqual(["inst-b", "inst-c"]));

  await click("cancel-edit");
  await waitFor(() => body().getByText("Discard unsaved changes?"));
  act(() => {
    fireEvent.click(body().getByText("Discard changes"));
  });
  await waitFor(() => expect(cells()).toEqual(["inst-a"]));
  expect(document.querySelector('[data-testid="edit-toolbar"]')).toBeNull();
  expect(document.activeElement).toBe(body().getByTestId("edit-layout"));
});

test("copying another cluster's layout replaces this one and is kept per cluster", async () => {
  stored.set("prod-east", { revision: 1, config: cfg("b", "c") });
  stored.set("stage", { revision: 2, config: cfg("a") });
  const first = await mount(["a"]);

  await click("edit-layout");
  await waitFor(() => body().getByTestId("copy-layout"));
  await click("copy-layout");
  const row = await waitFor(() => {
    const found = body()
      .getAllByTestId("copy-layout-option")
      .find(
        (r: HTMLElement) => r.getAttribute("data-cluster-id") === "prod-east",
      );
    expect(found).toBeTruthy();
    return found as HTMLElement;
  });
  act(() => {
    fireEvent.click(row);
  });
  await waitFor(() => expect(cells()).toEqual(["inst-b", "inst-c"]));

  await click("save-layout");
  await waitFor(() => body().getByTestId("edit-layout"));
  expect(stored.get("local")?.config.items.map((i) => i.id)).toEqual([
    "fixture-dash-b",
    "fixture-dash-c",
  ]);
  first.unmount();

  // A cluster switch reloads the page, which is a fresh mount on the new one.
  selectedCluster.value = "stage";
  const second = render(<DashboardV2 />);
  await waitFor(() => expect(cells()).toEqual(["inst-a"]));
  second.unmount();

  selectedCluster.value = "local";
  render(<DashboardV2 />);
  await waitFor(() => expect(cells()).toEqual(["inst-b", "inst-c"]));
});

test("mounting starts one refresh loop and unmounting stops it", async () => {
  const view = await mount(["a"]);
  expect(loops).toHaveLength(1);
  expect(loops[0].stopped).toBe(false);

  // Re-renders from edit mode and from a new time range must not start more.
  await click("edit-layout");
  act(() => {
    fireEvent.click(body().getByText("6h"));
  });
  await click("cancel-edit");
  expect(loops).toHaveLength(1);
  expect(loops[0].stopped).toBe(false);

  view.unmount();
  await waitFor(() => expect(loops[0].stopped).toBe(true));
  expect(loops).toHaveLength(1);
});
