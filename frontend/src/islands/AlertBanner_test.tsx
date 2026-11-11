import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type { AlertEvent } from "@/lib/k8s-types.ts";
import { disconnectWS } from "@/src/lib/ws.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

// Bun's own WebSocket and fetch, captured before happy-dom replaces them.
const bunWebSocket = globalThis.WebSocket;
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: AlertBanner } = await import("./AlertBanner.tsx");

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

// Another test file may have left the shared ws store holding a socket it
// never closed; start each test from no socket so the banner opens a fake one.
beforeEach(() => {
  disconnectWS();
});

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

afterAll(() => {
  globalThis.WebSocket = bunWebSocket;
  globalThis.fetch = bunFetch;
});

const alert = (fingerprint: string, severity: string) =>
  ({ fingerprint, severity }) as unknown as AlertEvent;

function stubAlerts(alerts: AlertEvent[]) {
  setAccessToken("t");
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ data: alerts }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof fetch;
}

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

function pushAlertEvent(eventType: string, object: unknown) {
  const socket = FakeWebSocket.instances.at(-1);
  if (!socket?.onmessage) throw new Error("the banner opened no socket");
  const onmessage = socket.onmessage;
  onmessage({
    data: JSON.stringify({
      type: "event",
      id: "alertbanner",
      eventType,
      object,
    }),
  });
}

test("shows nothing when there are no alerts", async () => {
  stubAlerts([]);
  const { container } = render(<AlertBanner />);
  await settle();
  expect(container.textContent).toBe("");
});

test("counts alerts with critical and warning chips, and Dismiss hides the banner", async () => {
  stubAlerts([
    alert("a", "critical"),
    alert("b", "warning"),
    alert("c", "warning"),
  ]);
  const { container } = render(<AlertBanner />);
  await settle();
  expect(body().getByText("3 active alerts")).toBeTruthy();
  expect(body().getByText("1 critical")).toBeTruthy();
  expect(body().getByText("2 warning")).toBeTruthy();

  fireEvent.click(body().getByRole("button", { name: "Dismiss" }));
  expect(container.textContent).toBe("");
});

test("uses the singular for one alert and omits chips for other severities", async () => {
  stubAlerts([alert("a", "info")]);
  render(<AlertBanner />);
  await settle();
  expect(body().getByText("1 active alert")).toBeTruthy();
  expect(body().queryByText(/critical|warning/)).toBeNull();
});

test("socket events add an alert once per fingerprint and remove resolved ones", async () => {
  stubAlerts([alert("a", "warning")]);
  render(<AlertBanner />);
  await settle();
  expect(body().getByText("1 active alert")).toBeTruthy();

  await act(async () => pushAlertEvent("ADDED", alert("b", "critical")));
  await act(async () => pushAlertEvent("ADDED", alert("b", "critical")));
  expect(body().getByText("2 active alerts")).toBeTruthy();
  expect(body().getByText("1 critical")).toBeTruthy();

  await act(async () => pushAlertEvent("DELETED", alert("a", "warning")));
  expect(body().getByText("1 active alert")).toBeTruthy();
  expect(body().queryByText(/warning/)).toBeNull();
});
