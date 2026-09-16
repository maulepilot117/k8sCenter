import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * R18's standing guard: dev and prod must dispatch /ws/* through the same
 * code path. The divergence R18 closes (frontend/vite.config.ts's Vite
 * `server.proxy` sending `/ws` straight to the backend, bypassing the
 * allowlist and traversal guard in dev) was reintroducible precisely
 * because nothing detected it — this is that detector.
 *
 * Scope: the dev-configuration surface, derived rather than listed. That is
 * every root-level `astro.config.*` / `vite.config.*`, plus every
 * `*-plugin.ts` in this directory -- because `astro.config.mjs` loads a plugin
 * array, so a `/ws` proxy added in a new sibling plugin module is as much a
 * dev/prod divergence as one written into the config itself, and a hardcoded
 * two-entry list would not see it.
 *
 * Deriving the list rather than naming it is the point. The list was
 * `["../astro.config.mjs", "./dev-plugin.ts"]` with a note saying to widen it
 * once the Fresh tree's own vite.config.ts was deleted; the tree was deleted
 * and the list was not widened, because nothing failed when it wasn't.
 * `filesToScan` is exported and asserted so a silently empty or mis-globbed
 * result fails loudly instead of passing vacuously.
 *
 * The scan deliberately stops short of the whole frontend: `server/ws-proxy.ts`
 * and `server/ws-allowlist.ts` contain `/ws` literals precisely because they
 * are the sanctioned path, and flagging them would make the guard useless.
 */

// Matches a "/ws" (or "/ws/") proxy key anywhere in the scanned config.
//
// The previous form anchored on `proxy: {` and then scanned `[^}]*`, which
// cannot cross a preceding entry's closing brace -- so it found the key only
// when /ws happened to be the FIRST proxy entry, and a realistic
// `proxy: { "/api": {...}, "/ws": {...} }` sailed through. The guard exists
// to catch exactly that reintroduction, and it did not. Dropping the prefix
// requirement costs nothing here: these config files contain no other
// "/ws"-keyed object, and a false positive is a loud, cheap failure while a
// false negative silently un-does R18.
const WS_PROXY_KEY_PATTERN = /["'`]\/ws\/?["'`]\s*:/;
// Bounded lookahead (not comma-terminated) so this also catches /ws showing
// up as the rewrite *target* (e.g. `path.replace(/^\/socket/, "/ws")`), not
// just as the rewritten-from source.
const WS_REWRITE_PATTERN = /rewrite\s*:[\s\S]{0,80}\/ws\b/i;

export function sourceHasWsProxy(source: string): boolean {
  return WS_PROXY_KEY_PATTERN.test(source) || WS_REWRITE_PATTERN.test(source);
}

const HERE = dirname(fileURLToPath(import.meta.url));
const FRONTEND_ROOT = resolve(HERE, "..");

/** A root-level build/dev config: `astro.config.mjs`, `vite.config.ts`, ... */
const ROOT_CONFIG_PATTERN = /^(astro|vite)\.config\.(m?[jt]s|cjs)$/;
/** A dev-server plugin module that `astro.config.mjs` can load. */
const PLUGIN_PATTERN = /-plugin\.tsx?$/;

/**
 * The dev-configuration surface, resolved from disk. Exported so a test can
 * assert what it actually contains -- an empty or mis-globbed list would
 * otherwise report success having scanned nothing.
 */
export function filesToScan(
  frontendRoot: string = FRONTEND_ROOT,
  serverDir: string = HERE,
): string[] {
  const found: string[] = [];
  if (existsSync(frontendRoot)) {
    for (const entry of readdirSync(frontendRoot)) {
      if (ROOT_CONFIG_PATTERN.test(entry))
        found.push(join(frontendRoot, entry));
    }
  }
  if (existsSync(serverDir)) {
    for (const entry of readdirSync(serverDir)) {
      if (PLUGIN_PATTERN.test(entry) && !entry.endsWith("_test.ts")) {
        found.push(join(serverDir, entry));
      }
    }
  }
  return found.sort();
}

function main(): void {
  const targets = filesToScan();
  const offenders: string[] = [];

  // A scan that found nothing to scan is a broken guard, not a passing one.
  if (targets.length === 0) {
    console.error(
      "R18 guard: found no dev-configuration files to scan. The guard cannot " +
        "pass vacuously -- check FRONTEND_ROOT and the config/plugin patterns.",
    );
    process.exit(1);
  }

  for (const abs of targets) {
    const source = readFileSync(abs, "utf-8");
    if (sourceHasWsProxy(source)) {
      offenders.push(abs);
    }
  }

  if (offenders.length > 0) {
    console.error(
      "R18 guard: found a /ws proxy or rewrite in the Astro dev config:",
    );
    for (const f of offenders) console.error(`  ${f}`);
    console.error(
      "WebSocket upgrades must go through frontend/server/ws-proxy.ts in " +
        "both dev and prod (R18).",
    );
    process.exit(1);
  }

  console.log(
    "R18 guard: no /ws proxy or rewrite found in the Astro dev config.",
  );
}

if (import.meta.main) {
  main();
}
