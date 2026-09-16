import { expect, test } from "bun:test";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  FORKED_ROOTS,
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

/**
 * The scope assertion, made against FORKED_ROOTS rather than against whichever
 * twins exist today.
 *
 * Two earlier versions of this test rotted the same way. It named
 * `islands/YamlEditor.tsx` until U12 deleted `frontend/islands/`, then
 * `components/ui/Logo.tsx` and `components/k8s/detail/index.tsx` until U12's
 * follow-up deleted the last `components/` twins. Each rewrite re-pinned it to
 * tree contents and bought one commit of life.
 *
 * What the guard must cover is the three forked directories -- the seven dead
 * imports that prompted it pointed at components as well as lib. That is a
 * property of the guard, so assert it on the guard. Whether a twin currently
 * exists in any of them is the tree's business and changes without notice.
 */
test("FORKED_ROOTS covers every directory that was forked into src/", () => {
  expect([...FORKED_ROOTS].sort()).toEqual(["components", "islands", "lib"]);
});

/**
 * And the behavioural half: forkedTwins() must actually walk each of those
 * roots, not just the first. Driven over a fixture tree so it stays true
 * whatever the real tree holds.
 */
test("forkedTwins walks every FORKED_ROOT, not just lib", () => {
  const dir = mkdtempSync(join(tmpdir(), "k8sc-roots-"));
  for (const root of FORKED_ROOTS) {
    mkdirSync(join(dir, root), { recursive: true });
    mkdirSync(join(dir, "src", root), { recursive: true });
    writeFileSync(join(dir, root, "probe.ts"), "export const x = 1;\n");
    writeFileSync(join(dir, "src", root, "probe.ts"), "export const x = 1;\n");
  }
  const found = forkedTwins(dir);
  for (const root of FORKED_ROOTS) {
    expect(found.has(`${root}/probe.ts`)).toBe(true);
  }
  rmSync(dir, { recursive: true, force: true });
});

/** The real tree still has its lib twins, and the exemptions still hold. */
test("the real tree's known twins and exemptions are unchanged", () => {
  const twins = forkedTwins();
  expect(twins.has("lib/cluster.ts")).toBe(true);
  expect(twins.has("lib/ws.ts")).toBe(true);
  expect(twins.has("lib/namespace.ts")).toBe(true);
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
