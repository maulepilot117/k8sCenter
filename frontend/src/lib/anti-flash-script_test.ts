import { expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { FRESH_ANTI_FLASH_SCRIPT } from "./__fixtures__/fresh-parity.ts";
import { ANTI_FLASH_SCRIPT } from "./anti-flash-script.ts";

/**
 * The parity reference is the frozen fixture, not the Fresh tree (U12 step
 * 2b). Reading `frontend/routes/_app.tsx` directly was the right check while
 * both trees existed, and becomes a broken test the moment U12 deletes it —
 * and a test that vanishes with its source stops guarding these bytes exactly
 * when it becomes the only thing that does.
 *
 * The Fresh source is still consulted, as a drift guard, for as long as it
 * exists: if someone edits _app.tsx before the tree is removed, the fixture
 * and the source disagree and this file says so. After deletion that check
 * self-disables and the fixture assertion carries on alone.
 */
const FRESH_APP_TSX = fileURLToPath(
  new URL("../../routes/_app.tsx", import.meta.url),
);

function readFreshAntiFlashScript(): string | null {
  if (!existsSync(FRESH_APP_TSX)) return null;
  const source = readFileSync(FRESH_APP_TSX, "utf-8");
  const match = source.match(/__html:\s*\n?\s*`([^`]*)`/);
  if (!match) {
    throw new Error(
      "routes/_app.tsx exists but its dangerouslySetInnerHTML script could " +
        "not be found — has it moved or changed shape? Either fix the match, " +
        "or, if the Fresh tree is on its way out, delete the file rather than " +
        "leaving a half-readable reference behind.",
    );
  }
  return match[1];
}

test("ANTI_FLASH_SCRIPT is byte-identical to the frozen Fresh original (KTD13)", () => {
  expect(ANTI_FLASH_SCRIPT).toBe(FRESH_ANTI_FLASH_SCRIPT);
});

test("the frozen fixture still matches routes/_app.tsx, while that file exists", () => {
  const fromSource = readFreshAntiFlashScript();
  if (fromSource === null) return; // Fresh tree deleted; the fixture stands alone.
  expect(FRESH_ANTI_FLASH_SCRIPT).toBe(fromSource);
});

test("ANTI_FLASH_SCRIPT reads exactly the kc.theme and k8scenter-animations keys", () => {
  expect(ANTI_FLASH_SCRIPT).toContain('localStorage.getItem("kc.theme")');
  expect(ANTI_FLASH_SCRIPT).toContain(
    'localStorage.getItem("k8scenter-animations")',
  );
});

test("ANTI_FLASH_SCRIPT never throws even if localStorage is unavailable", () => {
  // Both IIFEs wrap their body in try/catch with an empty catch — this is
  // a structural check (string shape), not an execution test, since bun
  // has no DOM/localStorage to actually run the script against.
  const tryCount = (ANTI_FLASH_SCRIPT.match(/try\{/g) ?? []).length;
  const catchCount = (ANTI_FLASH_SCRIPT.match(/catch\(e\)\{\}/g) ?? []).length;
  expect(tryCount).toBe(2);
  expect(catchCount).toBe(2);
});

test("ANTI_FLASH_SCRIPT contains no HTML metacharacters that would need escaping when inlined", () => {
  // set:html injects this raw; if it ever grows a literal <, >, or & this
  // assumption (and the CSP script-src 'unsafe-inline' allowance) should
  // be re-examined.
  expect(ANTI_FLASH_SCRIPT).not.toMatch(/[<>&]/);
});
