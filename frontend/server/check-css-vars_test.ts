import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import {
  checkTree,
  declaredProperties,
  referencedProperties,
} from "./check-css-vars.ts";

test("a declaration, an @property rule and an inline-style key define a property", () => {
  const css = [
    ":root { --accent: #fff; --bg-base:#000 }",
    "@property --num { syntax: '<integer>'; }",
  ].join("\n");
  const tsx = [
    'const s = { "--fill": "50%", color: "red" };',
    'el.style.setProperty("--offset", "3px");',
  ].join("\n");
  expect([...declaredProperties(css)].sort()).toEqual([
    "--accent",
    "--bg-base",
    "--num",
  ]);
  expect([...declaredProperties(tsx)].sort()).toEqual(["--fill", "--offset"]);
});

test("var() and Tailwind's (--x) shorthand reference a property, with its line", () => {
  const code = [
    'const a = { color: "var(--accent)" };',
    'const b = "color-mix(in srgb, var( --hover ) 30%, transparent)";',
    'const c = "bg-(--surface) text-[var(--brand)]";',
    'const d = "var(--bg-base, #05080f)";',
  ].join("\n");
  expect(referencedProperties(code)).toEqual([
    { name: "--accent", line: 1 },
    { name: "--hover", line: 2 },
    { name: "--surface", line: 3 },
    { name: "--brand", line: 3 },
    { name: "--bg-base", line: 4 },
  ]);
});

test("a property named in a comment is not a reference", () => {
  const code = [
    "/* was var(--brand), which nothing defines */",
    "// var(--brand) drew nothing",
    "{/* var(--brand) */}",
    "<!-- var(--brand) -->",
    'const url = "http://example.test"; const c = "var(--accent)";',
  ].join("\n");
  expect(referencedProperties(code)).toEqual([{ name: "--accent", line: 5 }]);
});

function tree(files: Record<string, string>): string {
  const root = mkdtempSync(join(tmpdir(), "css-vars-"));
  for (const [path, content] of Object.entries(files)) {
    const full = join(root, path);
    mkdirSync(dirname(full), { recursive: true });
    writeFileSync(full, content);
  }
  return root;
}

test("a reference to a property nothing defines fails, naming file and line", () => {
  const root = tree({
    "assets/styles.css":
      ":root { --accent: #fff; }\na { color: var(--accent); }",
    "assets/themes.generated.css": ":root { --bg-surface: #111; }",
    "src/islands/A.tsx": [
      'const ok = "var(--accent)";',
      'const ok2 = "var(--bg-surface)";',
      'const bad = "var(--brand)";',
    ].join("\n"),
    "lib/b.ts": 'export const c = "var(--danger)";',
    "src/pages/p.astro": '<div style={{ borderColor: "var(--border)" }} />',
    "src/islands/A_test.tsx": 'const fixture = "var(--not-scanned)";',
  });
  try {
    expect(checkTree(root)).toEqual([
      { file: "lib/b.ts", line: 1, name: "--danger" },
      { file: "src/islands/A.tsx", line: 3, name: "--brand" },
      { file: "src/pages/p.astro", line: 1, name: "--border" },
    ]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("a stylesheet's own undefined reference fails too", () => {
  const root = tree({
    "assets/styles.css":
      "@keyframes k { from { stroke-dashoffset: var(--len); } }",
  });
  try {
    expect(checkTree(root)).toEqual([
      { file: "assets/styles.css", line: 1, name: "--len" },
    ]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("a property set inline by one component counts as defined for the tree", () => {
  const root = tree({
    "assets/styles.css": ".bar { width: var(--fill); }",
    "src/islands/Bar.tsx": 'export const s = { "--fill": "40%" };',
  });
  try {
    expect(checkTree(root)).toEqual([]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("the frontend references no undefined custom property", () => {
  expect(checkTree()).toEqual([]);
});
