/**
 * Build-time guard: every CSS custom property the frontend reads is defined
 * somewhere.
 *
 * `var(--brand)` with no `--brand` declared resolves to the property's initial
 * value: no stroke, no fill, a transparent background, inherited text colour.
 * Nothing warns. The theme's tokens are the raw names in
 * assets/themes.generated.css and the :root block of assets/styles.css
 * (`--accent`, `--bg-surface`, `--error`); the `--color-*` names in its
 * `@theme` block are Tailwind's bridge for utility classes, and charts take
 * theirs from CHART_TOKENS in components/charts/chart-theme.ts.
 *
 * A property counts as defined when the tree declares it anywhere: a
 * `--x: …` declaration or `@property --x` rule in assets/*.css, or an
 * inline-style key (`{ "--x": … }`) or `setProperty("--x", …)` in source.
 * A reference is `var(--x)` (with or without a fallback) or Tailwind's
 * `(--x)` shorthand, in assets/*.css or in src/, components/ and lib/
 * (tests excluded). Comments are dropped first.
 *
 * Tailwind's own default-theme variables (`--color-red-500`, `--spacing`) do
 * not count as defined: Tailwind emits one only when a utility uses it, so a
 * `var()` naming one directly can still resolve to nothing.
 */

import { readFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { FRONTEND_ROOT, findFiles, sourceFiles } from "./source-files.ts";

const NAME = "--[A-Za-z0-9_-]+";

/** Removes block, HTML and whole-line `//` comments, keeping line numbers. */
function stripComments(text: string): string {
  const blank = (m: string) => m.replace(/[^\n]/g, " ");
  return text
    .replace(/\/\*[\s\S]*?\*\//g, blank)
    .replace(/<!--[\s\S]*?-->/g, blank)
    .replace(/^\s*\/\/.*$/gm, blank);
}

/** The custom properties `text` defines. */
export function declaredProperties(text: string): Set<string> {
  const code = stripComments(text);
  const found = new Set<string>();
  const patterns = [
    // A declaration `--x:`, or an inline-style key `"--x":`.
    new RegExp(`(?<![\\w-])["']?(${NAME})["']?\\s*:`, "g"),
    new RegExp(`@property\\s+(${NAME})`, "g"),
    new RegExp(`setProperty\\(\\s*["'\`](${NAME})`, "g"),
  ];
  for (const pattern of patterns) {
    for (const m of code.matchAll(pattern)) found.add(m[1]);
  }
  return found;
}

export interface Reference {
  name: string;
  line: number;
}

/** The custom properties `text` reads, in order, with their line. */
export function referencedProperties(text: string): Reference[] {
  const code = stripComments(text);
  const refs: { name: string; index: number }[] = [];
  for (const m of code.matchAll(new RegExp(`var\\(\\s*(${NAME})`, "g"))) {
    refs.push({ name: m[1], index: m.index ?? 0 });
  }
  // Tailwind v4's shorthand: `bg-(--surface)`, `fill-(--accent)`.
  for (const m of code.matchAll(new RegExp(`[a-z]-\\((${NAME})\\)`, "g"))) {
    refs.push({ name: m[1], index: m.index ?? 0 });
  }
  refs.sort((a, b) => a.index - b.index);
  return refs.map((r) => ({
    name: r.name,
    line: code.slice(0, r.index).split("\n").length,
  }));
}

export interface Finding {
  file: string;
  line: number;
  name: string;
}

export function checkTree(root: string = FRONTEND_ROOT): Finding[] {
  const files = [
    ...findFiles(join(root, "assets"), /\.css$/),
    ...sourceFiles(root),
  ].map((path) => ({
    file: relative(root, path).split(sep).join("/"),
    text: readFileSync(path, "utf8"),
  }));

  const defined = new Set<string>();
  for (const { text } of files) {
    for (const name of declaredProperties(text)) defined.add(name);
  }

  const findings: Finding[] = [];
  for (const { file, text } of files) {
    for (const ref of referencedProperties(text)) {
      if (!defined.has(ref.name)) findings.push({ file, ...ref });
    }
  }
  findings.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
  return findings;
}

function main(): void {
  const findings = checkTree();
  if (findings.length > 0) {
    console.error(
      `CSS custom property guard: ${findings.length} reference(s) to a property nothing defines:`,
    );
    for (const f of findings) {
      console.error(`  ${f.file}:${f.line}  var(${f.name})`);
    }
    console.error(
      "An undefined property draws nothing. Use a theme token from " +
        "assets/themes.generated.css or assets/styles.css (--accent, " +
        "--bg-surface, --error …), or CHART_TOKENS for chart strokes.",
    );
    process.exit(1);
  }
  console.log(
    "CSS custom property guard: every var(--…) names a defined property.",
  );
}

if (import.meta.main) {
  main();
}
