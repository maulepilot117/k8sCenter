/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * The bell's list marks each row with its severity dot. A filled dot reads
 * as "unread", so a read notification's dot is a ring, and "Mark all read"
 * turns every dot into one. As in DiagnosticWorkspace_test.tsx, IS_BROWSER
 * is stubbed to true for this island and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: NotificationBell } = await import("./NotificationBell.tsx");

class NoopWebSocket {
  onopen: (() => void) | null = null;
  onmessage: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  send() {}
  close() {}
}

const realWebSocket = globalThis.WebSocket;
const realFetch = globalThis.fetch;
let host: HTMLElement | null = null;

beforeAll(() => {
  globalThis.WebSocket = NoopWebSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  globalThis.fetch = realFetch;
  setAccessToken(null);
});

afterAll(() => {
  globalThis.WebSocket = realWebSocket;
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
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

function stubApi(items: unknown[]) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    const json = (data: unknown) =>
      new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    if (url.includes("/notifications/unread-count")) return json({ count: 1 });
    if (url.includes("/notifications/read-all")) return json({});
    if (url.includes("/notifications")) return json(items);
    return json(null);
  }) as unknown as typeof globalThis.fetch;
}

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

/** The dots a reader can see: a read row's dot has no fill and no outline. */
function visibleDots(root: HTMLElement): number {
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
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<NotificationBell />, host as HTMLElement));
  await settle();

  const bell = host.querySelector(
    'button[aria-label="Notifications"]',
  ) as HTMLButtonElement;
  await act(async () => bell.click());
  await settle();
  expect(visibleDots(host)).toBe(1);

  const markAll = [...host.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === "Mark all read",
  ) as HTMLButtonElement;
  await act(async () => markAll.click());
  await settle();
  expect(visibleDots(host)).toBe(0);
});
