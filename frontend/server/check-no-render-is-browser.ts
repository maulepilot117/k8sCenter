/**
 * Build-time guard: no component or hook decides what to render by asking
 * whether it runs in a browser.
 *
 * React hydrates an island by rendering it once more in the browser and
 * comparing that render with the server's HTML. A render that reads
 * `IS_BROWSER` (or `hasDom()`, or `typeof window`) answers false on the
 * server and true in the browser, so the two renders differ: React logs a
 * hydration error and throws the server HTML away. `useIsClient()` is the
 * hydration-safe form of the same question. It answers false on the server
 * and while hydrating, then true, so the browser's first render matches the
 * server's and the client-only markup arrives in the render after it.
 *
 * A browser check is a finding when it runs while a component or hook
 * renders:
 *
 * - directly in the body of a component (a function named in PascalCase) or
 *   a hook (`useX`), including inside JSX and inside callbacks the render
 *   calls straight away, such as `.map()`, `useMemo()` or an IIFE;
 * - inside a function, local or at module scope, that such a render calls.
 *
 * It is not a finding where the code runs after hydration or outside React:
 *
 * - in an effect (`useEffect`, `useLayoutEffect`, `useInsertionEffect`), a
 *   `useCallback` or `useImperativeHandle` callback, an `on*` or `ref` JSX
 *   attribute, or an `on*` callback in an options object;
 * - in a named function the render only passes on (an event handler, a
 *   callback handed to a hook) and never calls;
 * - at module scope, such as a store reading localStorage once at load.
 *
 * A lazy state initialiser (`useState(() => ...)`, `useState(init)`,
 * `useReducer`'s third argument) is part of the render: React calls it during
 * the browser's first render, so state it takes from the browser reaches the
 * markup before `useIsClient()` turns true. Start from the server's value
 * and move to the browser's in an effect, as useAdoptSelectedNamespace does.
 *
 * `use-is-client.ts` is the one place that turns `IS_BROWSER` into a render
 * value, and it is skipped.
 *
 * The guard reads one file at a time, so it does not see a check that runs
 * in a function imported from another module, or a module-scope store that
 * read localStorage and is then rendered. The e2e page-error fixture catches
 * the text mismatches those cause. React's production build keeps a
 * mismatched attribute without a word, so an attribute-only mismatch is
 * caught by neither.
 */

import { readFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { parse } from "@babel/parser";
import { FRONTEND_ROOT, findFiles } from "./source-files.ts";

const SCANNED_DIRS = ["src", "components", "lib"];
const SCANNED_FILE = /^(?!.*_test\.).+\.(ts|tsx)$/;
const SKIPPED = new Set(["src/lib/use-is-client.ts"]);
const SKIPPED_DIRS = ["src/test-utils/"];

const BROWSER_GLOBALS = new Set([
  "document",
  "window",
  "localStorage",
  "sessionStorage",
  "navigator",
]);

/** Callbacks React calls after the render has committed. */
const DEFERRED_HOOKS = new Set([
  "useEffect",
  "useLayoutEffect",
  "useInsertionEffect",
  "useCallback",
  "useImperativeHandle",
]);

export interface Finding {
  file: string;
  line: number;
  message: string;
}

// biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
type Node = any;

const FUNCTION_TYPES = new Set([
  "FunctionDeclaration",
  "FunctionExpression",
  "ArrowFunctionExpression",
  "ObjectMethod",
  "ClassMethod",
  "ClassPrivateMethod",
]);

function isRenderName(name: string | null): name is string {
  return name !== null && (/^[A-Z]/.test(name) || /^use[A-Z0-9]/.test(name));
}

function calleeName(call: Node): string | null {
  const callee = call.callee;
  if (callee.type === "Identifier") return callee.name;
  if (callee.type === "MemberExpression" && !callee.computed) {
    return callee.property.name ?? null;
  }
  return null;
}

/** What a browser check node asks, or null if it is not one. */
function browserCheck(node: Node, parent: Node): string | null {
  if (node.type === "Identifier" && node.name === "IS_BROWSER") {
    if (!parent) return null;
    if (
      parent.type === "ImportSpecifier" ||
      parent.type === "ImportDefaultSpecifier" ||
      parent.type === "ExportSpecifier" ||
      (parent.type === "VariableDeclarator" && parent.id === node) ||
      (parent.type === "MemberExpression" &&
        parent.property === node &&
        !parent.computed) ||
      ((parent.type === "ObjectProperty" ||
        parent.type === "TSPropertySignature") &&
        parent.key === node &&
        !parent.computed)
    ) {
      return null;
    }
    return "reads IS_BROWSER";
  }
  if (
    node.type === "CallExpression" &&
    node.callee.type === "Identifier" &&
    node.callee.name === "hasDom"
  ) {
    return "calls hasDom()";
  }
  if (
    node.type === "UnaryExpression" &&
    node.operator === "typeof" &&
    node.argument.type === "Identifier" &&
    BROWSER_GLOBALS.has(node.argument.name)
  ) {
    return `checks typeof ${node.argument.name}`;
  }
  return null;
}

/** The argument React calls as a lazy state initialiser, if `call` has one. */
function lazyInitialiser(call: Node): Node | null {
  const name = calleeName(call);
  if (name === "useState") return call.arguments[0] ?? null;
  if (name === "useReducer") return call.arguments[2] ?? null;
  return null;
}

export function checkSource(code: string, file: string): Finding[] {
  let ast: Node;
  try {
    ast = parse(code, { sourceType: "module", plugins: ["typescript", "jsx"] });
  } catch (error) {
    throw new Error(`${file}: could not parse (${(error as Error).message})`);
  }

  const parents = new Map<Node, Node>();
  const checks: { node: Node; what: string }[] = [];
  const calls = new Map<string, Node[]>();
  const record = (name: string, site: Node): void => {
    const list = calls.get(name) ?? [];
    list.push(site);
    calls.set(name, list);
  };

  const visit = (node: Node, parent: Node): void => {
    if (!node || typeof node !== "object") return;
    if (Array.isArray(node)) {
      for (const child of node) visit(child, parent);
      return;
    }
    if (typeof node.type !== "string") return;
    parents.set(node, parent);
    const what = browserCheck(node, parent);
    if (what) checks.push({ node, what });
    if (node.type === "CallExpression" && node.callee.type === "Identifier") {
      record(node.callee.name, node);
      // `useState(init)` calls `init` while rendering, as a call would.
      const init = lazyInitialiser(node);
      if (init?.type === "Identifier") record(init.name, init);
    }
    for (const key of Object.keys(node)) {
      if (key === "loc" || key === "start" || key === "end") continue;
      if (key === "leadingComments" || key === "trailingComments") continue;
      visit(node[key], node);
    }
  };
  visit(ast.program, null);

  /** The name a function is declared or assigned under, if any. */
  const functionName = (fn: Node): string | null => {
    if (fn.id?.name) return fn.id.name;
    let parent = parents.get(fn);
    // `const X = memo(() => ...)` / `forwardRef(...)` name the component.
    if (parent?.type === "CallExpression" && parent.arguments.includes(fn)) {
      const wrapper = calleeName(parent);
      if (wrapper === "memo" || wrapper === "forwardRef") {
        parent = parents.get(parent);
      }
    }
    if (
      parent?.type === "VariableDeclarator" &&
      parent.id.type === "Identifier"
    ) {
      return parent.id.name;
    }
    return null;
  };

  type Role = "deferred" | "inline" | { named: string };

  /** How a function that is not itself a component or hook gets run. */
  const roleOf = (fn: Node): Role => {
    const parent = parents.get(fn);
    if (parent?.type === "CallExpression" && parent.arguments.includes(fn)) {
      const callee = calleeName(parent);
      if (callee && DEFERRED_HOOKS.has(callee)) return "deferred";
      return "inline";
    }
    if (parent?.type === "JSXExpressionContainer") {
      const attr = parents.get(parent);
      if (attr?.type === "JSXAttribute" && attr.name.type === "JSXIdentifier") {
        const name: string = attr.name.name;
        if (/^on[A-Z]/.test(name) || name === "ref") return "deferred";
      }
      return "inline";
    }
    // `{ onApplySuccess: (res) => ... }`: an `on*` callback in an options
    // object runs when its event happens, not while rendering.
    if (
      parent?.type === "ObjectProperty" &&
      parent.value === fn &&
      !parent.computed &&
      /^on[A-Z]/.test(parent.key.name ?? parent.key.value ?? "")
    ) {
      return "deferred";
    }
    if (fn.type === "ObjectMethod" && /^on[A-Z]/.test(fn.key.name ?? "")) {
      return "deferred";
    }
    const name = functionName(fn);
    if (name !== null) return { named: name };
    return "inline";
  };

  /**
   * The component or hook whose render runs `node`, or null when it only
   * runs after hydration or outside a render.
   */
  const renderingOwner = (node: Node, visiting: Set<string>): string | null => {
    for (let at = parents.get(node); at; at = parents.get(at)) {
      if (!FUNCTION_TYPES.has(at.type)) continue;
      const name = functionName(at);
      if (isRenderName(name)) return name;
      const role = roleOf(at);
      if (role === "deferred") return null;
      if (role === "inline") continue;
      // A named helper renders only if a render calls it.
      if (visiting.has(role.named)) return null;
      visiting.add(role.named);
      for (const call of calls.get(role.named) ?? []) {
        const owner = renderingOwner(call, visiting);
        if (owner) return `${owner} (through ${role.named}())`;
      }
      return null;
    }
    return null;
  };

  const findings: Finding[] = [];
  for (const { node, what } of checks) {
    const owner = renderingOwner(node, new Set());
    if (owner) {
      findings.push({
        file,
        line: node.loc.start.line,
        message: `${what} while rendering ${owner}; use useIsClient() so the browser's first render matches the server's`,
      });
    }
  }
  return findings;
}

export function checkTree(root: string = FRONTEND_ROOT): Finding[] {
  const findings: Finding[] = [];
  const toRel = (path: string) => relative(root, path).split(sep).join("/");
  const files = SCANNED_DIRS.flatMap((dir) =>
    findFiles(join(root, dir), SCANNED_FILE),
  );
  for (const path of files) {
    const rel = toRel(path);
    if (SKIPPED.has(rel) || SKIPPED_DIRS.some((d) => rel.startsWith(d))) {
      continue;
    }
    findings.push(...checkSource(readFileSync(path, "utf8"), rel));
  }
  findings.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
  return findings;
}

function main(): void {
  const findings = checkTree();
  if (findings.length > 0) {
    console.error(`Render browser-check guard: ${findings.length} problem(s):`);
    for (const f of findings) {
      console.error(`  ${f.file}:${f.line}  ${f.message}`);
    }
    console.error(
      "A render that asks whether it runs in a browser renders differently " +
        "on the server and in the browser, and React discards the server " +
        "HTML. Gate the markup on useIsClient(), or move the check into an " +
        "effect or event handler.",
    );
    process.exit(1);
  }
  console.log(
    "Render browser-check guard: no component or hook render reads " +
      "IS_BROWSER, hasDom() or typeof window.",
  );
}

if (import.meta.main) {
  main();
}
