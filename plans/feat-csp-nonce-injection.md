# feat: CSP Hardening via Fresh Middleware

Replace manual security headers in `_middleware.ts` with Fresh's built-in `csp()` and `headers()` middleware. Improve CSP directives with `base-uri` and `object-src` hardening.

## Problem Statement

Security headers are manually set in `_middleware.ts` with a TODO comment about nonce-based CSP. Fresh 2.2.0 now exports a `csp()` middleware that provides cleaner header management with good defaults (`object-src 'none'`, `base-uri 'self'`, `upgrade-insecure-requests`).

## What Changed Since Our Last Assessment

**Fresh 2.2.0's `csp()` is a static header middleware — it does NOT inject per-request nonces into `<script>` tags.** The old Fresh 1.x `useCSP()` hook had nonce injection, but Fresh 2.x's `csp()` is purely a header-setting convenience. True nonce-based CSP would require writing a custom HTML transformer to inject nonces into Fresh's server-rendered output — out of scope for this item.

**What we CAN do:** Use `csp()` to replace our manual header code, add missing hardening directives (`base-uri`, `object-src`, `form-action`), and clean up the middleware file. `'unsafe-inline'` must remain in `script-src` for now.

---

## Implementation Plan

### Step 1: Replace Manual Headers with Fresh Middleware

**`frontend/main.ts`:**

```typescript
import { App, csp, staticFiles } from "fresh";
import type { State } from "@/utils.ts";

export const app = new App<State>();

app.use(csp({
  csp: [
    "default-src 'self'",
    "script-src 'self' 'unsafe-inline' https://esm.sh/",
    "style-src 'self' 'unsafe-inline' https://esm.sh/",
    "img-src 'self' data:",
    "connect-src 'self' https://esm.sh/",
    "worker-src 'self' blob: https://esm.sh/",
    "frame-src 'self'",
    "frame-ancestors 'none'",
    // These are new — not in our current manual CSP:
    "base-uri 'self'",
    "object-src 'none'",
    "form-action 'self'",
  ],
}));

app.use(staticFiles());
app.fsRoutes();
```

Fresh's `csp()` defaults already include `base-uri 'self'`, `object-src 'none'`, and `upgrade-insecure-requests`. By passing our own `csp` array, we override the defaults entirely — so we must include all directives we want.

**`frontend/routes/_middleware.ts`** — delete entirely. The state values it sets (`user: null`, `title: "k8sCenter"`) are unused — auth state lives client-side in signals, and the title is hardcoded in `_app.tsx`.

**`frontend/routes/_app.tsx`** — add `headers()` middleware for non-CSP security headers. Actually, `headers()` is not needed if we put everything in `csp()`. The non-CSP headers (`X-Frame-Options`, etc.) can go in a simple middleware in `main.ts`:

```typescript
app.use(async (ctx) => {
  const res = await ctx.next();
  res.headers.set("X-Frame-Options", "DENY");
  res.headers.set("X-Content-Type-Options", "nosniff");
  res.headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  res.headers.set("Permissions-Policy", "camera=(), microphone=(), geolocation=()");
  return res;
});
```

Or if `headers()` is also exported from Fresh, use that instead.

### Step 2: Verify + Test

- `curl -I http://localhost:5173/` — verify CSP header includes `base-uri 'self'` and `object-src 'none'`
- Verify exactly ONE `Content-Security-Policy` header (no duplication)
- Verify islands hydrate (login, dashboard, resource tables)
- Verify Monaco editor loads
- Verify WebSocket connections work
- No CSP violations in browser DevTools console

---

## Acceptance Criteria

- [ ] Security headers set via Fresh `csp()` middleware in `main.ts`
- [ ] `_middleware.ts` deleted (unused state initialization)
- [ ] CSP includes `base-uri 'self'`, `object-src 'none'`, `form-action 'self'` (new hardening)
- [ ] All existing functionality works (islands, Monaco, WS)
- [ ] No CSP violations in browser console
- [ ] `deno lint && deno fmt --check && deno task build` pass

## Files to Modify

| File | Action | Purpose |
|------|--------|---------|
| `frontend/main.ts` | Modify | Add `csp()` middleware + non-CSP headers |
| `frontend/routes/_middleware.ts` | Delete | Headers moved to main.ts, state values unused |

## Future Work

**True nonce-based CSP (eliminating `'unsafe-inline'` from `script-src`)** would require:
- A custom Fresh middleware that generates a per-request nonce
- An HTML transform that injects the nonce into all `<script>` tags in the rendered output
- Moving the `_app.tsx` dark-mode `dangerouslySetInnerHTML` script to an external file
- This is tracked but deferred until Fresh adds native nonce injection support

## What This Achieves

- Cleaner code: 1 file instead of 2, using framework API
- New hardening: `base-uri 'self'`, `object-src 'none'`, `form-action 'self'`
- Removes unused middleware state initialization
- Removes the misleading TODO about Fresh nonce support
