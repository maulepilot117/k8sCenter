import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { findDuplicateLibState } from "./check-no-duplicate-lib-state.ts";

const FRONTEND_DIR = dirname(dirname(fileURLToPath(import.meta.url)));

test("the tree has no forked lib module reachable from src as a second copy", () => {
  expect(findDuplicateLibState()).toEqual([]);
});

/**
 * The three modules this guard was written for. Pinning them by name matters
 * more than it looks: the failure they caused (two `selectedCluster` signals,
 * two WebSocket clients) is invisible to every other check in this repo, and
 * a well-meaning "let's just copy it back, the import is cleaner" would
 * silently reintroduce it. If one of these stops being a re-export, that is a
 * decision someone has to make against a failing test.
 */
for (const name of ["cluster", "ws", "namespace"]) {
  test(`lib/${name}.ts re-exports its src/lib twin rather than duplicating it`, () => {
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

test("the ported cluster module reads IS_BROWSER at runtime, not from the test shim", () => {
  // The shim exports a constant `false`. When lib/cluster.ts imported it, the
  // localStorage restore and the persist effect were both tree-shaken out of
  // the production bundle, which is how the API client ended up permanently
  // pinned to the local cluster.
  const source = readFileSync(
    join(FRONTEND_DIR, "src", "lib", "cluster.ts"),
    "utf-8",
  );
  expect(source).toContain('from "@/src/lib/is-browser.ts"');
  expect(source).not.toContain('from "fresh/runtime"');
});
