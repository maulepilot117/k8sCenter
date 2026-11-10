import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * A widget on React APIs must read its data sources with `useSourceState`.
 * `dashboardData.state(key)` in render shows the first value and never
 * updates, because nothing subscribes the component to the key.
 *
 * The live-update tests beside each widget catch that by driving a source
 * change and watching the card. This check reads the files instead, so it
 * also catches a widget whose test would pass only because something else
 * re-rendered it.
 */
const DIR = import.meta.dir;
const PRAGMA = "@jsxImportSource react";

const converted = readdirSync(DIR)
  .filter((f) => f.endsWith("Widget.tsx"))
  .map((f) => ({ file: f, src: readFileSync(join(DIR, f), "utf8") }))
  .filter(({ src }) => src.includes(PRAGMA));

describe("widgets on React APIs read sources through useSourceState", () => {
  test("there are converted widgets to check", () => {
    expect(converted.length).toBeGreaterThan(0);
  });

  test("no converted widget calls dashboardData.state", () => {
    const offenders = converted
      .filter(({ src }) => /dashboardData\s*\.\s*state\s*[<(]/.test(src))
      .map(({ file }) => file);
    expect(offenders).toEqual([]);
  });
});
