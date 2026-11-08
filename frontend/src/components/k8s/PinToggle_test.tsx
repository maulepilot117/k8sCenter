/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import type { PinConfig } from "@/lib/preference-types.ts";
import type { PinRecord } from "@/lib/preferences.ts";
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
afterAll(() => {
  globalThis.fetch = bunFetch;
});

const { PinToggle } = await import("./PinToggle.tsx");
const { pins, pinsLoaded, pinsUnavailable } = await import(
  "@/src/lib/pin-store.ts"
);
const { selectedCluster } = await import("@/src/lib/cluster.ts");

interface Call {
  method: string;
  url: string;
  body: string | undefined;
}
let calls: Call[] = [];
type Handler = (call: Call) => Response | Promise<Response>;
let handler: Handler = () => json(200, { data: [] });

function json(status: number, payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
function fail(status: number, reason?: string): Response {
  return json(status, { error: { code: status, message: "x", reason } });
}

function config(uid: string): PinConfig {
  return {
    schemaVersion: 1,
    resourceKind: "deployments",
    group: "",
    version: "",
    namespace: "shop",
    name: "web",
    uid,
    displayKind: "Deployment",
  };
}
function record(id: string, uid: string): PinRecord {
  return {
    id,
    kind: "pin",
    name: "Deployment shop/web",
    clusterId: selectedCluster.value,
    schemaVersion: 1,
    config: config(uid),
  } as PinRecord;
}

beforeEach(() => {
  calls = [];
  handler = () => json(200, { data: [] });
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    const call = {
      method: init?.method ?? "GET",
      url: String(url),
      body: init?.body as string | undefined,
    };
    calls.push(call);
    return handler(call);
  }) as unknown as typeof fetch;
  pins.value = [];
  pinsLoaded.value = true;
  pinsUnavailable.value = undefined;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
});

function mount(
  over: Partial<{ uid: string | undefined; deleted: boolean }> = {},
) {
  return render(
    <PinToggle
      resourceKind="deployments"
      displayKind="Deployment"
      namespace="shop"
      name="web"
      uid={"uid" in over ? over.uid : "u1"}
      deleted={over.deleted ?? false}
    />,
  );
}
const toggle = () => body().getByTestId("pin-toggle");
const button = () => body().getByTestId("pin-button") as HTMLButtonElement;

test("an unpinned resource reads Pin and is clickable", () => {
  mount();
  expect(toggle().getAttribute("data-pin-state")).toBe("unpinned");
  expect(button().textContent).toBe("Pin");
  expect(button().disabled).toBe(false);
});

test("an existing matching pin renders as pinned and unpins on click", async () => {
  pins.value = [record("p1", "u1")];
  handler = () => new Response(null, { status: 204 });
  mount();
  expect(toggle().getAttribute("data-pin-state")).toBe("pinned");
  expect(button().textContent).toBe("Pinned");
  expect(button().getAttribute("aria-label")).toBe("Unpin Deployment web");

  await act(async () => {
    fireEvent.click(button());
  });
  await waitFor(() => {
    expect(toggle().getAttribute("data-pin-state")).toBe("unpinned");
  });
  expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
    "DELETE /api/v1/preferences/pins/p1",
  ]);
});

test("clicking Pin posts the pin and then shows the pinned state", async () => {
  handler = (c) =>
    json(201, {
      data: record("new", JSON.parse(c.body ?? "{}").config.uid),
    });
  mount();
  await act(async () => {
    fireEvent.click(button());
  });
  await waitFor(() => {
    expect(toggle().getAttribute("data-pin-state")).toBe("pinned");
  });
  expect(calls).toHaveLength(1);
  expect(calls[0]?.method).toBe("POST");
  const sent = JSON.parse(calls[0]?.body ?? "{}");
  expect(sent.name).toBe("Deployment shop/web");
  expect(sent.config.uid).toBe("u1");
  expect(button().disabled).toBe(false);
});

test("a failed pin request leaves the control unpinned and reports the failure", async () => {
  handler = () => fail(500);
  mount();
  await act(async () => {
    fireEvent.click(button());
  });
  await waitFor(() => {
    expect(body().getByTestId("pin-error").textContent).toBe(
      "Could not reach the preference service.",
    );
  });
  expect(toggle().getAttribute("data-pin-state")).toBe("unpinned");
  expect(button().disabled).toBe(false);
  expect(button().getAttribute("aria-busy")).toBeNull();
});

test("the pin limit failure names the limit", async () => {
  handler = () => fail(409, "limit_reached");
  mount();
  await act(async () => {
    fireEvent.click(button());
  });
  await waitFor(() => {
    expect(body().getByTestId("pin-error").textContent).toContain("-pin limit");
  });
});

test("already_pinned reloads the list instead of reporting an error", async () => {
  handler = (c) =>
    c.method === "POST"
      ? fail(409, "already_pinned")
      : json(200, { data: [record("p9", "u1")] });
  mount();
  await act(async () => {
    fireEvent.click(button());
  });
  await waitFor(() => {
    expect(toggle().getAttribute("data-pin-state")).toBe("pinned");
  });
  expect(body().queryByTestId("pin-error")).toBeNull();
  expect(calls.map((c) => c.method)).toEqual(["POST", "GET"]);
});

test("a double click sends one request while the first is pending", async () => {
  let release: (r: Response) => void = () => {};
  handler = () =>
    new Promise<Response>((resolve) => {
      release = resolve;
    });
  mount();
  await act(async () => {
    fireEvent.click(button());
  });
  expect(button().disabled).toBe(true);
  expect(button().getAttribute("aria-busy")).toBe("true");
  await act(async () => {
    fireEvent.click(button());
  });
  expect(calls).toHaveLength(1);
  await act(async () => {
    release(json(201, { data: record("new", "u1") }));
  });
  await waitFor(() => {
    expect(button().disabled).toBe(false);
  });
});

test("a failed unpin reports the failure and keeps the pin", async () => {
  pins.value = [record("p1", "u1")];
  handler = (c) =>
    c.method === "DELETE"
      ? fail(500)
      : json(200, { data: [record("p1", "u1")] });
  mount();
  await act(async () => {
    fireEvent.click(button());
  });
  await waitFor(() => {
    expect(body().getByTestId("pin-error")).toBeTruthy();
  });
  expect(toggle().getAttribute("data-pin-state")).toBe("pinned");
});

test("a 404 on unpin reconciles quietly", async () => {
  pins.value = [record("p1", "u1")];
  handler = (c) =>
    c.method === "DELETE" ? fail(404) : json(200, { data: [] });
  mount();
  await act(async () => {
    fireEvent.click(button());
  });
  await waitFor(() => {
    expect(toggle().getAttribute("data-pin-state")).toBe("unpinned");
  });
  expect(body().queryByTestId("pin-error")).toBeNull();
});

test("a pin for a replaced object offers Re-pin, which deletes then creates", async () => {
  pins.value = [record("old", "u0")];
  handler = (c) =>
    c.method === "DELETE"
      ? new Response(null, { status: 204 })
      : json(201, { data: record("new", "u1") });
  mount();
  expect(toggle().getAttribute("data-pin-state")).toBe("replaced");
  expect(button().textContent).toBe("Pinned (replaced)");
  await act(async () => {
    fireEvent.click(body().getByTestId("pin-repin"));
  });
  await waitFor(() => {
    expect(toggle().getAttribute("data-pin-state")).toBe("pinned");
  });
  expect(calls.map((c) => c.method)).toEqual(["DELETE", "POST"]);
});

test("a deleted target cannot be pinned, and an existing pin reads target deleted", () => {
  const first = mount({ deleted: true });
  expect(toggle().getAttribute("data-pin-state")).toBe("unpinned");
  expect(button().disabled).toBe(true);
  first.unmount();

  pins.value = [record("p1", "u1")];
  mount({ deleted: true });
  expect(button().textContent).toBe("Pinned (target deleted)");
});

test("loading and unavailable states disable the control", () => {
  const a = mount({ uid: undefined });
  expect(toggle().getAttribute("data-pin-state")).toBe("loading");
  expect(button().disabled).toBe(true);
  a.unmount();

  pinsLoaded.value = false;
  const b = mount();
  expect(toggle().getAttribute("data-pin-state")).toBe("loading");
  b.unmount();

  pinsUnavailable.value = "database_unavailable";
  mount();
  expect(toggle().getAttribute("data-pin-state")).toBe("unavailable");
  expect(button().textContent).toBe("Pin unavailable");
  expect(button().disabled).toBe(true);
});

test("the control follows the pin store after mount", async () => {
  mount();
  expect(toggle().getAttribute("data-pin-state")).toBe("unpinned");
  await act(async () => {
    pins.value = [record("p1", "u1")];
  });
  expect(toggle().getAttribute("data-pin-state")).toBe("pinned");
});

test("a pin from another cluster does not count until that cluster is selected", async () => {
  const home = selectedCluster.value;
  try {
    pins.value = [{ ...record("p1", "u1"), clusterId: "other-cluster" }];
    mount();
    expect(toggle().getAttribute("data-pin-state")).toBe("unpinned");

    await act(async () => {
      selectedCluster.value = "other-cluster";
    });
    expect(toggle().getAttribute("data-pin-state")).toBe("pinned");
  } finally {
    selectedCluster.value = home;
  }
});
