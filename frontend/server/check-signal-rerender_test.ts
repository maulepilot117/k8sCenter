import { afterEach, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { checkIslands } from "./check-signal-rerender.ts";
import { FRONTEND_ROOT } from "./source-files.ts";

const STORE = [
  'import { signal } from "@preact/signals-core";',
  "export const count = signal(0);",
].join("\n");

const PAGE = [
  "---",
  'import A from "../islands/A.tsx";',
  "---",
  "<A client:load />",
].join("\n");

let roots: string[] = [];
afterEach(() => {
  for (const r of roots) rmSync(r, { recursive: true, force: true });
  roots = [];
});

/** A frontend tree with one page hydrating src/islands/A.tsx. */
function tree(files: Record<string, string>): string {
  const root = mkdtempSync(join(tmpdir(), "k8sc-signal-rerender-"));
  roots.push(root);
  const all = {
    "src/pages/index.astro": PAGE,
    "src/lib/store.ts": STORE,
    ...files,
  };
  for (const [path, code] of Object.entries(all)) {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), code);
  }
  return root;
}

const READS_STORE = [
  'import { count } from "../lib/store.ts";',
  "export default function A() {",
  "  return <p>{count.value}</p>;",
  "}",
].join("\n");

test("a Preact island reading a store signal without @preact/signals fails, naming the read", () => {
  const root = tree({ "src/islands/A.tsx": READS_STORE });
  expect(checkIslands(root)).toEqual([
    { island: "src/islands/A.tsx", file: "src/islands/A.tsx", line: 3 },
  ]);
});

test("the read deep in a Preact component the island renders is found", () => {
  const root = tree({
    "src/islands/A.tsx": [
      'import { Counter } from "../components/Counter.tsx";',
      "export default function A() { return <Counter />; }",
    ].join("\n"),
    "src/components/Counter.tsx": READS_STORE.replace(
      "export default function A",
      "export function Counter",
    ),
  });
  expect(checkIslands(root)).toEqual([
    {
      island: "src/islands/A.tsx",
      file: "src/components/Counter.tsx",
      line: 3,
    },
  ]);
});

test("loading @preact/signals anywhere in the island's imports passes", () => {
  const direct = tree({
    "src/islands/A.tsx": `import "@preact/signals";\n${READS_STORE}`,
  });
  expect(checkIslands(direct)).toEqual([]);

  const transitive = tree({
    "src/islands/A.tsx": `import { useX } from "../lib/x.ts";\n${READS_STORE}`,
    "src/lib/x.ts": 'export { useSignal as useX } from "@preact/signals";',
  });
  expect(checkIslands(transitive)).toEqual([]);
});

test("a type-only import of @preact/signals does not load it", () => {
  const typeOnly = [
    'import type { Signal } from "@preact/signals";',
    'import { type ReadonlySignal } from "@preact/signals";',
    READS_STORE,
  ].join("\n");
  const root = tree({ "src/islands/A.tsx": typeOnly });
  expect(checkIslands(root)).toHaveLength(1);
});

test("a file on React APIs does not need the patch", () => {
  const root = tree({
    "src/islands/A.tsx": `/** @jsxImportSource react */\n${READS_STORE}`,
  });
  expect(checkIslands(root)).toEqual([]);
});

test(".value read in a store module, an event target, or an assignment is not a render read", () => {
  const root = tree({
    "src/lib/store.ts": `${STORE}\nexport const read = () => count.value;`,
    "src/islands/A.tsx": [
      'import { count, read } from "../lib/store.ts";',
      "export default function A() {",
      "  const bump = () => { count.value = read() + 1; count.value++; };",
      "  return <input onInput={(e) => e.currentTarget.value} onClick={bump} />;",
      "}",
    ].join("\n"),
  });
  expect(checkIslands(root)).toEqual([]);
});

test("an island that reaches no signal, or is not hydrated, is not checked", () => {
  const noSignal = tree({
    "src/islands/A.tsx": [
      "export default function A({ o }: { o: { value: string } }) {",
      "  return <p>{o.value}</p>;",
      "}",
    ].join("\n"),
  });
  expect(checkIslands(noSignal)).toEqual([]);

  const unhydrated = tree({
    "src/pages/index.astro": PAGE.replace(" client:load", ""),
    "src/islands/A.tsx": READS_STORE,
  });
  expect(checkIslands(unhydrated)).toEqual([]);
});

test("the frontend tree passes", () => {
  expect(checkIslands(FRONTEND_ROOT)).toEqual([]);
});
