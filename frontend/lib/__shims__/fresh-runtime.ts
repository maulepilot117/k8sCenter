/**
 * Test-only shim for the `fresh/runtime` jsr: specifier.
 *
 * `fresh/runtime` resolves only through Deno's import map. Bun has none, so
 * `bun test` cannot load any module that imports it; `tsconfig.json` maps the
 * bare specifier here instead.
 *
 * WHAT STILL DEPENDS ON THIS, and therefore when it can go: `lib/nav.ts`,
 * `lib/theme.ts`, `lib/resource-counts.ts`, `lib/useNotificationCrud.ts` and
 * `lib/hooks/*` -- all in the pre-migration tree. It goes when U12 deletes
 * that tree, together with the `paths` entry in tsconfig.json. It is NOT
 * waiting on `lib/cluster.ts` any more: that file is now a bare re-export of
 * its ported twin and imports nothing.
 *
 * THIS CONSTANT IS A LIE IN A BROWSER, and that is the danger. It reports
 * `false` unconditionally, so anything that reaches it through a real build
 * silently behaves as though it were server-rendered forever: effects gated
 * on IS_BROWSER never run, and a bundler tree-shakes them out entirely. That
 * is not hypothetical -- seven files under `src/` once imported pre-migration
 * components through it, and every YAML editor and the ESO chain graph
 * rendered a dead placeholder in production while all four gates stayed
 * green. `server/check-no-duplicate-lib-state.ts` exists to make that
 * unrepeatable. Do not import this from anything that ships.
 */
export const IS_BROWSER = false;
