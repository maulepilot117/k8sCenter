import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { checkSource, checkTree } from "./check-no-preact.ts";
import { FRONTEND_ROOT } from "./source-files.ts";

const messages = (code: string) =>
  checkSource(code, "x.tsx").map((f) => `${f.line}: ${f.message}`);

test("every Preact package import fails, naming the specifier and line", () => {
  const code = [
    'import { useState } from "preact/hooks";',
    'import type { ComponentChildren } from "preact";',
    'import { useSignal } from "@preact/signals";',
    'import { render } from "@testing-library/preact";',
    'import { render as ssr } from "preact-render-to-string";',
    'import { signal } from "@preact/signals-core";',
    'import type { Signal } from "@preact/signals-react";',
  ].join("\n");
  expect(messages(code)).toEqual([
    '1: imports "preact/hooks"; the frontend runs on React',
    '2: imports "preact"; the frontend runs on React',
    '3: imports "@preact/signals"; the frontend runs on React',
    '4: imports "@testing-library/preact"; the frontend runs on React',
    '5: imports "preact-render-to-string"; the frontend runs on React',
    '6: imports "@preact/signals-core"; the frontend runs on React',
    '7: imports "@preact/signals-react"; the frontend runs on React',
  ]);
});

test("re-exports and dynamic imports count as imports", () => {
  const code = [
    'export { useEffect } from "preact/hooks";',
    'export * from "preact/compat";',
    'const m = await import("preact");',
  ].join("\n");
  expect(messages(code)).toEqual([
    '1: imports "preact/hooks"; the frontend runs on React',
    '2: imports "preact/compat"; the frontend runs on React',
    '3: imports "preact"; the frontend runs on React',
  ]);
});

test("React imports and the in-repo signal store pass", () => {
  const code = [
    'import { signal } from "@/src/lib/signals.ts";',
    'import { useState } from "react";',
    'import { render } from "@testing-library/react";',
    'import { renderToString } from "react-dom/server";',
  ].join("\n");
  expect(messages(code)).toEqual([]);
});

test("any jsxImportSource pragma fails, react included", () => {
  expect(
    messages("/** @jsxImportSource preact */\nexport const A = () => <div />;"),
  ).toEqual([
    '1: sets "@jsxImportSource preact"; tsconfig.json sets the JSX runtime, and it is React',
  ]);
  expect(
    messages("/** @jsxImportSource react */\nexport const A = () => <div />;"),
  ).toEqual([
    '1: sets "@jsxImportSource react"; tsconfig.json sets the JSX runtime, and it is React',
  ]);
});

test("a pragma after the first statement fails too; Bun still reads it", () => {
  expect(
    messages("export const A = 1;\n/** @jsxImportSource preact */"),
  ).toEqual([
    '2: sets "@jsxImportSource preact"; tsconfig.json sets the JSX runtime, and it is React',
  ]);
});

test("Preact-only attributes on DOM elements fail, naming React's spelling", () => {
  const code = [
    "export const A = () => (",
    "  <>",
    '    <div class="p-2" />',
    '    <label for="a" />',
    "    <input onFocusOut={() => {}} />",
    '    <path stroke-width="2" xlink:href="#b" />',
    "  </>",
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([
    '3: <div> uses "class"; React spells it "className"',
    '4: <label> uses "for"; React spells it "htmlFor"',
    '5: <input> uses "onFocusOut"; React spells it "onBlur"',
    '6: <path> uses "stroke-width"; React spells it "strokeWidth"',
    '6: <path> uses "xlink:href"; React spells it "xlinkHref"',
  ]);
});

test("aria-, data-, custom elements and component props pass", () => {
  const code = [
    "export const A = () => (",
    "  <>",
    '    <div aria-label="x" data-id="y" className="z" />',
    '    <my-widget some-attr="1" />',
    '    <Card class="prop-not-attribute" />',
    "  </>",
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([]);
});

test("the tree check covers source, tests, server code and astro callers", () => {
  const root = mkdtempSync(join(tmpdir(), "k8sc-no-preact-"));
  try {
    const files: Record<string, string> = {
      "src/islands/Old.tsx": 'import { useState } from "preact/hooks";',
      "lib/old_test.tsx": 'import { render } from "preact-render-to-string";',
      "server/tool.ts": 'import { h } from "preact";',
      "components/New.tsx":
        'import { useState } from "react";\nexport const B = () => <div className="b" />;',
      "lib/store.ts": 'import { signal } from "@preact/signals-core";',
      "lib/new-store.ts": 'import { signal } from "@/src/lib/signals.ts";',
      "src/pages/index.astro": [
        "---",
        'import { Logo } from "../components/ui/Logo.tsx";',
        'import Island from "../islands/Island.tsx";',
        "---",
        '<div class="plain-html-is-fine" />',
        '<Logo class="mx-auto" />',
        '<Logo className="ok" />',
        '<Island class:list={["a"]} />',
      ].join("\n"),
    };
    for (const [path, contents] of Object.entries(files)) {
      mkdirSync(dirname(join(root, path)), { recursive: true });
      writeFileSync(join(root, path), contents);
    }
    expect(
      checkTree(root).map((f) => `${f.file}:${f.line} ${f.message}`),
    ).toEqual([
      'lib/old_test.tsx:1 imports "preact-render-to-string"; the frontend runs on React',
      'lib/store.ts:1 imports "@preact/signals-core"; the frontend runs on React',
      'server/tool.ts:1 imports "preact"; the frontend runs on React',
      'src/islands/Old.tsx:1 imports "preact/hooks"; the frontend runs on React',
      'src/pages/index.astro:6 <Logo> is a React component; pass "className", not "class"',
    ]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("a file that cannot be parsed fails with its path", () => {
  expect(() => checkSource("const = ;", "broken.ts")).toThrow(
    "broken.ts: could not parse",
  );
});

test("the repository itself has no Preact", () => {
  expect(checkTree(FRONTEND_ROOT)).toEqual([]);
});
