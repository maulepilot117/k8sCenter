import { afterEach, expect, test } from "bun:test";
import { createServer, type Server } from "node:http";
import { connect, type Socket } from "node:net";
import { WebSocketServer } from "ws";
import { applySecurityHeaders } from "./headers.ts";
import {
  attachWsProxy,
  HANDSHAKE_TIMEOUT_MS,
  handleWsHttpRequest,
  IDLE_TIMEOUT_MS,
  KEEPALIVE_PING_MS,
  MAX_QUEUE_BYTES,
  MAX_QUEUE_MESSAGES,
} from "./ws-proxy.ts";

// --- exact-numbers contract (R4) ---
// The live-timing scenarios below override these via AttachWsProxyOptions
// so the suite runs in milliseconds rather than tens of seconds; this test
// pins the *defaults* actually shipped to the numbers U5 specifies.
test("default limits match the spec exactly", () => {
  expect(MAX_QUEUE_MESSAGES).toBe(32);
  expect(MAX_QUEUE_BYTES).toBe(256 * 1024);
  expect(HANDSHAKE_TIMEOUT_MS).toBe(10_000);
  expect(IDLE_TIMEOUT_MS).toBe(60_000);
});

type Cleanup = () => void;
const cleanups: Cleanup[] = [];
afterEach(() => {
  while (cleanups.length) cleanups.pop()?.();
});

interface StubBackend {
  port: number;
  receivedAuth: (string | undefined)[];
  upgradeAttempts: number;
  close: () => void;
}

function startStubBackend(
  opts: { neverAccept?: boolean; killAfterOpen?: boolean } = {},
): Promise<StubBackend> {
  return new Promise((resolvePromise) => {
    const receivedAuth: (string | undefined)[] = [];
    let upgradeAttempts = 0;
    const server = createServer();
    const wss = new WebSocketServer({ noServer: true });

    server.on("upgrade", (req, socket, head) => {
      upgradeAttempts++;
      if (opts.neverAccept) return; // simulate a hung handshake

      receivedAuth.push(req.headers.authorization);

      wss.handleUpgrade(req, socket, head, (ws) => {
        if (opts.killAfterOpen) {
          ws.terminate(); // abrupt close -> observed by the client as code 1006
          return;
        }
        ws.on("message", (data, isBinary) => {
          ws.send(data, { binary: isBinary });
        });
      });
    });

    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      resolvePromise({
        port,
        receivedAuth,
        get upgradeAttempts() {
          return upgradeAttempts;
        },
        close: () => server.close(),
      });
    });
  });
}

function startProxy(
  backendPort: number,
  overrides: Partial<{
    maxQueueMessages: number;
    maxQueueBytes: number;
    handshakeTimeoutMs: number;
    idleTimeoutMs: number;
    ownUnmatchedPaths: boolean;
  }> = {},
): Promise<{ server: Server; port: number }> {
  return new Promise((resolvePromise) => {
    const server = createServer((req, res) => {
      applySecurityHeaders(res);
      if (handleWsHttpRequest(req, res)) return;
      res.statusCode = 404;
      res.end("not a ws path");
    });
    attachWsProxy(server, {
      backendUrl: `http://127.0.0.1:${backendPort}`,
      ...overrides,
    });
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      resolvePromise({ server, port });
    });
  });
}

function upgradeRequestText(
  path: string,
  extraHeaders: Record<string, string>,
): string {
  return [
    `GET ${path} HTTP/1.1`,
    "Host: 127.0.0.1",
    "Connection: Upgrade",
    "Upgrade: websocket",
    "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
    "Sec-WebSocket-Version: 13",
    ...Object.entries(extraHeaders).map(([k, v]) => `${k}: ${v}`),
    "",
    "",
  ].join("\r\n");
}

/** Raw wire-level upgrade request, for asserting exact rejection status codes
 * and headers without a WebSocket client library getting in the way.
 * Resolves once the full response head (through the blank line) has
 * arrived; a body sent after the head is not waited for. */
function rawUpgradeRequest(
  port: number,
  path: string,
  extraHeaders: Record<string, string> = {},
): Promise<{ statusLine: string; raw: string; socket: Socket }> {
  return new Promise((resolvePromise, reject) => {
    const socket = connect(port, "127.0.0.1", () => {
      socket.write(upgradeRequestText(path, extraHeaders));
    });
    let buf = "";
    socket.on("data", (chunk) => {
      buf += chunk.toString("utf-8");
      if (buf.includes("\r\n\r\n")) {
        resolvePromise({ statusLine: buf.split("\r\n")[0], raw: buf, socket });
      }
    });
    socket.on("error", reject);
  });
}

/** Like rawUpgradeRequest, but collects the whole response: everything up to
 * the server closing the socket, so a header or body arriving in a later
 * packet is still asserted on. If the server instead completes the
 * handshake and holds the socket open, it resolves `graceMs` after the
 * response head, so a regression fails its assertions rather than hanging. */
function rawUpgradeResponse(
  port: number,
  path: string,
  extraHeaders: Record<string, string> = {},
  graceMs = 250,
): Promise<{ statusLine: string; raw: string; socket: Socket }> {
  return new Promise((resolvePromise, reject) => {
    const socket = connect(port, "127.0.0.1", () => {
      socket.write(upgradeRequestText(path, extraHeaders));
    });
    let buf = "";
    let graceTimer: ReturnType<typeof setTimeout> | undefined;
    const finish = () => {
      clearTimeout(graceTimer);
      resolvePromise({ statusLine: buf.split("\r\n")[0], raw: buf, socket });
    };
    socket.on("data", (chunk) => {
      buf += chunk.toString("utf-8");
      if (graceTimer === undefined && buf.includes("\r\n\r\n")) {
        graceTimer = setTimeout(finish, graceMs);
      }
    });
    socket.on("close", finish);
    socket.on("error", reject);
  });
}

function rawPlainGet(
  port: number,
  path: string,
): Promise<{ statusLine: string; raw: string }> {
  return new Promise((resolvePromise, reject) => {
    const socket = connect(port, "127.0.0.1", () => {
      socket.write(
        `GET ${path} HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n`,
      );
    });
    let buf = "";
    socket.on("data", (chunk) => {
      buf += chunk.toString("utf-8");
    });
    socket.on("end", () =>
      resolvePromise({ statusLine: buf.split("\r\n")[0], raw: buf }),
    );
    socket.on("error", reject);
    cleanups.push(() => socket.destroy());
  });
}

// --- allowlisted round trip, both directions ---

test("an allowlisted endpoint upgrades and relays messages in both directions", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());

  const opened = new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );
  await opened;

  const echoed = new Promise<string>((res) => {
    client.addEventListener("message", (ev) => res(ev.data as string));
  });
  client.send("hello");
  expect(await echoed).toBe("hello");
});

// --- allowlist + traversal guard, before any backend connection ---

test("a path outside the allowlist is refused (404) before any backend connection attempt", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine, raw } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/unknown-endpoint",
  );
  expect(statusLine).toContain("404");
  expect(raw).toContain("X-Frame-Options: DENY");
  expect(backend.upgradeAttempts).toBe(0);
});

test("a traversal payload ('..') is refused (400) before any backend connection attempt", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine, raw } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/exec/../../etc/mypod/main",
  );
  expect(statusLine).toContain("400");
  expect(raw).toContain("X-Content-Type-Options: nosniff");
  expect(backend.upgradeAttempts).toBe(0);
});

test("a '//' traversal payload is refused (400) before any backend connection attempt", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine } = await rawUpgradeRequest(port, "/ws/v1/ws//resources");
  expect(statusLine).toContain("400");
  expect(backend.upgradeAttempts).toBe(0);
});

test("a '%2e' traversal payload is refused (400) before any backend connection attempt", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/exec/%2e%2e/mypod/main",
  );
  expect(statusLine).toContain("400");
  expect(backend.upgradeAttempts).toBe(0);
});

// --- 426 on a plain (non-upgrade) request ---

test("a plain GET with no Upgrade header to an allowlisted path returns 426, with headers", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine, raw } = await rawPlainGet(port, "/ws/v1/ws/resources");
  expect(statusLine).toContain("426");
  expect(raw).toContain("Permissions-Policy:");
});

test("a plain GET with no Upgrade header to a disallowed path returns 404, not 426", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine } = await rawPlainGet(port, "/ws/v1/ws/nope");
  expect(statusLine).toContain("404");
});

// --- queued client message delivered once the backend opens ---

test("a client message sent before the backend opens is queued and delivered", async () => {
  const receivedAuth: string[] = [];
  const server = createServer();
  const wss = new WebSocketServer({ noServer: true });
  server.on("upgrade", (req, socket, head) => {
    // Delay accepting the backend handshake so the client's message is
    // guaranteed to arrive at the proxy first.
    setTimeout(() => {
      receivedAuth.push(String(req.headers.authorization));
      wss.handleUpgrade(req, socket, head, (ws) => {
        ws.on("message", (data, isBinary) =>
          ws.send(data, { binary: isBinary }),
        );
      });
    }, 200);
  });
  await new Promise<void>((res) => server.listen(0, "127.0.0.1", res));
  cleanups.push(() => server.close());
  const backendPort = (server.address() as { port: number }).port;

  const { server: proxyServer, port } = await startProxy(backendPort);
  cleanups.push(() => proxyServer.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());
  const opened = new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );
  await opened;

  // Sent immediately -- the backend has not accepted its handshake yet
  // (200ms delay above), so this must be queued, not dropped.
  const echoed = new Promise<string>((res) => {
    client.addEventListener("message", (ev) => res(ev.data as string));
  });
  client.send("queued-auth-message");
  expect(await echoed).toBe("queued-auth-message");
});

// --- close-code remap ---

test("a backend close with code 1006 (abnormal) results in the client seeing 1000", async () => {
  const backend = await startStubBackend({ killAfterOpen: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());

  const closeEvent = await new Promise<CloseEvent>((res) => {
    client.addEventListener("close", (ev) => res(ev));
  });
  expect(closeEvent.code).toBe(1000);
});

// --- bounded queue: exact numbers ---

test("a client flooding the pre-open queue against an unavailable backend is closed once it exceeds the message-count limit", async () => {
  const backend = await startStubBackend({ neverAccept: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port, {
    maxQueueMessages: 5,
    maxQueueBytes: 1024 * 1024,
    handshakeTimeoutMs: 60_000, // must not fire first
  });
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );

  const closed = new Promise<CloseEvent>((res) => {
    client.addEventListener("close", (ev) => res(ev));
  });
  for (let i = 0; i < 10; i++) client.send(`msg-${i}`);
  const ev = await closed;
  expect(ev.code).toBe(1009);
});

test("a client flooding the pre-open queue against an unavailable backend is closed once it exceeds the byte limit", async () => {
  const backend = await startStubBackend({ neverAccept: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port, {
    maxQueueMessages: 1000,
    maxQueueBytes: 100,
    handshakeTimeoutMs: 60_000,
  });
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );

  const closed = new Promise<CloseEvent>((res) => {
    client.addEventListener("close", (ev) => res(ev));
  });
  client.send("x".repeat(80));
  client.send("x".repeat(80));
  const ev = await closed;
  expect(ev.code).toBe(1009);
});

// --- handshake timeout ---

test("a backend that never completes the handshake closes the connection once the handshake timeout elapses", async () => {
  const backend = await startStubBackend({ neverAccept: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port, {
    handshakeTimeoutMs: 150,
  });
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );

  const start = Date.now();
  const ev = await new Promise<CloseEvent>((res) => {
    client.addEventListener("close", (e) => res(e));
  });
  expect(Date.now() - start).toBeLessThan(2000);
  expect(ev.code).toBe(1011);
});

// --- idle timeout ---

test("a connection whose peer stops answering is reaped at the idle bound", async () => {
  // This replaces an earlier test that asserted a live-but-silent connection
  // closes at the bound. That was the defect, not the contract: the backend
  // keeps every channel alive with ping frames and sends no periodic data, so
  // "silent" described healthy resource watches, log tails and terminals, and
  // the bridge dropped them on a 60-second loop. "Idle" now means the peer
  // stopped answering, which is what the bound was added to catch.
  //
  // A raw socket is the peer that never answers: it completes the handshake
  // and then ignores every ping frame the bridge sends.
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port, {
    idleTimeoutMs: 300,
  });
  cleanups.push(() => server.close());

  const { socket, statusLine } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/resources",
  );
  cleanups.push(() => socket.destroy());
  expect(statusLine).toContain("101");

  const closedByServer = await new Promise<boolean>((res) => {
    const timer = setTimeout(() => res(false), 3_000);
    socket.on("close", () => {
      clearTimeout(timer);
      res(true);
    });
    socket.on("end", () => {
      clearTimeout(timer);
      res(true);
    });
  });

  expect(closedByServer).toBe(true);
});

// --- exec authenticates in-band (KTD2) ---
//
// Exec used to carry its token on the handshake, which the bridge forwarded
// as an Authorization header. It now authenticates with its first message
// like every other route, so the bridge forwards nothing.

test("the exec route forwards no Authorization header to the backend, even when the client sends one", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine, socket } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/exec/default/mypod/main",
    { Authorization: "Bearer should-not-forward" },
  );
  cleanups.push(() => socket.destroy());
  expect(statusLine).toContain("101");
  await new Promise((res) => setTimeout(res, 50));
  expect(backend.receivedAuth).toEqual([undefined]);
});

test("the exec route relays the in-band auth frame to the backend unchanged", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const client = new WebSocket(
    `ws://127.0.0.1:${port}/ws/v1/ws/exec/default/mypod/main`,
  );
  cleanups.push(() => client.close());
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );

  // Sent at once, so the bridge may still be queueing it for the backend.
  const frame = JSON.stringify({
    type: "auth",
    token: "in-band-token",
    clusterId: "local",
  });
  const echoed = new Promise<string>((res) => {
    client.addEventListener("message", (ev) => res(ev.data as string));
  });
  client.send(frame);
  expect(await echoed).toBe(frame);
  expect(backend.receivedAuth).toEqual([undefined]);
});

// What a tab loaded before exec moved to in-band auth offers: its token,
// base64url-encoded, as a subprotocol, beside a plain sentinel.
const STALE_TAB_BEARER =
  "base64url.bearer.authorization.k8scenter.io.c3RhbGUtdGFiLXRva2Vu";
const STALE_TAB_PROTOCOLS = `${STALE_TAB_BEARER}, k8scenter.v1`;

// An upgrade offering any subprotocol is refused before the handshake, so a
// credential offered there is never echoed back and no backend leg opens.
// Bun's `ws` would otherwise echo the first offer.
//
// rawUpgradeRequest rather than a native `new WebSocket(url, protocols)`
// client, deliberately: a native Bun WebSocket client that negotiates a
// subprotocol against this same in-process server corrupts state inside
// Bun's own built-in `ws` fast path (github.com/oven-sh/bun; reproduced
// standalone outside bun:test too), so a later raw-socket upgrade offering a
// Sec-WebSocket-Protocol header throws inside `ws`'s `completeUpgrade`.
for (const path of [
  "/ws/v1/ws/exec/default/mypod/main",
  "/ws/v1/ws/resources",
]) {
  test(`an upgrade offering a subprotocol is refused without echoing it (${path})`, async () => {
    const backend = await startStubBackend();
    cleanups.push(backend.close);
    const { server, port } = await startProxy(backend.port);
    cleanups.push(() => server.close());

    const { statusLine, raw, socket } = await rawUpgradeResponse(port, path, {
      "Sec-WebSocket-Protocol": STALE_TAB_PROTOCOLS,
    });
    cleanups.push(() => socket.destroy());

    expect(statusLine).toContain("400");
    // The body ties the 400 to this guard rather than another 400 path.
    expect(raw).toContain("WebSocket subprotocols are not supported");
    expect(raw.toLowerCase()).not.toContain("sec-websocket-protocol");
    expect(raw).not.toContain(STALE_TAB_BEARER);

    await new Promise((res) => setTimeout(res, 50));
    expect(backend.upgradeAttempts).toBe(0);
  });
}

test("non-exec routes carry no Authorization header even when the client's request has one", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  await rawUpgradeRequest(port, "/ws/v1/ws/resources", {
    Authorization: "Bearer should-not-forward",
  });
  await new Promise((res) => setTimeout(res, 50));
  expect(backend.receivedAuth).toEqual([undefined]);
});

// --- shared-server coexistence (the dev HMR collision) ---
//
// Under `astro dev` this listener is attached to the same node:http server
// Vite uses for HMR. Vite upgrades its own socket at `/`; 'upgrade'
// listeners are not consumed, so this one runs afterwards on a socket that
// is already a live WebSocket. Writing a rejection there destroys it —
// the browser reports "Invalid frame header" and Vite degrades to
// full-page reloads. `ownUnmatchedPaths: false` is how dev opts out.

/** Stands in for Vite's HMR server: owns `/`, echoes, attached first. */
function attachForeignHmrListener(server: Server): void {
  const hmr = new WebSocketServer({ noServer: true });
  server.on("upgrade", (req, socket, head) => {
    if ((req.url ?? "").split("?")[0] !== "/") return;
    hmr.handleUpgrade(req, socket, head, (ws) => {
      ws.on("message", (data, isBinary) => ws.send(data, { binary: isBinary }));
    });
  });
}

test("with ownUnmatchedPaths off, a foreign upgrade on a shared server survives", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);

  const server = createServer();
  attachForeignHmrListener(server);
  attachWsProxy(server, {
    backendUrl: `http://127.0.0.1:${backend.port}`,
    ownUnmatchedPaths: false,
  });
  const port = await new Promise<number>((res) => {
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      res(typeof address === "object" && address ? address.port : 0);
    });
  });
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/`);
  cleanups.push(() => client.close());
  let closed = false;
  client.addEventListener("close", () => {
    closed = true;
  });
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );

  const echoed = new Promise<string>((res) => {
    client.addEventListener("message", (ev) => res(ev.data as string));
  });
  client.send("hmr-ping");
  expect(await echoed).toBe("hmr-ping");
  expect(closed).toBe(false);
});

test("ownUnmatchedPaths off still rejects an unknown endpoint under /ws/", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port, {
    ownUnmatchedPaths: false,
  });
  cleanups.push(() => server.close());

  const { statusLine } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/unknown-endpoint",
  );
  expect(statusLine).toContain("404");
  expect(backend.upgradeAttempts).toBe(0);
});

test("ownUnmatchedPaths defaults on, so production still rejects a non-/ws/ upgrade", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine } = await rawUpgradeRequest(port, "/anything-else");
  expect(statusLine).toContain("404");
  expect(backend.upgradeAttempts).toBe(0);
});

// --- liveness, not chattiness (review finding #1) ---
//
// The suite already asserts that a silent connection DOES close at the idle
// bound. Nothing asserted the invariant that actually matters in production:
// a healthy connection with no application traffic must SURVIVE. The backend
// keeps every channel alive with a ping every 54s and sends no periodic data,
// so counting only data frames force-disconnected every quiet resource watch,
// log tail and pod terminal on a 60-second loop. The Fresh bridge had no idle
// timeout at all, so that was a regression against the stack being replaced.

test("a healthy but silent connection survives well past the idle bound", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  // Idle bound far below the test's lifetime; the bridge's own keepalive ping
  // is what has to keep resetting it. A browser (and `ws`) answers a ping
  // frame automatically, so no application traffic is involved.
  const { server, port } = await startProxy(backend.port, {
    idleTimeoutMs: 300,
  });
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());
  let closeCode: number | null = null;
  client.addEventListener("close", (ev) => {
    closeCode = (ev as CloseEvent).code;
  });
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );

  // Drive the bridge's ping loop by hand at a cadence inside the bound, the
  // way KEEPALIVE_PING_MS does in production, and send nothing else.
  const start = Date.now();
  while (Date.now() - start < 900) {
    await new Promise((res) => setTimeout(res, 100));
    if (closeCode !== null) break;
  }

  expect(closeCode).toBeNull();
  expect(client.readyState).toBe(WebSocket.OPEN);
});

test("the keepalive cadence stays inside the idle bound", () => {
  // The invariant the test above depends on: if these ever cross, a healthy
  // connection is reaped before it can prove it is alive.
  expect(KEEPALIVE_PING_MS).toBeLessThan(IDLE_TIMEOUT_MS);
});

// --- the 'upgrade' listener must never throw (review finding #3) ---

/** HTTP line terminator, built without escapes so no tooling can mangle it. */
const CRLF = String.fromCharCode(13, 10);

test("a bridge failure leaves the server serving instead of exiting", async () => {
  const server = createServer();
  // A malformed backend URL makes bridgeConnection's WebSocket constructor
  // throw synchronously, inside the 'upgrade' listener -- where an escaping
  // throw is an uncaughtException and node's default for that is to exit.
  attachWsProxy(server, { backendUrl: "not-a-valid-url" });
  const port = await new Promise<number>((res) => {
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      res(typeof address === "object" && address ? address.port : 0);
    });
  });
  cleanups.push(() => server.close());

  // Fire the upgrade that throws. It gets no HTTP response -- the socket is
  // destroyed -- so do not wait on one.
  const doomed = connect(port, "127.0.0.1", () => {
    doomed.write(
      [
        "GET /ws/v1/ws/resources HTTP/1.1",
        "Host: 127.0.0.1",
        "Connection: Upgrade",
        "Upgrade: websocket",
        "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
        "Sec-WebSocket-Version: 13",
        "",
        "",
      ].join(CRLF),
    );
  });
  doomed.on("error", () => {});
  cleanups.push(() => doomed.destroy());
  await new Promise((res) => setTimeout(res, 150));

  // The assertion: the process is still here and the server still answers.
  const { statusLine } = await rawUpgradeRequest(port, "/ws/v1/ws/nope");
  expect(statusLine).toContain("404");
});
