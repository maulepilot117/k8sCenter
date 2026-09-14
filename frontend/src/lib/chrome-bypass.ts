/**
 * Ported verbatim from the early-return check in `frontend/routes/_layout.tsx`:
 * login, setup, and the OIDC callback flow under `/auth/*` render full-screen,
 * without the three-column chrome (icon rail, secondary nav, top bar).
 *
 * Astro has no automatic folder-scoped layout the way Fresh's `_layout.tsx`
 * is implicitly applied to every route beneath it, so `ChromeLayout.astro`
 * calls this explicitly instead of relying on file placement.
 */
export function shouldBypassChrome(pathname: string): boolean {
  return (
    pathname === "/login" ||
    pathname === "/setup" ||
    pathname.startsWith("/auth/")
  );
}
