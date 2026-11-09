import { expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * Each wizard owns its unsaved-changes flag and hands the setter to the
 * review step, which clears it after a clean apply. The prop is optional, so a
 * wizard that forgets it still type-checks and keeps prompting "Leave site?"
 * after the resource was created. Only the Secret wizard drives an apply end
 * to end in its tests; this check covers the wiring of every other one.
 */
test("every wizard that guards unsaved changes lets the review step clear the flag", () => {
  const dir = import.meta.dir;
  const unwired = readdirSync(dir)
    .filter((f) => f.endsWith("Wizard.tsx"))
    .filter((f) => {
      const src = readFileSync(join(dir, f), "utf8");
      return (
        src.includes("useDirtyGuard(dirty)") &&
        src.includes("<WizardReviewStep") &&
        !src.includes("onDirtyChange={setDirty}")
      );
    });
  expect(unwired).toEqual([]);
});
