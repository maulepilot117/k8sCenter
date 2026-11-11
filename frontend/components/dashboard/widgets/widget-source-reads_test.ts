import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * A widget must read its data sources with `useSourceState`.
 * `dashboardData.state(key)` in render shows the first value and never
 * updates, because nothing subscribes the component to the key.
 *
 * The live-update tests beside each widget catch that by driving a source
 * change and watching the card. This check reads the files instead, so it
 * also catches a widget whose test would pass only because something else
 * re-rendered it.
 */
const DIR = import.meta.dir;

const widgets = readdirSync(DIR)
  .filter((f) => f.endsWith("Widget.tsx"))
  .map((f) => ({ file: f, src: readFileSync(join(DIR, f), "utf8") }));

describe("widgets read sources through useSourceState", () => {
  test("there are widgets to check", () => {
    expect(widgets.length).toBeGreaterThan(0);
  });

  test("no widget calls dashboardData.state", () => {
    const offenders = widgets
      .filter(({ src }) => /dashboardData\s*\.\s*state\s*[<(]/.test(src))
      .map(({ file }) => file);
    expect(offenders).toEqual([]);
  });
});
