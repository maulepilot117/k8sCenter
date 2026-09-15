/**
 * KTD11: the cluster-scoped CRD detail URL is preserved by rewriting it in
 * the outer server, not by redirecting.
 *
 * `/extensions/:group/:resource/_/:name` is the operator-facing URL Fresh
 * served today (`frontend/routes/extensions/[group]/[resource]/_/[name].tsx`;
 * `frontend/islands/CRDResourceList.tsx` is the one place that generates
 * this shape, substituting `_` for the namespace segment on a cluster-
 * scoped resource that has none). Astro's router treats any `_`-prefixed
 * file or directory under `src/pages/` as private and non-routable, so
 * that literal path cannot be expressed as a page file.
 *
 * The fix: the page lives at a routable segment (`cluster-scoped` in place
 * of `_`) and this module rewrites the incoming request path onto it
 * *before* Astro's router ever sees the request — mutating `req.url` on
 * the raw Node request, not issuing an HTTP redirect. The browser's
 * address bar, and `Astro.url` inside the page, still show the original
 * `/extensions/:group/:resource/_/:name` (R19); only the internal
 * dispatch target changes.
 *
 * U8 ports the page itself (`frontend/routes/extensions/[group]/[resource]/_/[name].tsx`
 * -> `frontend/src/pages/extensions/[group]/[resource]/cluster-scoped/[name].astro`)
 * and leaves a comment there naming this module, so the mapping is
 * discoverable from either side.
 */

const CLUSTER_SCOPED_CRD_PATTERN =
  /^\/extensions\/([^/]+)\/([^/]+)\/_\/([^/]+)$/;

/** The routable segment Astro's page for this route must live under. */
export const CLUSTER_SCOPED_CRD_SEGMENT = "cluster-scoped";

/**
 * Rewrites a cluster-scoped CRD detail pathname onto its routable Astro
 * target. Returns the rewritten pathname, or the input unchanged if it
 * does not match the shape. Pure and pathname-only (no query string) so it
 * is trivial to unit test; `applyClusterScopedCrdRewrite` below handles the
 * query string and the raw request object.
 */
export function rewriteClusterScopedCrdPathname(pathname: string): string {
  const match = pathname.match(CLUSTER_SCOPED_CRD_PATTERN);
  if (!match) return pathname;
  const [, group, resource, name] = match;
  return `/extensions/${group}/${resource}/${CLUSTER_SCOPED_CRD_SEGMENT}/${name}`;
}

/**
 * Mutates `req.url` in place when it matches the KTD11 shape, preserving
 * the query string. No-op otherwise. Call this on the raw Node request
 * after the malformed-request guard and before handing off to Astro (both
 * dispatch.ts's production chain and dev-plugin.ts's dev-server chain do
 * this at the same point, so dev and prod never diverge on this route —
 * the same reasoning as R18 for the WebSocket dispatch).
 */
export function applyClusterScopedCrdRewrite(req: { url?: string }): void {
  const raw = req.url ?? "/";
  const queryIndex = raw.indexOf("?");
  const pathname = queryIndex === -1 ? raw : raw.slice(0, queryIndex);
  const query = queryIndex === -1 ? "" : raw.slice(queryIndex);

  const rewritten = rewriteClusterScopedCrdPathname(pathname);
  if (rewritten !== pathname) {
    req.url = rewritten + query;
  }
}
