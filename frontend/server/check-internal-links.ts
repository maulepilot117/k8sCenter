/**
 * Build-time guard: every internal link written in the source must resolve
 * to a page under src/pages.
 *
 * A link to a page that does not exist is not an error anywhere: the build
 * passes, the type checker is happy, and the user gets a 404. The 2026-11-05
 * UI walkthrough found five (a Certificate's Secret link under /workloads
 * instead of /config, Velero rows linking to detail pages that were never
 * built, the PromQL gate linking to a /cluster/dashboard that was renamed).
 *
 * What it reads: `href="…"`, `href={`…`}`, `href: "…"` and
 * `location.href = "…"` (string or template literal) values starting with "/" in src/, components/ and
 * lib/. A `${…}` part matches any one path segment. A link whose first
 * segment is interpolated (`${base}/x`) cannot be resolved statically and is
 * skipped, as are /api/ and /ws/ paths and links to files (a dot in the last
 * segment). The query string and fragment are ignored.
 */

import { type Dirent, readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const FRONTEND_ROOT = dirname(dirname(fileURLToPath(import.meta.url)));
const SOURCE_DIRS = ["src", "components", "lib"];
const SKIP_DIRS = new Set(["node_modules", "dist", ".astro"]);

/** A route under src/pages: one entry per path segment. */
type Route = string[];

/** Marks a link segment built from an interpolation. */
const ANY = "\u0000";

export interface LinkOffender {
  file: string;
  line: number;
  href: string;
}

/** The route each page file serves, from its path under src/pages. */
export function routeFromPagePath(pagePath: string): Route {
  const parts = pagePath
    .replace(/\.astro$/, "")
    .split("/")
    .filter(Boolean);
  if (parts.at(-1) === "index") parts.pop();
  return parts;
}

/**
 * The path part of a link as segments, or null when it is not checked: not
 * internal, an API or WebSocket path, a file, or a first segment that is
 * itself interpolated.
 */
export function linkSegments(href: string): string[] | null {
  if (!href.startsWith("/") || href.startsWith("//")) return null;
  // Interpolations become a placeholder first, so a "?" or "#" inside one
  // does not end the path early.
  const flat = href.replace(/\$\{[^}]*\}/g, ANY);
  const path = flat.split(/[?#]/)[0];
  const segments = path.split("/").filter(Boolean);
  if (segments.length === 0) return [];
  if (segments[0] === "api" || segments[0] === "ws") return null;
  if (segments[0].includes(ANY)) return null;
  const last = segments.at(-1) as string;
  if (!last.includes(ANY) && last.includes(".")) return null;
  return segments;
}

function segmentMatches(route: string, link: string): boolean {
  if (route.startsWith("[") && route.endsWith("]")) return true;
  if (link.includes(ANY)) {
    // "prefix${x}suffix" could produce the route's literal segment.
    const [head, ...rest] = link.split(ANY);
    const tail = rest.at(-1) ?? "";
    return route.startsWith(head) && route.endsWith(tail);
  }
  return route === link;
}

export function resolves(link: string[], routes: Route[]): boolean {
  return routes.some(
    (route) =>
      route.length === link.length &&
      route.every((seg, i) => segmentMatches(seg, link[i])),
  );
}

/** Every checked internal link in `source`, with its 1-based line. */
export function extractLinks(source: string): { line: number; href: string }[] {
  const patterns = [
    // href="/x" in JSX, and location.href = "/x" or = `/x/${y}`
    /\bhref\s*=\s*[`"'](\/[^`"']*)[`"']/g,
    // href={`/x/${y}`} or href={"/x"}
    /\bhref\s*=\s*\{\s*[`"'](\/[^`"']*)[`"']\s*\}/g,
    // { href: "/x" } or { href: `/x/${y}` }
    /\bhref\s*:\s*[`"'](\/[^`"']*)[`"']/g,
  ];
  const found: { line: number; href: string }[] = [];
  for (const re of patterns) {
    for (const m of source.matchAll(re)) {
      const line = source.slice(0, m.index).split("\n").length;
      found.push({ line, href: m[1] });
    }
  }
  return found.sort((a, b) => a.line - b.line);
}

function walk(dir: string, out: string[], match: RegExp): void {
  let entries: Dirent[];
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    if (SKIP_DIRS.has(entry.name)) continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out, match);
    else if (match.test(entry.name)) out.push(full);
  }
}

export function loadRoutes(root: string = FRONTEND_ROOT): Route[] {
  const pagesDir = join(root, "src", "pages");
  const pages: string[] = [];
  walk(pagesDir, pages, /\.astro$/);
  return pages.map((p) =>
    routeFromPagePath(relative(pagesDir, p).split(sep).join("/")),
  );
}

export function checkInternalLinks(root: string = FRONTEND_ROOT): {
  routes: number;
  links: number;
  offenders: LinkOffender[];
} {
  const routes = loadRoutes(root);
  const files: string[] = [];
  for (const dir of SOURCE_DIRS) {
    walk(join(root, dir), files, /^(?!.*_test\.)[^.]+.*\.(tsx|ts|astro)$/);
  }
  const offenders: LinkOffender[] = [];
  let links = 0;
  for (const file of files) {
    const source = readFileSync(file, "utf8");
    for (const { line, href } of extractLinks(source)) {
      const segments = linkSegments(href);
      if (segments === null) continue;
      links++;
      if (!resolves(segments, routes)) {
        offenders.push({
          file: relative(root, file).split(sep).join("/"),
          line,
          href,
        });
      }
    }
  }
  offenders.sort((a, b) =>
    a.file === b.file ? a.line - b.line : a.file < b.file ? -1 : 1,
  );
  return { routes: routes.length, links, offenders };
}

function main(): void {
  const { routes, links, offenders } = checkInternalLinks();
  if (routes === 0) {
    console.error("Internal link guard: found no pages under src/pages.");
    process.exit(1);
  }
  if (offenders.length > 0) {
    console.error(
      `Internal link guard: ${offenders.length} link(s) match no page under src/pages:`,
    );
    for (const o of offenders) {
      console.error(`  ${o.file}:${o.line}  ${o.href}`);
    }
    console.error(
      "Each would 404. Point it at an existing page, or add the page it means.",
    );
    process.exit(1);
  }
  console.log(
    `Internal link guard: all ${links} internal links resolve to one of ${routes} pages.`,
  );
}

if (import.meta.main) {
  main();
}
