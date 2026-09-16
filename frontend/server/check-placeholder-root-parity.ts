import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "@babel/parser";

/**
 * Standing guard for the SSR-placeholder / hydrated-root divergence.
 *
 * Preact's hydration pass matches the server-rendered element and recurses
 * into its children *without re-applying that element's own props* -- it sets
 * event handlers and nothing else. For an island written in the usual shape
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
 * Two of these shipped (see IconRail.tsx and MonacoEditor.tsx, both of which
 * now hoist the root's attributes into a shared constant) and were caught only
 * by KTD14's one-time screenshot comparison. This guard exists because that
 * comparison cannot be run after U12, which deleted the tree it diffed
 * against.
 *
 * It also could not have found these on its own. The Fresh tree carried the
 * byte-identical placeholders, so a cross-tree diff cancelled the defect out
 * on both sides. Only the divergences the *port* introduced were visible to
 * it. The baseline is that blind spot, written down.
 *
 * ## What counts as a root
 *
 * A return's root is the first node that becomes a real DOM element:
 *
 * - A plain lowercase JSX element is its own root.
 * - A fragment has no DOM node, but Preact flattens it and matches its
 *   children positionally, so the fragment's first element child is the node
 *   that can be stranded. The walk descends into it.
 * - A conditional (`cond ? <a/> : <b/>`) can render either branch, so both are
 *   candidate roots and each is compared.
 * - A capitalised name is a component; its own root is checked when that
 *   component is visited, so it yields no candidate here.
 *
 * A tag mismatch between two candidates is deliberately not a finding. Preact
 * discards and recreates an element whose type changed, and a freshly created
 * element gets a full prop diff -- so a `<div>` placeholder into a `<section>`
 * root is wasteful but correct. Only same-tag pairs can strand props.
 *
 * ## Which props are compared
 *
 * All of them, minus the ones hydration does apply. `class`/`className` and
 * `style` are the ones that have bitten, but nothing about the mechanism is
 * specific to them: `id`, `role`, `aria-*`, `data-*`, `title`, `tabIndex` and
 * `hidden` strand exactly the same way, and an `aria-live` that never reaches
 * the DOM is an accessibility defect no screenshot diff would show. Event
 * handlers (`on*`) are excluded because hydration is precisely what attaches
 * them; `key` and `ref` are not DOM attributes.
 *
 * ## The baseline
 *
 * Pre-existing divergences live in placeholder-root-parity-baseline.json, one
 * entry each. They are inherited from the Fresh tree rather than introduced by
 * the migration, and fixing them would change what the app renders -- which is
 * precisely what U12 is required to prove it has *not* done. They are a
 * tracked backlog, not an exemption in principle.
 *
 * The baseline is checked in both directions. An entry that no longer matches
 * anything fails the guard too, so a fixed divergence forces its record to be
 * deleted and the list can only shrink.
 *
 * An entry is keyed by its *occurrence* -- the component's ordinal in the
 * file, the placeholder's ordinal within that component, the hydrated return's
 * ordinal, and which candidate root of each -- as well as by the two roots'
 * attributes. Keying on attributes alone let a newly added branch whose root
 * text happened to match an existing entry inherit that entry's permission
 * silently. Ordinals shift when a branch is added or reordered, which fails
 * the guard and forces a fresh look; that is the intended cost. Line numbers
 * are deliberately not part of the key -- an edit anywhere above would
 * invalidate an entry for no reason -- and appear only in diagnostics.
 */

const HERE = dirname(fileURLToPath(import.meta.url));
export const FRONTEND_ROOT = resolve(HERE, "..");
export const BASELINE_PATH = join(
  HERE,
  "placeholder-root-parity-baseline.json",
);

/** Attribute names hydration applies itself, plus the two non-DOM props. */
function isHydrationAppliedProp(name: string): boolean {
  return (
    /^on[A-Z]/.test(name) ||
    name === "key" ||
    name === "ref" ||
    name === "dangerouslySetInnerHTML"
  );
}

/** A candidate root element, and every prop of it hydration will not re-apply. */
export interface RootShape {
  tag: string;
  /** Attribute name -> source text of its value. `true` for a bare attribute. */
  attrs: Record<string, string>;
}

/** Where in a component a divergence occurs. Stable under edits above it. */
export interface Occurrence {
  /** Index of the enclosing function among the file's functions. */
  component: number;
  /** Index of the placeholder return among the component's placeholders. */
  placeholder: number;
  /** Which candidate root of that return (fragments/conditionals yield more). */
  placeholderRoot: number;
  /** Index of the hydrated return among the component's non-placeholders. */
  hydrated: number;
  /** Which candidate root of that return. */
  hydratedRoot: number;
}

export interface Divergence {
  /** Frontend-relative, forward-slashed. */
  file: string;
  tag: string;
  at: Occurrence;
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
  at: Occurrence;
  placeholder: { attrs: Record<string, string> };
  hydrated: { attrs: Record<string, string> };
  note: string;
}

/**
 * Collapses runs of whitespace so a reformatted multi-line object compares
 * equal to the same object on one line, and drops a trailing comma before a
 * closer. Biome owns the formatting of these files and can rewrap an object
 * literal or add a trailing comma without anyone touching it; neither must
 * read as a new divergence.
 */
export function normalizeAttr(text: string | null): string | null {
  if (text === null) return null;
  return text
    .replace(/\s+/g, " ")
    .replace(/,(\s*[}\])])/g, "$1")
    .trim();
}

function normalizeAttrs(attrs: Record<string, string>): string {
  return Object.keys(attrs)
    .sort()
    .map((k) => `${k}=${normalizeAttr(attrs[k])}`)
    .join("\u0001");
}

function sameAttrs(
  a: Record<string, string>,
  b: Record<string, string>,
): boolean {
  return normalizeAttrs(a) === normalizeAttrs(b);
}

/** `class` and `className` are the same DOM property; normalise the name. */
function canonicalAttrName(name: string): string {
  return name === "className" ? "class" : name;
}

/**
 * Every candidate root a return argument can produce. See the file banner for
 * why a fragment and a conditional yield candidates rather than nothing.
 */
// biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
export function describeRoots(node: any, code: string): RootShape[] {
  let n = node;
  while (
    n &&
    (n.type === "TSAsExpression" ||
      n.type === "TSNonNullExpression" ||
      n.type === "ParenthesizedExpression")
  ) {
    n = n.expression;
  }
  if (!n) return [];

  // Either branch can render, so both are candidates.
  if (n.type === "ConditionalExpression") {
    return [
      ...describeRoots(n.consequent, code),
      ...describeRoots(n.alternate, code),
    ];
  }
  // `cond && <div/>` renders the element or nothing; the element is the
  // candidate. `a || <div/>` likewise.
  if (n.type === "LogicalExpression") {
    return [...describeRoots(n.left, code), ...describeRoots(n.right, code)];
  }

  // A fragment has no DOM node of its own. Preact flattens it and matches its
  // children positionally, so the first element child is what can be stranded.
  if (n.type === "JSXFragment") {
    for (const child of n.children ?? []) {
      if (child.type === "JSXText" && child.value.trim() === "") continue;
      if (child.type === "JSXElement" || child.type === "JSXFragment") {
        return describeRoots(child, code);
      }
      // A `{...}` expression as the first child could render anything; the
      // walk cannot know what, so it reports no candidate rather than a wrong
      // one. Recorded as a known limit in the guard's own docs.
      return [];
    }
    return [];
  }

  if (n.type !== "JSXElement") return [];

  const name = n.openingElement.name;
  // A member expression (`<Foo.Bar />`) or a namespaced name is a component.
  if (name.type !== "JSXIdentifier") return [];
  const tag: string = name.name;
  // Capitalised means a component in JSX. Only lowercase intrinsics become a
  // real DOM node whose props hydration can strand.
  if (tag[0] !== tag[0].toLowerCase()) return [];

  const attrs: Record<string, string> = {};
  for (const attr of n.openingElement.attributes) {
    if (attr.type === "JSXSpreadAttribute") {
      // The spread's contents are unknowable statically. Record it verbatim so
      // two identical spreads compare equal and a changed one is a finding,
      // rather than silently comparing the props around it as if complete.
      attrs[`...${code.slice(attr.argument.start, attr.argument.end)}`] =
        "true";
      continue;
    }
    if (attr.type !== "JSXAttribute") continue;
    const rawName = String(attr.name.name);
    if (isHydrationAppliedProp(rawName)) continue;
    attrs[canonicalAttrName(rawName)] = attr.value
      ? code.slice(attr.value.start, attr.value.end)
      : "true";
  }
  return [{ tag, attrs }];
}

/**
 * True when this `if` test gates an SSR placeholder.
 *
 * Matches a bare `!IS_BROWSER` and `!IS_BROWSER` appearing as an operand of a
 * top-level `||` / `&&` chain. The compound form is not hypothetical: six
 * islands ship `if (!IS_BROWSER || loading.value) return <placeholder/>;`, and
 * matching only the bare form meant the walk skipped those components
 * entirely -- reporting no divergence because it never looked.
 */
// biome-ignore lint/suspicious/noExplicitAny: untyped Babel AST
export function isNotBrowser(test: any): boolean {
  if (!test) return false;
  if (
    test.type === "UnaryExpression" &&
    test.operator === "!" &&
    test.argument?.type === "Identifier" &&
    test.argument.name === "IS_BROWSER"
  ) {
    return true;
  }
  if (
    test.type === "LogicalExpression" &&
    (test.operator === "||" || test.operator === "&&")
  ) {
    return isNotBrowser(test.left) || isNotBrowser(test.right);
  }
  return false;
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

  for (let componentIndex = 0; componentIndex < functions.length; ) {
    const fn = functions[componentIndex];
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

    const currentComponent = componentIndex;
    componentIndex += 1;
    if (placeholderReturns.length === 0) continue;

    const placeholderSet = new Set(placeholderReturns);
    const hydratedReturns = allReturns.filter((r) => !placeholderSet.has(r));

    for (let pi = 0; pi < placeholderReturns.length; pi++) {
      const placeholder = placeholderReturns[pi];
      const phRoots = describeRoots(placeholder.argument, code);

      for (let pr = 0; pr < phRoots.length; pr++) {
        const phRoot = phRoots[pr];

        for (let hi = 0; hi < hydratedReturns.length; hi++) {
          const hydrated = hydratedReturns[hi];
          const hyRoots = describeRoots(hydrated.argument, code);

          for (let hr = 0; hr < hyRoots.length; hr++) {
            const hyRoot = hyRoots[hr];
            // Different tag: Preact recreates the node and diffs all props
            // onto it, so nothing is stranded.
            if (hyRoot.tag !== phRoot.tag) continue;
            if (sameAttrs(phRoot.attrs, hyRoot.attrs)) continue;

            divergences.push({
              file,
              tag: phRoot.tag,
              at: {
                component: currentComponent,
                placeholder: pi,
                placeholderRoot: pr,
                hydrated: hi,
                hydratedRoot: hr,
              },
              placeholder: phRoot,
              hydrated: hyRoot,
              placeholderLine: placeholder.loc.start.line,
              hydratedLine: hydrated.loc.start.line,
            });
          }
        }
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
 * Every component file the guard covers.
 *
 * The whole of `src/` and the whole of the shared `components/` tree, rather
 * than a hardcoded list of subdirectories. The first version named
 * `src/islands` and `src/components` on the premise that `components/` at the
 * frontend root was part of the Fresh tree U12 deletes. It is not -- it is
 * shared infrastructure that survives, and 124 of its files render inside
 * Astro islands, so they were outside the only detector this defect class has.
 * Walking the trees rather than a list also means a future `src/layouts` or
 * `src/widgets` is covered without anyone remembering to add it.
 */
export function defaultSourceFiles(root: string = FRONTEND_ROOT): string[] {
  const files: string[] = [];
  walkTsx(join(root, "src"), files);
  walkTsx(join(root, "components"), files);
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
 * The identity of a divergence: which file, which occurrence within it, which
 * tag, and the two roots' attributes.
 *
 * The occurrence is what stops a newly added branch from inheriting an
 * existing entry's permission. Without it the key was file + tag + attribute
 * text, so a second placeholder/hydrated pair in an already-baselined file
 * whose roots happened to read the same was suppressed with no new entry and
 * no stale entry -- the one hole that let a real regression through a guard
 * that reported success.
 */
export function divergenceKey(d: {
  file: string;
  tag: string;
  at: Occurrence;
  placeholder: { attrs: Record<string, string> };
  hydrated: { attrs: Record<string, string> };
}): string {
  return [
    d.file,
    d.tag,
    `${d.at.component}:${d.at.placeholder}.${d.at.placeholderRoot}` +
      `->${d.at.hydrated}.${d.at.hydratedRoot}`,
    normalizeAttrs(d.placeholder.attrs),
    normalizeAttrs(d.hydrated.attrs),
  ].join("\u0000");
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
    if (!baselineKeys.has(key)) unbaselined.push(d);
  }

  return {
    unbaselined,
    stale: baseline.filter((e) => !seen.has(divergenceKey(e))),
  };
}

function describe(shape: { attrs: Record<string, string> }): string {
  const keys = Object.keys(shape.attrs).sort();
  if (keys.length === 0) return "(no attributes)";
  return keys.map((k) => `${k}=${normalizeAttr(shape.attrs[k])}`).join(" ");
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
      "\nHoist the root's attributes into one constant used by both returns, " +
        "so the two cannot drift apart (see IconRail.tsx's RAIL_NAV_STYLE or " +
        "MonacoEditor.tsx's EDITOR_ROOT_CLASS). Do not add an entry to " +
        "placeholder-root-parity-baseline.json for new code -- that file " +
        "records what the pre-migration tree already shipped, and an entry " +
        "there silences the only detector this defect class has.",
    );
  }

  if (stale.length > 0) {
    console.error(
      "\nPlaceholder/hydrated root parity guard: baseline entries that no " +
        "longer match anything. If you fixed one, delete its entry. If you " +
        "added or reordered a return branch, the occurrence it names has " +
        "moved -- re-check the divergence is still the inherited one before " +
        "updating the entry:\n",
    );
    for (const e of stale) {
      console.error(
        `  ${e.file}  <${e.tag}>  (occurrence ${
          `${e.at.component}:${e.at.placeholder}.${e.at.placeholderRoot}` +
          `->${e.at.hydrated}.${e.at.hydratedRoot}`
        })`,
      );
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
