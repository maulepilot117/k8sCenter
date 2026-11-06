/**
 * File discovery shared by the build guards that scan application source
 * (check-internal-links.ts, check-tailwind-colors.ts).
 */

import { type Dirent, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/** The frontend directory: the root the guards resolve paths against. */
export const FRONTEND_ROOT = dirname(dirname(fileURLToPath(import.meta.url)));

const SOURCE_DIRS = ["src", "components", "lib"];
const SKIP_DIRS = new Set(["node_modules", "dist", ".astro"]);
const SOURCE_FILE = /^(?!.*_test\.).+\.(tsx|ts|astro)$/;

/**
 * Every file under `dir` whose name matches `match`, skipping dependency and
 * build directories. A directory that does not exist yields nothing.
 */
export function findFiles(dir: string, match: RegExp): string[] {
  const out: string[] = [];
  const walk = (current: string) => {
    let entries: Dirent[];
    try {
      entries = readdirSync(current, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      if (SKIP_DIRS.has(entry.name)) continue;
      const full = join(current, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (match.test(entry.name)) out.push(full);
    }
  };
  walk(dir);
  return out;
}

/** The application's source files under `root` (src, components, lib), tests excluded. */
export function sourceFiles(root: string): string[] {
  return SOURCE_DIRS.flatMap((dir) => findFiles(join(root, dir), SOURCE_FILE));
}
