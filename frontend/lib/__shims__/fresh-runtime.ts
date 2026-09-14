/**
 * Test-only shim for the `fresh/runtime` jsr: specifier.
 *
 * `frontend/lib/cluster.ts` imports `IS_BROWSER` from `"fresh/runtime"`,
 * which `frontend/deno.json`'s import map resolves to
 * `jsr:@fresh/core@^2.2.0/runtime`. Bun has no jsr: resolution and no import
 * map, so `bun test` cannot load `cluster.ts` (or anything that transitively
 * imports it, e.g. `api.ts`) without this stand-in. `frontend/tsconfig.json`
 * maps the bare specifier `fresh/runtime` to this file via `paths`, which Bun
 * honours for module resolution; `deno.json`'s import map is untouched, so
 * the real Deno/Fresh build keeps resolving the real package.
 *
 * `false` is the correct value here, not a placeholder: it is the value
 * Deno would produce in a non-browser (server/test) context, and it is what
 * gates `cluster.ts`'s `localStorage` access — under `bun test` there is no
 * DOM, so that access must stay off.
 *
 * Delete this file (and its tsconfig.json `paths` entry) once `cluster.ts`
 * no longer imports `fresh/runtime` — i.e. once the real port away from
 * Fresh reaches this module.
 */
export const IS_BROWSER = false;
