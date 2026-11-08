/**
 * Build-time guard: a signal read on Preact APIs must still re-render.
 *
 * A Preact component that reads a signal with a bare `.value` during render
 * re-renders when that signal changes only because @preact/signals patched
 * Preact when it loaded. Nothing in the component says so: the read works on
 * any page where something else happened to import @preact/signals first.
 * While shared code moves to React APIs, those imports disappear from the
 * modules that used to carry them, and an island still on Preact can lose
 * its last one. The page then renders once and silently stops updating;
 * types, tests of the shared code and the build all still pass.
 *
 * So for every island a page or layout hydrates, this walks the island's
 * runtime imports (type-only imports are erased and do not count). If the
 * island reaches a signal (@preact/signals-core) and some file it loads on
 * Preact APIs reads `.value`, it must also load @preact/signals itself.
 * Only files that render on Preact APIs count. Files on React APIs are
 * skipped: under React a bare read never subscribes, so they read through
 * useSignalValue and need no patch. Store and helper modules (no JSX, no
 * Preact import) read `.value` in functions, not in a render.
 *
 * The `.value` test is syntactic, so it also counts a read of an ordinary
 * object's `value` field; an event's `target.value` / `currentTarget.value`
 * and assignments are left out. A false positive costs a side-effect
 * `import "@preact/signals"` in a Preact-API file the island loads.
 *
 * Islands are checked one at a time rather than per page: each island's
 * chunk hydrates on its own, so another island on the page loading the
 * patch first is an ordering accident, not a guarantee.
 */

import { readFileSync } from "node:fs";
import { relative } from "node:path";
import { parse } from "@babel/parser";
import {
  CLIENT_DIRECTIVE_PATTERN,
  defaultEntryPoints,
  extractAstroTemplate,
  findIslandImports,
  findTagCallSites,
  resolveSpecifier,
} from "./check-island-hydration-directives.ts";
import { checkSource } from "./check-react-bridge.ts";
import { FRONTEND_ROOT } from "./source-files.ts";

const SIGNALS_CORE = "@preact/signals-core";
const SIGNALS_PATCH = "@preact/signals";

// biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
type Node = any;

export interface ModuleFacts {
  /** Specifiers imported or re-exported at runtime, in source order. */
  runtimeImports: string[];
  /**
   * The file renders on Preact APIs: it has JSX or imports Preact, and is not
   * on React APIs (no react import, no pragma). A store or helper module
   * with neither reads `.value` outside any render.
   */
  rendersOnPreact: boolean;
  /** Lines of `.value` reads that could be a signal read. */
  valueReads: number[];
}

function walk(node: Node, visit: (n: Node, parent: Node) => void): void {
  const stack: [Node, Node][] = [[node, null]];
  while (stack.length > 0) {
    const [current, parent] = stack.pop() as [Node, Node];
    visit(current, parent);
    for (const key of Object.keys(current)) {
      if (key === "loc" || key === "leadingComments") continue;
      const child = current[key];
      if (Array.isArray(child)) {
        for (const c of child) {
          if (c && typeof c.type === "string") stack.push([c, current]);
        }
      } else if (child && typeof child.type === "string") {
        stack.push([child, current]);
      }
    }
  }
}

/** True when every specifier of an import is a type: TypeScript erases it. */
function erased(node: Node): boolean {
  if (node.importKind === "type" || node.exportKind === "type") return true;
  const specifiers: Node[] = node.specifiers ?? [];
  return (
    specifiers.length > 0 &&
    specifiers.every((s) => s.importKind === "type" || s.exportKind === "type")
  );
}

/** `e.target.value`, `currentTarget.value` and the like read a DOM element. */
function readsEventTarget(object: Node): boolean {
  const name =
    object.type === "Identifier"
      ? object.name
      : (object.type === "MemberExpression" ||
            object.type === "OptionalMemberExpression") &&
          !object.computed
        ? object.property.name
        : null;
  return name === "target" || name === "currentTarget";
}

export function moduleFacts(code: string, file: string): ModuleFacts {
  let ast: Node;
  try {
    ast = parse(code, { sourceType: "module", plugins: ["typescript", "jsx"] });
  } catch (error) {
    throw new Error(`${file}: could not parse (${(error as Error).message})`);
  }
  const runtimeImports: string[] = [];
  const valueReads: number[] = [];
  let hasJsx = false;
  walk(ast.program, (node, parent) => {
    switch (node.type) {
      case "JSXElement":
      case "JSXFragment":
        hasJsx = true;
        return;
      case "ImportDeclaration":
      case "ExportNamedDeclaration":
      case "ExportAllDeclaration":
        if (node.source && !erased(node))
          runtimeImports.push(node.source.value);
        return;
      case "ImportExpression":
        if (node.source?.type === "StringLiteral") {
          runtimeImports.push(node.source.value);
        }
        return;
      case "CallExpression":
        if (
          node.callee.type === "Import" &&
          node.arguments[0]?.type === "StringLiteral"
        ) {
          runtimeImports.push(node.arguments[0].value);
        }
        return;
      case "MemberExpression":
      case "OptionalMemberExpression": {
        if (node.computed || node.property.name !== "value") return;
        if (readsEventTarget(node.object)) return;
        const assigned =
          (parent?.type === "AssignmentExpression" && parent.left === node) ||
          (parent?.type === "UpdateExpression" && parent.argument === node);
        if (!assigned) valueReads.push(node.loc.start.line);
        return;
      }
    }
  });
  valueReads.sort((a, b) => a - b);
  const { usesReact, usesPreact } = checkSource(code, file);
  return {
    runtimeImports,
    rendersOnPreact: !usesReact && (usesPreact || hasJsx),
    valueReads,
  };
}

export interface RerenderFinding {
  /** The hydrated island, relative to the frontend root. */
  island: string;
  /** The Preact-API file whose `.value` read would not re-render. */
  file: string;
  line: number;
}

/** Every hydrated island in the tree whose signal reads would not re-render. */
export function checkIslands(root: string = FRONTEND_ROOT): RerenderFinding[] {
  const rel = (abs: string) => relative(root, abs).split("\\").join("/");
  const facts = new Map<string, ModuleFacts | null>();
  const factsOf = (abs: string): ModuleFacts | null => {
    if (!facts.has(abs)) {
      if (!/\.(ts|tsx)$/.test(abs)) {
        facts.set(abs, null);
      } else {
        facts.set(abs, moduleFacts(readFileSync(abs, "utf-8"), rel(abs)));
      }
    }
    return facts.get(abs) ?? null;
  };

  const islands = new Set<string>();
  for (const entry of defaultEntryPoints(root)) {
    const template = extractAstroTemplate(readFileSync(entry, "utf-8"));
    for (const { local, resolved } of findIslandImports(entry, root)) {
      const hydrated = findTagCallSites(template, local).some((tag) =>
        CLIENT_DIRECTIVE_PATTERN.test(tag),
      );
      if (hydrated) islands.add(resolved);
    }
  }

  const findings: RerenderFinding[] = [];
  for (const island of [...islands].sort()) {
    const seen = new Set<string>();
    const packages = new Set<string>();
    const stack = [`${root}/${island}`.replace(/\\/g, "/")];
    let firstRead: { file: string; line: number } | null = null;
    while (stack.length > 0) {
      const abs = stack.pop() as string;
      if (seen.has(abs)) continue;
      seen.add(abs);
      const mod = factsOf(abs);
      if (!mod) continue;
      if (mod.rendersOnPreact && mod.valueReads.length > 0 && !firstRead) {
        firstRead = { file: rel(abs), line: mod.valueReads[0] };
      }
      for (const specifier of mod.runtimeImports) {
        const local = resolveSpecifier(abs, specifier, root);
        if (local) stack.push(local.replace(/\\/g, "/"));
        else packages.add(specifier);
      }
    }
    if (
      firstRead &&
      packages.has(SIGNALS_CORE) &&
      !packages.has(SIGNALS_PATCH)
    ) {
      findings.push({ island, ...firstRead });
    }
  }
  return findings;
}

function main(): void {
  const findings = checkIslands();
  if (findings.length > 0) {
    console.error(
      "Signal re-render guard: these islands read a signal's .value on " +
        "Preact APIs but load nothing that imports @preact/signals, so the " +
        "read renders once and never updates. Add `import " +
        '"@preact/signals";` to a Preact-API file the island loads, or ' +
        "read the signal through useSignalValue:",
    );
    for (const f of findings) {
      console.error(`  ${f.island}: ${f.file}:${f.line} reads .value`);
    }
    process.exit(1);
  }
  console.log(
    "Signal re-render guard: every hydrated island that reads a signal on " +
      "Preact APIs loads @preact/signals.",
  );
}

if (import.meta.main) {
  main();
}
