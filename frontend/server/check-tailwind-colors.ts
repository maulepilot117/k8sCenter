/**
 * Build-time guard: every colour-style Tailwind class in the source must
 * produce CSS.
 *
 * Tailwind v4 generates a colour utility only for a `--color-*` token in the
 * `@theme` block of assets/styles.css. A class naming a token that does not
 * exist (`bg-bg-elevated`, `text-status-error`, `border-border-emphasis`)
 * compiles to nothing: no error, no warning, just a transparent background or
 * the default text colour.
 *
 * Which classes produce CSS is decided by Tailwind itself, not by a list kept
 * here: the guard loads the app's own stylesheet into Tailwind's design system
 * and asks it for each class's CSS (`candidatesToCss`, the call Tailwind's
 * editor tooling makes). That API is marked unstable; if a Tailwind upgrade
 * changes it, check-tailwind-colors_test.ts fails rather than this guard
 * passing silently.
 *
 * Candidates are the whitespace-separated words of every string literal in
 * src/, components/ and lib/ (tests excluded) whose utility part, after any
 * variants such as `hover:`, starts with a colour-taking prefix. Words with an
 * interpolation are skipped, and so are object keys ("stroke-width": …).
 */

import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, relative, sep } from "node:path";
import { __unstable__loadDesignSystem } from "tailwindcss";
import { FRONTEND_ROOT, sourceFiles } from "./source-files.ts";

/** Utility prefixes that take a colour. */
const COLOUR_PREFIX =
  /^(bg|text|border|ring|outline|divide|fill|stroke|from|via|to|placeholder|caret|decoration|accent|shadow)-/;

/** Characters a Tailwind class can contain, arbitrary values included. */
const CLASS_CHARS = /^[a-z0-9:!\-/.[\]%_#(),]+$/i;

/**
 * CSS words that share a colour prefix and turn up in inline style values
 * (`boxSizing: "border-box"`, `transition: "border-color 0.15s"`). They are
 * not classes.
 */
const CSS_WORDS = new Set([
  "border-box",
  "border-color",
  "border-width",
  "background-color",
  "stroke-dashoffset",
  "stroke-width",
  "text-shadow",
]);

/**
 * Removes every match of `comment`, repeating until nothing changes. One pass
 * is not enough: removing a comment nested inside another's opener
 * (`<!<!-- x -->-- y -->`) leaves a new opener behind.
 */
function stripUntilStable(text: string, comment: RegExp): string {
  let previous: string;
  let current = text;
  do {
    previous = current;
    current = current.replace(comment, " ");
  } while (current !== previous);
  return current;
}

/**
 * The colour-style class candidates in `source`. Comments are dropped first
 * (block comments, and whole-line `//` ones: a trailing `//` may be part of a
 * URL). Template literals are read with their `${…}` parts removed, and
 * quoted strings are read wherever they are, so the branches of
 * ``class={`p-2 ${on ? "a" : "b"}`}`` count. A quoted string that is an
 * object key (followed by ":" and preceded by "{", "," or a line start) is
 * skipped; a ternary branch is not.
 */
export function extractCandidates(raw: string): string[] {
  const source = stripUntilStable(
    stripUntilStable(raw, /\/\*[\s\S]*?\*\//g),
    /<!--[\s\S]*?-->/g,
  ).replace(/^\s*\/\/.*$/gm, "");
  const found = new Set<string>();
  const addWords = (text: string) => {
    for (const word of text.split(/\s+/)) {
      if (!word || word.includes("$") || !CLASS_CHARS.test(word)) continue;
      if (CSS_WORDS.has(word)) continue;
      const utility = (word.split(":").at(-1) as string).replace(/^!/, "");
      if (!COLOUR_PREFIX.test(utility) || utility.endsWith("-")) continue;
      found.add(word);
    }
  };
  for (const m of source.matchAll(/`([^`]*)`/g)) {
    addWords(m[1].replace(/\$\{(?:[^{}]|\{[^{}]*\})*\}/g, " "));
  }
  for (const m of source.matchAll(/(["'])((?:(?!\1)[^\n\\])*)\1/g)) {
    const end = (m.index ?? 0) + m[0].length;
    const before = source.slice(0, m.index).trimEnd().at(-1);
    const isKey =
      /^\s*:/.test(source.slice(end)) &&
      (before === undefined ||
        before === "{" ||
        before === "," ||
        /\n\s*$/.test(source.slice(0, m.index)));
    if (!isKey) addWords(m[2]);
  }
  return [...found];
}

/** Classes as Tailwind sees them: which produce CSS. */
export interface ClassResolver {
  candidatesToCss(classes: string[]): (string | null)[];
}

/** Loads assets/styles.css into Tailwind's design system. */
export async function loadResolver(
  root: string = FRONTEND_ROOT,
): Promise<ClassResolver> {
  const require = createRequire(join(root, "package.json"));
  const assets = join(root, "assets");
  const css = readFileSync(join(assets, "styles.css"), "utf8");
  return __unstable__loadDesignSystem(css, {
    base: assets,
    async loadStylesheet(id: string, base: string) {
      // "tailwindcss" and the app's own relative imports carry utilities and
      // tokens. A package stylesheet (xterm's) carries neither.
      const path =
        id === "tailwindcss"
          ? require.resolve("tailwindcss/index.css")
          : id.startsWith(".")
            ? join(base, id)
            : null;
      if (path === null) return { path: id, base, content: "" };
      return { path, base: dirname(path), content: readFileSync(path, "utf8") };
    },
  });
}

export interface ColourOffender {
  className: string;
  files: string[];
}

export function findUnknownColourClasses(
  resolver: ClassResolver,
  root: string = FRONTEND_ROOT,
): { candidates: number; offenders: ColourOffender[] } {
  const usedIn = new Map<string, Set<string>>();
  for (const file of sourceFiles(root)) {
    const rel = relative(root, file).split(sep).join("/");
    for (const c of extractCandidates(readFileSync(file, "utf8"))) {
      if (!usedIn.has(c)) usedIn.set(c, new Set());
      usedIn.get(c)?.add(rel);
    }
  }
  const classes = [...usedIn.keys()].sort();
  const css = resolver.candidatesToCss(classes);
  const offenders = classes
    .filter((_, i) => css[i] === null)
    .map((className) => ({
      className,
      files: [...(usedIn.get(className) ?? [])].sort(),
    }));
  return { candidates: classes.length, offenders };
}

async function main(): Promise<void> {
  const { candidates, offenders } = findUnknownColourClasses(
    await loadResolver(),
  );
  if (offenders.length > 0) {
    console.error(
      `Tailwind colour guard: ${offenders.length} class(es) produce no CSS:`,
    );
    for (const o of offenders) {
      console.error(`  ${o.className}  (${o.files.join(", ")})`);
    }
    console.error(
      "Each names a colour token that is not in the @theme block of " +
        "assets/styles.css, so it renders as nothing. Use an existing " +
        "--color-* token (bg-elevated, text-text-muted, border-border-primary, " +
        "bg-danger, …), or add the token there.",
    );
    process.exit(1);
  }
  console.log(
    `Tailwind colour guard: all ${candidates} colour classes produce CSS.`,
  );
}

if (import.meta.main) {
  await main();
}
