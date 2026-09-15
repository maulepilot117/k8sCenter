/**
 * Build-time guard: no module may exist twice in the client bundle.
 *
 * The migration forked modules out of `frontend/lib/`, `frontend/islands/` and
 * `frontend/components/` into `frontend/src/`. The forks are byte-equivalent
 * apart from one import line, which is precisely what makes the resulting bug
 * invisible: module-scope state is per module, so two copies of `cluster.ts`
 * meant two `selectedCluster` signals. The cluster switcher wrote to the
 * islands' copy while every API call read `lib/api.ts`'s copy, and that copy's
 * `IS_BROWSER` came from the test-only `fresh/runtime` shim as the constant
 * `false`, so its localStorage restore and persist effect were tree-shaken out
 * entirely. Net effect in the browser: every request went to the local cluster
 * regardless of what the user selected. All four gates were green throughout.
 *
 * The same shape then recurred one directory over, and this guard's first
 * version could not see it: seven files under `src/` imported the Fresh
 * `YamlEditor`, `NamespaceTopology`, `NetworkPolicyWizard` and `Logo`, whose
 * `IS_BROWSER` resolved to the same constant `false`. Every YAML-editing
 * surface and the ESO chain graph rendered a dead placeholder in the built
 * app. The first version missed it for three separate reasons, each of which
 * this version fixes:
 *
 *   1. It matched only `from "@/..."` with double quotes, so single-quoted and
 *      side-effect and dynamic imports were invisible.
 *   2. It followed only specifiers beginning `lib/`, so every edge through
 *      `islands/` and `components/` was invisible.
 *   3. It compared string prefixes rather than resolved paths, so relative
 *      imports were invisible — and `lib/yaml-apply.ts` already uses one.
 *
 * So the rule is now stated once and enforced on the resolved graph:
 *
 *   A module under `frontend/{lib,islands,components}/` that has a same-named
 *   twin under `frontend/src/` must not be reachable from `frontend/src/` as a
 *   second copy. Either it re-exports the twin (one instance, fine) or the src
 *   tree must not reach it at all.
 *
 * The check runs on the source graph rather than the built output, so it fails
 * in `bun run check` rather than after a build, and so it names the import
 * chain that caused it instead of a hashed chunk filename. It is deliberately
 * the source graph and not the bundle: whether Rollup coincidentally
 * deduplicates two similar modules is not a property to depend on for
 * correctness — `namespace.ts` is the case that proved it.
 *
 * Pure data and type-only modules are exempt: duplicating a lookup table costs
 * bytes, not correctness. The exemption is by explicit name, not by heuristic,
 * so adding one is a decision somebody makes on purpose.
 */

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveImportPath } from "./check-no-signal-store-in-ssr.ts";

const FRONTEND_DIR = dirname(dirname(fileURLToPath(import.meta.url)));

/**
 * The old-tree directories that were forked into `src/`.
 *
 * Exported so a test can assert this scope directly. Asserting it through
 * whichever twins happen to exist in the tree is what made the previous two
 * versions of that test rot: it named an `islands/` twin until U12 deleted
 * that directory, then a `components/` twin until the same unit's follow-up
 * deleted the last one. The set of directories is the invariant; which files
 * currently sit in them is not.
 *
 * `islands` stays listed although `frontend/islands/` is gone: the entry costs
 * one `existsSync` and it is the shape of the next fork.
 */
export const FORKED_ROOTS = ["lib", "islands", "components"] as const;

/**
 * Forked twins that hold no module-scope state — types and frozen data.
 * A second copy of these wastes bytes and cannot change behaviour.
 */
const STATELESS_TWINS: ReadonlySet<string> = new Set([
  "lib/eso-types.ts",
  "lib/eso-yaml-templates.ts",
]);

export interface DuplicateFinding {
  /** Repo-relative path of the old-tree module that exists in two live copies. */
  twin: string;
  /** The `src/` file whose import graph reaches it. */
  entry: string;
  /** How the entry reaches the twin, entry first. Repo-relative paths. */
  chain: string[];
}

function walk(dir: string, out: string[] = []): string[] {
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir)) {
    if (
      entry === "node_modules" ||
      entry === "dist" ||
      entry === ".astro" ||
      entry === "_fresh" ||
      entry === "__shims__"
    ) {
      continue;
    }
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx|astro)$/.test(entry)) out.push(p);
  }
  return out;
}

function rel(abs: string): string {
  return relative(FRONTEND_DIR, abs).split("\\").join("/");
}

/**
 * Every repo-internal module a file imports **for its value**, as resolved
 * absolute paths.
 *
 * Quote-agnostic, and it follows dynamic and bare side-effect imports as well
 * as the ordinary form, because all three put a module instance in the bundle.
 *
 * Type-only edges are deliberately excluded, and that exclusion is the whole
 * reason this does not reuse `extractImportSpecifiers` from the SSR guard:
 * `import type` erases at compile time and creates no second instance. Five
 * shared wizard-form components import a `…WizardForm` type back out of the
 * pre-migration island that declares it; counting those as duplication would
 * report five islands that are not duplicated at all, and a guard that cries
 * wolf is one somebody eventually deletes.
 */
export function valueImportsOf(absFile: string): string[] {
  if (!existsSync(absFile)) return [];
  const source = readFileSync(absFile, "utf-8");
  const specifiers: string[] = [];
  const patterns: RegExp[] = [
    // import ... from "x" / export ... from "x", excluding `import type` and
    // `export type`. The lazy body stops a "from" inside one statement's
    // named-import list from swallowing the next statement.
    /(?:import|export)\s+(?!type\s)[\s\S]*?from\s+["']([^"']+)["']/g,
    // import("x")
    /import\s*\(\s*["']([^"']+)["']\s*\)/g,
    // bare `import "x";`
    /import\s+["']([^"']+)["']/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      if (match[1]) specifiers.push(match[1]);
    }
  }
  // A single statement can still mix a value default with named types
  // (`import X, { type Y } from`); that is a value edge and is kept. Only a
  // whole `import type` statement is dropped.
  const out: string[] = [];
  for (const spec of specifiers) {
    const resolved = resolveImportPath(absFile, spec, FRONTEND_DIR);
    if (resolved) out.push(resolved);
  }
  return out;
}

/**
 * Twins are `<root>/x` that also exist as `src/<root>/x`, at any depth, for
 * every forked root — not just top-level `.ts` under `lib/`.
 */
export function forkedTwins(frontendDir: string = FRONTEND_DIR): Set<string> {
  const twins = new Set<string>();
  for (const root of FORKED_ROOTS) {
    const srcRoot = join(frontendDir, "src", root);
    if (!existsSync(srcRoot)) continue;
    for (const srcFile of walk(srcRoot)) {
      if (/_test\.tsx?$/.test(srcFile)) continue;
      const suffix = relative(srcRoot, srcFile);
      const oldFile = join(frontendDir, root, suffix);
      if (!existsSync(oldFile)) continue;
      const key = relative(frontendDir, oldFile).split("\\").join("/");
      if (STATELESS_TWINS.has(key)) continue;
      twins.add(key);
    }
  }
  return twins;
}

/**
 * A module that only re-exports its `src/` twin is the fix, not the bug: one
 * module instance, reachable by either path. Recognised by the presence of
 * that re-export and the absence of any import of its own.
 */
function isReExportOfTwin(twinRel: string): boolean {
  const abs = join(FRONTEND_DIR, twinRel);
  if (!existsSync(abs)) return false;
  const source = readFileSync(abs, "utf-8");
  const target = `@/src/${twinRel}`;
  const hasReExport =
    source.includes(`export * from "${target}"`) ||
    source.includes(`export * from '${target}'`);
  return hasReExport && !/^\s*import\s/m.test(source);
}

export function findDuplicateLibState(): DuplicateFinding[] {
  const twins = forkedTwins();
  const findings: DuplicateFinding[] = [];
  const reported = new Set<string>();
  const srcDir = join(FRONTEND_DIR, "src");

  for (const entryFile of walk(srcDir)) {
    if (/_test\.tsx?$/.test(entryFile)) continue;

    const stack: Array<{ abs: string; chain: string[] }> = [
      { abs: entryFile, chain: [rel(entryFile)] },
    ];
    const seen = new Set<string>([entryFile]);

    while (stack.length > 0) {
      const { abs, chain } = stack.pop() as { abs: string; chain: string[] };

      for (const next of valueImportsOf(abs)) {
        const nextRel = rel(next);

        // Anything under src/ is the ported tree; keep walking through it,
        // because a src file can reach an old-tree module two hops down.
        if (twins.has(nextRel) && !isReExportOfTwin(nextRel)) {
          const key = `${rel(entryFile)}->${nextRel}`;
          if (!reported.has(key)) {
            reported.add(key);
            findings.push({
              twin: nextRel,
              entry: rel(entryFile),
              chain: [...chain, nextRel],
            });
          }
          continue;
        }

        if (seen.has(next)) continue;
        seen.add(next);
        stack.push({ abs: next, chain: [...chain, nextRel] });
      }
    }
  }

  findings.sort((a, b) =>
    a.twin === b.twin
      ? a.entry.localeCompare(b.entry)
      : a.twin.localeCompare(b.twin),
  );
  return findings;
}

function main(): void {
  const findings = findDuplicateLibState();

  if (findings.length > 0) {
    console.error(
      "Duplicate-module guard: the src tree reaches a pre-migration module " +
        "that also exists under frontend/src, so both copies ship and their " +
        "module-scope state is not shared:",
    );
    for (const f of findings) {
      console.error(`  ${f.twin}`);
      console.error(`      via ${f.chain.join(" -> ")}`);
    }
    console.error(
      "\nFix by importing the src twin instead:\n" +
        '  import X from "@/src/<path>";\n' +
        "or, when many call sites reach the old path, by making the old module " +
        "re-export its twin:\n" +
        '  export * from "@/src/<path>";\n' +
        "Either leaves one module instance. Do not resolve this by deleting " +
        "the guard: a signal, a socket, a cache or an IS_BROWSER constant that " +
        "exists twice fails only in a browser, and only for a user.",
    );
    process.exit(1);
  }

  console.log(
    "Duplicate-module guard: no forked pre-migration module is reachable " +
      "from src as a second copy.",
  );
}

if (import.meta.main) {
  main();
}

export { FRONTEND_DIR };
