/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { fetchCurrentUser, logout } from "@/lib/auth.ts";
import { disconnectWS } from "@/src/lib/ws.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The notification feed lists the page the server returns, refetches when a
 * filter or the page changes (a filter returns to the first page), keeps only
 * the newest response, marks an unread row read on click, and refetches
 * after a live "notifications" event with the filters as they are then.
 */
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

const { default: NotificationFeed } = await import("./NotificationFeed.tsx");

interface Call {
  method: string;
  url: string;
  keepalive?: boolean;
}
let calls: Call[] = [];
let total = 60;
let feedFails = false;
/** Per-request hook so a test can hold a list request. */
let hold: (url: URL) => Promise<Response> | null = () => null;

/** A socket that never connects; the test pushes events into the latest one. */
class FakeSocket {
  static readonly OPEN = 1;
  static instances: FakeSocket[] = [];
  onopen: unknown = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: unknown = null;
  onerror: unknown = null;
  readyState = 0;
  constructor() {
    FakeSocket.instances.push(this);
  }
  send() {}
  close() {}
}

const note = (i: number, over: Record<string, unknown> = {}) => ({
  id: `n-${i}`,
  source: "alert",
  severity: "critical",
  title: `Notification ${String(i).padStart(2, "0")}`,
  message: "m",
  createdAt: "2026-11-05T00:00:00Z",
  ...over,
});

function page(url: URL) {
  const offset = Number(url.searchParams.get("offset") ?? 0);
  const limit = Number(url.searchParams.get("limit") ?? 25);
  const n = Math.max(0, Math.min(limit, total - offset));
  return Array.from({ length: n }, (_, i) => note(offset + i));
}

function stubApi() {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = new URL(String(input), "http://localhost/");
    calls.push({
      method: init?.method ?? "GET",
      url: `${url.pathname}${url.search}`,
      keepalive: init?.keepalive,
    });
    if (
      url.pathname === "/api/v1/notifications" &&
      (init?.method ?? "GET") === "GET"
    ) {
      const held = hold(url);
      if (held) return held;
      if (feedFails) return new Response("{}", { status: 500 });
      return new Response(
        JSON.stringify({ data: page(url), metadata: { total } }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    }
    return json({});
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
  total = 60;
  feedFails = false;
  hold = () => null;
  FakeSocket.instances = [];
  globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
  setAccessToken("t");
  stubApi();
});
afterEach(async () => {
  globalThis.fetch = (async () =>
    json({})) as unknown as typeof globalThis.fetch;
  await act(async () => {
    await logout();
  });
  disconnectWS();
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
  setAccessToken(null);
});
afterAll(() => {
  disconnectWS();
});

async function mount() {
  const view = render(<NotificationFeed />);
  await settle();
  return view.container as HTMLElement;
}

const lists = () =>
  calls.filter((c) => c.method === "GET" && c.url.startsWith("/api/v1/notif"));
const lastList = () => new URL(lists().at(-1)?.url ?? "", "http://localhost/");
const titles = (root: HTMLElement) =>
  [...root.querySelectorAll("tbody tr")].map(
    (r) => r.querySelectorAll("td")[2].textContent,
  );

const choose = async (label: string, value: string) => {
  const select = [...document.querySelectorAll("label")]
    .find((l) => l.firstChild?.textContent === label)
    ?.querySelector("select") as Element;
  await act(async () => {
    fireEvent.change(select, { target: { value } });
  });
  await settle();
};

test("shows the first page with its range and total", async () => {
  const root = await mount();
  expect(titles(root)).toHaveLength(25);
  expect(titles(root)[0]).toBe("Notification 00");
  expect(root.textContent).toContain("Showing 1-25 of 60");
  const url = lastList();
  expect(url.searchParams.get("limit")).toBe("25");
  expect(url.searchParams.get("offset")).toBe("0");
  expect(url.searchParams.has("source")).toBe(false);
});

test("an empty feed says there are no notifications", async () => {
  total = 0;
  const root = await mount();
  expect(root.textContent).toContain("No notifications yet");
});

test("a failed fetch shows the empty state", async () => {
  feedFails = true;
  const root = await mount();
  expect(root.textContent).toContain("No notifications yet");
});

test("Next and Previous move by one page and stop at both ends", async () => {
  const root = await mount();
  const prev = body().getByRole("button", {
    name: "Previous",
  }) as HTMLButtonElement;
  expect(prev.disabled).toBe(true);
  const next = () =>
    body().getByRole("button", { name: "Next" }) as HTMLButtonElement;
  await act(async () => {
    fireEvent.click(next());
  });
  await settle();
  expect(lastList().searchParams.get("offset")).toBe("25");
  expect(titles(root)[0]).toBe("Notification 25");
  await act(async () => {
    fireEvent.click(next());
  });
  await settle();
  expect(root.textContent).toContain("Showing 51-60 of 60");
  expect(next().disabled).toBe(true);
  await act(async () => {
    fireEvent.click(
      body().getByRole("button", { name: "Previous" }) as HTMLButtonElement,
    );
  });
  await settle();
  expect(titles(root)[0]).toBe("Notification 25");
});

test("changing a filter sends it and returns to the first page", async () => {
  const root = await mount();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Next" }));
  });
  await settle();
  expect(lastList().searchParams.get("offset")).toBe("25");

  await choose("Severity", "warning");
  expect(lastList().searchParams.get("severity")).toBe("warning");
  expect(lastList().searchParams.get("offset")).toBe("0");
  expect(titles(root)[0]).toBe("Notification 00");

  await choose("Status", "unread");
  expect(lastList().searchParams.get("read")).toBe("unread");
  expect(lastList().searchParams.get("severity")).toBe("warning");

  await choose("Source", "policy");
  expect(lastList().searchParams.get("source")).toBe("policy");

  await choose("Source", "all");
  expect(lastList().searchParams.has("source")).toBe(false);
});

test("a slow response for an older filter does not replace the newer list", async () => {
  const release: Array<(r: Response) => void> = [];
  hold = (url) =>
    url.searchParams.get("severity") === "info"
      ? new Promise<Response>((resolve) => {
          release.push(resolve);
        })
      : null;
  const root = await mount();
  await choose("Severity", "info");
  await choose("Severity", "critical");
  expect(titles(root)[0]).toBe("Notification 00");
  await act(async () => {
    release[0](
      new Response(
        JSON.stringify({
          data: [note(99, { title: "Stale one" })],
          metadata: { total: 1 },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    await new Promise((r) => setTimeout(r, 0));
  });
  expect(root.textContent).not.toContain("Stale one");
  expect(root.textContent).toContain("Showing 1-25 of 60");
});

test("clicking an unread row marks it read at once and sends one keepalive request", async () => {
  // A cluster notification has no link for a non-admin, so no navigation.
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = new URL(String(input), "http://localhost/");
    calls.push({
      method: init?.method ?? "GET",
      url: `${url.pathname}${url.search}`,
      keepalive: init?.keepalive,
    });
    if ((init?.method ?? "GET") === "GET") {
      return new Response(
        JSON.stringify({
          data: [
            note(1, { source: "cluster", title: "Unread one" }),
            note(2, { source: "cluster", title: "Read one", read: true }),
          ],
          metadata: { total: 2 },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    }
    return json({});
  }) as unknown as typeof globalThis.fetch;
  const root = await mount();
  const cell = (text: string) =>
    [...root.querySelectorAll("tbody td")].find(
      (td) => td.textContent === text,
    ) as HTMLElement;
  expect(cell("Unread one").style.fontWeight).toBe("500");
  expect(cell("Read one").style.fontWeight).toBe("400");

  await act(async () => {
    fireEvent.click(cell("Read one"));
  });
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);

  await act(async () => {
    fireEvent.click(cell("Unread one"));
  });
  const posts = calls.filter((c) => c.method === "POST");
  expect(posts).toHaveLength(1);
  expect(posts[0].url).toBe("/api/v1/notifications/n-1/read");
  expect(posts[0].keepalive).toBe(true);
  expect(cell("Unread one").style.fontWeight).toBe("400");

  await act(async () => {
    fireEvent.click(cell("Unread one"));
  });
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
});

test("admin-only destinations link for an admin and not for a viewer", async () => {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/auth/me") || url.includes("/auth/")) {
      return json({
        user: {
          id: "u1",
          username: "u",
          provider: "local",
          kubernetesUsername: "u",
          kubernetesGroups: [],
          roles: ["admin"],
        },
        rbac: {},
      });
    }
    return new Response(
      JSON.stringify({
        data: [note(1, { source: "cluster", title: "Cluster event" })],
        metadata: { total: 1 },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  }) as unknown as typeof globalThis.fetch;
  const root = await mount();
  const titleCell = () =>
    [...root.querySelectorAll("tbody td")].find(
      (td) => td.textContent === "Cluster event",
    ) as HTMLElement;
  expect(titleCell().querySelector("span")).toBeNull();

  await act(async () => {
    await fetchCurrentUser();
  });
  expect(titleCell().querySelector("span")?.textContent).toBe("Cluster event");
});

test("a live notifications event refetches after the debounce with the current filters", async () => {
  const root = await mount();
  await choose("Severity", "warning");
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Next" }));
  });
  await settle();
  const before = lists().length;
  expect(lastList().searchParams.get("offset")).toBe("25");

  const sock = FakeSocket.instances.at(-1) as FakeSocket;
  await act(async () => {
    sock.onmessage?.({
      data: JSON.stringify({
        type: "event",
        id: "notif-feed",
        eventType: "ADDED",
        object: {},
      }),
    });
  });
  expect(lists()).toHaveLength(before);
  await act(async () => {
    await new Promise((r) => setTimeout(r, 2100));
  });
  expect(lists()).toHaveLength(before + 1);
  expect(lastList().searchParams.get("severity")).toBe("warning");
  expect(lastList().searchParams.get("offset")).toBe("25");
  expect(titles(root)[0]).toBe("Notification 25");
});
