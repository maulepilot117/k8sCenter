import { defineMiddleware } from "astro:middleware";

/**
 * Two responsibilities Astro's routing does not provide out of the box
 * (U7's error-surface requirement):
 *
 * 1. Astro auto-routes 404 (unmatched path) and 500 (unhandled render
 *    exception) to src/pages/404.astro and 500.astro respectively, but has
 *    no equivalent for 403 — a page that sets `Astro.response.status =
 *    403` still renders whatever markup that page itself authored. This
 *    middleware inspects the response after the fact and, on 403, hands
 *    the request to src/pages/403.astro instead — via `context.rewrite`,
 *    not a redirect, so the operator's address bar never changes (same
 *    "rewrite, not redirect" principle as the server-side KTD11 rewrite in
 *    frontend/server/rewrites.ts, applied here to a status rather than a
 *    path).
 *
 * 2. An exception thrown by this middleware's OWN logic must still render
 *    the 500 surface rather than a raw stack trace. (A page component
 *    throwing during rendering is a different, already-handled case —
 *    Astro's own pipeline funnels that to 500.astro before it would ever
 *    reach here; this catch is for a defect in onRequest itself, including
 *    ones future middleware layered on top of this adds.)
 *
 * The `x-rendered-error-surface` response header, set by 403.astro itself,
 * is the re-entry guard: without it, rewriting to /403 would see /403's
 * own legitimate 403 status on the way back out and rewrite again, forever.
 */
export const onRequest = defineMiddleware(async (context, next) => {
  let response: Response;
  try {
    response = await next();
  } catch (err) {
    // Bind and log it. The defects this catch exists for are middleware-
    // internal, and swallowing the only evidence of them means a 500 in
    // production leaves no server-side trace at all. Matches the
    // console.error convention already used in server/api-proxy.ts.
    console.error(
      "[middleware] unhandled error rendering",
      context.url.pathname,
      err,
    );
    return context.rewrite("/500");
  }

  if (
    response.status === 403 &&
    !response.headers.has("x-rendered-error-surface")
  ) {
    return context.rewrite("/403");
  }

  return response;
});
