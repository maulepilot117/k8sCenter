/**
 * The exec WS subprotocol auth convention (U-subproto), shared verbatim
 * between the browser (`src/islands/PodTerminal.tsx`) and the Bun proxy
 * (`server/ws-proxy.ts`).
 *
 * Browsers cannot set custom headers on a WebSocket handshake, and the
 * previous fix (an `access_token` query parameter) put the credential in a
 * URL that an ingress in front of the frontend pod logs by default. This
 * follows the pattern the Kubernetes apiserver itself uses for exactly this
 * problem: the client offers two `Sec-WebSocket-Protocol` values --
 *
 *   1. a credential-bearing one, `${WS_AUTH_BEARER_PROTOCOL_PREFIX}<token>`,
 *      with the token base64url-encoded *without padding* so it is a valid
 *      RFC 6455 subprotocol token -- a raw JWT can contain characters that
 *      aren't, and `=` padding definitely isn't;
 *   2. a plain sentinel, `WS_AUTH_SENTINEL_PROTOCOL`, which is the one the
 *      server echoes back in its handshake response.
 *
 * The server MUST select and echo only the sentinel -- echoing the
 * credential-bearing value back would put the token in a response header.
 * A browser fails the handshake if the server echoes a protocol the client
 * didn't offer, or stays silent when the client offered any at all, so the
 * sentinel exists purely to give the server something safe to echo.
 *
 * Implemented with the Web-standard `atob`/`btoa`/TextEncoder/TextDecoder
 * rather than Node's `Buffer` so the exact same code runs unmodified in the
 * browser, under Bun (the proxy's runtime), and under `bun test`.
 */

export const WS_AUTH_SENTINEL_PROTOCOL = "k8scenter.v1";

export const WS_AUTH_BEARER_PROTOCOL_PREFIX =
  "base64url.bearer.authorization.k8scenter.io.";

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

function base64UrlToBytes(value: string): Uint8Array {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** Builds the credential-bearing subprotocol value for a bearer token. */
export function encodeBearerSubprotocol(token: string): string {
  const encoded = bytesToBase64Url(new TextEncoder().encode(token));
  return `${WS_AUTH_BEARER_PROTOCOL_PREFIX}${encoded}`;
}

/**
 * Recovers the bearer token from a single subprotocol value, or `undefined`
 * if `protocol` isn't a credential-bearing one (wrong prefix, empty payload,
 * or invalid base64url -- never throws).
 */
export function decodeBearerSubprotocol(protocol: string): string | undefined {
  if (!protocol.startsWith(WS_AUTH_BEARER_PROTOCOL_PREFIX)) return undefined;
  const encoded = protocol.slice(WS_AUTH_BEARER_PROTOCOL_PREFIX.length);
  if (!encoded) return undefined;
  try {
    const token = new TextDecoder().decode(base64UrlToBytes(encoded));
    return token || undefined;
  } catch {
    return undefined;
  }
}

/** Splits a raw `Sec-WebSocket-Protocol` header value into trimmed tokens. */
export function parseSubprotocolHeader(
  headerValue: string | undefined | null,
): string[] {
  if (!headerValue) return [];
  return headerValue
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean);
}

/**
 * Finds the bearer token among a client's offered subprotocols (header
 * order doesn't matter -- the credential-bearing one is identified by its
 * prefix, not position).
 */
export function findBearerInSubprotocols(
  headerValue: string | undefined | null,
): string | undefined {
  for (const protocol of parseSubprotocolHeader(headerValue)) {
    const token = decodeBearerSubprotocol(protocol);
    if (token) return token;
  }
  return undefined;
}

/**
 * `WebSocketServer`'s `handleProtocols` option: given the `Set` of
 * subprotocols the client offered, selects which one (if any) the server
 * echoes back. Returns the sentinel when the client offered it, `false`
 * otherwise (which sends no `Sec-WebSocket-Protocol` header at all) --
 * never the credential-bearing value.
 */
export function selectWsSubprotocol(protocols: Set<string>): string | false {
  return protocols.has(WS_AUTH_SENTINEL_PROTOCOL)
    ? WS_AUTH_SENTINEL_PROTOCOL
    : false;
}
