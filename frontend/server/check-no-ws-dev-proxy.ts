import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * R18's standing guard: dev and prod must dispatch /ws/* through the same
 * code path. The divergence R18 closes (frontend/vite.config.ts's Vite
 * `server.proxy` sending `/ws` straight to the backend, bypassing the
 * allowlist and traversal guard in dev) was reintroducible precisely
 * because nothing detected it — this is that detector.
 *
 * Scope note (U5's second scope correction): this guard scans only the
 * Astro/Bun dev configuration (astro.config.mjs and this directory's own
 * dev-plugin.ts) — it must NOT flag frontend/vite.config.ts, which is the
 * Fresh tree's dev proxy and is still required there until U12 deletes
 * that tree. Widen FILES_TO_SCAN to the whole frontend once the Fresh tree
 * — and its vite.config.ts — is gone.
 */

const WS_PROXY_KEY_PATTERN = /proxy\s*:\s*\{[^}]*["'`]\/ws["'`]\s*:/s;
// Bounded lookahead (not comma-terminated) so this also catches /ws showing
// up as the rewrite *target* (e.g. `path.replace(/^\/socket/, "/ws")`), not
// just as the rewritten-from source.
const WS_REWRITE_PATTERN = /rewrite\s*:[\s\S]{0,80}\/ws\b/i;

export function sourceHasWsProxy(source: string): boolean {
  return WS_PROXY_KEY_PATTERN.test(source) || WS_REWRITE_PATTERN.test(source);
}

const FILES_TO_SCAN = ["../astro.config.mjs", "./dev-plugin.ts"];

function main(): void {
  const here = dirname(fileURLToPath(import.meta.url));
  const offenders: string[] = [];

  for (const rel of FILES_TO_SCAN) {
    const abs = join(here, rel);
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
      "WebSocket upgrades must go through frontend/server/ws-proxy.ts in both dev and " +
        "prod (R18). frontend/vite.config.ts is exempt until U12 deletes the Fresh tree.",
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
