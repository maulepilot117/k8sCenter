import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "@babel/parser";

/**
 * Standing guard for the SSR-placeholder / hydrated-root divergence.
 *
 * Preact's hydration pass matches the server-rendered element and recurses
 * into its children *without re-applying that element's own props*. For an
 * island written in the usual shape
 *
 *     if (!IS_BROWSER) return <div class="p-6" />;   // SSR placeholder
 *     ...
 *     return <div class="flex flex-col h-full">...</div>;  // hydrated root
 *
 * the browser keeps `class="p-6"` for the life of the page. The children are
 * created fresh (nothing matching them exists in the DOM yet) so they get
 * their real props; only the root, the one node that already exists, silently
 * keeps whatever SSR emitted. Nothing else in this repo can see it: the
 * server renders the placeholder, `astro check` and `bun test` never mount
 * anything, and the E2E suite asserts behaviour rather than computed style.
 *
 * Two of these shipped and were caught only by KTD14's one-time screenshot
 * comparison (see IconRail.tsx and MonacoEditor.tsx, both of which now hoist
 * the root's attributes into a shared constant). This guard exists because
 * that comparison cannot be run again after U12: it diffs the Fresh tree
 * against the Astro tree, and U12 deletes the Fresh tree.
 *
 * It also could not have found these on its own. The Fresh tree carries the
 * byte-identical placeholders (`islands/CRDResourceList.tsx:168` emits the
 * same `minHeight: "400px"` root as its `src/` twin), so a cross-tree diff
 * cancels the defect out on both sides. Only the divergences the *port*
 * introduced were visible to it. The baseline below is that blind spot,
 * written down.
 *
 * A tag mismatch between the two roots is deliberately not a finding. Preact
 * discards and recreates an element whose type changed, and a freshly created
 * element gets a full prop diff -- so `<div>` placeholder into `<section>`
 * root is wasteful but correct. Only same-tag pairs can strand props.
 *
 * ## The baseline
 *
 * The pre-existing divergences live in placeholder-root-parity-baseline.json,
 * one entry each. They are inherited from the Fresh tree rather than
 * introduced by the migration, and fixing them would change what the app
 * renders -- which is precisely what U12 is required to prove it has *not*
 * done. They are a tracked backlog, not an exemption in principle.
 *
 * The baseline is checked in both directions. An entry that no longer matches
 * anything fails the guard too, so a fixed divergence forces its record to be
 * deleted and the list can only shrink. Matching is on the source text of the
 * two roots rather than on line numbers, so an unrelated edit above does not
 * invalidate an entry, while editing the classes themselves does.
 */

const HERE = dirname(fileURLToPath(import.meta.url));
export const FRONTEND_ROOT = resolve(HERE, "..");
export const BASELINE_PATH = join(
  HERE,
  "placeholder-root-parity-baseline.json",
);

/** The roots this guard reads off a single JSX return. */
export interface RootShape {
  tag: string;
  /** Source text of the `class`/`className` attribute value, or null. */
  class: string | null;
  /** Source text of the `style` attribute value, or null. */
  style: string | null;
}

export interface Divergence {
  /** Frontend-relative, forward-slashed. */
  file: string;
  tag: string;
  placeholder: RootShape;
  hydrated: RootShape;
  /** 1-based line of the placeholder return, for the error message only. */
  placeholderLine: number;
  /** 1-based line of the hydrated return, for the error message only. */
  hydratedLine: number;
}

export interface BaselineEntry {
  file: string;
  tag: string;
  placeholder: { class: string | null; style: string | null };
  hydrated: { class: string | null; style: string | null };
  note: string;
}

/**
 * Collapses runs of whitespace so a reformatted multi-line style object
 * compares equal to the same object on one line. Biome owns the formatting of
 * these files and can rewrap an object literal without anyone touching it;
 * that must not read as a new divergence.
 */
export function normalizeAttr(text: string | null): string | null {
  if (text === null) return null;
  return (
    text
      .replace(/\s+/g, " ")
      // A trailing comma before a closer is purely a consequence of whether
      // the literal is wrapped across lines, which Biome decides. Without
      // this, adding one property to a style object silently rewrites the key
      // of an unrelated entry two lines away.
      .replace(/,(\s*[}\])])/g, "$1")
      .trim()
  );
}

function sameShape(
  a: { class: string | null; style: string | null },
  b: { class: string | null; style: string | null },
): boolean {
  return (
    normalizeAttr(a.class) === normalizeAttr(b.class) &&
    normalizeAttr(a.style) === normalizeAttr(b.style)
  );
}

/**
 * Reads the root element of a return argument. Returns null for anything that
 * is not a plain JSX element: a fragment (no root element exists in the DOM,
 * so nothing can be stranded), a component (`<Modal />` -- its own root is
 * checked when that component is itself visited), null, or a conditional.
 */
export function describeRoot(node: unknown, code: string): RootShape | null {
  // biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
  let n = node as any;
  while (
    n &&
    (n.type === "TSAsExpression" || n.type === "TSNonNullExpression")
  ) {
    n = n.expression;
  }
  if (!n || n.type !== "JSXElement") return null;

  const name = n.openingElement.name;
  // A member expression (`<Foo.Bar />`) or a namespaced name is a component,
  // not an intrinsic element.
  if (name.type !== "JSXIdentifier") return null;
  const tag: string = name.name;
  // Capitalised means a component in JSX. Only lowercase intrinsics become a
  // real DOM node whose props hydration can strand.
  if (tag[0] !== tag[0].toLowerCase()) return null;

  let cls: string | null = null;
  let style: string | null = null;
  for (const attr of n.openingElement.attributes) {
    if (attr.type !== "JSXAttribute") continue;
    const attrName = attr.name.name;
    if (attrName === "class" || attrName === "className") {
      cls = attr.value ? code.slice(attr.value.start, attr.value.end) : "true";
    } else if (attrName === "style") {
      style = attr.value
        ? code.slice(attr.value.start, attr.value.end)
        : "true";
    }
  }
  return { tag, class: cls, style };
}

/** True for the test expression `!IS_BROWSER`. */
// biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
function isNotBrowser(test: any): boolean {
  return (
    test?.type === "UnaryExpression" &&
    test.operator === "!" &&
    test.argument?.type === "Identifier" &&
    test.argument.name === "IS_BROWSER"
  );
}

const FUNCTION_TYPES = new Set([
  "FunctionDeclaration",
  "FunctionExpression",
  "ArrowFunctionExpression",
]);

/**
 * Walks a subtree without descending into a nested function, so a `return`
 * belonging to a `useEffect` cleanup or a `.map()` callback is never mistaken
 * for one of this component's own render returns. That distinction is the
 * whole reason this is an AST walk rather than a regex: `if (!IS_BROWSER)
 * return;` inside an effect is the single most common use of the identifier
 * in this tree and has nothing to do with rendering.
 */
// biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
function walkOwnScope(node: any, visit: (n: any) => void): void {
  if (!node || typeof node !== "object") return;
  if (Array.isArray(node)) {
    for (const child of node) walkOwnScope(child, visit);
    return;
  }
  if (FUNCTION_TYPES.has(node.type) || node.type === "ClassDeclaration") return;
  visit(node);
  for (const key of Object.keys(node)) {
    if (key === "type" || key === "loc" || key === "start" || key === "end") {
      continue;
    }
    walkOwnScope(node[key], visit);
  }
}

/**
 * Finds every render-level divergence in one source file.
 *
 * Exported so the test can drive it over fixture strings rather than only
 * over the real tree.
 */
export function findDivergencesInSource(
  code: string,
  file: string,
): Divergence[] {
  if (!code.includes("IS_BROWSER")) return [];

  // biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
  let ast: any;
  try {
    ast = parse(code, { sourceType: "module", plugins: ["typescript", "jsx"] });
  } catch (error) {
    throw new Error(`${file}: could not parse (${(error as Error).message})`);
  }

  // biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
  const functions: any[] = [];
  // biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
  (function collect(node: any): void {
    if (!node || typeof node !== "object") return;
    if (Array.isArray(node)) {
      for (const child of node) collect(child);
      return;
    }
    if (FUNCTION_TYPES.has(node.type)) functions.push(node);
    for (const key of Object.keys(node)) {
      if (key === "type" || key === "loc") continue;
      collect(node[key]);
    }
  })(ast.program.body);

  const divergences: Divergence[] = [];

  for (const fn of functions) {
    // biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
    const placeholderReturns: any[] = [];
    // biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
    const allReturns: any[] = [];

    walkOwnScope(fn.body, (node) => {
      if (node.type === "ReturnStatement" && node.argument) {
        allReturns.push(node);
      }
      if (node.type === "IfStatement" && isNotBrowser(node.test)) {
        walkOwnScope(node.consequent, (inner) => {
          if (inner.type === "ReturnStatement" && inner.argument) {
            placeholderReturns.push(inner);
          }
        });
      }
    });

    if (placeholderReturns.length === 0) continue;
    const placeholderSet = new Set(placeholderReturns);

    for (const placeholder of placeholderReturns) {
      const phRoot = describeRoot(placeholder.argument, code);
      if (!phRoot) continue;

      for (const hydrated of allReturns) {
        if (placeholderSet.has(hydrated)) continue;
        const hyRoot = describeRoot(hydrated.argument, code);
        if (!hyRoot) continue;
        // Different tag: Preact recreates the node and diffs all props onto
        // it, so nothing is stranded.
        if (hyRoot.tag !== phRoot.tag) continue;
        if (sameShape(phRoot, hyRoot)) continue;

        divergences.push({
          file,
          tag: phRoot.tag,
          placeholder: phRoot,
          hydrated: hyRoot,
          placeholderLine: placeholder.loc.start.line,
          hydratedLine: hydrated.loc.start.line,
        });
      }
    }
  }

  return divergences;
}

function walkTsx(dirAbs: string, out: string[]): void {
  if (!existsSync(dirAbs)) return;
  for (const entry of readdirSync(dirAbs)) {
    const full = join(dirAbs, entry);
    if (statSync(full).isDirectory()) {
      walkTsx(full, out);
    } else if (entry.endsWith(".tsx") && !entry.endsWith("_test.tsx")) {
      out.push(full);
    }
  }
}

/**
 * Every component file the guard covers: the Astro tree only. `islands/` and
 * `components/` at the frontend root are the Fresh tree, which U12 deletes;
 * baselining them would leave dead entries behind the moment it goes.
 */
export function defaultSourceFiles(root: string = FRONTEND_ROOT): string[] {
  const files: string[] = [];
  walkTsx(join(root, "src", "islands"), files);
  walkTsx(join(root, "src", "components"), files);
  return files.sort();
}

export function findDivergences(
  files: string[] = defaultSourceFiles(),
  root: string = FRONTEND_ROOT,
): Divergence[] {
  const out: Divergence[] = [];
  for (const abs of files) {
    const rel = relative(root, abs).split("\\").join("/");
    out.push(...findDivergencesInSource(readFileSync(abs, "utf-8"), rel));
  }
  return out;
}

export function loadBaseline(path: string = BASELINE_PATH): BaselineEntry[] {
  if (!existsSync(path)) return [];
  return JSON.parse(readFileSync(path, "utf-8")) as BaselineEntry[];
}

export interface Reconciliation {
  /** Divergences with no baseline entry -- these fail the build. */
  unbaselined: Divergence[];
  /** Baseline entries matching nothing -- stale, must be deleted. */
  stale: BaselineEntry[];
}

/**
 * The identity of a divergence: which file, which tag, and the two roots'
 * attribute text. Deliberately not the line numbers -- an edit anywhere above
 * would otherwise invalidate an entry, while an edit to the classes
 * themselves must.
 *
 * One component can produce the same key several times, because a placeholder
 * is compared against every one of the component's hydrated returns and a
 * loading branch and an error branch often carry the same root. Those are one
 * fact about one placeholder, not several, so they collapse to a single
 * baseline entry.
 */
export function divergenceKey(d: {
  file: string;
  tag: string;
  placeholder: { class: string | null; style: string | null };
  hydrated: { class: string | null; style: string | null };
}): string {
  return [
    d.file,
    d.tag,
    normalizeAttr(d.placeholder.class),
    normalizeAttr(d.placeholder.style),
    normalizeAttr(d.hydrated.class),
    normalizeAttr(d.hydrated.style),
  ].join(" ");
}

export function reconcile(
  divergences: Divergence[],
  baseline: BaselineEntry[],
): Reconciliation {
  const baselineKeys = new Set(baseline.map(divergenceKey));
  const seen = new Set<string>();
  const unbaselined: Divergence[] = [];

  for (const d of divergences) {
    const key = divergenceKey(d);
    seen.add(key);
    // Report each distinct shape once, however many hydrated returns it was
    // compared against.
    if (
      !baselineKeys.has(key) &&
      !unbaselined.some((u) => divergenceKey(u) === key)
    ) {
      unbaselined.push(d);
    }
  }

  return {
    unbaselined,
    stale: baseline.filter((e) => !seen.has(divergenceKey(e))),
  };
}

function describe(shape: { class: string | null; style: string | null }) {
  const parts: string[] = [];
  parts.push(`class=${shape.class ?? "(none)"}`);
  parts.push(`style=${normalizeAttr(shape.style) ?? "(none)"}`);
  return parts.join(" ");
}

function main(): void {
  const { unbaselined, stale } = reconcile(findDivergences(), loadBaseline());

  if (unbaselined.length > 0) {
    console.error(
      "Placeholder/hydrated root parity guard: an island's SSR placeholder " +
        "root does not match its hydrated root.\n" +
        "Preact hydration keeps the server-rendered root's own props and " +
        "only recurses into children, so these never reach the browser:\n",
    );
    for (const d of unbaselined) {
      console.error(`  ${d.file}  <${d.tag}>`);
      console.error(
        `    placeholder (line ${d.placeholderLine}): ${describe(d.placeholder)}`,
      );
      console.error(
        `    hydrated    (line ${d.hydratedLine}): ${describe(d.hydrated)}`,
      );
    }
    console.error(
      "\nHoist the root's class/style into one constant used by both " +
        "returns, so the two cannot drift apart (see IconRail.tsx's " +
        "RAIL_NAV_STYLE or MonacoEditor.tsx's EDITOR_ROOT_CLASS). Do not " +
        "add an entry to placeholder-root-parity-baseline.json for new " +
        "code -- that file records what the Fresh tree already shipped.",
    );
  }

  if (stale.length > 0) {
    console.error(
      "\nPlaceholder/hydrated root parity guard: baseline entries that no " +
        "longer match anything. If you fixed one, delete its entry:\n",
    );
    for (const e of stale) {
      console.error(`  ${e.file}  <${e.tag}>`);
      console.error(`    placeholder: ${describe(e.placeholder)}`);
      console.error(`    hydrated:    ${describe(e.hydrated)}`);
    }
  }

  if (unbaselined.length > 0 || stale.length > 0) process.exit(1);

  const baselineCount = loadBaseline().length;
  console.log(
    `Placeholder/hydrated root parity guard: no new divergence ` +
      `(${baselineCount} inherited, baselined).`,
  );
}

if (import.meta.main) {
  main();
}
