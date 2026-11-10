/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import type { PinConfig, SavedViewConfig } from "@/lib/preference-types.ts";
import type { PinRecord, SavedViewRecord } from "@/lib/preferences.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's own fetch, captured before happy-dom replaces it.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: SavedViews } = await import("./SavedViews.tsx");
const { default: PinnedResources } = await import("./PinnedResources.tsx");
const { selectedCluster } = await import("@/src/lib/cluster.ts");
const { pins, pinsLoaded, pinsUnavailable } = await import(
  "@/src/lib/pin-store.ts"
);

const originalCluster = selectedCluster.value;

afterAll(() => {
  globalThis.fetch = bunFetch;
  selectedCluster.value = originalCluster;
});

interface Call {
  method: string;
  url: string;
  body: string | undefined;
}

let calls: Call[] = [];
let releaseWrites: Array<() => void> = [];

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/**
 * Routes by method and path. Writes wait until released, so a second click
 * lands while the first request is still in flight.
 */
function stubApi(routes: {
  views?: SavedViewRecord[];
  pins?: PinRecord[];
  pinsPending?: boolean;
}) {
  calls = [];
  releaseWrites = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({ method, url, body: init?.body as string | undefined });
    if (method === "GET" && url.endsWith("/preferences/views")) {
      return json(routes.views ?? []);
    }
    if (method === "GET" && url.endsWith("/preferences/pins")) {
      if (routes.pinsPending) return new Promise<Response>(() => {});
      return json(routes.pins ?? []);
    }
    await new Promise<void>((resolve) => releaseWrites.push(resolve));
    return method === "DELETE" ? new Response(null, { status: 204 }) : json({});
  }) as unknown as typeof fetch;
}

const writes = () => calls.filter((c) => c.method !== "GET");

const viewConfig = (over: Partial<SavedViewConfig> = {}): SavedViewConfig => ({
  schemaVersion: 1,
  resourceKind: "pods",
  namespace: "shop",
  search: "web",
  statusFilter: "all",
  sortKey: "name",
  sortDir: "asc",
  ...over,
});

const viewRecord = (
  id: string,
  name: string,
  over: Partial<SavedViewRecord> = {},
  config: Partial<SavedViewConfig> = {},
): SavedViewRecord => ({
  id,
  kind: "saved_view",
  name,
  clusterId: "local",
  schemaVersion: 1,
  revision: 1,
  config: viewConfig(config),
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...over,
});

const pinRecord = (
  id: string,
  name: string,
  clusterId = "local",
  over: Partial<PinConfig> = {},
): PinRecord => ({
  id,
  kind: "pin",
  name,
  clusterId,
  schemaVersion: 1,
  revision: 1,
  config: {
    schemaVersion: 1,
    resourceKind: "pods",
    group: "",
    version: "",
    namespace: "shop",
    name,
    uid: `uid-${id}`,
    displayKind: "Pod",
    ...over,
  },
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
});

beforeEach(() => {
  selectedCluster.value = "local";
  pins.value = [];
  pinsLoaded.value = false;
  pinsUnavailable.value = undefined;
});

afterEach(() => {
  globalThis.fetch = bunFetch;
});

const current = {
  resourceKind: "pods",
  namespace: "kube-system",
  search: "dns",
  statusFilter: "all",
  sortKey: "name",
  sortDir: "desc",
};

async function openMenu() {
  await waitFor(() =>
    expect(body().getByTestId("saved-views-toggle").textContent).toContain("("),
  );
  fireEvent.click(body().getByTestId("saved-views-toggle"));
  await waitFor(() => body().getByTestId("saved-views-menu"));
  await waitFor(() => expect(body().queryByText("Loading...")).toBeNull());
}

test("SavedViews lists this table's views on the active cluster from the response", async () => {
  stubApi({
    views: [
      viewRecord("1", "Prod pods"),
      viewRecord("2", "Staging pods", { clusterId: "other" }),
      viewRecord("3", "Deployments", {}, { resourceKind: "deployments" }),
    ],
  });
  render(
    <SavedViews resourceKind="pods" current={current} onApply={() => {}} />,
  );
  await openMenu();

  expect(body().getByTestId("saved-views-toggle").textContent).toBe(
    "Views (1)",
  );
  const rows = body().getAllByTestId("saved-view-row");
  expect(
    rows.map((r: HTMLElement) => r.getAttribute("data-view-name")),
  ).toEqual(["Prod pods"]);
  const other = body().getAllByTestId("saved-view-other-cluster");
  expect(
    other.map((r: HTMLElement) => r.getAttribute("data-cluster-id")),
  ).toEqual(["other"]);
});

test("SavedViews: two clicks on Save send one create request", async () => {
  stubApi({ views: [] });
  render(
    <SavedViews resourceKind="pods" current={current} onApply={() => {}} />,
  );
  await openMenu();
  fireEvent.click(body().getByTestId("saved-view-save"));
  const input = body().getByTestId("saved-view-name-input") as HTMLInputElement;
  fireEvent.input(input, { target: { value: "Mine" } });
  await waitFor(() => expect(input.value).toBe("Mine"));

  const save = body().getByTestId("saved-view-save-confirm");
  fireEvent.click(save);
  fireEvent.click(save);
  await waitFor(() => expect(writes().length).toBe(1));
  // Let any second request show up before counting.
  await new Promise((r) => setTimeout(r, 30));

  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("POST");
  expect(writes()[0].url).toEndWith("/api/v1/preferences/views");
  expect(JSON.parse(writes()[0].body ?? "{}")).toEqual({
    name: "Mine",
    config: {
      schemaVersion: 1,
      resourceKind: "pods",
      namespace: "kube-system",
      search: "dns",
      statusFilter: "all",
      sortKey: "name",
      sortDir: "desc",
    },
  });
  for (const release of releaseWrites) release();
});

test("SavedViews: confirming a delete sends one delete request for that view", async () => {
  stubApi({ views: [viewRecord("v1", "Prod pods")] });
  render(
    <SavedViews resourceKind="pods" current={current} onApply={() => {}} />,
  );
  await openMenu();
  fireEvent.click(body().getByTestId("saved-view-delete"));
  const dialog: HTMLElement = await waitFor(() => body().getByRole("dialog"));
  const confirm = Array.from(dialog.querySelectorAll("button")).find(
    (b: HTMLButtonElement) => b.textContent === "Delete",
  ) as HTMLButtonElement;
  fireEvent.click(confirm);
  fireEvent.click(confirm);
  await waitFor(() => expect(writes().length).toBe(1));
  await new Promise((r) => setTimeout(r, 30));

  expect(writes()).toHaveLength(1);
  expect(writes()[0].method).toBe("DELETE");
  expect(writes()[0].url).toEndWith("/api/v1/preferences/views/v1");
  for (const release of releaseWrites) release();
});

test("SavedViews: applying a view hands the parent the view's filters", async () => {
  stubApi({
    views: [
      viewRecord(
        "v1",
        "Prod pods",
        {},
        {
          namespace: "prod",
          search: "api",
          statusFilter: "running",
          sortKey: "age",
          sortDir: "desc",
        },
      ),
    ],
  });
  const applied: Array<{ state: unknown; warnings: string[] }> = [];
  render(
    <SavedViews
      resourceKind="pods"
      current={current}
      onApply={(state, warnings) => applied.push({ state, warnings })}
    />,
  );
  await openMenu();
  fireEvent.click(body().getByTestId("saved-view-apply"));

  expect(applied).toEqual([
    {
      state: {
        resourceKind: "pods",
        namespace: "prod",
        search: "api",
        statusFilter: "running",
        sortKey: "age",
        sortDir: "desc",
      },
      warnings: [],
    },
  ]);
  // The menu closes once a view is applied.
  expect(body().queryByTestId("saved-views-menu")).toBeNull();
});

test("PinnedResources shows the active cluster's pins and hides another cluster's", async () => {
  stubApi({
    pins: [pinRecord("p1", "web-1"), pinRecord("p2", "web-2", "other")],
  });
  render(<PinnedResources currentPath="/workloads/pods/shop/web-1" />);

  await waitFor(() => body().getByTestId("pinned-row"));
  const rows = body().getAllByTestId("pinned-row");
  expect(rows.map((r: HTMLElement) => r.getAttribute("data-pin-name"))).toEqual(
    ["web-1"],
  );
  expect(rows[0].getAttribute("href")).toBe("/workloads/pods/shop/web-1");
  expect(body().queryByText("web-2")).toBeNull();
});

test("PinnedResources follows the pin store after mount", async () => {
  stubApi({ pins: [pinRecord("p1", "web-1")] });
  render(<PinnedResources currentPath="/" />);
  await waitFor(() => body().getByText("web-1"));

  act(() => {
    pins.value = [...pins.value, pinRecord("p3", "web-3")];
  });
  expect(body().getByText("web-3")).toBeTruthy();

  act(() => {
    pins.value = pins.value.filter((p) => p.id !== "p1");
  });
  expect(body().queryByText("web-1")).toBeNull();
  expect(body().getByText("web-3")).toBeTruthy();
});

test("PinnedResources shows loading, then unavailable, then the empty state", async () => {
  stubApi({ pinsPending: true });
  render(<PinnedResources currentPath="/" />);

  expect(body().getByText("Loading...")).toBeTruthy();

  act(() => {
    pinsUnavailable.value = "database_unavailable";
  });
  expect(body().getByTestId("pinned-unavailable").textContent).toBe(
    "Pins need a database, and this deployment has none.",
  );
  expect(body().queryByText("Loading...")).toBeNull();

  act(() => {
    pinsUnavailable.value = undefined;
    pinsLoaded.value = true;
  });
  expect(body().getByTestId("pinned-empty").textContent).toContain(
    "Nothing pinned yet.",
  );
});

test("PinnedResources unpin sends one delete and drops the row", async () => {
  stubApi({ pins: [pinRecord("p1", "web-1")] });
  render(<PinnedResources currentPath="/" />);
  await waitFor(() => body().getByTestId("unpin"));

  fireEvent.click(body().getByTestId("unpin"));
  await waitFor(() => expect(writes().length).toBe(1));
  expect(writes()[0].method).toBe("DELETE");
  expect(writes()[0].url).toEndWith("/api/v1/preferences/pins/p1");
  await act(async () => {
    for (const release of releaseWrites) release();
  });
  await waitFor(() => expect(body().queryByText("web-1")).toBeNull());
});
