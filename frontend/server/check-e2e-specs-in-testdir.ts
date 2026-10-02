/**
 * Build-time guard: every Playwright spec under e2e/ must sit inside the
 * config's `testDir`.
 *
 * Playwright only discovers tests under `testDir` (e2e/playwright.config.ts
 * sets it to ./tests). A spec saved anywhere else is not an error to
 * Playwright -- it is simply never collected, so the suite goes on passing
 * while those tests never run. Three specs (velero, flux-notifications,
 * namespace-limits) sat at the e2e/ root that way for months, about 900 lines
 * that had never executed (defect #4 in docs/plans/2026-09-10-EXECUTION-ORDER.md).
 *
 * That failure is invisible by construction, which is why it gets a guard
 * rather than a review checklist line.
 */

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const REPO_ROOT = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const DEFAULT_E2E_DIR = join(REPO_ROOT, "e2e");

/**
 * Playwright's default `testMatch`: `**\/*.@(spec|test).?(c|m)[jt]s?(x)`.
 * The config sets no testMatch of its own for the suite, so this is what
 * decides whether a file is a test file.
 */
const TEST_FILE = /\.(spec|test)\.[cm]?[jt]sx?$/;

/**
 * Directories that hold tooling output or dependencies, never authored specs.
 * `playwright` holds the saved auth state.
 */
const SKIP_DIRS = new Set([
  "node_modules",
  "playwright-report",
  "test-results",
  "playwright",
  "blob-report",
]);

export interface E2eSpecCheck {
  ok: boolean;
  /** Absolute testDir resolved from the config, or null if unreadable. */
  testDir: string | null;
  /** Spec files outside testDir, relative to the e2e directory. */
  offenders: string[];
  /** Number of spec files found inside testDir. */
  specsInTestDir: number;
  /** Why the check could not run, when it could not. */
  error?: string;
}

/**
 * The top-level `testDir` from a Playwright config's source.
 *
 * Parsed rather than imported: importing the config needs @playwright/test,
 * which lives in e2e/node_modules and is not installed where this guard runs
 * (the frontend job). The top-level key is the first `testDir:` in the file;
 * project-level ones (the setup project's ./fixtures) come after it, inside
 * `projects`.
 */
export function readTestDir(configSource: string): string | null {
  const projectsAt = configSource.search(/\bprojects\s*:/);
  const head =
    projectsAt === -1 ? configSource : configSource.slice(0, projectsAt);
  const match = head.match(/\btestDir\s*:\s*["'`]([^"'`]+)["'`]/);
  return match ? match[1] : null;
}

function walk(dir: string, out: string[]): void {
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walk(full, out);
    } else if (TEST_FILE.test(entry)) {
      out.push(full);
    }
  }
}

export function checkE2eSpecs(e2eDir: string = DEFAULT_E2E_DIR): E2eSpecCheck {
  const configPath = join(e2eDir, "playwright.config.ts");
  if (!existsSync(configPath)) {
    return {
      ok: false,
      testDir: null,
      offenders: [],
      specsInTestDir: 0,
      error: `no Playwright config at ${configPath}`,
    };
  }
  const declared = readTestDir(readFileSync(configPath, "utf8"));
  if (!declared) {
    return {
      ok: false,
      testDir: null,
      offenders: [],
      specsInTestDir: 0,
      error:
        "could not find a top-level testDir in playwright.config.ts; " +
        "update readTestDir if the config's shape changed",
    };
  }
  const testDir = resolve(e2eDir, declared);

  const specs: string[] = [];
  walk(e2eDir, specs);

  const offenders: string[] = [];
  let specsInTestDir = 0;
  for (const spec of specs) {
    if (spec.startsWith(testDir + sep)) {
      specsInTestDir++;
    } else {
      offenders.push(relative(e2eDir, spec).split(sep).join("/"));
    }
  }
  offenders.sort();

  if (specsInTestDir === 0) {
    return {
      ok: false,
      testDir,
      offenders,
      specsInTestDir,
      error: `testDir ${testDir} contains no spec files`,
    };
  }
  return { ok: offenders.length === 0, testDir, offenders, specsInTestDir };
}

function main(): void {
  const result = checkE2eSpecs();

  if (result.error) {
    console.error(`E2E spec guard: ${result.error}`);
    process.exit(1);
  }

  if (result.offenders.length > 0) {
    console.error(
      `E2E spec guard: these spec files are outside Playwright's testDir (${result.testDir}):`,
    );
    for (const f of result.offenders) console.error(`  e2e/${f}`);
    console.error(
      "Playwright never collects them, so they never run and nothing fails. " +
        "Move them under e2e/tests/ and import from ../fixtures/base.ts and " +
        "../helpers.ts like the other specs.",
    );
    process.exit(1);
  }

  console.log(
    `E2E spec guard: all ${result.specsInTestDir} spec files are inside testDir.`,
  );
}

if (import.meta.main) {
  main();
}
