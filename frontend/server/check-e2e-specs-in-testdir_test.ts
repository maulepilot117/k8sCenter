import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { checkE2eSpecs, readTestDir } from "./check-e2e-specs-in-testdir.ts";

const CONFIG = `import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  projects: [
    { name: "setup", testDir: "./fixtures", testMatch: /.*\\.setup\\.ts/ },
  ],
});
`;

/** A throwaway e2e directory holding `files` (path -> contents). */
function fixture(files: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), "k8sc-e2e-specs-"));
  for (const [path, contents] of Object.entries(files)) {
    const full = join(dir, path);
    mkdirSync(dirname(full), { recursive: true });
    writeFileSync(full, contents);
  }
  return dir;
}

function withFixture(
  files: Record<string, string>,
  fn: (dir: string) => void,
): void {
  const dir = fixture(files);
  try {
    fn(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test("the real e2e directory has every spec inside testDir", () => {
  const result = checkE2eSpecs();
  expect(result.error).toBeUndefined();
  expect(result.offenders).toEqual([]);
  expect(result.specsInTestDir).toBeGreaterThan(0);
  expect(result.ok).toBe(true);
});

test("a spec at the e2e root is an offender", () => {
  withFixture(
    {
      "playwright.config.ts": CONFIG,
      "tests/auth.spec.ts": "",
      "velero.spec.ts": "",
    },
    (dir) => {
      const result = checkE2eSpecs(dir);
      expect(result.ok).toBe(false);
      expect(result.offenders).toEqual(["velero.spec.ts"]);
      expect(result.specsInTestDir).toBe(1);
    },
  );
});

test("Playwright's default testMatch decides what a test file is", () => {
  withFixture(
    {
      "playwright.config.ts": CONFIG,
      "tests/nested/deep.spec.ts": "",
      "tests/other.test.tsx": "",
      "stray.test.ts": "",
      "elsewhere/also.spec.mjs": "",
      "helpers.ts": "",
      "fixtures/auth.setup.ts": "",
    },
    (dir) => {
      const result = checkE2eSpecs(dir);
      expect(result.offenders).toEqual([
        "elsewhere/also.spec.mjs",
        "stray.test.ts",
      ]);
      expect(result.specsInTestDir).toBe(2);
    },
  );
});

test("a spec under a project-level testDir is still outside the suite's testDir", () => {
  withFixture(
    {
      "playwright.config.ts": CONFIG,
      "tests/a.spec.ts": "",
      "fixtures/lost.spec.ts": "",
    },
    (dir) => {
      expect(checkE2eSpecs(dir).offenders).toEqual(["fixtures/lost.spec.ts"]);
    },
  );
});

test("dependency and report directories are not searched", () => {
  withFixture(
    {
      "playwright.config.ts": CONFIG,
      "tests/a.spec.ts": "",
      "node_modules/pkg/x.spec.ts": "",
      "playwright-report/y.spec.ts": "",
      "test-results/z.spec.ts": "",
    },
    (dir) => {
      const result = checkE2eSpecs(dir);
      expect(result.offenders).toEqual([]);
      expect(result.ok).toBe(true);
    },
  );
});

test("a sibling directory sharing testDir's prefix is not inside it", () => {
  withFixture(
    {
      "playwright.config.ts": CONFIG,
      "tests/a.spec.ts": "",
      "tests-old/b.spec.ts": "",
    },
    (dir) => {
      expect(checkE2eSpecs(dir).offenders).toEqual(["tests-old/b.spec.ts"]);
    },
  );
});

test("an empty testDir fails rather than passing vacuously", () => {
  withFixture({ "playwright.config.ts": CONFIG }, (dir) => {
    const result = checkE2eSpecs(dir);
    expect(result.ok).toBe(false);
    expect(result.error).toContain("contains no spec files");
  });
});

test("a config without a readable testDir fails loudly", () => {
  withFixture(
    {
      "playwright.config.ts": "export default {};",
      "tests/a.spec.ts": "",
    },
    (dir) => {
      const result = checkE2eSpecs(dir);
      expect(result.ok).toBe(false);
      expect(result.error).toContain("top-level testDir");
    },
  );
});

test("a missing config fails loudly", () => {
  withFixture({ "tests/a.spec.ts": "" }, (dir) => {
    const result = checkE2eSpecs(dir);
    expect(result.ok).toBe(false);
    expect(result.error).toContain("no Playwright config");
  });
});

test("readTestDir takes the top-level key, not a project's", () => {
  expect(readTestDir(CONFIG)).toBe("./tests");
  expect(
    readTestDir(`export default { projects: [{ testDir: "./fixtures" }] };`),
  ).toBeNull();
});
