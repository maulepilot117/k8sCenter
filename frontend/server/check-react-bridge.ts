/**
 * Build-time guard for the move from Preact to React.
 *
 * Source moves to React APIs file by file while the app still runs on
 * Preact: tsconfig.json's `paths` and astro.config.mjs's `vite.resolve.alias`
 * resolve every `react` import to preact/compat. Two things can go wrong in
 * that window that nothing else catches.
 *
 * 1. A file imports from both `react` and Preact (`preact`, `preact/*`,
 *    `@preact/signals`). It works today, because both resolve to the same
 *    runtime, and breaks the moment the real React runtime is installed.
 *    Type imports count: `ComponentChildren` from preact is a Preact API.
 *
 * 2. A file that imports from `react` uses a JSX attribute only Preact
 *    understands: `class`, `for`, `onFocusOut`/`onFocusIn`, or a hyphenated
 *    or namespaced SVG attribute (`stroke-width`, `xlink:href`). Preact's
 *    types accept all of them, so `astro check` passes, and React would drop
 *    them silently. `aria-*` and `data-*` are hyphenated in React too, and
 *    attributes on a custom element (a tag with a hyphen) are left alone.
 *
 * `@preact/signals-core` is not a Preact API: shared stores stay on it.
 * `@testing-library/preact` is not one either: converted tests use it until
 * the runtime switches, and its query API matches the React one.
 *
 * There is no baseline. A file is either still on Preact APIs (allowed, and
 * counted as progress) or on React APIs (held to both rules). Unconverted
 * files are not inspected for attributes, because `class` is correct there.
 *
 * Scans every .ts and .tsx file under src, components and lib, tests
 * included, since tests convert along with the code they cover.
 */

import { readFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { parse } from "@babel/parser";
import { FRONTEND_ROOT, findFiles } from "./source-files.ts";

const SCANNED_DIRS = ["src", "components", "lib"];
const SCANNED_FILE = /\.(ts|tsx)$/;

/** React attribute to use instead of each Preact-only one. */
const PREACT_ONLY_ATTRIBUTES: Record<string, string> = {
  class: "className",
  for: "htmlFor",
  onFocusOut: "onBlur",
  onFocusIn: "onFocus",
};

export interface BridgeFinding {
  file: string;
  line: number;
  message: string;
}

export interface SourceReport {
  /** True when the file imports anything from `react` or `react-dom`. */
  usesReact: boolean;
  /** True when the file imports anything from Preact's own APIs. */
  usesPreact: boolean;
  findings: BridgeFinding[];
}

export interface TreeReport {
  findings: BridgeFinding[];
  /** Files still importing Preact APIs, relative to the root, sorted. */
  preactFiles: string[];
}

export function isReactSpecifier(s: string): boolean {
  return (
    s === "react" ||
    s === "react-dom" ||
    s.startsWith("react/") ||
    s.startsWith("react-dom/")
  );
}

export function isPreactSpecifier(s: string): boolean {
  return s === "preact" || s.startsWith("preact/") || s === "@preact/signals";
}

/** `stroke-width` becomes `strokeWidth`; `xlink:href` becomes `xlinkHref`. */
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

/** The module specifier a node imports from, or null. */
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

/** The tag name of a JSX opening element, as written. */
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

export function checkSource(code: string, file: string): SourceReport {
  let ast: Node;
  try {
    ast = parse(code, { sourceType: "module", plugins: ["typescript", "jsx"] });
  } catch (error) {
    throw new Error(`${file}: could not parse (${(error as Error).message})`);
  }

  const reactImports: { source: string; line: number }[] = [];
  const preactImports: { source: string; line: number }[] = [];
  const openings: Node[] = [];
  walk(ast.program, (node) => {
    const source = importSource(node);
    if (source !== null) {
      const at = { source, line: node.loc.start.line };
      if (isReactSpecifier(source)) reactImports.push(at);
      else if (isPreactSpecifier(source)) preactImports.push(at);
    }
    if (node.type === "JSXOpeningElement") openings.push(node);
  });

  const usesReact = reactImports.length > 0;
  const usesPreact = preactImports.length > 0;
  const findings: BridgeFinding[] = [];

  if (usesReact && usesPreact) {
    const list = (imports: typeof reactImports) =>
      imports.map((i) => `"${i.source}" (line ${i.line})`).join(", ");
    findings.push({
      file,
      line: preactImports[0].line,
      message:
        `imports from both React (${list(reactImports)}) and Preact ` +
        `(${list(preactImports)}); convert the Preact imports to React`,
    });
  }

  if (usesReact) {
    for (const opening of openings) {
      const tag = tagName(opening.name);
      for (const attr of opening.attributes) {
        const message = attributeFinding(attr, tag);
        if (message)
          findings.push({ file, line: attr.loc.start.line, message });
      }
    }
  }

  return { usesReact, usesPreact, findings };
}

export function checkTree(root: string = FRONTEND_ROOT): TreeReport {
  const findings: BridgeFinding[] = [];
  const preactFiles: string[] = [];
  const files = SCANNED_DIRS.flatMap((dir) =>
    findFiles(join(root, dir), SCANNED_FILE),
  );
  for (const path of files) {
    const rel = relative(root, path).split(sep).join("/");
    const report = checkSource(readFileSync(path, "utf8"), rel);
    findings.push(...report.findings);
    if (report.usesPreact) preactFiles.push(rel);
  }
  findings.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
  return { findings, preactFiles: preactFiles.sort() };
}

function main(): void {
  const { findings, preactFiles } = checkTree();
  if (findings.length > 0) {
    console.error(
      `React bridge guard: ${findings.length} problem(s) in files on React APIs:`,
    );
    for (const f of findings) {
      console.error(`  ${f.file}:${f.line}  ${f.message}`);
    }
    console.error(
      "A file that imports from react must use only React APIs and React's " +
        "JSX vocabulary. Both resolve to preact/compat for now, so this runs " +
        "today and breaks when the real React runtime is installed.",
    );
    process.exit(1);
  }
  console.log(
    `React bridge guard: no file mixes React and Preact APIs ` +
      `(${preactFiles.length} file(s) still on Preact APIs).`,
  );
}

if (import.meta.main) {
  main();
}
