import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { ANTI_FLASH_SCRIPT } from "./anti-flash-script.ts";

/**
 * `frontend/routes/_app.tsx` is untouched by this unit (U9/U12 own its
 * removal) and stays the parity reference until then (KD4). Extracting the
 * literal script text out of it, rather than re-typing it by hand into
 * this test, is what makes "byte-identical" a checked fact instead of an
 * assertion.
 */
function readFreshAntiFlashScript(): string {
  const appTsxPath = fileURLToPath(
    new URL("../../routes/_app.tsx", import.meta.url),
  );
  const source = readFileSync(appTsxPath, "utf-8");
  const match = source.match(/__html:\s*\n?\s*`([^`]*)`/);
  if (!match) {
    throw new Error(
      "Could not find the dangerouslySetInnerHTML script in routes/_app.tsx — " +
        "has it moved or changed shape?",
    );
  }
  return match[1];
}

test("ANTI_FLASH_SCRIPT is byte-identical to the Fresh original in routes/_app.tsx (KTD13)", () => {
  expect(ANTI_FLASH_SCRIPT).toBe(readFreshAntiFlashScript());
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
