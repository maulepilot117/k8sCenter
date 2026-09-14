/**
 * Real replacement for Fresh's `IS_BROWSER` (`fresh/runtime`).
 *
 * Fresh's own runtime defines `IS_BROWSER` identically —
 * `typeof document !== "undefined"` — so this is a value-neutral port, not a
 * behavior change: false during Astro SSR (Node, no DOM) and false under
 * `bun test` (no DOM either), true once a `client:load` island hydrates in a
 * real browser.
 *
 * `frontend/lib/__shims__/fresh-runtime.ts` is a *different*, narrower thing:
 * a test-only shim that `tsconfig.json` maps the bare `fresh/runtime`
 * specifier to, hardcoded to `false` so `bun test` can load files that still
 * import the bare specifier (the ones this migration hasn't reached yet). Any
 * module ported off Fresh — this migration's islands and the four signal
 * stores included — imports this module directly instead, by specifier
 * (`@/src/lib/is-browser.ts`), so it needs no tsconfig redirect and behaves
 * correctly in the one context the shim gets wrong: real browser hydration.
 */
export const IS_BROWSER: boolean = typeof document !== "undefined";
