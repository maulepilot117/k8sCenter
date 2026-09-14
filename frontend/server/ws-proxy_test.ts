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
  opts: {
    requireAuth?: boolean;
    neverAccept?: boolean;
    killAfterOpen?: boolean;
  } = {},
): Promise<StubBackend> {
  return new Promise((resolvePromise) => {
    const receivedAuth: (string | undefined)[] = [];
    let upgradeAttempts = 0;
    const server = createServer();
    const wss = new WebSocketServer({ noServer: true });

    server.on("upgrade", (req, socket, head) => {
      upgradeAttempts++;
      if (opts.neverAccept) return; // simulate a hung handshake

      if (opts.requireAuth && !req.headers.authorization) {
        socket.write("HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n");
        socket.destroy();
        return;
      }
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

/** Raw wire-level upgrade request, for asserting exact rejection status codes
 * and headers without a WebSocket client library getting in the way. */
function rawUpgradeRequest(
  port: number,
  path: string,
  extraHeaders: Record<string, string> = {},
): Promise<{ statusLine: string; raw: string; socket: Socket }> {
  return new Promise((resolvePromise, reject) => {
    const socket = connect(port, "127.0.0.1", () => {
      const headerLines = [
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
      socket.write(headerLines);
    });
    let buf = "";
    socket.on("data", (chunk) => {
      buf += chunk.toString("utf-8");
      if (buf.includes("\r\n\r\n") || buf.includes("\r\n")) {
        resolvePromise({ statusLine: buf.split("\r\n")[0], raw: buf, socket });
      }
    });
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

test("an idle established connection closes once the idle timeout elapses", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port, {
    idleTimeoutMs: 150,
  });
  cleanups.push(() => server.close());

  const client = new WebSocket(`ws://127.0.0.1:${port}/ws/v1/ws/resources`);
  cleanups.push(() => client.close());
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );

  const ev = await new Promise<CloseEvent>((res) => {
    client.addEventListener("close", (e) => res(e));
  });
  expect(ev.code).toBe(1000);
});

// --- exec Bearer header forwarding ---

test("the exec route forwards the client's Authorization header to the backend upgrade", async () => {
  const backend = await startStubBackend({ requireAuth: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/exec/default/mypod/main",
    {
      Authorization: "Bearer test-token-123",
    },
  );
  expect(statusLine).toContain("101");
  // Give the proxy's own upgrade callback a moment to run before asserting.
  await new Promise((res) => setTimeout(res, 50));
  expect(backend.receivedAuth).toContain("Bearer test-token-123");
});

test("a no-header control on the exec route is refused by the backend with 401, proving the header is what carries auth", async () => {
  const backend = await startStubBackend({ requireAuth: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const client = new WebSocket(
    `ws://127.0.0.1:${port}/ws/v1/ws/exec/default/mypod/main`,
  );
  cleanups.push(() => client.close());

  // The client-facing upgrade always completes immediately -- ported from
  // the original Fresh route, which decouples it from the backend
  // connection outcome (the backend socket is only opened once the client
  // side is already open). The proof that the Bearer header is what
  // carries auth is that, without it, the backend answers 401 and never
  // records an Authorization header, and this connection -- unlike the
  // header-bearing one above -- gets torn back down instead of relaying.
  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );
  const closeEvent = await new Promise<CloseEvent>((res) => {
    client.addEventListener("close", (ev) => res(ev));
  });
  expect(closeEvent).toBeTruthy();
  expect(backend.receivedAuth).toHaveLength(0);
});

// --- exec auth via ?access_token= (U13) ---
//
// The browser WebSocket constructor has no parameter for custom request
// headers -- PodTerminal.tsx cannot attach Authorization the way the tests
// above do with a raw socket. This is the fallback it actually uses: the
// token travels as a query parameter that defaultGetAuthHeader converts
// into a real Authorization header on the outbound backend leg.

test("the exec route forwards an access_token query parameter as a Bearer header to the backend upgrade", async () => {
  const backend = await startStubBackend({ requireAuth: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const client = new WebSocket(
    `ws://127.0.0.1:${port}/ws/v1/ws/exec/default/mypod/main?access_token=query-token-456`,
  );
  cleanups.push(() => client.close());

  await new Promise<void>((res) =>
    client.addEventListener("open", () => res()),
  );
  await new Promise((res) => setTimeout(res, 50));
  expect(backend.receivedAuth).toContain("Bearer query-token-456");
});

test("an explicit Authorization header wins over an access_token query parameter when both are present", async () => {
  const backend = await startStubBackend({ requireAuth: true });
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  const { statusLine } = await rawUpgradeRequest(
    port,
    "/ws/v1/ws/exec/default/mypod/main?access_token=should-lose",
    { Authorization: "Bearer header-wins" },
  );
  expect(statusLine).toContain("101");
  await new Promise((res) => setTimeout(res, 50));
  expect(backend.receivedAuth).toContain("Bearer header-wins");
  expect(backend.receivedAuth).not.toContain("Bearer should-lose");
});

test("the access_token query parameter is not forwarded on non-exec routes", async () => {
  const backend = await startStubBackend();
  cleanups.push(backend.close);
  const { server, port } = await startProxy(backend.port);
  cleanups.push(() => server.close());

  await rawUpgradeRequest(
    port,
    "/ws/v1/ws/resources?access_token=should-not-forward",
  );
  await new Promise((res) => setTimeout(res, 50));
  expect(backend.receivedAuth).toEqual([undefined]);
});

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
