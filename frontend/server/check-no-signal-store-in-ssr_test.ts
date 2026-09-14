import { afterAll, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  defaultEntryPoints,
  extractAstroFrontmatter,
  extractImportSpecifiers,
  findSsrSignalStoreLeaks,
  isIslandPath,
  resolveImportPath,
} from "./check-no-signal-store-in-ssr.ts";

// --- pure-function unit coverage ---

test("extractAstroFrontmatter returns only the fenced script, not the template", () => {
  const source = `---
import Foo from "./Foo.astro";
const x = 1;
---
<div>import "not-real" from "template text";</div>
`;
  const fm = extractAstroFrontmatter(source);
  expect(fm).toContain('import Foo from "./Foo.astro"');
  expect(fm).not.toContain("not-real");
});

test("extractAstroFrontmatter returns empty string for a file with no frontmatter fence", () => {
  expect(extractAstroFrontmatter("<div>just markup</div>")).toBe("");
});

test("extractImportSpecifiers finds static imports, re-exports, dynamic imports, and bare imports", () => {
  const source = `
    import { a } from "./a.ts";
    import b from "@/lib/b.ts";
    export { c } from "./c.ts";
    const mod = await import("./d.ts");
    import "./e.css";
  `;
  expect(extractImportSpecifiers(source)).toEqual([
    "./a.ts",
    "@/lib/b.ts",
    "./c.ts",
    "./d.ts",
    "./e.css",
  ]);
});

test("isIslandPath recognizes both the Astro and Fresh islands directories", () => {
  expect(isIslandPath("src/islands/AlertBanner.tsx")).toBe(true);
  expect(isIslandPath("islands/AlertBanner.tsx")).toBe(true);
  expect(isIslandPath("src/lib/cluster.ts")).toBe(false);
  expect(isIslandPath("src/layouts/ChromeLayout.astro")).toBe(false);
});

// --- the real, committed repo ---

test("the real repo has no SSR entry point that reaches a signal store outside an island", () => {
  const violations = findSsrSignalStoreLeaks();
  expect(violations).toEqual([]);
});

test("defaultEntryPoints finds the real pages, layouts, and middleware.ts", () => {
  const entries = defaultEntryPoints();
  expect(entries.some((e) => e.endsWith("index.astro"))).toBe(true);
  expect(entries.some((e) => e.endsWith("ChromeLayout.astro"))).toBe(true);
});

// --- synthetic fixture: proves the checker actually fails on a real violation ---
//
// Built as real files under a temp root (rather than mocking the fs) so this
// exercises resolveImportPath's actual `@/` alias handling and
// findSsrSignalStoreLeaks's actual traversal -- the same code path `bun run
// server/check-no-signal-store-in-ssr.ts` runs, not a stand-in for it.

const fixtureRoot = mkdtempSync(join(tmpdir(), "ssr-boundary-fixture-"));
afterAll(() => rmSync(fixtureRoot, { recursive: true, force: true }));

function write(relPath: string, content: string): string {
  const abs = join(fixtureRoot, relPath);
  mkdirSync(join(abs, ".."), { recursive: true });
  writeFileSync(abs, content, "utf-8");
  return abs;
}

// A clean layout: imports only an island (sanctioned) and a plain leaf lib
// module (not a signal store) -- the baseline this suite proves stays green.
write(
  "src/islands/SafeIsland.tsx",
  `import { selectedCluster } from "@/src/lib/cluster.ts";
export default function SafeIsland() { return selectedCluster.value; }
`,
);
write(
  "src/lib/cluster.ts",
  `export const selectedCluster = { value: "local" };\n`,
);
write("src/lib/plain-helper.ts", `export const copy = "hello";\n`);
const cleanLayout = write(
  "src/layouts/CleanLayout.astro",
  `---
import SafeIsland from "../islands/SafeIsland.tsx";
import { copy } from "@/src/lib/plain-helper.ts";
---
<SafeIsland client:load />
<p>{copy}</p>
`,
);

// The violation: a page whose OWN frontmatter imports an innocuous-looking
// helper (not itself in FORBIDDEN_MODULES, so a direct-import-only check
// would pass it) that in turn imports lib/cluster.ts. This is the shape
// frontend/lib/api.ts and frontend/lib/auth.ts actually have in the real
// repo -- neither name appears in a naive "does this page import a signal
// store" grep, but both reach one a hop later.
write(
  "lib/session-reader.ts",
  `import { selectedCluster } from "@/src/lib/cluster.ts";
export function currentCluster() { return selectedCluster.value; }
`,
);
const violatingPage = write(
  "src/pages/leaky.astro",
  `---
import { currentCluster } from "@/lib/session-reader.ts";
const data = currentCluster();
---
<p>{data}</p>
`,
);

test("a clean fixture layout that only reaches a signal store through an island reports no violation", () => {
  const violations = findSsrSignalStoreLeaks([cleanLayout], fixtureRoot);
  expect(violations).toEqual([]);
});

test("resolveImportPath follows the @/ alias against the given root", () => {
  const resolved = resolveImportPath(
    violatingPage,
    "@/lib/session-reader.ts",
    fixtureRoot,
  );
  expect(resolved).toBe(join(fixtureRoot, "lib", "session-reader.ts"));
});

test("a page whose frontmatter imports an innocuous helper is flagged two hops deep, via lib/cluster.ts", () => {
  const violations = findSsrSignalStoreLeaks([violatingPage], fixtureRoot);
  expect(violations).toHaveLength(1);
  expect(violations[0]!.entry).toBe("src/pages/leaky.astro");
  expect(violations[0]!.chain).toEqual([
    "src/pages/leaky.astro",
    "lib/session-reader.ts",
    "src/lib/cluster.ts",
  ]);
});

test("removing the direct import (deliberate fix) clears the violation", () => {
  // Same fixture, but the entry point points at a page with no such import
  // -- the "introduce, observe failure, then remove it" scenario without
  // mutating the committed repo.
  const fixedPage = write(
    "src/pages/fixed.astro",
    `---
// no api.ts import
---
<p>fixed</p>
`,
  );
  const violations = findSsrSignalStoreLeaks([fixedPage], fixtureRoot);
  expect(violations).toEqual([]);
});
