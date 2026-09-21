import { expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
// Importing the widget modules is what registers them, and registration is
// what gives the invariants below something to check. Without registration
// every invariant iterates an empty registry and passes vacuously, so a
// malformed widget would ship green.
//
// The manifest is the single import list, shared with the production render
// path so the two cannot register different sets. The drift guard at the
// bottom of this file checks it against the directory, so adding a widget
// file without listing it fails here rather than silently shrinking what the
// invariants cover.
import "@/components/dashboard/widgets/index.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "./default-layout.ts";
import { KNOWN_PARAM_KEYS } from "./params.ts";
import {
  allWidgets,
  getWidget,
  isRetiredWidgetId,
  RETIRED_WIDGET_IDS,
  registerWidget,
  widgetsForScope,
} from "./registry.ts";
import type { WidgetDef } from "./types.ts";
import {
  DASHBOARD_SCOPES,
  DATA_SOURCE_KEYS,
  DISPLAY_MODES,
  WIDGET_FAMILIES,
} from "./types.ts";

// The registry is the allowlist the server validates against and the catalog
// the palette renders. Every invariant below exists because breaking it
// produces a layout that cannot be rendered, stored, or restored.
//
// Most invariants iterate the registered set, which the manifest import above
// populates with every shipped widget. Each one collects offenders rather than
// asserting per widget, so a failure names the widget instead of only the
// assertion that tripped.
//
// READ BEFORE ADDING A COUNT ASSERTION: `bun test` shares module state across
// test files in one run (verified, not assumed), and the registry is
// append-only by design -- there is no unregister. So the `fixture-*` widgets
// registered below stay in the registry for every other test file in the same
// run. `expect(allWidgets()).toHaveLength(10)` in a D4/D5 test would therefore
// see 12 and fail. Assert on ids, or filter out the `fixture-` prefix.
// The fixtures are deliberately valid definitions so they cannot trip the
// invariants above regardless of the order test files happen to run in.

const KEBAB = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Minimal valid definition, for the tests that exercise registration. */
function defFixture(id: string, over: Partial<WidgetDef> = {}): WidgetDef {
  return {
    id,
    title: "Fixture",
    family: "cluster",
    scopes: ["overview"],
    sources: ["dashboard-summary"],
    minW: 2,
    minH: 2,
    defaultW: 4,
    defaultH: 4,
    modes: ["normal"],
    render: () => null as unknown as ReturnType<WidgetDef["render"]>,
    ...over,
  };
}

test("registry: ids are unique", () => {
  const ids = allWidgets().map((w) => w.id);
  expect(ids.length).toBe(new Set(ids).size);
});

test("registry: ids are kebab-case", () => {
  const offenders = allWidgets()
    .map((w) => w.id)
    .filter((id) => !KEBAB.test(id));
  expect(offenders).toEqual([]);
});

test("registry: no live widget reuses a retired id", () => {
  // A reused id would silently resurrect a stored placement that meant
  // something else.
  const offenders = allWidgets()
    .map((w) => w.id)
    .filter((id) => isRetiredWidgetId(id));
  expect(offenders).toEqual([]);
});

test("registry: every widget implements normal", () => {
  // pickMode falls back toward normal from both directions. A widget without
  // it has no guaranteed rendering at any size.
  const offenders = allWidgets()
    .filter((w) => !w.modes.includes("normal"))
    .map((w) => w.id);
  expect(offenders).toEqual([]);
});

test("registry: modes, family, scopes and sources are all declared values", () => {
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    for (const m of w.modes) {
      if (!DISPLAY_MODES.includes(m)) offenders.push(`${w.id} mode ${m}`);
    }
    if (!WIDGET_FAMILIES.includes(w.family)) {
      offenders.push(`${w.id} family ${w.family}`);
    }
    if (w.scopes.length === 0) offenders.push(`${w.id} has no scope`);
    for (const s of w.scopes) {
      if (!DASHBOARD_SCOPES.includes(s)) offenders.push(`${w.id} scope ${s}`);
    }
    for (const src of w.sources) {
      if (!DATA_SOURCE_KEYS.includes(src)) {
        offenders.push(`${w.id} source ${src}`);
      }
    }
  }
  expect(offenders).toEqual([]);
});

test("registry: default size is at least the minimum size", () => {
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    if (w.defaultW < w.minW) offenders.push(`${w.id} defaultW < minW`);
    if (w.defaultH < w.minH) offenders.push(`${w.id} defaultH < minH`);
  }
  expect(offenders).toEqual([]);
});

test("registry: nothing is wider than the grid", () => {
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    if (w.defaultW > 12) offenders.push(`${w.id} defaultW exceeds 12 columns`);
    if (w.minW < 1) offenders.push(`${w.id} minW below 1`);
  }
  expect(offenders).toEqual([]);
});

test("registry: nothing is short enough to stack the editor's two handles", () => {
  // A row is DASHBOARD_ROW_HEIGHT tall, so minH 1 is a 40px card -- exactly the
  // height of the drag handle that covers the title row. The 24px resize grip
  // in the bottom-right corner would then sit inside the drag handle's box,
  // stealing its last 24px and putting two pointer targets on top of each
  // other, which WCAG 2.2 AA Target Size (Minimum) forbids of adjacent targets.
  //
  // minH 2 is a 96px card, which leaves the two 32px apart. This is the
  // invariant DashboardGrid.tsx's handle geometry depends on and the types
  // cannot express.
  const offenders = allWidgets()
    .filter((w) => w.minH < 2)
    .map((w) => `${w.id} minH below 2`);
  expect(offenders).toEqual([]);
});

test("getWidget: unknown id is undefined, not a throw", () => {
  // Reads drop unknown ids with a notice (spec D-7), so lookup must be
  // total rather than exceptional.
  expect(getWidget("no-such-widget")).toBeUndefined();
});

test("widgetsForScope: returns only widgets declaring that scope", () => {
  const offenders = widgetsForScope("overview")
    .filter((w) => !w.scopes.includes("overview"))
    .map((w) => w.id);
  expect(offenders).toEqual([]);
});

test("registerWidget: a widget becomes findable by id and scope", () => {
  const id = "fixture-registers";
  registerWidget(defFixture(id));
  expect(getWidget(id)?.id).toBe(id);
  expect(widgetsForScope("overview").map((w) => w.id)).toContain(id);
});

test("registerWidget: a duplicate id throws", () => {
  // Second registration of an id would otherwise silently replace the first,
  // surfacing as a widget that renders the wrong thing.
  const id = "fixture-duplicate";
  registerWidget(defFixture(id));
  expect(() => registerWidget(defFixture(id))).toThrow(
    `duplicate widget id ${id}`,
  );
});

test("registerWidget: a definition without normal is rejected", () => {
  // Enforced at the runtime boundary, not only by the invariant test above.
  // pickMode returns "normal" for an empty mode list (undefined would be
  // worse), so a widget lacking it would receive a mode it does not implement.
  expect(() =>
    registerWidget(defFixture("fixture-no-normal", { modes: ["compact"] })),
  ).toThrow('must implement the "normal" display mode');
  expect(() =>
    registerWidget(defFixture("fixture-empty-modes", { modes: [] })),
  ).toThrow('must implement the "normal" display mode');
  // Rejected means not registered, not registered-then-flagged.
  expect(getWidget("fixture-no-normal")).toBeUndefined();
  expect(getWidget("fixture-empty-modes")).toBeUndefined();
});

test("RETIRED_WIDGET_IDS cannot be mutated at runtime", () => {
  // Retirement is permanent. Note this is a frozen array, not a frozen Set:
  // Object.freeze does not prevent Set.prototype.add, so a Set here would
  // document a guarantee it does not have.
  expect(() => {
    (RETIRED_WIDGET_IDS as string[]).push("cluster-health");
  }).toThrow();
  expect(RETIRED_WIDGET_IDS).toHaveLength(0);
});

test("isRetiredWidgetId: a live id is not retired", () => {
  expect(isRetiredWidgetId("cluster-health")).toBe(false);
});

test("optionalSources is always a subset of sources", () => {
  // An optional source that is not declared in `sources` is never fetched, so
  // the widget would render forever against a source nobody requested.
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    for (const k of w.optionalSources ?? []) {
      if (!w.sources.includes(k)) {
        offenders.push(`${w.id} marks ${k} optional but does not declare it`);
      }
    }
  }
  expect(offenders).toEqual([]);
});

/**
 * The widget ids the server will accept, read from the Go allowlist itself.
 *
 * This used to be a hand-typed literal, mirrored by a second hand-typed
 * literal on the Go side, and the pair did not do what both files claimed.
 * Each test compared its own registry against its own literal, in its own
 * language; neither read the other. So the natural edit -- add the widget to
 * `registry.ts` and to the literal right here, which is where the failure
 * points you -- left the Go allowlist untouched and both suites green. The
 * user then builds a layout the editor offers and the server refuses to
 * store, with nothing red anywhere.
 *
 * Reading the real map closes that. A widget added on one side only now fails
 * here, which is what the contract always said it did.
 */
/** One widget's spec exactly as the Go catalog declares it. */
interface ServerWidgetSpec {
  minW: number;
  minH: number;
  /**
   * Parameter key -> the closed set of values it accepts. An empty array is
   * the "any value inside the generic bounds" case and is NOT the same as the
   * key being absent. Null when the widget declares no parameters at all.
   */
  params: Record<string, string[]> | null;
}

/**
 * Returns the inner text of the brace group that opens at `openIndex`.
 *
 * A widget entry is written either inline (`{MinW: 2, MinH: 2}`) or across
 * several lines with a nested `map[string][]string{...}` inside it, so the
 * end of an entry cannot be found by scanning for the next `}`.
 */
function braceBody(src: string, openIndex: number): string {
  let depth = 0;
  for (let i = openIndex; i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}") {
      depth--;
      if (depth === 0) return src.slice(openIndex + 1, i);
    }
  }
  throw new Error(
    "unbalanced braces while parsing allowedWidgets; the map's shape changed",
  );
}

/**
 * Parses the whole `allowedWidgets` catalog out of the Go source -- ids,
 * minimums and parameters.
 *
 * Reading the real Go values rather than keeping a second hand-typed copy is
 * the entire point of this helper. A pin that compares each language's
 * catalog against a table written in that same language is two
 * self-comparisons wearing one name: the natural edit, changing the widget
 * and the table sitting beside it, leaves both suites green while the two
 * catalogs disagree. Only the ids were read here before, so the size and
 * parameter halves of the contract were exactly that.
 */
function serverAllowedWidgets(): Record<string, ServerWidgetSpec> {
  const goFile = join(
    import.meta.dir,
    "../../../backend/internal/preferences/dashboard.go",
  );
  const src = readFileSync(goFile, "utf8");
  const open = src.indexOf("allowedWidgets = map[string]widgetSpec{");
  if (open === -1) {
    throw new Error(
      `could not find allowedWidgets in ${goFile}. If the map was renamed or ` +
        `moved, point this at it -- do not delete this test, because it is ` +
        `the only thing that checks the two catalogs against each other.`,
    );
  }
  const block = braceBody(src, src.indexOf("{", open));

  // Parameter keys are Go constants, not string literals, and the constant is
  // where the spelling the server actually stores is decided -- a namespace
  // key spelled any other way stores and renders identically while silently
  // opting the widget out of per-read re-authorization. Resolve them so this
  // pin checks the stored spelling rather than the identifier someone typed.
  const constants: Record<string, string> = {};
  for (const m of src.matchAll(/^const (paramKey\w+) = "([^"]+)"$/gm)) {
    constants[m[1]] = m[2];
  }

  const specs: Record<string, ServerWidgetSpec> = {};
  for (const m of block.matchAll(/^\t"([a-z0-9-]+)":\s*\{/gm)) {
    const id = m[1];
    const body = braceBody(block, m.index + m[0].length - 1);
    const minW = Number(/\bMinW:\s*(\d+)/.exec(body)?.[1]);
    const minH = Number(/\bMinH:\s*(\d+)/.exec(body)?.[1]);
    if (!Number.isInteger(minW) || !Number.isInteger(minH)) {
      throw new Error(
        `could not parse MinW/MinH for "${id}" out of allowedWidgets in ` +
          `${goFile}; the entry's shape changed and this parse is now blind ` +
          `to it. Fix the parse -- a silently unparsed entry is an unpinned ` +
          `one.`,
      );
    }
    specs[id] = { minW, minH, params: parseServerParams(body, constants, id) };
  }
  if (Object.keys(specs).length === 0) {
    throw new Error(
      `parsed zero widget ids out of allowedWidgets in ${goFile}; the map's ` +
        `shape changed and this parse is now vacuous.`,
    );
  }
  return specs;
}

/** Parses one entry's `Params:` map, resolving Go constant keys. */
function parseServerParams(
  body: string,
  constants: Record<string, string>,
  id: string,
): Record<string, string[]> | null {
  const at = body.indexOf("Params:");
  if (at === -1) return null;
  const mapAt = body.indexOf("map[string][]string", at);
  if (mapAt === -1) {
    throw new Error(
      `"${id}" declares Params in a shape this parse does not understand; ` +
        `fix the parse rather than leaving the parameter half unpinned.`,
    );
  }
  const inner = braceBody(body, body.indexOf("{", mapAt));
  const params: Record<string, string[]> = {};
  for (const m of inner.matchAll(/(?:"([^"]+)"|(\w+))\s*:\s*\{([^}]*)\}/g)) {
    const key = m[1] ?? constants[m[2]];
    if (key === undefined) {
      throw new Error(
        `"${id}" declares parameter key \`${m[2]}\`, which is not a ` +
          `paramKey* constant in dashboard.go, so this parse cannot resolve ` +
          `the spelling the server stores.`,
      );
    }
    params[key] = [...m[3].matchAll(/"([^"]*)"/g)].map((v) => v[1]);
  }
  return params;
}

function serverAllowedWidgetIDs(): string[] {
  return Object.keys(serverAllowedWidgets()).sort();
}

test("registry ids are pinned to the server-side allowlist", () => {
  // The other half of a cross-language contract. The server validates a saved
  // layout against `allowedWidgets` in
  // backend/internal/preferences/dashboard.go -- so a widget added here and
  // not there produces a layout the user can build in the editor and the
  // server then refuses on save.
  //
  // `fixture-*` ids are filtered out: `bun test` shares module state across
  // files and the registry is append-only, so the registration tests above
  // leave their fixtures behind (see the note at the top of this file).
  const ids = allWidgets()
    .map((w) => w.id)
    .filter((id) => !id.startsWith("fixture-"))
    .sort();
  const server = serverAllowedWidgetIDs();

  // Subset, not equality. The server's map is deliberately a superset: a
  // retired widget keeps its entry there so a stored layout that still
  // carries the placement is not bricked, while the registry loses it. That
  // is the documented retirement procedure, so asserting equality would make
  // the first correct retirement fail this test.
  const unregistered = ids.filter((id) => !server.includes(id));
  expect(unregistered).toEqual([]);

  // The other direction is not free, though: an id the server accepts and the
  // registry does not know is either a retirement or a widget someone deleted
  // without retiring it. Only the first is allowed, and RETIRED_WIDGET_IDS is
  // where that is declared.
  const serverOnly = server.filter(
    (id) => !ids.includes(id) && !isRetiredWidgetId(id),
  );
  expect(serverOnly).toEqual([]);
});

test("registry minimums and parameters are pinned to the server-side catalog", () => {
  // The other half of the size contract. The server refuses a placement below
  // a widget's declared minimum, using its own copy of these numbers in
  // `allowedWidgets` (backend/internal/preferences/dashboard.go) because it
  // cannot read this registry. A minimum changed here and not there produces
  // an editor that lets the user resize to something the server then rejects,
  // citing a bound the client never showed -- or the reverse, an editor that
  // refuses a size the server would have taken.
  //
  // The Go values are PARSED out of dashboard.go rather than restated here.
  // A literal in this file would only be this language's copy of the numbers,
  // so the natural edit -- change the widget, change the table beside it --
  // would keep both suites green while the two catalogs disagreed. That is
  // precisely what a mutation of cpu-tile's MinW proved before this changed.
  //
  // The Go half is TestContractParity/"widget specs" in
  // backend/internal/preferences/parity_test.go. It is an in-language pin
  // against accidental edits to allowedWidgets; THIS test is the one that
  // actually compares the two languages.
  const server = serverAllowedWidgets();
  const drift: string[] = [];

  for (const w of allWidgets()) {
    if (w.id.startsWith("fixture-")) continue;
    const spec = server[w.id];
    // A widget registered here and absent there is the ids test's finding,
    // not this one's; reporting it twice would just double the noise.
    if (!spec) continue;

    if (w.minW !== spec.minW || w.minH !== spec.minH) {
      drift.push(
        `${w.id} minimum: registry ${w.minW}x${w.minH}, ` +
          `server ${spec.minW}x${spec.minH}`,
      );
    }

    const here = normalizeParams(w.params ?? null);
    const there = normalizeParams(spec.params);
    if (here !== there) {
      drift.push(`${w.id} parameters: registry ${here}, server ${there}`);
    }
  }

  expect(drift).toEqual([]);
});

/**
 * Canonical string for a parameter surface, so the two languages compare by
 * value. Key order and value order are not part of the contract; the set of
 * keys and the set of values each key accepts are.
 */
function normalizeParams(
  params: Readonly<Record<string, readonly string[]>> | null,
): string {
  if (params === null || Object.keys(params).length === 0) return "none";
  return Object.keys(params)
    .sort()
    .map((k) => `${k}=[${[...params[k]].sort().join(",")}]`)
    .join(" ");
}

test("the parameterized widgets and their declared keys are pinned", () => {
  // This replaced the "no shipped widget declares parameters yet" tripwire the
  // moment the first parameterized widget landed, which is exactly what that
  // tripwire was for. The pin is stricter than the one it replaced: the server
  // validates a stored value against the widget's own declaration
  // (`allowedWidgets` in backend/internal/preferences/dashboard.go), so a key
  // added here and not there makes every layout carrying it unsaveable, and a
  // value set narrowed on one side only makes the two disagree about which
  // values are legal.
  //
  // An empty array means "any value inside the generic bounds" -- a namespace
  // name, whose legal values are not knowable ahead of time -- and is
  // deliberately NOT the same as the key being absent. The Go half is
  // TestContractParity/"widget specs".
  const declared = Object.fromEntries(
    allWidgets()
      .filter((w) => !w.id.startsWith("fixture-"))
      .filter((w) => w.params !== undefined)
      .map((w) => [w.id, w.params]),
  );
  expect(declared).toEqual({
    "diagnostics-summary": { namespace: [] },
  });
});

test("every declared parameter key is one the server recognises", () => {
  // The namespace key is special-cased by the read path, which re-authorizes
  // its value on every read (R5). A widget spelling it differently would look
  // identical in the editor and in storage and would silently opt out of that
  // -- the placement would never be withheld, and a user who lost access to
  // the namespace would go on seeing the card. Nothing else would notice, so
  // this and `registerWidget`'s own guard are the whole defence.
  const offenders: string[] = [];
  for (const w of allWidgets()) {
    for (const key of Object.keys(w.params ?? {})) {
      if (!KNOWN_PARAM_KEYS.includes(key)) {
        offenders.push(`${w.id} declares unknown param ${key}`);
      }
    }
  }
  expect(offenders).toEqual([]);
});

test("registerWidget: an unknown parameter key is rejected", () => {
  // Enforced at the runtime boundary and not only by the invariant above: a
  // definition registered from anywhere has to satisfy it, and the failure
  // this prevents is invisible by construction.
  expect(() =>
    registerWidget(defFixture("fixture-bad-param", { params: { ns: [] } })),
  ).toThrow("ns");
  expect(getWidget("fixture-bad-param")).toBeUndefined();
});

test("the default layout satisfies every widget's declared minimum", () => {
  // The default is the one layout every user starts from, and it is now
  // validated server-side against these minimums -- so a minimum raised above
  // what the default uses would make the starting dashboard unsavable. The Go
  // side runs the same layout through the real validator; this catches the
  // mismatch at its source, where the numbers actually live.
  const offenders: string[] = [];
  for (const item of DEFAULT_OVERVIEW_LAYOUT.items) {
    const def = getWidget(item.id);
    if (!def) {
      offenders.push(`${item.id} is not registered`);
      continue;
    }
    if (item.w < def.minW) {
      offenders.push(`${item.id} w=${item.w} below minW=${def.minW}`);
    }
    if (item.h < def.minH) {
      offenders.push(`${item.id} h=${item.h} below minH=${def.minH}`);
    }
  }
  expect(offenders).toEqual([]);
});

test("every widget module is listed in the manifest", () => {
  // The drift guard. Registration is an import side effect, so a widget file
  // the manifest does not import registers nothing -- and every invariant
  // above would keep passing while covering one widget fewer than the
  // codebase contains. That is the vacuous-pass failure this suite exists to
  // prevent, narrowed to "widgets someone forgot to list".
  const dir = join(
    import.meta.dir,
    "..",
    "..",
    "components",
    "dashboard",
    "widgets",
  );
  const modules = readdirSync(dir).filter(
    (f) => f.endsWith(".tsx") && !f.endsWith("_test.tsx"),
  );
  const manifest = readFileSync(join(dir, "index.ts"), "utf8");

  expect(modules.length).toBeGreaterThan(0);
  const missing = modules.filter((f) => !manifest.includes(`"./${f}"`));
  expect(missing).toEqual([]);
});
