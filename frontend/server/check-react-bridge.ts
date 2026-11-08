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
 * 3. tsconfig.json's `paths` and astro.config.mjs's alias list drift apart,
 *    so type-checking and tests resolve a `react` import to a different
 *    file from the one the browser runs. The two are compared by the file
 *    each target resolves to, because tsconfig names a path and Vite a
 *    package specifier.
 *
 * React's own lint rules (hooks at top level, effect dependencies) come from
 * Biome's React domain, which biome.json enables per file through the
 * override listing converted files. Add a file there when it converts, so a
 * React-only mistake shows up in the change that introduces it.
 */

import { readFileSync } from "node:fs";
import { join, posix, relative, sep } from "node:path";
import { parse } from "@babel/parser";
import { FRONTEND_ROOT, findFiles } from "./source-files.ts";

const SCANNED_DIRS = ["src", "components", "lib"];
const SCANNED_FILE = /\.(ts|tsx)$/;

const JSX_IMPORT_SOURCE_REACT = /@jsxImportSource\s+react\b/;

/**
 * The camelCase SVG prop names preact/compat rewrites to hyphenated
 * attributes (its CAMEL_PROPS pattern). Preact without compat sets them as
 * written, which SVG ignores.
 */
const COMPAT_CAMEL_SVG =
  /^(?:accent|alignment|arabic|baseline|cap|clip(?!PathU)|color|dominant|fill|flood|font|glyph(?!R)|horiz|image(?!S)|letter|lighting|marker(?!H|W|U)|overline|paint|pointer|shape|stop|strikethrough|stroke|text(?!L)|transform|underline|unicode|units|v|vector|vert|word|writing|x(?!C))[A-Z]/;

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
  usesReact: boolean;
  usesPreact: boolean;
  findings: BridgeFinding[];
}

export interface TreeReport {
  findings: BridgeFinding[];
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

/** A component element given a `class` attribute, and where it came from. */
interface ClassPassed {
  name: string;
  source: string;
  line: number;
}

interface Analysis {
  report: SourceReport;
  classPassed: ClassPassed[];
}

export function checkSource(code: string, file: string): SourceReport {
  return analyse(code, file).report;
}

function analyse(code: string, file: string): Analysis {
  let ast: Node;
  try {
    ast = parse(code, { sourceType: "module", plugins: ["typescript", "jsx"] });
  } catch (error) {
    throw new Error(`${file}: could not parse (${(error as Error).message})`);
  }

  const reactImports: { source: string; line: number }[] = [];
  const preactImports: { source: string; line: number }[] = [];
  const openings: Node[] = [];
  let hasFragment = false;
  const importedFrom = new Map<string, string>();
  walk(ast.program, (node) => {
    const source = importSource(node);
    if (source !== null) {
      const at = { source, line: node.loc.start.line };
      if (isReactSpecifier(source)) reactImports.push(at);
      else if (isPreactSpecifier(source)) preactImports.push(at);
    }
    if (node.type === "ImportDeclaration" && node.importKind !== "type") {
      for (const spec of node.specifiers) {
        importedFrom.set(spec.local.name, node.source.value);
      }
    }
    if (node.type === "JSXOpeningElement") openings.push(node);
    if (node.type === "JSXFragment") hasFragment = true;
  });
  const hasJsx = openings.length > 0 || hasFragment;
  // The pragma counts only where JSX tools read it: in a comment before the
  // first statement.
  const firstStatement = ast.program.body[0]?.start ?? Number.POSITIVE_INFINITY;
  const marked = (ast.comments ?? []).some(
    (c: Node) =>
      c.end <= firstStatement && JSX_IMPORT_SOURCE_REACT.test(c.value),
  );

  const classPassed: ClassPassed[] = [];
  for (const opening of openings) {
    if (opening.name.type !== "JSXIdentifier") continue;
    const source = importedFrom.get(opening.name.name);
    if (!source) continue;
    for (const attr of opening.attributes) {
      if (attr.type === "JSXAttribute" && attr.name.name === "class") {
        const line = attr.loc.start.line;
        classPassed.push({ name: opening.name.name, source, line });
      }
    }
  }

  // The pragma marks a file as on React APIs even when it imports nothing
  // from react (a component that is only JSX).
  const importsReact = reactImports.length > 0;
  const usesReact = importsReact || marked;
  const usesPreact = preactImports.length > 0;
  const findings: BridgeFinding[] = [];

  if (usesReact && usesPreact) {
    const list = (imports: typeof reactImports) =>
      imports.map((i) => `"${i.source}" (line ${i.line})`).join(", ");
    const reactSide = importsReact
      ? list(reactImports)
      : "`@jsxImportSource react`";
    findings.push({
      file,
      line: preactImports[0].line,
      message:
        `imports from both React (${reactSide}) and Preact ` +
        `(${list(preactImports)}); convert the Preact imports to React`,
    });
  }

  if (!marked) {
    for (const opening of openings) {
      // Only DOM elements: a component's props are not attributes.
      const intrinsic =
        opening.name.type === "JSXIdentifier" &&
        /^[a-z]/.test(opening.name.name);
      if (!intrinsic) continue;
      for (const attr of opening.attributes) {
        if (
          attr.type === "JSXAttribute" &&
          attr.name.type === "JSXIdentifier" &&
          COMPAT_CAMEL_SVG.test(attr.name.name)
        ) {
          findings.push({
            file,
            line: attr.loc.start.line,
            message:
              `<${tagName(opening.name)}> uses "${attr.name.name}", which only ` +
              "preact/compat turns into an attribute; mark the file " +
              "`/** @jsxImportSource react */`",
          });
        }
      }
    }
  }

  if (importsReact && hasJsx && !marked) {
    findings.push({
      file,
      line: 1,
      message:
        "has JSX on React APIs but no `/** @jsxImportSource react */`; " +
        "without it the JSX does not load preact/compat, and React prop " +
        "names such as strokeWidth reach the DOM unconverted",
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

  return { report: { usesReact, usesPreact, findings }, classPassed };
}

/** The scanned file a local import resolves to, relative to the root. */
function resolveLocal(
  from: string,
  source: string,
  known: Set<string>,
): string | null {
  let base: string;
  if (source.startsWith("@/")) base = source.slice(2);
  else if (source.startsWith("."))
    base = posix.join(posix.dirname(from), source);
  else return null;
  base = posix.normalize(base);
  for (const candidate of [base, `${base}.tsx`, `${base}.ts`]) {
    if (known.has(candidate)) return candidate;
  }
  return null;
}

/**
 * Component tags in an .astro file that are given `class`, with the module
 * each tag was imported from in the frontmatter. `class:list` is Astro's own
 * directive and is not a `class` attribute.
 */
export function astroClassPassed(code: string): ClassPassed[] {
  const front = /^---\r?\n([\s\S]*?)\r?\n---/.exec(code);
  if (!front) return [];
  const importedFrom = new Map<string, string>();
  for (const m of front[1].matchAll(
    /import\s+([A-Z]\w*)\s+from\s+["']([^"']+)["']/g,
  )) {
    importedFrom.set(m[1], m[2]);
  }
  for (const m of front[1].matchAll(
    /import\s*\{([^}]*)\}\s*from\s+["']([^"']+)["']/g,
  )) {
    for (const part of m[1].split(",")) {
      const local = part
        .trim()
        .split(/\s+as\s+/)
        .pop();
      if (local) importedFrom.set(local, m[2]);
    }
  }
  const found: ClassPassed[] = [];
  for (const m of code.matchAll(/<([A-Z]\w*)(\s[^>]*)?>/g)) {
    const source = importedFrom.get(m[1]);
    if (!source || !m[2] || !/(^|\s)class\s*=/.test(m[2])) continue;
    const line = code.slice(0, m.index).split("\n").length;
    found.push({ name: m[1], source, line });
  }
  return found;
}

export function checkTree(root: string = FRONTEND_ROOT): TreeReport {
  const findings: BridgeFinding[] = [];
  const preactFiles: string[] = [];
  const reactFiles = new Set<string>();
  const callers: { file: string; usesReact: boolean; uses: ClassPassed[] }[] =
    [];
  const files = SCANNED_DIRS.flatMap((dir) =>
    findFiles(join(root, dir), SCANNED_FILE),
  );
  const toRel = (path: string) => relative(root, path).split(sep).join("/");
  const known = new Set(files.map(toRel));
  for (const path of files) {
    const rel = toRel(path);
    const { report, classPassed } = analyse(readFileSync(path, "utf8"), rel);
    findings.push(...report.findings);
    if (report.usesPreact) preactFiles.push(rel);
    if (report.usesReact) reactFiles.add(rel);
    callers.push({ file: rel, usesReact: report.usesReact, uses: classPassed });
  }
  for (const path of findFiles(join(root, "src"), /\.astro$/)) {
    const uses = astroClassPassed(readFileSync(path, "utf8"));
    callers.push({ file: toRel(path), usesReact: false, uses });
  }

  // A component on React APIs reads className; `class` passed to it is
  // dropped once the real React runtime is installed. A caller on React APIs
  // is already flagged for every `class` it writes, so it is skipped here.
  for (const caller of callers) {
    if (caller.usesReact) continue;
    for (const use of caller.uses) {
      const target = resolveLocal(caller.file, use.source, known);
      if (target && reactFiles.has(target)) {
        findings.push({
          file: caller.file,
          line: use.line,
          message: `<${use.name}> is on React APIs; pass "className", not "class"`,
        });
      }
    }
  }
  findings.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
  return { findings, preactFiles: preactFiles.sort() };
}

/** The `react*` entries of tsconfig.json's `paths`: specifier -> target. */
export function tsconfigAliases(text: string): Map<string, string> {
  const config = Bun.JSONC.parse(text) as {
    compilerOptions?: { paths?: Record<string, string[]> };
  };
  const paths = config.compilerOptions?.paths ?? {};
  return new Map(
    Object.entries(paths)
      .filter(([specifier]) => isReactSpecifier(specifier))
      .map(([specifier, targets]) => [specifier, targets[0]]),
  );
}

/**
 * The `react*` entries of astro.config.mjs's alias list, read from its
 * `{ find: /^…$/, replacement: "…" }` literals: specifier -> target. An
 * entry whose pattern is not an anchored literal is a finding, because the
 * check could not tell which specifiers it rewrites.
 */
export function viteAliases(
  code: string,
  file: string,
): { aliases: Map<string, string>; findings: BridgeFinding[] } {
  const ast = parse(code, { sourceType: "module" });
  const aliases = new Map<string, string>();
  const findings: BridgeFinding[] = [];
  walk(ast.program, (node) => {
    if (node.type !== "ObjectExpression") return;
    const prop = (key: string) =>
      node.properties.find(
        (p: Node) => p.type === "ObjectProperty" && p.key.name === key,
      )?.value;
    const find = prop("find");
    const replacement = prop("replacement");
    if (find?.type !== "RegExpLiteral" || !replacement) return;
    const literal = /^\^((?:[\w@.-]|\\\/)+)\$$/.exec(find.pattern);
    if (!literal || replacement.type !== "StringLiteral") {
      findings.push({
        file,
        line: node.loc.start.line,
        message: `alias /${find.pattern}/ is not an anchored literal with a string replacement; the alias check cannot read it`,
      });
      return;
    }
    const specifier = literal[1].replaceAll("\\/", "/");
    if (isReactSpecifier(specifier)) aliases.set(specifier, replacement.value);
  });
  return { aliases, findings };
}

function resolveFrom(root: string, target: string): string | null {
  try {
    return relative(root, Bun.resolveSync(target, root)).split(sep).join("/");
  } catch {
    return null;
  }
}

/** tsconfig and Vite must send every `react*` specifier to the same file. */
export function checkAliasMaps(root: string = FRONTEND_ROOT): BridgeFinding[] {
  const tsconfig = tsconfigAliases(
    readFileSync(join(root, "tsconfig.json"), "utf8"),
  );
  const vite = viteAliases(
    readFileSync(join(root, "astro.config.mjs"), "utf8"),
    "astro.config.mjs",
  );
  const findings = [...vite.findings];
  const specifiers = [...new Set([...tsconfig.keys(), ...vite.aliases.keys()])];
  for (const specifier of specifiers.sort()) {
    const fromTs = tsconfig.get(specifier);
    const fromVite = vite.aliases.get(specifier);
    if (fromTs === undefined || fromVite === undefined) {
      findings.push({
        file: fromTs === undefined ? "tsconfig.json" : "astro.config.mjs",
        line: 1,
        message: `"${specifier}" is aliased in ${fromTs === undefined ? "astro.config.mjs" : "tsconfig.json"} only`,
      });
      continue;
    }
    const tsFile = resolveFrom(root, fromTs);
    const viteFile = resolveFrom(root, fromVite);
    if (tsFile === null || viteFile === null || tsFile !== viteFile) {
      findings.push({
        file: "tsconfig.json",
        line: 1,
        message:
          `"${specifier}" resolves to ${tsFile ?? `nothing ("${fromTs}")`} ` +
          `in tsconfig.json but ${viteFile ?? `nothing ("${fromVite}")`} in astro.config.mjs`,
      });
    }
  }
  return findings;
}

function main(): void {
  const { findings, preactFiles } = checkTree();
  const aliasFindings = checkAliasMaps();
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
  }
  if (aliasFindings.length > 0) {
    console.error(
      `React bridge guard: the react alias maps disagree (${aliasFindings.length}):`,
    );
    for (const f of aliasFindings) {
      console.error(`  ${f.file}:${f.line}  ${f.message}`);
    }
    console.error(
      "tsconfig.json's paths and astro.config.mjs's alias list must send " +
        "each react specifier to the same file, or tests and the browser run " +
        "different code.",
    );
  }
  if (findings.length > 0 || aliasFindings.length > 0) process.exit(1);
  console.log(
    `React bridge guard: no file mixes React and Preact APIs ` +
      `(${preactFiles.length} file(s) still on Preact APIs); the react alias ` +
      "maps agree.",
  );
}

if (import.meta.main) {
  main();
}
