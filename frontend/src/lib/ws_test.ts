import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { refreshSession, setAccessToken } from "@/lib/api.ts";

/**
 * A socket the module has already dropped (disconnect, or a reconnect that
 * replaced it) can still fire open or close afterwards. Those late events must
 * not touch the module's current socket or crash.
 */

// Other test files replace this module with mock.module, which Bun applies to
// the whole test process. The query suffix loads a separate, real instance.
const REAL_WS = "./ws.ts?real";
const { disconnectWS, subscribe, wsStatus } = (await import(
  REAL_WS
)) as typeof import("./ws.ts");

class FakeSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeSocket[] = [];

  readyState = FakeSocket.CONNECTING;
  sent: Array<Record<string, unknown>> = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(public url: string) {
    FakeSocket.instances.push(this);
  }

  send(data: string) {
    if (this.readyState !== FakeSocket.OPEN) {
      throw new Error("send on a socket that is not open");
    }
    this.sent.push(JSON.parse(data));
  }

  close() {
    this.closed = true;
    this.readyState = FakeSocket.CLOSED;
  }
}

let originalSocket: typeof globalThis.WebSocket;
let originalLocation: PropertyDescriptor | undefined;

beforeAll(() => {
  originalSocket = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket as unknown as typeof globalThis.WebSocket;
  originalLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
  Object.defineProperty(globalThis, "location", {
    configurable: true,
    value: { protocol: "http:", host: "localhost" },
  });
});

afterAll(() => {
  globalThis.WebSocket = originalSocket;
  if (originalLocation) {
    Object.defineProperty(globalThis, "location", originalLocation);
  } else {
    delete (globalThis as { location?: unknown }).location;
  }
});

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
  disconnectWS();
  FakeSocket.instances = [];
  setAccessToken(null);
});

const noop = () => {};

test("a socket opened after disconnect does not crash or send", () => {
  setAccessToken("t");
  subscribe("a", "pods", "default", noop);
  const [first] = FakeSocket.instances;

  disconnectWS();
  first.readyState = FakeSocket.OPEN;
  expect(() => first.onopen?.()).not.toThrow();
  expect(first.sent).toEqual([]);
});

test("late events from a replaced socket leave the current one alone", () => {
  setAccessToken("t");
  subscribe("a", "pods", "default", noop);
  const [first] = FakeSocket.instances;
  disconnectWS();

  subscribe("b", "pods", "default", noop);
  expect(FakeSocket.instances).toHaveLength(2);
  const second = FakeSocket.instances[1];

  // The old socket's open must not authenticate on the new, still-connecting one.
  first.readyState = FakeSocket.OPEN;
  first.onopen?.();
  expect(second.sent).toEqual([]);

  // The old socket's close must not forget the new one: a further subscribe
  // reuses it instead of opening a third.
  first.onclose?.({ code: 1000 });
  subscribe("c", "pods", "default", noop);
  expect(FakeSocket.instances).toHaveLength(2);
  expect(wsStatus.value).toBe("connecting");

  second.readyState = FakeSocket.OPEN;
  second.onopen?.();
  expect(second.sent).toEqual([{ type: "auth", token: "t" }]);
});

/** Answers /auth/refresh once `gate` is released, with a fresh token. */
function gatedRefresh(): { release: () => void } {
  let release = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  globalThis.fetch = (async () => {
    await gate;
    return new Response(
      JSON.stringify({ data: { accessToken: "fresh-token" } }),
      {
        status: 200,
        headers: { "Content-Type": "application/json" },
      },
    );
  }) as unknown as typeof globalThis.fetch;
  return { release };
}

test("a socket opened during the startup refresh authenticates once it lands", async () => {
  setAccessToken(null);
  const { release } = gatedRefresh();
  const boot = refreshSession();

  subscribe("a", "pods", "default", noop);
  const [sock] = FakeSocket.instances;
  sock.readyState = FakeSocket.OPEN;
  sock.onopen?.();
  await Promise.resolve();
  expect(sock.sent).toEqual([]);

  release();
  await boot;
  await new Promise((r) => setTimeout(r, 0));
  expect(sock.sent).toEqual([{ type: "auth", token: "fresh-token" }]);
});
