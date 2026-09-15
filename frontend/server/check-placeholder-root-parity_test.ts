import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  BASELINE_PATH,
  type BaselineEntry,
  describeRoot,
  divergenceKey,
  findDivergences,
  findDivergencesInSource,
  loadBaseline,
  normalizeAttr,
  reconcile,
} from "./check-placeholder-root-parity.ts";

const FRONTEND_DIR = dirname(dirname(fileURLToPath(import.meta.url)));

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
 * stranded class in a browser nobody is watching. A well-meaning "the
 * placeholder only needs the dimensions" would put both straight back.
 */
test("IconRail and MonacoEditor share one root constant between both returns", () => {
  const iconRail = readFileSync(
    join(FRONTEND_DIR, "src", "islands", "IconRail.tsx"),
    "utf-8",
  );
  // One definition, used by the placeholder and the hydrated root.
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

test("a placeholder whose root differs from the hydrated root is a finding", () => {
  const found = findDivergencesInSource(
    `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
     export default function Thing() {
       if (!IS_BROWSER) return <div class="p-6" />;
       return <div class="flex flex-col h-full">body</div>;
     }`,
    "Thing.tsx",
  );
  expect(found).toHaveLength(1);
  expect(found[0].placeholder.class).toBe('"p-6"');
  expect(found[0].hydrated.class).toBe('"flex flex-col h-full"');
});

test("an identical root is not a finding", () => {
  expect(
    findDivergencesInSource(
      `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
       export default function Thing() {
         if (!IS_BROWSER) return <div class="p-6" />;
         return <div class="p-6">body</div>;
       }`,
      "Thing.tsx",
    ),
  ).toEqual([]);
});

/**
 * Preact discards an element whose type changed and diffs every prop onto the
 * replacement, so a tag mismatch strands nothing. Flagging it would bury the
 * real findings in noise.
 */
test("a different tag is not a finding", () => {
  expect(
    findDivergencesInSource(
      `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
       export default function Thing() {
         if (!IS_BROWSER) return <div class="p-6" />;
         return <section class="flex">body</section>;
       }`,
      "Thing.tsx",
    ),
  ).toEqual([]);
});

/**
 * `if (!IS_BROWSER) return;` inside an effect is the most common use of the
 * identifier in this tree. Mistaking one for a render placeholder -- which a
 * regex over the same two lines would -- makes the guard useless.
 */
test("an IS_BROWSER guard inside a nested callback is not a placeholder", () => {
  expect(
    findDivergencesInSource(
      `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
       export default function Thing() {
         useEffect(() => {
           if (!IS_BROWSER) return;
           subscribe();
         }, []);
         return <div class="flex">body</div>;
       }`,
      "Thing.tsx",
    ),
  ).toEqual([]);
});

/** Only the root can be stranded; a child is created fresh and gets its props. */
test("a divergence below the root is not a finding", () => {
  expect(
    findDivergencesInSource(
      `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
       export default function Thing() {
         if (!IS_BROWSER) return <div class="p-6" />;
         return <div class="p-6"><span class="text-sm">body</span></div>;
       }`,
      "Thing.tsx",
    ),
  ).toEqual([]);
});

test("reformatting a style object does not read as a new divergence", () => {
  const oneLine = findDivergencesInSource(
    `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
     export default function Thing() {
       if (!IS_BROWSER) return <div style={{ minHeight: "400px" }} />;
       return <div class="flex">body</div>;
     }`,
    "Thing.tsx",
  );
  const wrapped = findDivergencesInSource(
    `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
     export default function Thing() {
       if (!IS_BROWSER) {
         return (
           <div
             style={{
               minHeight: "400px",
             }}
           />
         );
       }
       return <div class="flex">body</div>;
     }`,
    "Thing.tsx",
  );
  expect(divergenceKey(wrapped[0])).toBe(divergenceKey(oneLine[0]));
});

/**
 * One placeholder compared against a loading branch and an error branch that
 * happen to share a root is one fact, not two -- and two baseline entries for
 * it would leave one permanently unmatched, reported as stale forever.
 */
test("identical shapes across several hydrated returns collapse to one entry", () => {
  const found = findDivergencesInSource(
    `import { IS_BROWSER } from "@/src/lib/is-browser.ts";
     export default function Thing() {
       if (!IS_BROWSER) return <div class="p-6" />;
       if (loading) return <div class="flex">a</div>;
       return <div class="flex">b</div>;
     }`,
    "Thing.tsx",
  );
  expect(found).toHaveLength(2);
  const baseline: BaselineEntry[] = [
    {
      file: "Thing.tsx",
      tag: "div",
      placeholder: { class: '"p-6"', style: null },
      hydrated: { class: '"flex"', style: null },
      note: "test",
    },
  ];
  const { unbaselined, stale } = reconcile(found, baseline);
  expect(unbaselined).toEqual([]);
  expect(stale).toEqual([]);
});

/** A fixed divergence must force its record to be deleted, so the list shrinks. */
test("a baseline entry matching nothing is reported as stale", () => {
  const { stale } = reconcile(
    [],
    [
      {
        file: "Gone.tsx",
        tag: "div",
        placeholder: { class: '"p-6"', style: null },
        hydrated: { class: '"flex"', style: null },
        note: "test",
      },
    ],
  );
  expect(stale).toHaveLength(1);
  expect(stale[0].file).toBe("Gone.tsx");
});

test("a component root is not treated as a DOM element", () => {
  expect(
    describeRoot(
      {
        type: "JSXElement",
        openingElement: {
          name: { type: "JSXIdentifier", name: "Modal" },
          attributes: [],
        },
      },
      "",
    ),
  ).toBeNull();
});

test("normalizeAttr collapses whitespace and preserves null", () => {
  expect(normalizeAttr("{{ a: 1,\n   b: 2 }}")).toBe("{{ a: 1, b: 2 }}");
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
    expect(entry.file.startsWith("src/")).toBe(true);
  }
  // No duplicate keys -- a duplicate can never be matched and would be
  // reported as stale forever.
  const keys = baseline.map(divergenceKey);
  expect(new Set(keys).size).toBe(keys.length);
});
