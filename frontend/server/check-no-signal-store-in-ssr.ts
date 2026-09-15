import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * U13's standing guard for KTD5's SSR/client boundary.
 *
 * frontend/lib/api.ts's own header comment states the invariant this check
 * codifies: its module-level variables are process-global singletons in
 * Deno/Node, so "a server-side import would leak auth state across SSR
 * requests." The same banner (word for word or in substance) sits on
 * frontend/lib/auth.ts and every signal store U13 ported
 * (cluster.ts, namespace.ts, pin-store.ts, resource-counts.ts, ws.ts).
 *
 * A lint keyed on *direct* imports would pass a file that imports
 * frontend/lib/api.ts (which looks innocuous -- it's "just the API
 * client") while missing that api.ts itself imports cluster.ts. That two-hop
 * shape is real, not hypothetical: api.ts and auth.ts both import
 * cluster.ts today. So this check walks the whole transitive import graph
 * from every server-rendered entry point (an .astro page, an .astro
 * layout, or src/middleware.ts) and fails if that walk ever reaches one of
 * the FORBIDDEN_MODULES below.
 *
 * The one deliberate exception is an island component
 * (anything under src/islands/). Astro's islands architecture means a page
 * mounting `<SomeIsland client:load />` legitimately imports that island in
 * its own frontmatter, and Astro *does* execute the island's render
 * function during SSR to produce the initial markup -- so cluster.ts is
 * already, correctly, reachable from every real page through its chrome
 * islands (ChromeLayout -> AlertBanner -> ws.ts / cluster.ts, for one).
 * That is KTD5's whole design: the island itself gates its browser-only
 * side effects behind IS_BROWSER, so importing (and even SSR-rendering) the
 * island is safe. What must never happen is a *non-island* SSR code
 * path -- a page's own frontmatter, a layout, middleware.ts -- reaching a
 * signal store other than by mounting an island, since nothing gates that.
 * So this walk stops at any file under src/islands/ rather than recursing
 * into it; the entry points below wire directly into it.
 */

const HERE = dirname(fileURLToPath(import.meta.url));
export const FRONTEND_ROOT = resolve(HERE, "..");

/**
 * The known client-only singleton modules, repo-relative to FRONTEND_ROOT,
 * forward-slashed. Both the not-yet-deleted Fresh tree (frontend/lib/*) and
 * the Astro tree (frontend/src/lib/*) are listed -- the Fresh tree stays
 * reachable from old islands until U12, and nothing here assumes it is
 * gone. src/lib/api.ts and src/lib/auth.ts are listed even though U13 has
 * not ported them yet: the set names the invariant, not just today's
 * files, so porting either later does not silently drop its coverage.
 */
export const FORBIDDEN_MODULES: ReadonlySet<string> = new Set([
  "lib/api.ts",
  "lib/auth.ts",
  "lib/cluster.ts",
  "lib/namespace.ts",
  "lib/pin-store.ts",
  "lib/resource-counts.ts",
  "lib/ws.ts",
  "src/lib/api.ts",
  "src/lib/auth.ts",
  "src/lib/cluster.ts",
  "src/lib/namespace.ts",
  "src/lib/pin-store.ts",
  "src/lib/resource-counts.ts",
  "src/lib/ws.ts",
]);

const RESOLVABLE_EXTENSIONS = [".ts", ".tsx", ".astro", ".js", ".jsx"];

/** True for any file this walk treats as an opaque, sanctioned boundary. */
export function isIslandPath(repoRelativePath: string): boolean {
  return (
    repoRelativePath.startsWith("src/islands/") ||
    repoRelativePath.startsWith("islands/")
  );
}

/**
 * Pulls every static/dynamic import and re-export specifier out of a
 * source string. For an .astro file, callers should pass only the
 * frontmatter (see extractAstroFrontmatter) -- the template body below the
 * second `---` is markup, not JS, and any `import`-looking text there is
 * not a real import.
 */
export function extractImportSpecifiers(source: string): string[] {
  const specifiers: string[] = [];
  const patterns = [
    // import ... from "x"; export ... from "x"; (lazy body so a "from"
    // inside one statement's named-import list doesn't swallow the next
    // statement too)
    /\b(?:import|export)\s+(?:type\s+)?[\s\S]*?\bfrom\s+["']([^"']+)["']/g,
    // import("x")
    /\bimport\s*\(\s*["']([^"']+)["']\s*\)/g,
    // bare `import "x";`
    /\bimport\s+["']([^"']+)["']/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      if (match[1]) specifiers.push(match[1]);
    }
  }
  return specifiers;
}

/** Extracts the `---`-fenced frontmatter script from an .astro file's source. */
export function extractAstroFrontmatter(source: string): string {
  const match = /^---\r?\n([\s\S]*?)\r?\n---\r?\n/.exec(source);
  return match ? match[1] : "";
}

function tryResolve(basePath: string): string | null {
  if (existsSync(basePath) && !basePath.endsWith("/")) {
    try {
      if (readFileSync(basePath) !== undefined) return basePath;
    } catch {
      // fall through to extension/index probing
    }
  }
  for (const ext of RESOLVABLE_EXTENSIONS) {
    const withExt = basePath + ext;
    if (existsSync(withExt)) return withExt;
  }
  for (const ext of [".ts", ".tsx"]) {
    const indexPath = join(basePath, `index${ext}`);
    if (existsSync(indexPath)) return indexPath;
  }
  return null;
}

/**
 * Resolves an import specifier written in `fromFile` to an absolute path on
 * disk, or null for anything this checker can't or shouldn't follow (a bare
 * package specifier, `astro:*` virtual modules, a CSS/asset import, or a
 * path that doesn't resolve to a real file). Mirrors tsconfig.json's
 * `"@/*": ["./*"]` alias, which resolves against FRONTEND_ROOT.
 */
export function resolveImportPath(
  fromFile: string,
  specifier: string,
  root: string = FRONTEND_ROOT,
): string | null {
  if (/\.(css|svg|png|jpe?g|gif|webp|json)$/.test(specifier)) return null;

  let basePath: string;
  if (specifier.startsWith("@/")) {
    basePath = join(root, specifier.slice(2));
  } else if (specifier.startsWith(".")) {
    basePath = join(dirname(fromFile), specifier);
  } else {
    // Bare specifier: npm package, or an `astro:*` / `bun:*` virtual
    // module. Neither is part of this repo's own import graph.
    return null;
  }

  return tryResolve(basePath);
}

function toRepoRelative(absPath: string, root: string): string {
  return relative(root, absPath).split("\\").join("/");
}

function readImportsOf(absPath: string): string[] {
  let source: string;
  try {
    source = readFileSync(absPath, "utf-8");
  } catch {
    return [];
  }
  const script = absPath.endsWith(".astro")
    ? extractAstroFrontmatter(source)
    : source;
  return extractImportSpecifiers(script);
}

export interface Violation {
  /** The server-rendered entry point that reaches a forbidden module. */
  entry: string;
  /** The full import chain, entry first and the forbidden module last. */
  chain: string[];
}

/**
 * Walks the import graph from every server-rendered entry point (every
 * .astro file under src/pages and src/layouts, plus src/middleware.ts) and
 * reports every reachable FORBIDDEN_MODULES hit, stopping recursion at any
 * src/islands/ file (see the file banner for why that boundary is
 * intentional).
 */
export function findSsrSignalStoreLeaks(
  entryPoints: string[] = defaultEntryPoints(),
  root: string = FRONTEND_ROOT,
): Violation[] {
  const violations: Violation[] = [];

  for (const entryAbs of entryPoints) {
    const entryRel = toRepoRelative(entryAbs, root);
    const visited = new Set<string>();
    const stack: { path: string; chain: string[] }[] = [
      { path: entryAbs, chain: [entryRel] },
    ];

    while (stack.length > 0) {
      const { path: currentAbs, chain } = stack.pop()!;
      if (visited.has(currentAbs)) continue;
      visited.add(currentAbs);

      for (const specifier of readImportsOf(currentAbs)) {
        const resolved = resolveImportPath(currentAbs, specifier, root);
        if (!resolved) continue;
        const resolvedRel = toRepoRelative(resolved, root);
        const nextChain = [...chain, resolvedRel];

        if (FORBIDDEN_MODULES.has(resolvedRel)) {
          violations.push({ entry: entryRel, chain: nextChain });
          continue;
        }
        if (isIslandPath(resolvedRel)) continue; // sanctioned boundary
        if (visited.has(resolved)) continue;
        stack.push({ path: resolved, chain: nextChain });
      }
    }
  }

  return violations;
}

function walkAstroFiles(dirAbs: string, out: string[]): void {
  if (!existsSync(dirAbs)) return;
  for (const entry of readdirSync(dirAbs)) {
    const full = join(dirAbs, entry);
    const stat = statSync(full);
    if (stat.isDirectory()) {
      walkAstroFiles(full, out);
    } else if (entry.endsWith(".astro")) {
      out.push(full);
    }
  }
}

export function defaultEntryPoints(): string[] {
  const entries: string[] = [];
  walkAstroFiles(join(FRONTEND_ROOT, "src", "pages"), entries);
  walkAstroFiles(join(FRONTEND_ROOT, "src", "layouts"), entries);
  const middleware = join(FRONTEND_ROOT, "src", "middleware.ts");
  if (existsSync(middleware)) entries.push(middleware);
  return entries;
}

function main(): void {
  const violations = findSsrSignalStoreLeaks();

  if (violations.length > 0) {
    console.error(
      "Signal-store SSR-boundary guard: a server-rendered entry point can " +
        "reach a client-only signal store without going through an island:",
    );
    for (const v of violations) {
      console.error(
        `  ${v.entry}\n    -> ${v.chain.slice(1).join("\n    -> ")}`,
      );
    }
    console.error(
      "\nModule-level signals in these files are process-global " +
        "singletons (see frontend/lib/api.ts's header comment) -- an SSR " +
        "import outside an island leaks state across requests. Either " +
        "route the read through a mounted island, or remove the import.",
    );
    process.exit(1);
  }

  console.log(
    "Signal-store SSR-boundary guard: no server-rendered entry point " +
      "reaches a signal store outside an island.",
  );
}

if (import.meta.main) {
  main();
}
