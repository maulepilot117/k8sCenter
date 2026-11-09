/** @jsxImportSource react */
import { afterEach, expect, spyOn, test } from "bun:test";
import { esoApi } from "@/lib/eso-api.ts";
import type { ExternalSecret, SecretStore, Status } from "@/lib/eso-types.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The ESO dashboard: it loads status, ExternalSecrets and stores once, shows
 * the sync-health ring and failure table for the namespace picked in the top
 * bar, and reloads on Refresh.
 */
setupDom({ browser: true });
const { default: ESODashboard } = await import("./ESODashboard.tsx");

const restores: Array<() => void> = [];
afterEach(() => {
  for (const r of restores.splice(0)) r();
  selectedNamespace.value = "all";
});

function es(namespace: string, name: string, status: Status): ExternalSecret {
  return {
    namespace,
    name,
    uid: `${namespace}-${name}`,
    status,
    storeRef: { name: "vault", kind: "SecretStore" },
  } as ExternalSecret;
}

function store(namespace: string, name: string, provider: string): SecretStore {
  return {
    namespace,
    name,
    uid: `${namespace}-${name}`,
    scope: "Namespaced",
    status: "Synced",
    ready: true,
    provider,
  } as SecretStore;
}

function stubApi(items: () => ExternalSecret[]) {
  const calls = { status: 0 };
  const spies = [
    spyOn(esoApi, "status").mockImplementation((async () => {
      calls.status++;
      return { data: { detected: true, lastChecked: "now" } };
    }) as never),
    spyOn(esoApi, "listExternalSecrets").mockImplementation((async () => ({
      data: items(),
    })) as never),
    spyOn(esoApi, "listStores").mockImplementation((async () => ({
      data: [store("apps", "vault", "vault")],
    })) as never),
    spyOn(esoApi, "listClusterStores").mockImplementation((async () => ({
      data: [],
    })) as never),
  ];
  restores.push(() => {
    for (const s of spies) s.mockRestore();
  });
  return calls;
}

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

test("shows the ring and the broken ExternalSecrets, worst first", async () => {
  stubApi(() => [
    es("apps", "ok", "Synced"),
    es("apps", "stale", "Stale"),
    es("apps", "failed", "SyncFailed"),
  ]);
  const view = render(<ESODashboard />);
  await settle();

  expect(body().getByRole("img", { name: "1 of 3 synced" }));
  const rows = [...view.container.querySelectorAll("tbody tr")].map(
    (r) => r.querySelectorAll("td")[1].textContent,
  );
  expect(rows).toEqual(["failed", "stale"]);
});

test("re-filters when the namespace picker changes", async () => {
  stubApi(() => [
    es("apps", "a", "SyncFailed"),
    es("billing", "b", "SyncFailed"),
    es("billing", "c", "Synced"),
  ]);
  render(<ESODashboard />);
  await settle();
  expect(body().getByRole("img", { name: "1 of 3 synced" }));

  await act(async () => {
    selectedNamespace.value = "billing";
  });
  expect(body().getByRole("img", { name: "1 of 2 synced" }));
  expect(
    body().getByText("2 of 3 total (namespace: billing)", { exact: false }),
  );
});

test("Refresh reloads the data", async () => {
  let items = [es("apps", "a", "Synced")];
  const calls = stubApi(() => items);
  render(<ESODashboard />);
  await settle();
  expect(body().getByRole("img", { name: "1 of 1 synced" }));
  expect(calls.status).toBe(1);

  items = [es("apps", "a", "Synced"), es("apps", "b", "Stale")];
  fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  await settle();
  expect(calls.status).toBe(2);
  expect(body().getByRole("img", { name: "1 of 2 synced" }));
});

test("a failed load shows the error and no ring", async () => {
  const spy = spyOn(esoApi, "status").mockImplementation((async () => {
    throw new Error("down");
  }) as never);
  restores.push(() => spy.mockRestore());
  const others = [
    spyOn(esoApi, "listExternalSecrets"),
    spyOn(esoApi, "listStores"),
    spyOn(esoApi, "listClusterStores"),
  ].map((s) => s.mockImplementation((async () => ({ data: [] })) as never));
  restores.push(() => {
    for (const s of others) s.mockRestore();
  });
  render(<ESODashboard />);
  await settle();
  expect(body().getByText("Failed to load ESO dashboard data"));
  expect(body().queryByRole("img")).toBeNull();
});
