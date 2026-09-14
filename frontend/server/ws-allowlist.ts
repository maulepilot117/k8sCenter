/**
 * WebSocket path allowlist, traversal guard, and close-code remapping.
 *
 * Ported from frontend/routes/ws/[...path].ts (P2-087) — a behaviour port,
 * not a redesign (R6). Split out as pure, dependency-free functions so both
 * the live proxy (ws-proxy.ts) and its tests can exercise the decision logic
 * directly, without a socket.
 */

export type WsPathCheck =
  | { ok: true; path: string }
  | { ok: false; status: 400 | 404 };

/** The six backend WS endpoints the frontend is allowed to relay to. */
const ALLOWED_WS_PATTERNS: RegExp[] = [
  /^v1\/ws\/resources$/,
  /^v1\/ws\/logs\/[^/]+\/[^/]+\/[^/]+$/,
  /^v1\/ws\/exec\/[^/]+\/[^/]+\/[^/]+$/,
  /^v1\/ws\/alerts$/,
  /^v1\/ws\/flows$/,
  /^v1\/ws\/logs-search$/,
];

/**
 * Validates a raw request path such as "/ws/v1/ws/exec/default/mypod/main".
 *
 * Deliberately tests the RAW, still-percent-encoded string — no
 * decodeURIComponent step — so a literal ".." and its "%2e"-encoded form are
 * both caught by the same regex, without a decode step of our own that could
 * itself be fooled by double-encoding. This mirrors the original Fresh
 * route exactly.
 */
export function checkWsPath(rawUrl: string): WsPathCheck {
  const pathname = (rawUrl ?? "").split("?")[0] ?? "";
  const match = /^\/ws\/(.+)$/.exec(pathname);
  if (!match) {
    return { ok: false, status: 404 };
  }
  const path = match[1];

  if (!path.startsWith("v1/") || /\.\.|\/\/|%2e/i.test(path)) {
    return { ok: false, status: 400 };
  }

  if (!ALLOWED_WS_PATTERNS.some((pattern) => pattern.test(path))) {
    return { ok: false, status: 404 };
  }

  return { ok: true, path };
}

/**
 * True for the one WS route the backend gates with middleware.Auth at
 * upgrade time rather than in-band (see "Add the exec Bearer header" in
 * U5's brief, and backend/internal/server/routes.go).
 */
export function isExecPath(path: string): boolean {
  return /^v1\/ws\/exec\//.test(path);
}

/**
 * Reserved/invalid WebSocket close codes can only be received, never sent —
 * calling .close(1006) throws. Ported verbatim from the original route's
 * remap, which only excludes 1006 explicitly; that is the one case the
 * backend's own close events are known to produce, and this is a behaviour
 * port, not a hardening pass.
 */
export function remapCloseCode(code: number): number {
  return code >= 1000 && code <= 4999 && code !== 1006 ? code : 1000;
}
