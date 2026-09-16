import { expect, test } from "bun:test";
import { FRESH_ANTI_FLASH_SCRIPT } from "./__fixtures__/fresh-parity.ts";
import { ANTI_FLASH_SCRIPT } from "./anti-flash-script.ts";

/**
 * The parity reference is the frozen fixture, not the Fresh tree (U12 step
 * 2b). While both trees existed this file also re-read
 * `frontend/routes/_app.tsx` as a drift guard, so that editing the Fresh
 * source without updating the fixture would fail here. That guard was
 * written to self-disable once the tree was deleted; U12 deleted it, so it
 * was removed rather than left as a test that can only ever pass vacuously.
 * The fixture assertion below now carries these bytes alone.
 */

test("ANTI_FLASH_SCRIPT is byte-identical to the frozen Fresh original (KTD13)", () => {
  expect(ANTI_FLASH_SCRIPT).toBe(FRESH_ANTI_FLASH_SCRIPT);
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
