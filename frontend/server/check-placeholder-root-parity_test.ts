import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  BASELINE_PATH,
  type BaselineEntry,
  defaultSourceFiles,
  describeRoots,
  divergenceKey,
  findDivergences,
  findDivergencesInSource,
  isNotBrowser,
  loadBaseline,
  normalizeAttr,
  reconcile,
} from "./check-placeholder-root-parity.ts";

const FRONTEND_DIR = dirname(dirname(fileURLToPath(import.meta.url)));

/** Wraps a component body in a module so the fixtures stay readable. */
function island(body: string): string {
  return `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
     export default function Thing() {
${body}
     }`;
}

/**
 * The guard's reason for existing: the real tree must introduce no divergence
 * beyond the ones inherited from the Fresh tree and recorded in the baseline.
 */
test("the real tree has no unbaselined placeholder/hydrated root divergence", () => {
  const { unbaselined, stale } = reconcile(findDivergences(), loadBaseline());
  expect(unbaselined).toEqual([]);
  expect(stale).toEqual([]);
});

/**
 * The two islands KTD14's screenshot comparison caught. They are pinned by
 * name because the failure is invisible to every other check in this repo --
 * the page returns 200, the build is green, and the only symptom is a
 * stranded attribute in a browser nobody is watching. A well-meaning "the
 * placeholder only needs the dimensions" would put both straight back.
 */
test("IconRail and MonacoEditor share one root constant between both returns", () => {
  const iconRail = readFileSync(
    join(FRONTEND_DIR, "src", "islands", "IconRail.tsx"),
    "utf-8",
  );
  expect(iconRail.match(/RAIL_NAV_STYLE/g)?.length).toBeGreaterThanOrEqual(3);

  const monaco = readFileSync(
    join(FRONTEND_DIR, "src", "components", "ui", "MonacoEditor.tsx"),
    "utf-8",
  );
  expect(monaco.match(/EDITOR_ROOT_CLASS/g)?.length).toBeGreaterThanOrEqual(4);

  for (const source of [iconRail, monaco]) {
    expect(findDivergencesInSource(source, "pinned.tsx")).toEqual([]);
  }
});

// --- what the walk treats as a placeholder ---

test("a placeholder whose root differs from the hydrated root is a finding", () => {
  const found = findDivergencesInSource(
    island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return <div class="flex flex-col h-full">body</div>;`),
    "Thing.tsx",
  );
  expect(found).toHaveLength(1);
  expect(found[0].placeholder.attrs.class).toBe('"p-6"');
  expect(found[0].hydrated.attrs.class).toBe('"flex flex-col h-full"');
});

test("an identical root is not a finding", () => {
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return <div class="p-6">body</div>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

/**
 * The compound-condition idiom. Six shipping islands gate their placeholder on
 * `!IS_BROWSER || loading.value`, which is a LogicalExpression, not a bare
 * UnaryExpression. The first version of `isNotBrowser` matched only the bare
 * form, so `placeholderReturns` stayed empty and the walk skipped those
 * components entirely -- reporting no divergence because it never looked.
 */
test("a compound `!IS_BROWSER || x` guard is a placeholder", () => {
  const found = findDivergencesInSource(
    island(`       if (!IS_BROWSER || loading.value) return <div class="p-6" />;
       return <div class="flex">body</div>;`),
    "Thing.tsx",
  );
  expect(found).toHaveLength(1);
  expect(found[0].placeholder.attrs.class).toBe('"p-6"');
});

test("a compound `!IS_BROWSER && x` guard is a placeholder", () => {
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER && ready) return <div class="p-6" />;
       return <div class="flex">body</div>;`),
      "Thing.tsx",
    ),
  ).toHaveLength(1);
});

test("isNotBrowser accepts the bare and compound forms and nothing else", () => {
  const not = (name: string) => ({
    type: "UnaryExpression",
    operator: "!",
    argument: { type: "Identifier", name },
  });
  expect(isNotBrowser(not("IS_BROWSER"))).toBe(true);
  expect(isNotBrowser(not("SOMETHING_ELSE"))).toBe(false);
  expect(
    isNotBrowser({
      type: "LogicalExpression",
      operator: "||",
      left: not("IS_BROWSER"),
      right: { type: "Identifier", name: "loading" },
    }),
  ).toBe(true);
  expect(
    isNotBrowser({
      type: "LogicalExpression",
      operator: "||",
      left: { type: "Identifier", name: "loading" },
      right: { type: "Identifier", name: "other" },
    }),
  ).toBe(false);
});

/**
 * `if (!IS_BROWSER) return;` inside an effect is the most common use of the
 * identifier in this tree. Mistaking one for a render placeholder -- which a
 * regex over the same two lines would -- makes the guard useless.
 */
test("an IS_BROWSER guard inside a nested callback is not a placeholder", () => {
  expect(
    findDivergencesInSource(
      island(`       useEffect(() => {
         if (!IS_BROWSER) return;
         subscribe();
       }, []);
       return <div class="flex">body</div>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

// --- which root a return contributes ---

/**
 * Preact discards an element whose type changed and diffs every prop onto the
 * replacement, so a tag mismatch strands nothing. Flagging it would bury the
 * real findings in noise.
 */
test("a different tag is not a finding", () => {
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return <section class="flex">body</section>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

/** Only the root can be stranded; a child is created fresh and gets its props. */
test("a divergence below the root is not a finding", () => {
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return <div class="p-6"><span class="text-sm">body</span></div>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

/**
 * A fragment has no DOM node, but Preact flattens it and matches its children
 * positionally, so the first element child is what can be stranded. Returning
 * null for the fragment -- the first version's behaviour -- let a real
 * regression pass the only detector this defect class has.
 */
test("a fragment's first element child is the root", () => {
  const found = findDivergencesInSource(
    island(`       if (!IS_BROWSER) return <><div class="p-6" /></>;
       return <><div class="flex">body</div></>;`),
    "Thing.tsx",
  );
  expect(found).toHaveLength(1);
  expect(found[0].placeholder.attrs.class).toBe('"p-6"');
  expect(found[0].hydrated.attrs.class).toBe('"flex"');
});

test("a fragment wrapping a matching root is not a finding", () => {
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER) return <><div class="p-6" /></>;
       return <><div class="p-6">body</div></>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

/** Either branch of a conditional can render, so both are candidate roots. */
test("both branches of a ternary root are compared", () => {
  const found = findDivergencesInSource(
    island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return ready ? <div class="a">x</div> : <div class="b">y</div>;`),
    "Thing.tsx",
  );
  expect(found).toHaveLength(2);
  expect(found.map((f) => f.hydrated.attrs.class).sort()).toEqual([
    '"a"',
    '"b"',
  ]);
});

test("a component root yields no candidate", () => {
  expect(describeRoots({ type: "Identifier", name: "x" }, "")).toEqual([]);
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER) return <Modal class="p-6" />;
       return <Modal class="flex">body</Modal>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

// --- which props are compared ---

/**
 * The mechanism is not specific to class and style. Anything hydration does
 * not re-apply strands the same way, and an `aria-live` that never reaches the
 * DOM is an accessibility defect no screenshot diff would show.
 */
test("a divergence in a prop other than class or style is a finding", () => {
  const found = findDivergencesInSource(
    island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return <div class="p-6" id="term" role="log" aria-live="polite">body</div>;`),
    "Thing.tsx",
  );
  expect(found).toHaveLength(1);
  expect(found[0].hydrated.attrs.role).toBe('"log"');
  expect(found[0].hydrated.attrs["aria-live"]).toBe('"polite"');
});

/** Hydration is precisely what attaches event handlers, so they never strand. */
test("an event handler present on only one root is not a finding", () => {
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return <div class="p-6" onClick={go}>body</div>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

test("class and className are the same DOM property", () => {
  expect(
    findDivergencesInSource(
      island(`       if (!IS_BROWSER) return <div className="p-6" />;
       return <div class="p-6">body</div>;`),
      "Thing.tsx",
    ),
  ).toEqual([]);
});

test("reformatting a style object does not read as a new divergence", () => {
  const oneLine = findDivergencesInSource(
    island(`       if (!IS_BROWSER) return <div style={{ minHeight: "400px" }} />;
       return <div class="flex">body</div>;`),
    "Thing.tsx",
  );
  const wrapped = findDivergencesInSource(
    island(`       if (!IS_BROWSER) {
         return (
           <div
             style={{
               minHeight: "400px",
             }}
           />
         );
       }
       return <div class="flex">body</div>;`),
    "Thing.tsx",
  );
  expect(divergenceKey(wrapped[0])).toBe(divergenceKey(oneLine[0]));
});

// --- the baseline ---

/**
 * The hole that made the first baseline unsafe. Keying only on file, tag and
 * attribute text meant a newly added branch whose root happened to read the
 * same as an existing entry inherited that entry's permission: suppressed,
 * with no new entry and no stale entry to notice. The occurrence in the key is
 * what closes it.
 */
test("a second same-shaped branch does not inherit an existing baseline entry", () => {
  const one = findDivergencesInSource(
    island(`       if (!IS_BROWSER) return <div class="p-6" />;
       return <div class="flex">a</div>;`),
    "Thing.tsx",
  );
  const baseline: BaselineEntry[] = [
    {
      file: "Thing.tsx",
      tag: "div",
      at: one[0].at,
      placeholder: { attrs: one[0].placeholder.attrs },
      hydrated: { attrs: one[0].hydrated.attrs },
      note: "Inherited verbatim from the pre-migration Fresh tree.",
    },
  ];
  expect(reconcile(one, baseline).unbaselined).toEqual([]);

  // Same file, same shape, one extra branch added afterwards.
  const two = findDivergencesInSource(
    island(`       if (!IS_BROWSER) return <div class="p-6" />;
       if (loading) return <div class="flex">a</div>;
       return <div class="flex">b</div>;`),
    "Thing.tsx",
  );
  expect(two).toHaveLength(2);
  const { unbaselined } = reconcile(two, baseline);
  expect(unbaselined).toHaveLength(1);
});

/** A fixed divergence must force its record to be deleted, so the list shrinks. */
test("a baseline entry matching nothing is reported as stale", () => {
  const { stale } = reconcile(
    [],
    [
      {
        file: "Gone.tsx",
        tag: "div",
        at: {
          component: 0,
          placeholder: 0,
          placeholderRoot: 0,
          hydrated: 0,
          hydratedRoot: 0,
        },
        placeholder: { attrs: { class: '"p-6"' } },
        hydrated: { attrs: { class: '"flex"' } },
        note: "Inherited verbatim from the pre-migration Fresh tree.",
      },
    ],
  );
  expect(stale).toHaveLength(1);
  expect(stale[0].file).toBe("Gone.tsx");
});

test("normalizeAttr collapses whitespace, trailing commas, and preserves null", () => {
  expect(normalizeAttr("{{ a: 1,\n   b: 2 }}")).toBe("{{ a: 1, b: 2 }}");
  expect(normalizeAttr("{{ a: 1, }}")).toBe("{{ a: 1 }}");
  expect(normalizeAttr(null)).toBeNull();
});

/**
 * Every baseline entry must say where it came from. The file is a record of
 * what the pre-migration tree already shipped, not a place to silence a new
 * finding, and an entry with no provenance cannot be told apart from one.
 */
test("every baseline entry cites its pre-migration origin", () => {
  const baseline = JSON.parse(
    readFileSync(BASELINE_PATH, "utf-8"),
  ) as BaselineEntry[];
  expect(baseline.length).toBeGreaterThan(0);
  for (const entry of baseline) {
    expect(entry.note).toContain("Fresh tree");
    expect(entry.at).toBeDefined();
  }
  // No duplicate keys -- a duplicate can never be matched and would be
  // reported as stale forever.
  const keys = baseline.map(divergenceKey);
  expect(new Set(keys).size).toBe(keys.length);
});

// --- what the guard scans ---

/**
 * The scope assertion. The first version walked `src/islands` and
 * `src/components` on the premise that `components/` at the frontend root was
 * part of the Fresh tree U12 deletes. It is not -- it is shared infrastructure
 * that survives, and its files render inside Astro islands, so they sat
 * outside the only detector this defect class has with nothing failing.
 */
test("the scan covers the whole src tree and the shared components tree", () => {
  const files = defaultSourceFiles().map((f) =>
    f.replace(/\\/g, "/").replace(`${FRONTEND_DIR.replace(/\\/g, "/")}/`, ""),
  );
  expect(files.some((f) => f.startsWith("src/islands/"))).toBe(true);
  expect(files.some((f) => f.startsWith("src/components/"))).toBe(true);
  expect(files.some((f) => f.startsWith("components/"))).toBe(true);
  // Nothing under the scan may be a test file.
  expect(files.some((f) => f.endsWith("_test.tsx"))).toBe(false);
});

/**
 * And the shape of the scan: it walks trees rather than a hardcoded list of
 * subdirectories, so a future `src/layouts` or `src/widgets` is covered
 * without anyone remembering to add it.
 */
test("the scan is not limited to a fixed list of subdirectories", () => {
  const source = readFileSync(
    join(FRONTEND_DIR, "server", "check-placeholder-root-parity.ts"),
    "utf-8",
  );
  expect(source).toContain('walkTsx(join(root, "src"), files)');
  expect(source).toContain('walkTsx(join(root, "components"), files)');
});
