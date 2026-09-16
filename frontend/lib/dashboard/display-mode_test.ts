import { expect, test } from "bun:test";
import { pickMode } from "./display-mode.ts";
import type { DisplayMode } from "./types.ts";

// pickMode is the whole size-responsive contract. It is pure so that the
// behavior every one of the 39 widgets depends on is pinned by unit tests
// rather than discovered by resizing a browser.

const ALL: DisplayMode[] = ["compact", "normal", "expanded"];

test("pickMode: a small box is compact", () => {
  expect(pickMode(ALL, 200, 100)).toBe("compact");
});

test("pickMode: a mid box is normal", () => {
  expect(pickMode(ALL, 360, 200)).toBe("normal");
});

test("pickMode: a large box is expanded", () => {
  expect(pickMode(ALL, 700, 400)).toBe("expanded");
});

test("pickMode: both dimensions must qualify", () => {
  // A wide, short box is not expanded -- a detail table needs height.
  expect(pickMode(ALL, 900, 140)).toBe("compact");
  // A tall, narrow box is not expanded either.
  expect(pickMode(ALL, 240, 600)).toBe("compact");
});

test("pickMode: falls back to the nearest implemented smaller mode", () => {
  // A widget with no expanded rendering stays at normal in a huge box rather
  // than rendering nothing.
  expect(pickMode(["compact", "normal"], 900, 500)).toBe("normal");
});

test("pickMode: falls upward when nothing smaller is implemented", () => {
  // A widget that only implements normal must render normal in a tiny box.
  // Blank is never an acceptable answer.
  expect(pickMode(["normal"], 100, 60)).toBe("normal");
  expect(pickMode(["normal", "expanded"], 100, 60)).toBe("normal");
});

test("pickMode: exact boundary values select the larger mode", () => {
  // Boundaries are inclusive on the way up so a widget sized exactly to a
  // breakpoint does not flicker between modes on a one-pixel scroll shift.
  expect(pickMode(ALL, 280, 160)).toBe("normal");
  expect(pickMode(ALL, 520, 320)).toBe("expanded");
});

test("pickMode: one pixel below a boundary stays in the smaller mode", () => {
  // The complement of the boundary test above: inclusive-on-the-way-up is only
  // meaningful if exclusive-below actually holds on each axis independently.
  expect(pickMode(ALL, 279, 160)).toBe("compact");
  expect(pickMode(ALL, 280, 159)).toBe("compact");
  expect(pickMode(ALL, 519, 320)).toBe("normal");
  expect(pickMode(ALL, 520, 319)).toBe("normal");
});

test("pickMode: zero and negative sizes are compact, never a crash", () => {
  // ResizeObserver reports 0x0 for a hidden or not-yet-laid-out element.
  expect(pickMode(ALL, 0, 0)).toBe("compact");
  expect(pickMode(ALL, -10, -10)).toBe("compact");
});

test("pickMode: a negative box still honours an unimplemented compact", () => {
  // The -10 case reaches "compact" through the initialiser rather than the
  // fits() loop, so the implemented-mode fallback has to run for it too --
  // otherwise a widget without compact would render a mode it does not have.
  expect(pickMode(["normal", "expanded"], -10, -10)).toBe("normal");
});

test("pickMode: never returns a mode the widget does not implement", () => {
  // The done-condition for this unit, stated as a property over a grid of
  // boxes rather than a handful of points.
  const boxes = [0, 1, 279, 280, 281, 519, 520, 521, 2000];
  const subsets: DisplayMode[][] = [
    ["compact"],
    ["normal"],
    ["expanded"],
    ["compact", "normal"],
    ["compact", "expanded"],
    ["normal", "expanded"],
    ["compact", "normal", "expanded"],
  ];
  const offenders: string[] = [];
  for (const modes of subsets) {
    for (const w of boxes) {
      for (const h of boxes) {
        const got = pickMode(modes, w, h);
        if (!modes.includes(got)) {
          offenders.push(`[${modes.join("|")}] ${w}x${h} -> ${got}`);
        }
      }
    }
  }
  expect(offenders).toEqual([]);
});

test("pickMode: an empty mode list falls back to normal", () => {
  // Defensive: the registry test forbids this, but returning undefined here
  // would put `undefined` on a widget's props.
  expect(pickMode([], 400, 300)).toBe("normal");
});
