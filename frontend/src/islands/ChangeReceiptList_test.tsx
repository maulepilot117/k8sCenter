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

test("the pager and rows stay mounted, and focus stays put, while a page loads", async () => {
  urls = [];
  originalFetch = globalThis.fetch;
  let release: () => void = () => {};
  globalThis.fetch = ((input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    const body = (n: number) =>
      new Response(
        JSON.stringify({
          data: [view(n)],
          metadata: { total: 45, page: n, pageSize: 20 },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    if (url.includes("page=2")) {
      return new Promise<Response>((resolve) => {
        release = () => resolve(body(2));
      });
    }
    return Promise.resolve(body(1));
  }) as typeof globalThis.fetch;

  const root = await mount();
  const next = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "Next",
  ) as HTMLButtonElement;
  next.focus();
  await act(async () => {
    next.click();
  });
  await flush();

  // Loading page 2: the same button is still in the document, focused, and
  // inactive; page 1's rows are still on screen.
  expect(next.isConnected).toBe(true);
  expect(document.activeElement).toBe(next);
  expect(next.getAttribute("aria-disabled")).toBe("true");
  expect(root.textContent).toContain("Loading page 2…");
  expect(root.querySelector("tbody")?.textContent).toContain(
    view(1).operationId.slice(0, 8),
  );
  // Pressing it again while loading does nothing.
  await act(async () => {
    next.click();
  });
  expect(urls.filter((u) => u.includes("page=3"))).toHaveLength(0);

  await act(async () => {
    release();
  });
  await flush();
  expect(document.activeElement).toBe(next);
  expect(root.textContent).toContain("Page 2 of 3");
  expect(next.getAttribute("aria-disabled")).toBe("false");
});

test("a failed page load hides the old rows and never relabels them", async () => {
  urls = [];
  originalFetch = globalThis.fetch;
  let failPage2 = true;
  globalThis.fetch = ((input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    if (url.includes("page=2") && failPage2) {
      return Promise.resolve(
        new Response(
          JSON.stringify({ error: { code: 500, message: "boom" } }),
          { status: 500, headers: { "Content-Type": "application/json" } },
        ),
      );
    }
    const n = url.includes("page=2") ? 2 : 1;
    return Promise.resolve(
      new Response(
        JSON.stringify({
          data: [view(n)],
          metadata: { total: 45, page: n, pageSize: 20 },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
  }) as typeof globalThis.fetch;

  const root = await mount();
  const next = () =>
    [...root.querySelectorAll("button")].find(
      (b) => b.textContent === "Next",
    ) as HTMLButtonElement;
  await act(async () => {
    next().click();
  });
  await flush();

  // Page 1's rows are not presented as page 2, nor shown beside the error.
  expect(root.querySelector('[role="alert"]')?.textContent).toContain(
    "Could not load your recorded changes.",
  );
  expect(root.querySelector("tbody")).toBeNull();
  expect(root.textContent).toContain("Could not load page 2 of 3.");
  expect(root.textContent).not.toContain("Page 2 of 3");

  // Next retries the page that failed.
  failPage2 = false;
  await act(async () => {
    next().click();
  });
  await flush();
  expect(urls.filter((u) => u.includes("page=2"))).toHaveLength(2);
  expect(root.querySelector('[role="alert"]')).toBeNull();
  expect(root.textContent).toContain("Page 2 of 3");
  expect(root.querySelector("tbody")?.textContent).toContain(
    view(2).operationId.slice(0, 8),
  );
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
