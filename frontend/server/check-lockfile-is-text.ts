/**
 * Build-time guard: the committed Bun lockfile must be the text one.
 *
 * Bun can emit two lockfiles. `bun.lock` is JSONC and is what Trivy's `bun`
 * analyzer parses; `bun.lockb` is a binary format nothing in this repo's
 * toolchain can read. If the binary one is ever committed — `bun install
 * --save-text-lockfile` forgotten, a `bunfig.toml` edit, an older Bun on a
 * contributor's machine — the dependency scan silently stops covering the
 * frontend's npm tree, and the release gate goes on passing.
 *
 * That failure is invisible by construction, which is why it gets a guard
 * rather than a checklist line. See docs/solutions/frontend-bun-image.md for
 * what the Trivy gate does and does not cover.
 */

import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const FRONTEND_DIR = dirname(dirname(fileURLToPath(import.meta.url)));

export interface LockfileCheck {
  ok: boolean;
  /** Absolute paths of lockfiles that must not exist. */
  offenders: string[];
  /** True when the text lockfile is present, as it must be. */
  textLockfilePresent: boolean;
}

export function checkLockfiles(dir: string = FRONTEND_DIR): LockfileCheck {
  const offenders: string[] = [];
  const binary = join(dir, "bun.lockb");
  if (existsSync(binary)) offenders.push(binary);
  return {
    ok: offenders.length === 0 && existsSync(join(dir, "bun.lock")),
    offenders,
    textLockfilePresent: existsSync(join(dir, "bun.lock")),
  };
}

function main(): void {
  const result = checkLockfiles();

  if (result.offenders.length > 0) {
    console.error("Lockfile guard: a binary Bun lockfile is present:");
    for (const f of result.offenders) console.error(`  ${f}`);
    console.error(
      "Trivy parses bun.lock (text) and not bun.lockb (binary), so committing " +
        "the binary form makes the dependency scan cover nothing while still " +
        "reporting success. Delete it and run `bun install --save-text-lockfile`.",
    );
    process.exit(1);
  }

  if (!result.textLockfilePresent) {
    console.error(
      "Lockfile guard: frontend/bun.lock is missing. It is the lockfile the " +
        "container copies for scanning and the one `bun install --frozen-lockfile` " +
        "needs in CI.",
    );
    process.exit(1);
  }

  console.log("Lockfile guard: bun.lock present, no binary bun.lockb.");
}

if (import.meta.main) {
  main();
}
