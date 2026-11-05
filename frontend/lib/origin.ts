/**
 * The origin a stored bearer token is bound to, computed the way the server
 * computes it (backend/internal/urlorigin `Of`), so a binding form prompts
 * for the token exactly when the server would refuse to keep it.
 *
 * The origin is the lowercase scheme, "://", and the lowercase host with any
 * explicit port kept as given. Path, query, fragment and credentials are
 * dropped. Default ports are NOT normalized: "https://h" and "https://h:443"
 * are different origins, which only ever asks for the token again. The
 * browser's `URL.origin` drops a default port, so it cannot be used here.
 *
 * Returns "" for an empty, scheme-less, host-less or malformed URL. "" is
 * never a valid origin and matches nothing.
 */
export function urlOrigin(raw: string): string {
  const m = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/?#]*)/.exec(raw.trim());
  if (!m) return "";
  const authority = m[2];
  // Userinfo ends at the last "@"; a password may itself contain "@".
  const host = authority.slice(authority.lastIndexOf("@") + 1);
  if (host === "" || /[\s\\]/.test(host)) return "";
  // A port, when present, must be digits (Go's url.Parse rejects others).
  // An IPv6 literal keeps its colons inside the brackets.
  const port = /^(?:\[[^\]]*\]|[^:[\]]*)(?::(.*))?$/.exec(host);
  if (!port || (port[1] !== undefined && !/^\d*$/.test(port[1]))) return "";
  return `${m[1].toLowerCase()}://${host.toLowerCase()}`;
}

/**
 * Whether next has the same token origin as stored. An unusable URL on
 * either side is never the same origin, so a stored token is never assumed
 * to follow it.
 */
export function sameOrigin(stored: string, next: string): boolean {
  const o = urlOrigin(next);
  return o !== "" && o === urlOrigin(stored);
}
