import { expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  findDuplicateLibState,
  forkedTwins,
  valueImportsOf,
} from "./check-no-duplicate-lib-state.ts";

const FRONTEND_DIR = dirname(dirname(fileURLToPath(import.meta.url)));

test("the real tree has no forked module reachable from src as a second copy", () => {
  expect(findDuplicateLibState()).toEqual([]);
});

/**
 * The three modules the first version of this guard was written for. Pinning
 * them by name matters more than it looks: the failure they caused (two
 * `selectedCluster` signals, two WebSocket clients) is invisible to every
 * other check in this repo, and a well-meaning "let's just copy it back, the
 * import is cleaner" would silently reintroduce it.
 */
for (const name of ["cluster", "ws", "namespace"]) {
  test(`lib/${name}.ts re-exports its src twin rather than duplicating it`, () => {
    const source = readFileSync(
      join(FRONTEND_DIR, "lib", `${name}.ts`),
      "utf-8",
    );
    expect(source).toContain(`export * from "@/src/lib/${name}.ts"`);
    // A re-export plus an import of its own would mean two module graphs
    // again, which is the whole thing this prevents.
    expect(source).not.toMatch(/^\s*import\s/m);
  });
}

/**
 * The seven imports that shipped a dead YAML editor and a dead ESO chain
 * graph. Each pointed at a pre-migration island or component whose
 * `IS_BROWSER` resolves through tsconfig to the test-only shim's constant
 * `false`, so the component mounted and then did nothing. The build was green,
 * the page returned 200, and the editor was a placeholder div.
 */
for (const file of [
  "src/islands/YamlApplyPage.tsx",
  "src/islands/SecretStoreFromTemplateEditor.tsx",
  "src/islands/MeshRouteDetail.tsx",
  "src/islands/ESOChainPage.tsx",
  "src/islands/ESOChainPanel.tsx",
  "src/islands/NetworkingDashboard.tsx",
  "src/islands/SetupWizard.tsx",
]) {
  test(`${file} imports its twin from src, not the pre-migration tree`, () => {
    const source = readFileSync(join(FRONTEND_DIR, file), "utf-8");
    expect(source).not.toMatch(/from "@\/islands\//);
    expect(source).not.toMatch(/from "@\/components\/ui\/Logo\.tsx"/);
  });
}

test("forkedTwins covers islands and components, not just lib", () => {
  const twins = forkedTwins();
  expect(twins.has("lib/cluster.ts")).toBe(true);
  expect(twins.has("islands/YamlEditor.tsx")).toBe(true);
  expect(twins.has("islands/NamespaceTopology.tsx")).toBe(true);
  expect(twins.has("components/ui/Logo.tsx")).toBe(true);
  // Type-only and frozen-data twins are exempt by explicit name.
  expect(twins.has("lib/eso-types.ts")).toBe(false);
});

// --- valueImportsOf: the shapes the first version could not see ---

function fixture(contents: string, name = "probe.ts"): string {
  const dir = mkdtempSync(join(tmpdir(), "k8sc-dupguard-"));
  const file = join(dir, name);
  writeFileSync(file, contents);
  writeFileSync(join(dir, "sibling.ts"), "export const x = 1;\n");
  return file;
}

function cleanup(file: string): void {
  rmSync(dirname(file), { recursive: true, force: true });
}

test("a single-quoted specifier is seen", () => {
  const f = fixture("import { x } from './sibling.ts';\n");
  try {
    expect(valueImportsOf(f).map((p) => p.replace(/\\/g, "/"))).toEqual([
      join(dirname(f), "sibling.ts").replace(/\\/g, "/"),
    ]);
  } finally {
    cleanup(f);
  }
});

test("a bare side-effect import is seen", () => {
  const f = fixture('import "./sibling.ts";\n');
  try {
    expect(valueImportsOf(f)).toHaveLength(1);
  } finally {
    cleanup(f);
  }
});

test("a dynamic import is seen", () => {
  const f = fixture('const m = await import("./sibling.ts");\n');
  try {
    expect(valueImportsOf(f)).toHaveLength(1);
  } finally {
    cleanup(f);
  }
});

test("a type-only import is NOT a runtime edge", () => {
  // `import type` erases at compile time. Counting it would have reported
  // five wizard islands as duplicated when they are not: the shared
  // wizard-form components import a `...WizardForm` type back out of the
  // pre-migration island that declares it.
  const f = fixture('import type { X } from "./sibling.ts";\n');
  try {
    expect(valueImportsOf(f)).toEqual([]);
  } finally {
    cleanup(f);
  }
});

test("a value import that also names inline types is still a runtime edge", () => {
  const f = fixture('import X, { type Y } from "./sibling.ts";\n');
  try {
    expect(valueImportsOf(f)).toHaveLength(1);
  } finally {
    cleanup(f);
  }
});

test("a non-module asset import is ignored", () => {
  const f = fixture('import "./styles.css";\n');
  try {
    expect(valueImportsOf(f)).toEqual([]);
  } finally {
    cleanup(f);
  }
});
