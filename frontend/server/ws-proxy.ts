import type { IncomingMessage, ServerResponse } from "node:http";
import type { Socket } from "node:net";
import { type WebSocket as ServerClientSocket, WebSocketServer } from "ws";
import {
  findBearerInSubprotocols,
  selectWsSubprotocol,
} from "@/src/lib/ws-exec-auth.ts";
import { rawHeaderLines } from "./headers.ts";
import { checkWsPath, isExecPath, remapCloseCode } from "./ws-allowlist.ts";

/**
 * The native WebSocket bridge to the Go backend (KD5, KTD2). Ported from
 * frontend/routes/ws/[...path].ts's allowlist + relay + close-code
 * remapping (behaviour port, not a redesign — R6), with two additions U1
 * found necessary rather than assumed:
 *
 *  - A bounded queue, handshake timeout, and idle timeout. The original
 *    queues client messages (including the auth first-message) until the
 *    backend socket opens, with no limit — a slow or dead backend lets a
 *    client hold an allowlisted socket open and push messages until memory
 *    runs out.
 *  - The exec route's outbound upgrade carries the client's Bearer
 *    credential. See "Pre-existing defect found by U1" in the migration
 *    plan: middleware.Auth gates /api/v1/ws/exec/... at upgrade time, not
 *    in-band, and the original bridge sends no headers on any route.
 */

export const MAX_QUEUE_MESSAGES = 32;
export const MAX_QUEUE_BYTES = 256 * 1024;
export const HANDSHAKE_TIMEOUT_MS = 10_000;
export const IDLE_TIMEOUT_MS = 60_000;

const OPEN = 1;
const CONNECTING = 0;

export function getBackendUrl(): string {
  return process.env.BACKEND_URL ?? "http://localhost:8080";
}

function writeRawRejection(
  socket: Socket,
  status: 400 | 404 | 426,
  message: string,
): void {
  const statusText =
    status === 400
      ? "Bad Request"
      : status === 404
        ? "Not Found"
        : "Upgrade Required";
  const head = [
    `HTTP/1.1 ${status} ${statusText}`,
    ...rawHeaderLines(),
    "Content-Type: text/plain; charset=utf-8",
    `Content-Length: ${Buffer.byteLength(message)}`,
    "Connection: close",
    "",
    "",
  ].join("\r\n");
  try {
    socket.write(head + message);
  } catch {
    /* the socket may already be gone */
  } finally {
    socket.destroy();
  }
}

/**
 * Handles a plain (non-upgrade) HTTP request under /ws/*. Node's http
 * server only fires the 'upgrade' event when the client actually sent
 * Connection/Upgrade handshake headers, so any /ws/* request that reaches
 * the ordinary request path did NOT ask to upgrade — which is exactly the
 * 426 case, once the path itself clears the allowlist. Returns true if it
 * handled the request (caller should stop), false if the path isn't under
 * /ws/ at all.
 */
export function handleWsHttpRequest(
  req: IncomingMessage,
  res: ServerResponse,
): boolean {
  const url = req.url ?? "/";
  if (url !== "/ws" && !url.startsWith("/ws/")) {
    return false;
  }

  const check = checkWsPath(url);
  if (!check.ok) {
    res.statusCode = check.status;
    res.end(check.status === 400 ? "Invalid WS path" : "Unknown WS endpoint");
    return true;
  }

  res.statusCode = 426;
  res.end("Expected WebSocket upgrade");
  return true;
}

/**
 * The only thing attachWsProxy needs from "the server". Deliberately
 * structural (not node:http's concrete Server type) so it also accepts
 * Vite's ViteDevServer#httpServer, whose type is a union including
 * Http2SecureServer — dev-plugin.ts passes that union through as-is rather
 * than narrowing it, and this is the real, minimal contract either side
 * satisfies.
 */
export interface UpgradeCapableServer {
  on(
    event: "upgrade",
    listener: (req: IncomingMessage, socket: Socket, head: Buffer) => void,
  ): unknown;
}

export interface AttachWsProxyOptions {
  /** Overridable for tests; defaults to BACKEND_URL / http://localhost:8080. */
  backendUrl?: string;
  /** Overridable for tests; defaults to the inbound request's Authorization header. */
  getAuthHeader?: (req: IncomingMessage) => string | undefined;
  maxQueueMessages?: number;
  maxQueueBytes?: number;
  handshakeTimeoutMs?: number;
  idleTimeoutMs?: number;
  /**
   * Whether this listener may reject upgrades for paths outside `/ws/`.
   *
   * True in production, where prod.ts owns the server outright and an
   * unknown upgrade path should get a rejection rather than hang. False
   * under `astro dev`, where Vite owns the same httpServer for its HMR
   * socket at `/`: Vite completes that upgrade first, and this listener
   * still runs afterward. Rejecting there writes a raw HTTP response onto
   * an already-upgraded socket and destroys it, which kills HMR and makes
   * Vite full-page-reload in a loop. The bridge owns `/ws/*` and nothing
   * else, so off this path it must leave the socket alone.
   */
  ownUnmatchedPaths?: boolean;
}

/**
 * Attaches the WS bridge to any node:http-compatible server's 'upgrade'
 * event — the real production server (prod.ts) and Vite's dev httpServer
 * (dev-plugin.ts) both call this, so dev and prod run the identical
 * dispatch logic (R18).
 */
export function attachWsProxy(
  httpServer: UpgradeCapableServer,
  options: AttachWsProxyOptions = {},
): void {
  const ownUnmatchedPaths = options.ownUnmatchedPaths ?? true;
  // handleProtocols is what makes the echo safe: `ws` defaults to echoing
  // back whichever subprotocol the client listed first, which for the exec
  // route would be the credential-bearing one exactly as often as not. This
  // pins the choice to the sentinel, never the token (see ws-exec-auth.ts).
  const wss = new WebSocketServer({
    noServer: true,
    handleProtocols: selectWsSubprotocol,
  });

  httpServer.on(
    "upgrade",
    (req: IncomingMessage, socket: Socket, head: Buffer) => {
      const rawUrl = req.url ?? "";
      const check = checkWsPath(rawUrl);
      if (!check.ok) {
        // A path outside `/ws/` belongs to whoever else is listening on this
        // server — under `astro dev` that is Vite's HMR socket, which has
        // already completed its own upgrade by the time this listener runs.
        // Writing a rejection onto that socket destroys a live connection.
        if (check.reason === "outside" && !ownUnmatchedPaths) return;
        writeRawRejection(
          socket,
          check.status,
          check.status === 400 ? "Invalid WS path" : "Unknown WS endpoint",
        );
        return;
      }

      if ((req.headers.upgrade ?? "").toLowerCase() !== "websocket") {
        writeRawRejection(socket, 426, "Expected WebSocket upgrade");
        return;
      }

      const authHeader = isExecPath(check.path)
        ? (options.getAuthHeader ?? defaultGetAuthHeader)(req)
        : undefined;

      wss.handleUpgrade(req, socket, head, (clientWs) => {
        bridgeConnection(clientWs, check.path, {
          backendUrl: options.backendUrl ?? getBackendUrl(),
          authHeader,
          maxQueueMessages: options.maxQueueMessages ?? MAX_QUEUE_MESSAGES,
          maxQueueBytes: options.maxQueueBytes ?? MAX_QUEUE_BYTES,
          handshakeTimeoutMs:
            options.handshakeTimeoutMs ?? HANDSHAKE_TIMEOUT_MS,
          idleTimeoutMs: options.idleTimeoutMs ?? IDLE_TIMEOUT_MS,
        });
      });
    },
  );
}

/**
 * U13/U-subproto: the exec route's credential can arrive two ways.
 *
 * `req.headers.authorization` covers a non-browser client (this file's own
 * tests, a future CLI) that can set arbitrary request headers on the
 * handshake. It cannot cover the actual product: the WHATWG `WebSocket`
 * constructor browsers implement has no parameter for custom request
 * headers at all -- `PodTerminal.tsx` cannot attach `Authorization` to its
 * own handshake no matter how the bridge below forwards it. Verified
 * empirically against this proxy and the live backend (U13): a handshake
 * with the header opens and streams; the identical handshake without one
 * opens on the client side (bridgeConnection decouples that from the
 * backend outcome) and is torn down moments later once the backend's
 * middleware.Auth 401s the backend leg -- exactly PodTerminal's situation
 * before this fix.
 *
 * What the browser *can* set is `Sec-WebSocket-Protocol`, via the second
 * argument to `new WebSocket(url, protocols)`. U-subproto replaces the
 * `access_token` query-parameter fallback (which put the credential in a
 * URL an ingress logs by default) with that: PodTerminal offers a
 * credential-bearing subprotocol alongside a plain sentinel, and
 * `findBearerInSubprotocols` (ws-exec-auth.ts) recovers the token from it.
 * The header wins when both are present so the existing header-based tests
 * keep exercising that path unchanged.
 */
function defaultGetAuthHeader(req: IncomingMessage): string | undefined {
  if (req.headers.authorization) return req.headers.authorization;

  const token = findBearerInSubprotocols(req.headers["sec-websocket-protocol"]);
  return token ? `Bearer ${token}` : undefined;
}

/**
 * TypeScript's `ArrayBufferView`/typed-array types are generic over their
 * backing buffer, defaulting to the broader `ArrayBufferLike` (which
 * includes `SharedArrayBuffer`), while the global `WebSocket#send()`'s
 * `BufferSource` union pins that generic to plain `ArrayBuffer`. A Buffer
 * from `Buffer.concat`/`Buffer.from`/`Buffer.isBuffer` is always backed by
 * a real ArrayBuffer at runtime, so this narrows the static type to match
 * without copying the data.
 */
function asSendable(data: string | Buffer): string | Uint8Array<ArrayBuffer> {
  return typeof data === "string"
    ? data
    : (data as unknown as Uint8Array<ArrayBuffer>);
}

interface BridgeOptions {
  backendUrl: string;
  authHeader: string | undefined;
  maxQueueMessages: number;
  maxQueueBytes: number;
  handshakeTimeoutMs: number;
  idleTimeoutMs: number;
}

function bridgeConnection(
  clientWs: ServerClientSocket,
  path: string,
  opts: BridgeOptions,
): void {
  const target = new URL(opts.backendUrl);
  const wsProto = target.protocol === "https:" ? "wss:" : "ws:";
  const backendUrl = `${wsProto}//${target.host}/api/${path}`;

  // Bun's (and Deno's) WebSocket constructor accepts a non-standard
  // `headers` option in place of the spec's subprotocols argument (U1
  // proved this against the real backend: 101 with the header, 401
  // without). Every route except exec authenticates in-band via the first
  // queued message; exec needs the credential on the upgrade itself.
  const backendSocket = opts.authHeader
    ? new WebSocket(backendUrl, {
        headers: { Authorization: opts.authHeader },
      } as unknown as string[])
    : new WebSocket(backendUrl);
  backendSocket.binaryType = "arraybuffer";

  let queue: Array<string | Buffer> = [];
  let queueBytes = 0;
  let closed = false;
  let idleTimer: ReturnType<typeof setTimeout>;
  let handshakeTimer: ReturnType<typeof setTimeout> | null = null;

  const clearTimers = () => {
    clearTimeout(idleTimer);
    if (handshakeTimer) {
      clearTimeout(handshakeTimer);
      handshakeTimer = null;
    }
  };

  /** Server-initiated teardown: idle timeout, handshake timeout, queue overflow. */
  const closeBoth = (code: number, reason: string) => {
    if (closed) return;
    closed = true;
    clearTimers();
    try {
      if (clientWs.readyState === OPEN) {
        clientWs.close(remapCloseCode(code), reason);
      }
    } catch {
      /* ignore */
    }
    try {
      if (
        backendSocket.readyState === OPEN ||
        backendSocket.readyState === CONNECTING
      ) {
        backendSocket.close();
      }
    } catch {
      /* ignore */
    }
  };

  const resetIdle = () => {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(
      () => closeBoth(1000, "idle timeout"),
      opts.idleTimeoutMs,
    );
  };
  resetIdle();

  handshakeTimer = setTimeout(() => {
    if (backendSocket.readyState !== OPEN) {
      closeBoth(1011, "backend handshake timeout");
    }
  }, opts.handshakeTimeoutMs);

  // Client -> backend (or queue, until the backend socket opens). This is
  // what protects the auth token sent as the first message on the other
  // four routes.
  clientWs.on("message", (data: Buffer | Buffer[], isBinary: boolean) => {
    if (closed) return;
    resetIdle();
    const buf = Buffer.isBuffer(data) ? data : Buffer.concat(data);
    const payload: string | Buffer = isBinary ? buf : buf.toString("utf-8");

    if (backendSocket.readyState === OPEN) {
      backendSocket.send(asSendable(payload));
      return;
    }
    if (backendSocket.readyState !== CONNECTING) {
      return; // backend already closing/closed -- nothing sane to do
    }

    const size = Buffer.byteLength(payload);
    if (
      queue.length + 1 > opts.maxQueueMessages ||
      queueBytes + size > opts.maxQueueBytes
    ) {
      closeBoth(1009, "queue limit exceeded");
      return;
    }
    queue.push(payload);
    queueBytes += size;
  });

  backendSocket.addEventListener("open", () => {
    if (closed) return;
    if (handshakeTimer) {
      clearTimeout(handshakeTimer);
      handshakeTimer = null;
    }
    const pending = queue;
    queue = [];
    queueBytes = 0;
    for (const msg of pending) {
      backendSocket.send(asSendable(msg));
    }
  });

  // Backend -> client.
  backendSocket.addEventListener("message", (event: MessageEvent) => {
    if (closed) return;
    resetIdle();
    if (clientWs.readyState !== OPEN) return;
    const payload =
      typeof event.data === "string"
        ? event.data
        : Buffer.from(event.data as ArrayBuffer);
    clientWs.send(payload);
  });

  backendSocket.addEventListener("close", (event: CloseEvent) => {
    closeBoth(remapCloseCode(event.code), event.reason || "");
  });

  // Per the WebSocket spec, a 'close' event always follows an 'error' one
  // (failed handshake or abrupt termination both end in 'close' with a
  // code) -- so 'error' itself is a no-op here rather than a second,
  // racing path into closeBoth. Racing it against 'close' is what used to
  // make an abrupt backend termination surface as 1011 instead of the
  // 1006 -> 1000 remap 'close' is responsible for.
  backendSocket.addEventListener("error", () => {});

  clientWs.on("close", () => {
    if (closed) return;
    closed = true;
    clearTimers();
    try {
      if (
        backendSocket.readyState === OPEN ||
        backendSocket.readyState === CONNECTING
      ) {
        backendSocket.close();
      }
    } catch {
      /* ignore */
    }
  });

  clientWs.on("error", () => {
    try {
      backendSocket.close();
    } catch {
      /* ignore */
    }
  });
}
