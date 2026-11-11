import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The snapshot list shows the cluster's VolumeSnapshots, explains a missing
 * CRD, and deletes one after a type-to-confirm dialog: the row leaves at once,
 * comes back if the request fails, and a second press while the request is
 * running sends nothing.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: SnapshotList } = await import("./SnapshotList.tsx");

interface Call {
  method: string;
  url: string;
}
let calls: Call[] = [];
let releaseDelete: (() => void) | null = null;

const snap = (name: string, extra: Record<string, unknown> = {}) => ({
  name,
  namespace: "apps",
  volumeSnapshotClassName: "csi-class",
  sourcePVC: "data",
  readyToUse: true,
  restoreSize: "1Gi",
  createdAt: "2026-01-01T00:00:00Z",
  ...extra,
});

function stubApi(opts: {
  snapshots?: unknown[];
  available?: boolean;
  listStatus?: number;
  deleteStatus?: number;
  holdDelete?: boolean;
}) {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({ method, url });
    if (method === "DELETE") {
      if (opts.holdDelete) {
        await new Promise<void>((r) => {
          releaseDelete = r;
        });
      }
      return new Response(
        JSON.stringify(
          opts.deleteStatus && opts.deleteStatus >= 400
            ? { error: { code: opts.deleteStatus, message: "nope" } }
            : { data: {} },
        ),
        {
          status: opts.deleteStatus ?? 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    }
    if (opts.listStatus && opts.listStatus >= 400) {
      return new Response(
        JSON.stringify({
          error: { code: opts.listStatus, message: "list broke" },
        }),
        {
          status: opts.listStatus,
          headers: { "Content-Type": "application/json" },
        },
      );
    }
    return new Response(
      JSON.stringify({
        data: opts.snapshots ?? [],
        metadata: { available: opts.available ?? true },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
  releaseDelete = null;
  setAccessToken("t");
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

test("lists snapshots with source, class, size and status", async () => {
  stubApi({
    snapshots: [
      snap("snap-a"),
      snap("snap-b", { readyToUse: false, sourcePVC: "", restoreSize: "" }),
      snap("snap-c", { errorMessage: "boom" }),
    ],
  });
  const view = render(<SnapshotList />);
  await settle();
  expect(calls[0]?.url).toContain("/v1/storage/snapshots");
  const text = view.container.textContent ?? "";
  expect(text).toContain("Volume Snapshots (3)");
  expect(text).toContain("snap-a");
  expect(text).toContain("csi-class");
  expect(text).toContain("Ready");
  expect(text).toContain("Pending");
  expect(text).toContain("N/A");
  expect(text).toContain("Error");
  expect(text).toContain("boom");
  const links = Array.from(
    view.container.querySelectorAll<HTMLAnchorElement>("a"),
  );
  const restoreA = links[0];
  expect(restoreA?.getAttribute("href")).toBe(
    "/storage/snapshots/restore?ns=apps&name=snap-a&restoreSize=1Gi&snapshotClass=csi-class",
  );
  expect(links[1]?.getAttribute("href")).toBeNull();
});

test("shows the empty state when there are no snapshots", async () => {
  stubApi({ snapshots: [] });
  const view = render(<SnapshotList />);
  await settle();
  expect(view.container.textContent).toContain("No Volume Snapshots");
  body().getByRole("button", { name: "Create Snapshot" });
  body().getByRole("button", { name: "Schedule Snapshots" });
});

test("explains that the snapshot CRDs are missing", async () => {
  stubApi({ available: false });
  const view = render(<SnapshotList />);
  await settle();
  expect(view.container.textContent).toContain(
    "VolumeSnapshot CRDs Not Installed",
  );
  expect(view.container.textContent).not.toContain("Create Snapshot");
});

test("shows the error when loading fails", async () => {
  stubApi({ listStatus: 500 });
  const view = render(<SnapshotList />);
  await settle();
  expect(view.container.textContent).toContain("list broke");
});

async function openDelete(name: string) {
  const row = body().getByText(name).closest("tr") as HTMLElement;
  const del = Array.from(row.querySelectorAll("button")).find(
    (b) => b.textContent === "Delete",
  ) as HTMLButtonElement;
  await act(async () => {
    fireEvent.click(del);
  });
}

async function typeName(name: string) {
  const input = document.querySelector(
    `input[placeholder="${name}"]`,
  ) as HTMLInputElement;
  await act(async () => {
    fireEvent.change(input, { target: { value: name } });
  });
}

function confirmButton(): HTMLButtonElement {
  const dialog = document.querySelector('[role="dialog"]') ?? document.body;
  return Array.from(dialog.querySelectorAll("button")).find(
    (b) => b.textContent === "Delete" || b.textContent === "...",
  ) as HTMLButtonElement;
}

test("deleting needs the typed name, then removes the row and sends one DELETE", async () => {
  stubApi({ snapshots: [snap("snap-a"), snap("snap-b")] });
  const view = render(<SnapshotList />);
  await settle();
  await openDelete("snap-a");
  expect(confirmButton().disabled).toBe(true);
  await typeName("snap-a");
  expect(confirmButton().disabled).toBe(false);
  await act(async () => {
    fireEvent.click(confirmButton());
  });
  await settle();
  const deletes = calls.filter((c) => c.method === "DELETE");
  expect(deletes).toHaveLength(1);
  expect(deletes[0]?.url).toContain("/v1/storage/snapshots/apps/snap-a");
  expect(view.container.textContent).not.toContain("snap-a");
  expect(view.container.textContent).toContain("snap-b");
  expect(document.querySelector('input[placeholder="snap-a"]')).toBeNull();
});

test("a failed delete puts the row back", async () => {
  stubApi({ snapshots: [snap("snap-a")], deleteStatus: 500 });
  const view = render(<SnapshotList />);
  await settle();
  await openDelete("snap-a");
  await typeName("snap-a");
  await act(async () => {
    fireEvent.click(confirmButton());
  });
  await settle();
  expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  expect(view.container.textContent).toContain("snap-a");
});

test("pressing confirm twice before the request finishes sends one DELETE", async () => {
  stubApi({ snapshots: [snap("snap-a")], holdDelete: true });
  render(<SnapshotList />);
  await settle();
  await openDelete("snap-a");
  await typeName("snap-a");
  const button = confirmButton();
  act(() => {
    fireEvent.click(button);
    fireEvent.click(button);
  });
  await settle();
  expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  await act(async () => {
    releaseDelete?.();
  });
  await settle();
});
