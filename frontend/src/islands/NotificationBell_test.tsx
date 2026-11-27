import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The bell's list marks each row with its severity dot. A filled dot reads
 * as "unread", so a read notification's dot is a ring, and "Mark all read"
 * turns every dot into one. A notification event arriving over the socket
 * re-fetches the unread count, except for a short window after "Mark all
 * read".
 */
// Bun's own WebSocket, Date.now and fetch, captured before happy-dom
// replaces them.
const bunWebSocket = globalThis.WebSocket;
const bunFetch = globalThis.fetch;
const realNow = Date.now;
setupDom({ browser: true });

const { default: NotificationBell } = await import("./NotificationBell.tsx");
const { disconnectWS } = await import("@/src/lib/ws.ts");

/** Stands in for the browser socket the ws store opens. */
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  // Never open, so the ws store, which is shared by every test file in the
  // process, opens a fresh socket for each subscription.
  static OPEN = 1;
  static CONNECTING = 0;
  readyState = 3;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor() {
    FakeWebSocket.instances.push(this);
  }
  send() {}
  close() {}
}

globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;

// The ws store is one module shared by every test file in the run: drop a
// socket an earlier file left behind, so the bell opens one of these fakes.
beforeEach(() => {
  disconnectWS();
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  Date.now = realNow;
  setAccessToken(null);
});

afterAll(() => {
  globalThis.WebSocket = bunWebSocket;
  globalThis.fetch = bunFetch;
  Date.now = realNow;
});

const notification = (id: string, read: boolean) => ({
  id,
  source: "alert",
  severity: "warning",
  title: `title ${id}`,
  message: "",
  createdAt: new Date().toISOString(),
  ...(read ? { read: true } : {}),
});

/** Serves the notification endpoints; `server.unread` is what the count reads. */
function stubApi(items: unknown[], unread = 1) {
  const server = { unread, unreadFetches: 0 };
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    const json = (data: unknown) =>
      new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    if (url.includes("/notifications/unread-count")) {
      server.unreadFetches++;
      return json({ count: server.unread });
    }
    if (url.includes("/notifications/read-all")) return json({});
    if (url.includes("/notifications")) return json(items);
    return json(null);
  }) as unknown as typeof globalThis.fetch;
  return server;
}

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

/** The dots a reader can see: a read row's dot has no fill and no outline. */
function visibleDots(root: Element): number {
  return [...root.querySelectorAll("span")].filter((s) => {
    const style = (s as HTMLElement).style;
    return (
      style.borderRadius === "50%" &&
      style.width === "8px" &&
      (style.backgroundColor !== "transparent" || !!style.border)
    );
  }).length;
}

test("only unread rows show a dot, and Mark all read clears them all", async () => {
  stubApi([notification("a", false), notification("b", true)]);
  setAccessToken("t");
  const { container } = render(<NotificationBell />);
  await settle();

  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Notifications" }));
  });
  await settle();
  expect(visibleDots(container)).toBe(1);

  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Mark all read" }));
  });
  await settle();
  expect(visibleDots(container)).toBe(0);
});

test("Mark all read clears the badge", async () => {
  stubApi([notification("a", false)], 3);
  setAccessToken("t");
  render(<NotificationBell />);
  await settle();
  expect(body().getByText("3")).toBeTruthy();

  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Notifications" }));
  });
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Mark all read" }));
  });
  await settle();
  expect(body().queryByText("3")).toBeNull();
});

/** Delivers a notification event through the socket the ws store opened. */
function pushNotificationEvent() {
  const socket = FakeWebSocket.instances.at(-1);
  if (!socket?.onmessage) throw new Error("the bell opened no socket");
  const onmessage = socket.onmessage;
  onmessage({
    data: JSON.stringify({
      type: "event",
      id: "notif-bell",
      eventType: "ADDED",
      object: { id: "n1" },
    }),
  });
}

test("a notification event inside the suppression window after Mark all read leaves the badge cleared, and one after it brings the badge back", async () => {
  const server = stubApi([notification("a", false)], 1);
  setAccessToken("t");
  const start = realNow();
  let now = start;
  Date.now = () => now;
  render(<NotificationBell />);
  await settle();
  expect(body().getByText("1")).toBeTruthy();

  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Notifications" }));
  });
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Mark all read" }));
  });
  await settle();
  expect(body().queryByText("1")).toBeNull();

  // The server would report one unread again; only a re-fetch shows it.
  server.unread = 1;
  const fetchesBefore = server.unreadFetches;
  now = start + 1000;
  await act(async () => pushNotificationEvent());
  await settle();
  expect(server.unreadFetches).toBe(fetchesBefore);
  expect(body().queryByText("1")).toBeNull();

  now = start + 5000;
  await act(async () => pushNotificationEvent());
  await settle();
  expect(server.unreadFetches).toBe(fetchesBefore + 1);
  expect(body().getByText("1")).toBeTruthy();
});

// ---------------------------------------------------------------------------
// Rubrik rows (R24, R26)

const rubrik = (id: string, over: Record<string, unknown>) => ({
  ...notification(id, false),
  source: "rubrik",
  title: "Rubrik snapshot failed",
  message: "A Rubrik snapshot job failed.",
  ...over,
});

/** Each panel row's context line, or null when it has none. */
const contextLines = () =>
  [...document.querySelectorAll('[data-testid="notif-context"]')].map(
    (el) => el.textContent,
  );

test("Rubrik rows in the panel say which namespace, cluster and gap they are about", async () => {
  stubApi([
    rubrik("r1", { resourceNamespace: "team-a" }),
    rubrik("r2", { resourceNamespace: "team-b", clusterId: "c-remote" }),
    rubrik("r3", {
      title: "Rubrik monitoring gap",
      message: "Rubrik monitoring was down for about 48 hours.",
      clusterId: "local",
    }),
    notification("a", false),
  ]);
  setAccessToken("t");
  render(<NotificationBell />);
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Notifications" }));
  });
  await settle();
  // Two rows share the fixed title; only the line under it tells them apart.
  expect(body().getAllByText("Rubrik snapshot failed")).toHaveLength(2);
  expect(contextLines()).toEqual([
    "Namespace team-a. A Rubrik snapshot job failed.",
    "Namespace team-b, cluster c-remote. A Rubrik snapshot job failed.",
    "Rubrik monitoring was down for about 48 hours.",
  ]);
});

test("a Rubrik row's id-only frame re-fetches the count and renders nothing from the frame", async () => {
  // KTD11: the live frame for a Rubrik row is its id alone, no source or
  // title. The bell never reads the frame; it asks the server.
  const server = stubApi([rubrik("r1", { resourceNamespace: "team-a" })], 0);
  setAccessToken("t");
  const { container } = render(<NotificationBell />);
  await settle();
  expect(body().queryByText("1")).toBeNull();
  server.unread = 1;
  const before = server.unreadFetches;
  await act(async () => pushNotificationEvent());
  await settle();
  expect(server.unreadFetches).toBe(before + 1);
  expect(body().getByText("1")).toBeTruthy();
  expect(container.textContent).not.toContain("n1");
});
