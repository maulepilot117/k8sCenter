/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";

/**
 * Every notification endpoint answers with one envelope, `{ data: ... }`,
 * and `apiGet` returns that envelope as-is. The islands read
 * `res.data.data`, one level too deep: Channels crashed on an empty list,
 * Rules reported "Failed to load rules", the feed was always empty and the
 * bell's unread count never loaded.
 *
 * fetch answers with the backend's real response shapes. IS_BROWSER is
 * stubbed to true and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const CHANNEL = {
  id: "ch-1",
  name: "ops-slack",
  type: "slack",
  config: {},
  createdBy: "admin",
  createdAt: "2026-11-01T00:00:00Z",
};
const RULE = {
  id: "r-1",
  name: "critical-to-ops",
  sourceFilter: [],
  severityFilter: ["critical"],
  channelId: "ch-1",
  channelName: "ops-slack",
  enabled: true,
  createdBy: "admin",
  createdAt: "2026-11-01T00:00:00Z",
};
const NOTIFICATION = {
  id: "n-1",
  source: "alert",
  severity: "critical",
  title: "Node worker-1 not ready",
  message: "kubelet stopped posting status",
  createdAt: "2026-11-05T00:00:00Z",
};

/** The backend's bodies, keyed by path. Channels can be told to be empty. */
let emptyChannels = false;
function body(path: string): unknown {
  if (path.startsWith("/api/v1/notifications/channels")) {
    return { data: emptyChannels ? null : [CHANNEL] };
  }
  if (path.startsWith("/api/v1/notifications/rules")) return { data: [RULE] };
  if (path.startsWith("/api/v1/notifications/unread-count")) {
    return { data: { count: 3 } };
  }
  if (path.startsWith("/api/v1/notifications")) {
    return { data: [NOTIFICATION], metadata: { total: 1 } };
  }
  return { data: null };
}

const realFetch = globalThis.fetch;
globalThis.fetch = (async (input: RequestInfo | URL) => {
  const url = new URL(
    typeof input === "string"
      ? input
      : ((input as Request).url ?? String(input)),
    "http://localhost/",
  );
  return new Response(JSON.stringify(body(url.pathname)), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}) as unknown as typeof fetch;

// The feed and the bell subscribe to live updates; this socket never opens.
class InertSocket {
  onopen = null;
  onmessage = null;
  onclose = null;
  onerror = null;
  readyState = 0;
  send() {}
  close() {}
}
const realWebSocket = globalThis.WebSocket;
globalThis.WebSocket = InertSocket as unknown as typeof WebSocket;

const { default: NotificationChannels } = await import(
  "./NotificationChannels.tsx"
);
const { default: NotificationRules } = await import("./NotificationRules.tsx");
const { default: NotificationFeed } = await import("./NotificationFeed.tsx");
const { default: NotificationBell } = await import("./NotificationBell.tsx");

afterAll(() => {
  globalThis.fetch = realFetch;
  globalThis.WebSocket = realWebSocket;
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
afterEach(() => {
  emptyChannels = false;
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
});

async function mount(node: preact.ComponentChild): Promise<HTMLElement> {
  host = document.createElement("div");
  document.body.appendChild(host);
  await act(async () => {
    render(node, host as HTMLElement);
  });
  // Let the mount fetches resolve and the islands re-render.
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
  return host;
}

test("channels list the backend's channels", async () => {
  const el = await mount(<NotificationChannels />);
  expect(el.textContent).toContain("ops-slack");
});

test("channels survive an empty list (data: null)", async () => {
  emptyChannels = true;
  const el = await mount(<NotificationChannels />);
  expect(el.textContent).toContain("No channels configured yet.");
});

test("rules list the backend's rules", async () => {
  const el = await mount(<NotificationRules />);
  expect(el.textContent).not.toContain("Failed to load rules");
  expect(el.textContent).toContain("critical-to-ops");
});

test("the feed lists notifications and their total", async () => {
  const el = await mount(<NotificationFeed />);
  expect(el.textContent).toContain("Node worker-1 not ready");
  expect(el.textContent).toContain("of 1");
});

test("the bell shows the unread count", async () => {
  const el = await mount(<NotificationBell />);
  expect(el.textContent).toContain("3");
});
