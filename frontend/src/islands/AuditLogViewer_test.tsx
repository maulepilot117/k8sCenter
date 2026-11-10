/** @jsxImportSource react */
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
 * The audit log viewer loads the first page on mount, filters by user,
 * action and resource kind (a filter returns to page 1), and pages through
 * the results 50 at a time. fetch is stubbed per URL; everything else is real.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: AuditLogViewer } = await import("./AuditLogViewer.tsx");

const entry = (i: number, over: Record<string, unknown> = {}) => ({
  timestamp: `2026-10-01T10:00:${String(i).padStart(2, "0")}Z`,
  clusterID: "local",
  user: `user-${i}`,
  sourceIP: "10.0.0.1",
  action: "create",
  resourceKind: "deployment",
  resourceNamespace: "web",
  resourceName: `api-${i}`,
  result: "success",
  detail: `detail ${i}`,
  ...over,
});

let urls: string[] = [];

function stubApi(
  entries: unknown[] | ((url: string) => Response),
  total = 0,
  status = 200,
) {
  urls = [];
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    if (typeof entries === "function") return entries(url);
    if (status !== 200) {
      return new Response(JSON.stringify({ error: { code: status } }), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    }
    return new Response(
      JSON.stringify({ data: entries, metadata: { total } }),
      {
        status: 200,
        headers: { "Content-Type": "application/json" },
      },
    );
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => setAccessToken("t"));
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

async function mount() {
  const view = render(<AuditLogViewer />);
  await settle();
  return view.container as HTMLElement;
}

const button = (name: string) =>
  body().getByRole("button", { name }) as HTMLButtonElement;

const type = (label: string, value: string) =>
  act(async () => {
    fireEvent.change(body().getByLabelText(label), { target: { value } });
  });

test("loads the first page on mount and shows each entry", async () => {
  stubApi(
    [
      entry(1),
      entry(2, { result: "denied", resourceKind: "", detail: "" }),
      entry(3, { result: "error", resourceNamespace: "" }),
    ],
    3,
  );
  const root = await mount();

  expect(urls).toEqual(["/api/v1/audit/logs?page=1&pageSize=50"]);
  const text = root.textContent ?? "";
  expect(text).toContain("user-1");
  expect(text).toContain("deployment/web/api-1");
  expect(text).toContain("detail 1");
  expect(text).toContain("success");
  expect(text).toContain("denied");
  expect(text).toContain("error");
  // No kind: a dash; no namespace: the slash is dropped.
  expect(text).toContain("deployment/api-3");
  expect(text).toContain("3 entries · Page 1 of 1");
  expect(text).not.toContain("No audit entries found.");
});

test("an empty result says there are no entries", async () => {
  stubApi([], 0);
  const root = await mount();
  expect(root.textContent).toContain("No audit entries found.");
  expect(root.textContent).toContain("0 entries · Page 1 of 1");
});

test("a failed load falls back to the empty message", async () => {
  stubApi([], 0, 500);
  const root = await mount();
  expect(root.textContent).toContain("No audit entries found.");
});

test("Filter sends the three filters and returns to page 1", async () => {
  stubApi([entry(1)], 120);
  const root = await mount();
  await act(async () => {
    fireEvent.click(button("Next"));
  });
  await settle();
  expect(urls[1]).toBe("/api/v1/audit/logs?page=2&pageSize=50");
  expect(root.textContent).toContain("Page 2 of 3");

  await type("User", "alice");
  await type("Action", "login");
  await type("Resource Kind", "secret");
  // Typing alone does not fetch.
  expect(urls).toHaveLength(2);

  await act(async () => {
    fireEvent.click(button("Filter"));
  });
  await settle();
  expect(urls[2]).toBe(
    "/api/v1/audit/logs?page=1&pageSize=50&user=alice&action=login&kind=secret",
  );
  expect(root.textContent).toContain("Page 1 of 3");
});

test("paging moves between pages and stops at both ends", async () => {
  stubApi([entry(1)], 120);
  const root = await mount();
  expect(button("Previous").disabled).toBe(true);
  expect(button("Next").disabled).toBe(false);

  await act(async () => {
    fireEvent.click(button("Next"));
  });
  await settle();
  await act(async () => {
    fireEvent.click(button("Next"));
  });
  await settle();
  expect(urls.slice(1)).toEqual([
    "/api/v1/audit/logs?page=2&pageSize=50",
    "/api/v1/audit/logs?page=3&pageSize=50",
  ]);
  expect(root.textContent).toContain("Page 3 of 3");
  expect(button("Next").disabled).toBe(true);

  await act(async () => {
    fireEvent.click(button("Previous"));
  });
  await settle();
  expect(urls[3]).toBe("/api/v1/audit/logs?page=2&pageSize=50");
  expect(root.textContent).toContain("Page 2 of 3");
});

test("two Next presses before a render advance two pages", async () => {
  stubApi([entry(1)], 150);
  const root = await mount();
  const next = button("Next");
  await act(async () => {
    next.click();
    next.click();
  });
  await settle();
  expect(urls.slice(1)).toEqual([
    "/api/v1/audit/logs?page=2&pageSize=50",
    "/api/v1/audit/logs?page=3&pageSize=50",
  ]);
  expect(root.textContent).toContain("Page 3 of 3");
});

test("an unfiltered Filter press reloads page 1 without filter parameters", async () => {
  stubApi([entry(1)], 1);
  await mount();
  await act(async () => {
    fireEvent.click(button("Filter"));
  });
  await settle();
  expect(urls[1]).toBe("/api/v1/audit/logs?page=1&pageSize=50");
});
