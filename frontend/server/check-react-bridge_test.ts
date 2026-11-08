import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import {
  checkAliasMaps,
  checkSource,
  checkTree,
} from "./check-react-bridge.ts";
import { FRONTEND_ROOT } from "./source-files.ts";

/** The pragma a converted file with JSX starts with. */
const MARK = "/** @jsxImportSource react */";

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
    MARK,
    'import { useState } from "react";',
    "export const A = () => (",
    '  <div class="p-2" />',
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([
    '4: <div> uses "class"; React spells it "className"',
  ]);
});

test("a react file with onFocusOut fails", () => {
  const code = [
    MARK,
    'import { useState } from "react";',
    "export const A = () => <input onFocusOut={() => {}} />;",
  ].join("\n");
  expect(messages(code)).toEqual([
    '3: <input> uses "onFocusOut"; React spells it "onBlur"',
  ]);
});

test("a react file with for= or hyphenated SVG attributes fails", () => {
  const code = [
    MARK,
    'import { useId } from "react";',
    "export const A = () => (",
    "  <>",
    '    <label for="a" />',
    '    <path stroke-width="2" xlink:href="#b" />',
    "  </>",
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([
    '5: <label> uses "for"; React spells it "htmlFor"',
    '6: <path> uses "stroke-width"; React spells it "strokeWidth"',
    '6: <path> uses "xlink:href"; React spells it "xlinkHref"',
  ]);
});

test("aria-*, data-* and custom-element attributes are not flagged", () => {
  const code = [
    MARK,
    'import { useState } from "react";',
    "export const A = () => (",
    '  <div aria-label="x" data-testid="y" className="z">',
    '    <my-widget some-attr="1" />',
    "  </div>",
    ");",
  ].join("\n");
  expect(messages(code)).toEqual([]);
});

test("a file on React APIs with JSX must carry the jsxImportSource pragma", () => {
  const unmarked = [
    'import type { ReactNode } from "react";',
    "export const A = (p: { c: ReactNode }) => <div>{p.c}</div>;",
  ].join("\n");
  expect(messages(unmarked)).toEqual([
    "1: has JSX on React APIs but no `/** @jsxImportSource react */`; " +
      "without it the JSX does not load preact/compat, and React prop " +
      "names such as strokeWidth reach the DOM unconverted",
  ]);
  expect(messages(`${MARK}\n${unmarked}`)).toEqual([]);

  const noJsx = 'import { useState } from "react";\nexport const n = 1;';
  expect(messages(noJsx)).toEqual([]);
});

test("the pragma counts only before the first statement", () => {
  const late = [
    'import type { ReactNode } from "react";',
    MARK,
    "export const A = (p: { c: ReactNode }) => <div>{p.c}</div>;",
  ].join("\n");
  expect(messages(late)).toHaveLength(1);
});

test("the pragma alone puts a JSX-only file on React APIs", () => {
  const code = [MARK, 'export const S = () => <i class="x" />;'].join("\n");
  const report = checkSource(code, "x.tsx");
  expect(report.usesReact).toBe(true);
  expect(report.findings.map((f) => `${f.line}: ${f.message}`)).toEqual([
    '2: <i> uses "class"; React spells it "className"',
  ]);
});

test("a camelCase SVG attribute in a file without the pragma fails", () => {
  const code = [
    "export const S = () => (",
    '  <svg><path strokeWidth="2" fillRule="evenodd" d="M0" /></svg>',
    ");",
    "export const G = () => <Gauge strokeWidth={8} />;",
  ].join("\n");
  expect(messages(code)).toEqual([
    '2: <path> uses "strokeWidth", which only preact/compat turns into an ' +
      "attribute; mark the file `/** @jsxImportSource react */`",
    '2: <path> uses "fillRule", which only preact/compat turns into an ' +
      "attribute; mark the file `/** @jsxImportSource react */`",
  ]);
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
      "src/islands/New.tsx": `${MARK}\nimport { useState } from "react";\nexport const B = () => <div className="b" />;`,
      "src/islands/Bad.tsx": `${MARK}\nimport { useState } from "react";\nexport const C = () => <div class="c" />;`,
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
          line: 3,
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

test("passing class to a component on React APIs fails, from tsx and astro callers", () => {
  const root = mkdtempSync(join(tmpdir(), "k8sc-react-bridge-class-"));
  try {
    const files: Record<string, string> = {
      "components/ui/Spinner.tsx":
        `${MARK}\n` +
        'import type { ReactNode } from "react";\nexport function Spinner({ className }: { className?: string; children?: ReactNode }) { return <span className={className} />; }',
      "components/ui/OldBadge.tsx":
        'import type { ComponentChildren } from "preact";\nexport function OldBadge(p: { class?: string; children?: ComponentChildren }) { return <span class={p.class} />; }',
      "src/islands/Caller.tsx": [
        'import { useState } from "preact/hooks";',
        'import { Spinner } from "@/components/ui/Spinner.tsx";',
        'import { OldBadge } from "../../components/ui/OldBadge.tsx";',
        "export const A = () => (",
        "  <div>",
        '    <Spinner class="h-4" />',
        '    <Spinner className="h-4" />',
        '    <OldBadge class="x" />',
        "  </div>",
        ");",
      ].join("\n"),
      "src/islands/TypeOnly.tsx": [
        'import type { Spinner } from "@/components/ui/Spinner.tsx";',
        'export const B = (S: typeof Spinner) => <S class="y" />;',
      ].join("\n"),
      "src/pages/index.astro": [
        "---",
        'import { Spinner } from "@/components/ui/Spinner.tsx";',
        "---",
        '<Spinner class="mx-auto" />',
        '<Spinner class:list={["a"]} />',
        '<Spinner className="ok" />',
      ].join("\n"),
    };
    for (const [path, contents] of Object.entries(files)) {
      mkdirSync(dirname(join(root, path)), { recursive: true });
      writeFileSync(join(root, path), contents);
    }
    const { findings } = checkTree(root);
    expect(findings.map((f) => `${f.file}:${f.line} ${f.message}`)).toEqual([
      'src/islands/Caller.tsx:6 <Spinner> is on React APIs; pass "className", not "class"',
      'src/pages/index.astro:4 <Spinner> is on React APIs; pass "className", not "class"',
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

/**
 * A root with a fake package (`fake/a`, `fake/b`) and the two config files.
 * `tsPaths` is the tsconfig `paths` body; `viteAliases` the alias array body.
 */
function aliasRoot(tsPaths: string, viteAliases: string): string {
  const root = mkdtempSync(join(tmpdir(), "k8sc-react-alias-"));
  const files: Record<string, string> = {
    "node_modules/fake/package.json": '{ "name": "fake" }',
    "node_modules/fake/a.js": "export {};",
    "node_modules/fake/b.js": "export {};",
    "tsconfig.json": `{\n  // comments are allowed\n  "compilerOptions": { "paths": {\n    "@/*": ["./*"],\n${tsPaths}\n  } }\n}`,
    "astro.config.mjs": `export default { vite: { resolve: { alias: [\n${viteAliases}\n] } } };`,
  };
  for (const [path, contents] of Object.entries(files)) {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), contents);
  }
  return root;
}

function aliasFindings(tsPaths: string, viteAliases: string): string[] {
  const root = aliasRoot(tsPaths, viteAliases);
  try {
    return checkAliasMaps(root).map((f) => `${f.file}: ${f.message}`);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test("alias maps agree when a path and a specifier reach the same file", () => {
  expect(
    aliasFindings(
      '"react": ["./node_modules/fake/a"],\n"react-dom/client": ["./node_modules/fake/b"]',
      '{ find: /^react$/, replacement: "fake/a" },\n{ find: /^react-dom\\/client$/, replacement: "fake/b" }',
    ),
  ).toEqual([]);
});

test("alias targets that resolve to different files fail, naming both", () => {
  expect(
    aliasFindings(
      '"react": ["./node_modules/fake/a"]',
      '{ find: /^react$/, replacement: "fake/b" }',
    ),
  ).toEqual([
    'tsconfig.json: "react" resolves to node_modules/fake/a.js in ' +
      "tsconfig.json but node_modules/fake/b.js in astro.config.mjs",
  ]);
});

test("a react specifier aliased on one side only fails", () => {
  expect(
    aliasFindings(
      '"react": ["./node_modules/fake/a"],\n"react/jsx-runtime": ["./node_modules/fake/b"]',
      '{ find: /^react$/, replacement: "fake/a" }',
    ),
  ).toEqual([
    'astro.config.mjs: "react/jsx-runtime" is aliased in tsconfig.json only',
  ]);
});

test("a target that resolves to nothing fails", () => {
  expect(
    aliasFindings(
      '"react": ["./node_modules/fake/a"]',
      '{ find: /^react$/, replacement: "fake/missing" }',
    ),
  ).toEqual([
    'tsconfig.json: "react" resolves to node_modules/fake/a.js in ' +
      'tsconfig.json but nothing ("fake/missing") in astro.config.mjs',
  ]);
});

test("an alias pattern the check cannot read fails instead of being skipped", () => {
  expect(
    aliasFindings(
      '"react": ["./node_modules/fake/a"]',
      '{ find: /^react$/, replacement: "fake/a" },\n{ find: /^react-dom.*$/, replacement: "fake/b" }',
    ),
  ).toEqual([
    "astro.config.mjs: alias /^react-dom.*$/ is not an anchored literal " +
      "with a string replacement; the alias check cannot read it",
  ]);
});

test("the repository's own alias maps agree", () => {
  expect(checkAliasMaps(FRONTEND_ROOT)).toEqual([]);
});
