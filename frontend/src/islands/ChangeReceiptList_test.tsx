/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import type { ReceiptView } from "@/lib/change-types.ts";

const { default: ChangeReceiptList } = await import("./ChangeReceiptList.tsx");

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

let urls: string[] = [];
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
});

function view(n: number): ReceiptView {
  const id = `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
  return {
    operationId: id,
    receiptUrl: `/v1/changes/${id}`,
    ownerUsername: "alice",
    state: "partial",
    clusterId: "local",
    clusterGeneration: "local",
    targetGenerationChanged: false,
    contentDigest: "sha256:abc",
    documentCount: 3,
    recordedThrough: 3,
    force: false,
    containsSecret: false,
    verification: { state: "inconclusive", url: "" },
    createdAt: "2026-10-01T10:00:00Z",
  };
}

function stubFetch(status: number, body: unknown) {
  urls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request) => {
    urls.push(String(input));
    return Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

const flush = () =>
  act(async () => {
    for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0));
  });

async function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<ChangeReceiptList />, host as HTMLElement));
  await flush();
  return host;
}

test("lists receipts with both badges and a link per receipt", async () => {
  stubFetch(200, {
    data: [view(1)],
    metadata: { total: 1, page: 1, pageSize: 20 },
  });
  const root = await mount();
  expect(urls[0]).toBe("/api/v1/changes?page=1&pageSize=20");
  const row = root.querySelector("tbody tr");
  expect(row?.textContent).toContain("Partially applied");
  expect(row?.textContent).toContain("Verification inconclusive");
  expect(row?.querySelector("a")?.getAttribute("href")).toBe(
    `/changes/${view(1).operationId}`,
  );
  // One page only: no pager.
  expect(root.querySelector("nav")).toBeNull();
});

test("an empty list says how to create a record", async () => {
  stubFetch(200, { data: [], metadata: { total: 0 } });
  const root = await mount();
  expect(root.textContent).toContain("No recorded changes yet.");
  expect(
    [...root.querySelectorAll("a")].some(
      (a) => a.getAttribute("href") === "/tools/yaml-apply",
    ),
  ).toBe(true);
});

test("a 503 says change records are unavailable, not that there are none", async () => {
  stubFetch(503, { error: { code: 503, message: "no db" } });
  const root = await mount();
  expect(root.querySelector('[role="alert"]')?.textContent).toContain(
    "Change records are unavailable",
  );
  expect(root.textContent).not.toContain("No recorded changes yet.");
});

test("pages forward through the list", async () => {
  stubFetch(200, {
    data: [view(1)],
    metadata: { total: 45, page: 1, pageSize: 20 },
  });
  const root = await mount();
  expect(root.textContent).toContain("Page 1 of 3");
  const next = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "Next",
  );
  await act(async () => {
    next?.click();
  });
  await flush();
  expect(urls.at(-1)).toBe("/api/v1/changes?page=2&pageSize=20");
});
