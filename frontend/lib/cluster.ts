/**
 * Re-export of the ported module. One instance, deliberately.
 *
 * This file used to be a full copy that U9 forked into `src/lib/cluster.ts`
 * so the Astro tree could get a real `IS_BROWSER` instead of the test-only
 * `fresh/runtime` shim. The two copies were byte-equivalent apart from that
 * import — and that is exactly what made the bug invisible.
 *
 * Module-scope signals are per-module. The islands import
 * `@/src/lib/cluster.ts`; `lib/api.ts` (105 src importers), `lib/auth.ts`,
 * `lib/pin-store.ts` and `lib/resource-counts.ts` imported this one. Vite
 * emitted BOTH into the client bundle, so `selectedCluster` existed twice:
 * the switcher wrote to one signal and every API call read the other. Worse,
 * this copy's `IS_BROWSER` was the shim's constant `false`, so its
 * localStorage restore and persist effect were tree-shaken away entirely —
 * leaving the API client permanently pinned to the local cluster no matter
 * what the user selected. Every gate was green throughout.
 *
 * Re-exporting rather than repointing 105 import sites is the small change
 * that makes the duplication impossible instead of merely absent today.
 * `server/check-no-duplicate-lib-state.ts` fails the build if another pair
 * like this appears.
 */
export * from "@/src/lib/cluster.ts";
