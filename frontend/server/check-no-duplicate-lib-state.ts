/**
 * Build-time guard: no module may exist twice in the client bundle.
 *
 * The migration forked several `frontend/lib/*` modules into `frontend/src/lib/`
 * so the Astro tree could drop the `fresh/runtime` shim. The forks were
 * byte-equivalent apart from one import line, which is precisely what made the
 * resulting bug invisible: module-scope state is per-module, so two copies of
 * `cluster.ts` meant two `selectedCluster` signals. The cluster switcher wrote
 * to the islands' copy while every API call read `lib/api.ts`'s copy, and that
 * copy's `IS_BROWSER` was the shim's constant `false`, so its localStorage
 * restore and persist effect were tree-shaken out entirely. Net effect in the
 * browser: every request went to the local cluster regardless of what the user
 * selected. `ws.ts` had the same shape — two sockets, two subscription
 * registries. All four gates were green the whole time.
 *
 * Nothing in a type checker, a linter or a unit test can see this. What it
 * needs is a structural rule, so that is what this is:
 *
 *   A module under `frontend/lib/` that has a same-named twin under
 *   `frontend/src/lib/` must not be reachable from `frontend/src/` as a
 *   second copy. Either it re-exports the twin (one instance, fine) or the
 *   src tree must not reach it at all.
 *
 * The check is on the source graph rather than the built output, so it fails
 * in `bun run check` rather than after a build, and so it names the import
 * chain that caused it instead of a hashed chunk filename.
 *
 * Pure data and type-only modules are exempt: duplicating a lookup table
 * costs bytes, not correctness. The exemption is by explicit name, not by
 * heuristic, so adding one is a decision somebody makes on purpose.
 */

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const FRONTEND_DIR = dirname(dirname(fileURLToPath(import.meta.url)));

/**
 * Forked twins that hold no module-scope state — types and frozen data.
 * A second copy of these wastes bytes and cannot change behaviour.
 */
const STATELESS_TWINS = new Set(["eso-types.ts", "eso-yaml-templates.ts"]);

export interface DuplicateFinding {
  /** The `frontend/lib/` module that exists in two live copies. */
  twin: string;
  /** The `@/lib/...` module the src tree imports to reach it. */
  entry: string;
  /** How the entry reaches the twin, entry first. */
  chain: string[];
}

function walk(dir: string, out: string[] = []): string[] {
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir)) {
    if (
      entry === "node_modules" ||
      entry === "dist" ||
      entry === ".astro" ||
      entry === "_fresh"
    ) {
      continue;
    }
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx|astro)$/.test(entry)) out.push(p);
  }
  return out;
}

/** Every `@/`-prefixed specifier in a file, with the `@/` stripped. */
function importsOf(file: string): string[] {
  if (!existsSync(file)) return [];
  const source = readFileSync(file, "utf-8");
  const found: string[] = [];
  for (const m of source.matchAll(/from\s+"(@\/[^"]+)"/g)) {
    found.push(m[1].slice(2));
  }
  return found;
}

/** Twins are `lib/x.ts` that also exist as `src/lib/x.ts`. */
function forkedTwins(): Set<string> {
  const twins = new Set<string>();
  const srcLib = join(FRONTEND_DIR, "src", "lib");
  if (!existsSync(srcLib)) return twins;
  for (const entry of readdirSync(srcLib)) {
    if (!entry.endsWith(".ts") || entry.endsWith("_test.ts")) continue;
    if (STATELESS_TWINS.has(entry)) continue;
    if (existsSync(join(FRONTEND_DIR, "lib", entry))) {
      twins.add(`lib/${entry}`);
    }
  }
  return twins;
}

/**
 * A `lib/` module that only re-exports its `src/lib/` twin is the fix, not
 * the bug: one module instance, reachable by either path. Recognised by the
 * absence of any import other than that re-export.
 */
function isReExportOfTwin(rel: string): boolean {
  const abs = join(FRONTEND_DIR, rel);
  if (!existsSync(abs)) return false;
  const source = readFileSync(abs, "utf-8");
  const twin = `@/src/${rel}`;
  return (
    new RegExp(
      `export\\s+\\*\\s+from\\s+"${twin.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}"`,
    ).test(source) && !/^\s*import\s/m.test(source)
  );
}

export function findDuplicateLibState(): DuplicateFinding[] {
  const twins = forkedTwins();
  const findings: DuplicateFinding[] = [];
  const reported = new Set<string>();

  const entryPoints = new Set<string>();
  for (const file of walk(join(FRONTEND_DIR, "src"))) {
    if (/_test\.ts$/.test(file)) continue;
    for (const spec of importsOf(file)) {
      if (spec.startsWith("lib/")) entryPoints.add(spec);
    }
  }

  for (const entry of [...entryPoints].sort()) {
    const stack: Array<{ rel: string; chain: string[] }> = [
      { rel: entry, chain: [entry] },
    ];
    const seen = new Set<string>();
    while (stack.length > 0) {
      const { rel, chain } = stack.pop() as { rel: string; chain: string[] };
      if (seen.has(rel)) continue;
      seen.add(rel);

      if (twins.has(rel) && !isReExportOfTwin(rel)) {
        const key = `${entry}->${rel}`;
        if (!reported.has(key)) {
          reported.add(key);
          findings.push({ twin: rel, entry, chain });
        }
        continue;
      }

      for (const spec of importsOf(join(FRONTEND_DIR, rel))) {
        if (spec.startsWith("lib/")) {
          stack.push({ rel: spec, chain: [...chain, spec] });
        }
      }
    }
  }

  return findings;
}

function main(): void {
  const findings = findDuplicateLibState();

  if (findings.length > 0) {
    console.error(
      "Duplicate-module guard: the src tree reaches a frontend/lib module " +
        "that also exists under frontend/src/lib, so both copies ship and " +
        "their module-scope state is not shared:",
    );
    for (const f of findings) {
      console.error(`  ${f.twin}`);
      console.error(`      via @/${f.chain.join(" -> @/")}`);
    }
    console.error(
      "\nFix by making the frontend/lib module re-export its src/lib twin:\n" +
        '  export * from "@/src/lib/<name>.ts";\n' +
        "That leaves one module instance reachable from either import path. " +
        "Do not resolve this by deleting the guard: a signal, a socket or a " +
        "cache that exists twice fails only in a browser, and only for a user.",
    );
    process.exit(1);
  }

  console.log(
    "Duplicate-module guard: no forked frontend/lib module is reachable " +
      "from src as a second copy.",
  );
}

if (import.meta.main) {
  main();
}
