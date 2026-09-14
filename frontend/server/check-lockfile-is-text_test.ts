import { expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkLockfiles } from "./check-lockfile-is-text.ts";

function fixture(files: string[]): string {
  const dir = mkdtempSync(join(tmpdir(), "k8sc-lockfile-"));
  for (const f of files) writeFileSync(join(dir, f), "x");
  return dir;
}

test("the real frontend directory has a text lockfile and no binary one", () => {
  const result = checkLockfiles();
  expect(result.offenders).toEqual([]);
  expect(result.textLockfilePresent).toBe(true);
  expect(result.ok).toBe(true);
});

test("a committed bun.lockb is an offender", () => {
  const dir = fixture(["bun.lock", "bun.lockb"]);
  try {
    const result = checkLockfiles(dir);
    expect(result.ok).toBe(false);
    expect(result.offenders).toEqual([join(dir, "bun.lockb")]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("a missing bun.lock fails even with no binary lockfile present", () => {
  const dir = fixture([]);
  try {
    const result = checkLockfiles(dir);
    expect(result.ok).toBe(false);
    expect(result.offenders).toEqual([]);
    expect(result.textLockfilePresent).toBe(false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
