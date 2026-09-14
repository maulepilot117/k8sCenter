import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { extractAstroFrontmatter } from "./check-no-signal-store-in-ssr.ts";

/**
 * U8's standing guard for KTD4: every island hydrates with `client:load`,
 * and any lazier (or missing) directive is a parity defect, not an
 * optimization. Fresh hydrates every island on a page unconditionally;
 * Astro requires each call site to opt in explicitly, which means a
 * forgotten directive is silent at both `astro check` and `astro build` —
 * the page still renders its SSR markup, it just never hydrates. This
 * check is what actually catches that.
 *
 * Scope: every .astro file under src/pages and src/layouts (the same
 * entry-point set check-no-signal-store-in-ssr.ts uses, for the same
 * reason — these are the files Astro's router or ChromeLayout render
 * directly). For each entry point, this resolves every default-imported
 * local name whose specifier points at a file under src/islands/, then
 * scans the template (the markup below the frontmatter fence) for every
 * `<LocalName ...>` call site and requires a `client:*` directive
 * somewhere in that tag's attribute list.
 *
 * Deliberately NOT checked: islands imported and mounted from *inside*
 * another island's own .tsx source (e.g. TopBarV2.tsx mounting
 * ClusterSwitcher, ResourceDetail.tsx mounting PodTerminal). Those are
 * plain Preact composition inside an already-hydrated component tree, not
 * a second Astro-level hydration boundary — nothing to annotate there.
 */

const HERE = dirname(fileURLToPath(import.meta.url));
export const FRONTEND_ROOT = resolve(HERE, "..");

const RESOLVABLE_EXTENSIONS = [".ts", ".tsx", ".astro", ".js", ".jsx"];

function tryResolve(basePath: string): string | null {
  if (existsSync(basePath) && !basePath.endsWith("/")) {
    return basePath;
  }
  for (const ext of RESOLVABLE_EXTENSIONS) {
    const withExt = basePath + ext;
    if (existsSync(withExt)) return withExt;
  }
  return null;
}

function toRepoRelative(absPath: string, root: string): string {
  return relative(root, absPath).split("\\").join("/");
}

/** Resolves a relative or `@/`-rooted import specifier to an absolute path. */
export function resolveSpecifier(
  fromFile: string,
  specifier: string,
  root: string = FRONTEND_ROOT,
): string | null {
  let basePath: string;
  if (specifier.startsWith("@/")) {
    basePath = join(root, specifier.slice(2));
  } else if (specifier.startsWith(".")) {
    basePath = join(dirname(fromFile), specifier);
  } else {
    return null; // bare package specifier — never an island
  }
  return tryResolve(basePath);
}

export interface IslandImport {
  /** The local identifier the template references, e.g. "ResourceDetail". */
  local: string;
  /** Repo-relative path to the resolved island file. */
  resolved: string;
}

/**
 * Parses an .astro file's frontmatter for `import Name from "spec";`
 * default imports (the only import shape this route tree uses for
 * islands) and returns the ones that resolve under src/islands/.
 */
export function findIslandImports(
  absPath: string,
  root: string = FRONTEND_ROOT,
): IslandImport[] {
  const source = readFileSync(absPath, "utf-8");
  const frontmatter = extractAstroFrontmatter(source);
  const out: IslandImport[] = [];
  const importRe = /^import\s+(\w+)\s+from\s+["']([^"']+)["'];?\s*$/gm;
  for (const match of frontmatter.matchAll(importRe)) {
    const [, local, specifier] = match;
    const resolved = resolveSpecifier(absPath, specifier, root);
    if (!resolved) continue;
    const rel = toRepoRelative(resolved, root);
    if (rel.startsWith("src/islands/") || rel.startsWith("islands/")) {
      out.push({ local, resolved: rel });
    }
  }
  return out;
}

/** Everything below the second `---` fence — the Astro template markup. */
export function extractAstroTemplate(source: string): string {
  const match = /^---\r?\n[\s\S]*?\r?\n---\r?\n/.exec(source);
  return match ? source.slice(match[0].length) : source;
}

/**
 * Finds each `<Name ...>` / `<Name ... />` call site of `name` in `template`
 * and returns the full tag text for each, so callers can inspect its
 * attributes. Brace-depth aware so a `>` inside a `style={{...}}` or
 * comparison expression doesn't end the tag early, and quote-aware so a
 * `>` inside a string attribute value doesn't either.
 */
export function findTagCallSites(template: string, name: string): string[] {
  const tags: string[] = [];
  const openRe = new RegExp(`<${name}(?=[\\s/>])`, "g");
  for (const match of template.matchAll(openRe)) {
    const start = match.index ?? 0;
    let i = start;
    let depth = 0;
    let inStr: string | null = null;
    let end = -1;
    while (i < template.length) {
      const c = template[i];
      if (inStr) {
        if (c === inStr && template[i - 1] !== "\\") inStr = null;
      } else if (c === '"' || c === "'" || c === "`") {
        inStr = c;
      } else if (c === "{") {
        depth++;
      } else if (c === "}") {
        depth--;
      } else if (depth === 0 && c === ">") {
        end = i;
        break;
      }
      i++;
    }
    if (end === -1) continue; // unterminated tag — ignore, not this check's job
    tags.push(template.slice(start, end + 1));
  }
  return tags;
}

export interface Violation {
  file: string;
  island: string;
  tag: string;
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

export function defaultEntryPoints(root: string = FRONTEND_ROOT): string[] {
  const entries: string[] = [];
  walkAstroFiles(join(root, "src", "pages"), entries);
  walkAstroFiles(join(root, "src", "layouts"), entries);
  return entries;
}

const CLIENT_DIRECTIVE_PATTERN = /\bclient:[a-zA-Z-]+/;

export function findMissingHydrationDirectives(
  entryPoints: string[] = defaultEntryPoints(),
  root: string = FRONTEND_ROOT,
): Violation[] {
  const violations: Violation[] = [];
  for (const absPath of entryPoints) {
    const source = readFileSync(absPath, "utf-8");
    const template = extractAstroTemplate(source);
    const islandImports = findIslandImports(absPath, root);
    const fileRel = toRepoRelative(absPath, root);
    for (const { local } of islandImports) {
      const tags = findTagCallSites(template, local);
      for (const tag of tags) {
        if (!CLIENT_DIRECTIVE_PATTERN.test(tag)) {
          violations.push({ file: fileRel, island: local, tag });
        }
      }
    }
  }
  return violations;
}

function main(): void {
  const violations = findMissingHydrationDirectives();

  if (violations.length > 0) {
    console.error(
      "Island hydration guard (KTD4): every island call site must carry a " +
        "client:* directive (client:load, per KTD4 — any other directive " +
        "is also a parity defect, but this check only catches a missing " +
        "one):",
    );
    for (const v of violations) {
      console.error(`  ${v.file}: <${v.island}> — ${v.tag.split("\n")[0]}…`);
    }
    process.exit(1);
  }

  console.log(
    "Island hydration guard: every island call site under src/pages and " +
      "src/layouts carries a client:* directive.",
  );
}

if (import.meta.main) {
  main();
}
