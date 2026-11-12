/**
 * Build-time guard: the frontend runs on React, and nothing may bring Preact
 * back.
 *
 * 1. No file imports Preact: `preact`, `preact/*`, any `@preact/*` package,
 *    `@testing-library/preact` or `preact-render-to-string`, as an import, a
 *    re-export or a dynamic import, type-only imports included. A second UI
 *    runtime would load its own copy of hooks and context, and components
 *    from the two cannot render each other. `@preact/signals-core` has no UI
 *    runtime but is banned too: the shared stores use the in-repo
 *    `src/lib/signals.ts`, and a store built on a second signal library
 *    would not see the first one's signals, so updates would stop crossing
 *    between islands.
 *
 * 2. No file sets a `@jsxImportSource` pragma. `tsconfig.json` sets React's
 *    JSX runtime for every file, so a `react` pragma only repeats it, and any
 *    other pragma would compile that file's JSX against another runtime's
 *    element factory.
 *
 * 3. No JSX uses an attribute only Preact understands: `class`, `for`,
 *    `onFocusOut`/`onFocusIn`, or a hyphenated or namespaced SVG attribute
 *    (`stroke-width`, `xlink:href`). TypeScript accepts any hyphenated
 *    attribute name, so `astro check` does not catch the SVG spellings, and
 *    React renders them as unknown attributes with a console warning. `aria-*`
 *    and `data-*` are hyphenated in React too, and attributes on a custom
 *    element (a tag with a hyphen) are left alone.
 *
 * 4. No .astro page passes `class` to a component it imports. The React
 *    renderer hands `class` to the component as a prop named `class`, which
 *    a React component ignores, so the styling silently disappears. Pass
 *    `className`.
 *
 * React's own lint rules (hooks at top level, effect dependencies) come from
 * Biome's React domain.
 */

import { readFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { parse } from "@babel/parser";
import { FRONTEND_ROOT, findFiles } from "./source-files.ts";

const SCANNED_DIRS = ["src", "components", "lib", "server"];
const SCANNED_FILE = /\.(ts|tsx)$/;

const JSX_IMPORT_SOURCE = /@jsxImportSource\s+(\S+)/;

const PREACT_ONLY_ATTRIBUTES: Record<string, string> = {
  class: "className",
  for: "htmlFor",
  onFocusOut: "onBlur",
  onFocusIn: "onFocus",
};

export interface Finding {
  file: string;
  line: number;
  message: string;
}

export function isPreactSpecifier(s: string): boolean {
  return (
    s === "preact" ||
    s.startsWith("preact/") ||
    s.startsWith("@preact/") ||
    s === "@testing-library/preact" ||
    s === "preact-render-to-string" ||
    s.startsWith("preact-render-to-string/")
  );
}

function camelCase(name: string): string {
  return name.replace(/[-:]([a-z])/g, (_, c: string) => c.toUpperCase());
}

// biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
type Node = any;

function walk(node: Node, visit: (n: Node) => void): void {
  if (!node || typeof node !== "object") return;
  if (Array.isArray(node)) {
    for (const child of node) walk(child, visit);
    return;
  }
  if (typeof node.type === "string") visit(node);
  for (const key of Object.keys(node)) {
    if (key === "loc" || key === "start" || key === "end") continue;
    walk(node[key], visit);
  }
}

function importSource(node: Node): string | null {
  switch (node.type) {
    case "ImportDeclaration":
    case "ExportAllDeclaration":
      return node.source.value;
    case "ExportNamedDeclaration":
      return node.source ? node.source.value : null;
    case "CallExpression":
      return node.callee.type === "Import" &&
        node.arguments[0]?.type === "StringLiteral"
        ? node.arguments[0].value
        : null;
    case "ImportExpression":
      return node.source?.type === "StringLiteral" ? node.source.value : null;
    default:
      return null;
  }
}

function tagName(name: Node): string {
  if (name.type === "JSXIdentifier") return name.name;
  if (name.type === "JSXMemberExpression") {
    return `${tagName(name.object)}.${name.property.name}`;
  }
  return `${name.namespace.name}:${name.name.name}`;
}

function attributeFinding(attr: Node, tag: string): string | null {
  if (attr.type !== "JSXAttribute") return null;
  if (attr.name.type === "JSXNamespacedName") {
    const name = `${attr.name.namespace.name}:${attr.name.name.name}`;
    return `<${tag}> uses "${name}"; React spells it "${camelCase(name)}"`;
  }
  const name: string = attr.name.name;
  const replacement = PREACT_ONLY_ATTRIBUTES[name];
  if (replacement) {
    return `<${tag}> uses "${name}"; React spells it "${replacement}"`;
  }
  if (name.includes("-") && !/^(aria|data)-/.test(name) && !tag.includes("-")) {
    return `<${tag}> uses "${name}"; React spells it "${camelCase(name)}"`;
  }
  return null;
}

export function checkSource(code: string, file: string): Finding[] {
  let ast: Node;
  try {
    ast = parse(code, { sourceType: "module", plugins: ["typescript", "jsx"] });
  } catch (error) {
    throw new Error(`${file}: could not parse (${(error as Error).message})`);
  }

  const findings: Finding[] = [];
  // Every comment counts, not only those before the first statement: Bun's
  // transpiler still honours a pragma placed later in the file.
  for (const comment of ast.comments ?? []) {
    const pragma = JSX_IMPORT_SOURCE.exec(comment.value);
    if (pragma) {
      findings.push({
        file,
        line: comment.loc.start.line,
        message: `sets "@jsxImportSource ${pragma[1]}"; tsconfig.json sets the JSX runtime, and it is React`,
      });
    }
  }

  walk(ast.program, (node) => {
    const source = importSource(node);
    if (source !== null && isPreactSpecifier(source)) {
      findings.push({
        file,
        line: node.loc.start.line,
        message: `imports "${source}"; the frontend runs on React`,
      });
    }
    if (node.type === "JSXOpeningElement") {
      const tag = tagName(node.name);
      // Only DOM elements: a component's props are not attributes.
      if (!/^[a-z]/.test(tag)) return;
      for (const attr of node.attributes) {
        const message = attributeFinding(attr, tag);
        if (message)
          findings.push({ file, line: attr.loc.start.line, message });
      }
    }
  });
  return findings;
}

/**
 * Component tags in an .astro file that are given `class`, with the line of
 * each. Only tags imported in the frontmatter count; `class:list` is Astro's
 * own directive and is not a `class` attribute.
 */
export function astroClassPassed(
  code: string,
): { name: string; line: number }[] {
  const front = /^---\r?\n([\s\S]*?)\r?\n---/.exec(code);
  if (!front) return [];
  const imported = new Set<string>();
  for (const m of front[1].matchAll(/import\s+([A-Z]\w*)\s+from\s+["']/g)) {
    imported.add(m[1]);
  }
  for (const m of front[1].matchAll(/import\s*\{([^}]*)\}\s*from\s+["']/g)) {
    for (const part of m[1].split(",")) {
      const local = part
        .trim()
        .split(/\s+as\s+/)
        .pop();
      if (local) imported.add(local);
    }
  }
  const found: { name: string; line: number }[] = [];
  for (const m of code.matchAll(/<([A-Z]\w*)(\s[^>]*)?>/g)) {
    if (!imported.has(m[1]) || !m[2] || !/(^|\s)class\s*=/.test(m[2])) continue;
    const line = code.slice(0, m.index).split("\n").length;
    found.push({ name: m[1], line });
  }
  return found;
}

export function checkTree(root: string = FRONTEND_ROOT): Finding[] {
  const findings: Finding[] = [];
  const toRel = (path: string) => relative(root, path).split(sep).join("/");
  const files = SCANNED_DIRS.flatMap((dir) =>
    findFiles(join(root, dir), SCANNED_FILE),
  );
  for (const path of files) {
    findings.push(...checkSource(readFileSync(path, "utf8"), toRel(path)));
  }
  for (const path of findFiles(join(root, "src"), /\.astro$/)) {
    for (const use of astroClassPassed(readFileSync(path, "utf8"))) {
      findings.push({
        file: toRel(path),
        line: use.line,
        message: `<${use.name}> is a React component; pass "className", not "class"`,
      });
    }
  }
  findings.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
  return findings;
}

function main(): void {
  const findings = checkTree();
  if (findings.length > 0) {
    console.error(`No-Preact guard: ${findings.length} problem(s):`);
    for (const f of findings) {
      console.error(`  ${f.file}:${f.line}  ${f.message}`);
    }
    console.error(
      "The frontend runs on React: no Preact import, no @jsxImportSource pragma, " +
        "and React's JSX vocabulary only.",
    );
    process.exit(1);
  }
  console.log(
    "No-Preact guard: no Preact import, no @jsxImportSource pragma, and no " +
      "Preact-only JSX attribute.",
  );
}

if (import.meta.main) {
  main();
}
