import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { checkSource, checkTree } from "./check-react-bridge.ts";

const messages = (code: string) =>
  checkSource(code, "x.tsx").findings.map((f) => `${f.line}: ${f.message}`);

test("a file importing from react and @preact/signals fails, naming both", () => {
  const code = [
    'import { useState } from "react";',
    'import { useSignal } from "@preact/signals";',
  ].join("\n");
  expect(messages(code)).toEqual([
    '2: imports from both React ("react" (line 1)) and Preact ' +
      '("@preact/signals" (line 2)); convert the Preact imports to React',
  ]);
});

test("type imports, re-exports and dynamic imports count as imports", () => {
  const typeOnly = [
    'import { useState } from "react";',
    'import type { ComponentChildren } from "preact";',
  ].join("\n");
  expect(checkSource(typeOnly, "x.tsx").findings).toHaveLength(1);

  const reexport = [
    'export { useEffect } from "preact/hooks";',
    'const m = await import("react-dom/client");',
  ].join("\n");
  expect(checkSource(reexport, "x.tsx").findings).toHaveLength(1);
});

test("a react file with class= fails and names the line", () => {
  const code = [
    'import { useState } from "react";',
    "export const A = () => (",
    '  <div class="p-2" />',
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([
    '3: <div> uses "class"; React spells it "className"',
  ]);
});

test("a react file with onFocusOut fails", () => {
  const code = [
    'import { useState } from "react";',
    "export const A = () => <input onFocusOut={() => {}} />;",
  ].join("\n");
  expect(messages(code)).toEqual([
    '2: <input> uses "onFocusOut"; React spells it "onBlur"',
  ]);
});

test("a react file with for= or hyphenated SVG attributes fails", () => {
  const code = [
    'import { useId } from "react";',
    "export const A = () => (",
    "  <>",
    '    <label for="a" />',
    '    <path stroke-width="2" xlink:href="#b" />',
    "  </>",
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([
    '4: <label> uses "for"; React spells it "htmlFor"',
    '5: <path> uses "stroke-width"; React spells it "strokeWidth"',
    '5: <path> uses "xlink:href"; React spells it "xlinkHref"',
  ]);
});

test("aria-*, data-* and custom-element attributes are not flagged", () => {
  const code = [
    'import { useState } from "react";',
    "export const A = () => (",
    '  <div aria-label="x" data-testid="y" className="z">',
    '    <my-widget some-attr="1" />',
    "  </div>",
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([]);
});

test("a file importing only preact/hooks may use class=", () => {
  const code = [
    'import { useState } from "preact/hooks";',
    'export const A = () => <div class="p-2" />;',
  ].join("\n");
  const report = checkSource(code, "x.tsx");
  expect(report).toEqual({ usesReact: false, usesPreact: true, findings: [] });
});

test("signals-core and @testing-library/preact are not Preact APIs", () => {
  const code = [
    'import { signal } from "@preact/signals-core";',
    'import { render } from "@testing-library/preact";',
    'import { useState } from "react";',
  ].join("\n");
  expect(checkSource(code, "x.tsx")).toEqual({
    usesReact: true,
    usesPreact: false,
    findings: [],
  });
});

test("the tree check reports findings and counts files still on Preact", () => {
  const root = mkdtempSync(join(tmpdir(), "k8sc-react-bridge-"));
  try {
    const files: Record<string, string> = {
      "src/islands/Old.tsx":
        'import { useState } from "preact/hooks";\nexport const A = () => <div class="a" />;',
      "components/OldToo.tsx":
        'import type { ComponentChildren } from "preact";\nexport type C = ComponentChildren;',
      "lib/old_test.ts": 'import { signal } from "@preact/signals";',
      "src/islands/New.tsx":
        'import { useState } from "react";\nexport const B = () => <div className="b" />;',
      "src/islands/Bad.tsx":
        'import { useState } from "react";\nexport const C = () => <div class="c" />;',
      "lib/store.ts": 'import { signal } from "@preact/signals-core";',
      "src/pages/index.astro": '<div class="ignored" />',
    };
    for (const [path, contents] of Object.entries(files)) {
      mkdirSync(dirname(join(root, path)), { recursive: true });
      writeFileSync(join(root, path), contents);
    }
    expect(checkTree(root)).toEqual({
      findings: [
        {
          file: "src/islands/Bad.tsx",
          line: 2,
          message: '<div> uses "class"; React spells it "className"',
        },
      ],
      preactFiles: [
        "components/OldToo.tsx",
        "lib/old_test.ts",
        "src/islands/Old.tsx",
      ],
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("a file that cannot be parsed fails with its path", () => {
  expect(() => checkSource("const = ;", "broken.ts")).toThrow(
    "broken.ts: could not parse",
  );
});
