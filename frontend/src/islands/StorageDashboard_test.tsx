/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { resourceCounts } from "@/src/lib/resource-counts.ts";
import { disconnectWS } from "@/src/lib/ws.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The storage dashboard picks its page from the path: the overview with
 * PVC/volume/class counts from the shared counts store, a list for PVCs, PVs
 * or storage classes, or the snapshot list. Either "New" button opens its
 * wizard, and so does arriving with ?action=create.
 */
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

const { default: StorageDashboard } = await import("./StorageDashboard.tsx");

class QuietWebSocket {
  static readonly OPEN = 1;
  readyState = 0;
  send() {}
  close() {}
  addEventListener() {}
  removeEventListener() {}
}

let urls: string[] = [];

beforeEach(() => {
  urls = [];
  globalThis.WebSocket = QuietWebSocket as unknown as typeof WebSocket;
  setAccessToken("t");
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    return new Response(
      JSON.stringify({ data: [], metadata: { total: 0, available: true } }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  }) as unknown as typeof globalThis.fetch;
  window.history.replaceState(null, "", "/");
});
afterEach(() => {
  // Drop the quiet socket from the shared ws store; left in CONNECTING, it
  // stops a later test file's subscription from opening its own socket.
  disconnectWS();
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
  setAccessToken(null);
  resourceCounts.value = null;
  window.history.replaceState(null, "", "/");
});

test("overview shows the counts from the shared store", async () => {
  const view = render(<StorageDashboard currentPath="/storage" />);
  await settle();
  resourceCounts.value = {
    persistentvolumeclaims: 7,
    persistentvolumes: 4,
    storageclasses: 3,
  };
  await settle();
  const text = view.container.textContent ?? "";
  expect(view.container.querySelector("h1")?.textContent).toBe("Storage");
  expect(text).toContain("Manage persistent volumes, claims");
  expect(text).toContain("PVC Health");
  expect(text).toContain("Storage Resources");
  for (const label of ["PVCs", "Volumes", "StorageClasses"]) {
    expect(text).toContain(label);
  }
  expect(text).toContain("7");
  expect(text).toContain("4");
  expect(text).toContain("3");
});

test("the PVC donut names its count, and says there are none when there are none", async () => {
  const view = render(<StorageDashboard currentPath="/storage" />);
  await settle();
  const donutLabel = () =>
    view.container
      .querySelector('[data-chart="donut"]')
      ?.getAttribute("aria-label");

  await act(async () => {
    resourceCounts.value = { persistentvolumeclaims: 7 };
  });
  expect(donutLabel()).toBe("Total: 7");

  await act(async () => {
    resourceCounts.value = { persistentvolumeclaims: 0 };
  });
  expect(donutLabel()).toBe("No PVCs");
});

test("overview shows dashes until the counts have loaded", async () => {
  const view = render(<StorageDashboard currentPath="/storage/overview/" />);
  await settle();
  await act(async () => {
    resourceCounts.value = null;
  });
  expect(view.container.textContent).toContain("—");
});

test("list pages use their own title and the count in the subtitle", async () => {
  resourceCounts.value = { persistentvolumes: 9 };
  const view = render(<StorageDashboard currentPath="/cluster/pvs" />);
  await settle();
  resourceCounts.value = { persistentvolumes: 9 };
  await settle();
  expect(view.container.querySelector("h1")?.textContent).toBe(
    "Persistent Volumes",
  );
  expect(view.container.querySelector("p")?.textContent).toBe(
    "9 persistent volumes",
  );
  expect(urls.some((u) => u.includes("/v1/resources/pvs"))).toBe(true);
});

test("the PVC list page and the storage class page request their kind", async () => {
  const pvcs = render(<StorageDashboard currentPath="/storage/pvcs" />);
  await settle();
  expect(pvcs.container.querySelector("h1")?.textContent).toBe(
    "Persistent Volume Claims",
  );
  expect(urls.some((u) => u.includes("/v1/resources/pvcs"))).toBe(true);
  pvcs.unmount();
  urls = [];
  const sc = render(<StorageDashboard currentPath="/cluster/storageclasses" />);
  await settle();
  expect(sc.container.querySelector("h1")?.textContent).toBe("Storage Classes");
  expect(urls.some((u) => u.includes("/v1/resources/storageclasses"))).toBe(
    true,
  );
});

test("the snapshots page renders the snapshot list", async () => {
  const view = render(<StorageDashboard currentPath="/storage/snapshots" />);
  await settle();
  expect(view.container.querySelector("h1")?.textContent).toBe("Snapshots");
  expect(view.container.querySelector("p")?.textContent).toBe(
    "Volume snapshots",
  );
  expect(urls.some((u) => u.includes("/v1/storage/snapshots"))).toBe(true);
  expect(view.container.textContent).toContain("No Volume Snapshots");
});

test("New PVC and New StorageClass open their wizards", async () => {
  render(<StorageDashboard currentPath="/storage" />);
  await settle();
  expect(document.body.textContent).not.toContain("Create PVC");
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: /New PVC/ }));
  });
  expect(document.body.textContent).toContain("Create PVC");
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: /New StorageClass/ }));
  });
  expect(document.body.textContent).toContain("Create StorageClass");
});

test("?action=create opens the PVC wizard, or the class wizard on that page", async () => {
  window.history.replaceState(null, "", "/storage/pvcs?action=create");
  const pvcs = render(<StorageDashboard currentPath="/storage/pvcs" />);
  await settle();
  expect(document.body.textContent).toContain("Create PVC");
  expect(document.body.textContent).not.toContain("Create StorageClass");
  pvcs.unmount();
  window.history.replaceState(
    null,
    "",
    "/cluster/storageclasses?action=create",
  );
  render(<StorageDashboard currentPath="/cluster/storageclasses" />);
  await settle();
  expect(document.body.textContent).toContain("Create StorageClass");
  expect(document.body.textContent).not.toContain("Create PVC");
});
