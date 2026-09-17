import { describe, expect, test } from "bun:test";
import {
  cellFromPoint,
  compact,
  layoutHeight,
  moveItem,
  overlaps,
  resizeItem,
} from "./grid.ts";
import type { LayoutItem } from "./types.ts";
import { DASHBOARD_COLUMNS } from "./types.ts";

// Collision resolution and compaction are where a hand-rolled grid fails.
// These tests are the reason the geometry is pure: none of this is reachable
// from a component test, and the repo has no harness for one anyway.

function item(
  instanceId: string,
  x: number,
  y: number,
  w: number,
  h: number,
): LayoutItem {
  return { instanceId, id: `w-${instanceId}`, x, y, w, h };
}

/** Layout as "instanceId@x,y" sorted, for readable assertions. */
function shape(items: readonly LayoutItem[]): string[] {
  return items.map((i) => `${i.instanceId}@${i.x},${i.y}`).sort();
}

/** Every pair that overlaps, named, so a failure says which. */
function overlapping(items: readonly LayoutItem[]): string[] {
  const out: string[] = [];
  for (let a = 0; a < items.length; a++) {
    for (let b = a + 1; b < items.length; b++) {
      if (overlaps(items[a], items[b])) {
        out.push(`${items[a].instanceId}/${items[b].instanceId}`);
      }
    }
  }
  return out;
}

describe("overlaps", () => {
  test("touching edges do not overlap", () => {
    expect(overlaps(item("a", 0, 0, 3, 3), item("b", 3, 0, 3, 3))).toBe(false);
    expect(overlaps(item("a", 0, 0, 3, 3), item("b", 0, 3, 3, 3))).toBe(false);
  });

  test("a shared cell overlaps", () => {
    expect(overlaps(item("a", 0, 0, 3, 3), item("b", 2, 2, 3, 3))).toBe(true);
  });

  test("containment overlaps", () => {
    expect(overlaps(item("a", 0, 0, 6, 6), item("b", 1, 1, 2, 2))).toBe(true);
  });
});

describe("compact", () => {
  test("a floating item falls to the top", () => {
    expect(shape(compact([item("a", 0, 5, 3, 2)]))).toEqual(["a@0,0"]);
  });

  test("an item falls only as far as the item above it", () => {
    const out = compact([item("a", 0, 0, 3, 2), item("b", 0, 9, 3, 2)]);
    expect(shape(out)).toEqual(["a@0,0", "b@0,2"]);
  });

  test("independent columns fall independently", () => {
    const out = compact([item("a", 0, 4, 3, 2), item("b", 6, 9, 3, 2)]);
    expect(shape(out)).toEqual(["a@0,0", "b@6,0"]);
  });

  test("a wide item is blocked by anything it spans", () => {
    // c spans both columns, so it cannot pass either of the two above it.
    const out = compact([
      item("a", 0, 0, 4, 2),
      item("b", 8, 0, 4, 3),
      item("c", 0, 20, 12, 2),
    ]);
    expect(shape(out)).toEqual(["a@0,0", "b@8,0", "c@0,3"]);
  });

  test("is idempotent", () => {
    const once = compact([item("a", 0, 7, 3, 2), item("b", 0, 2, 3, 2)]);
    expect(shape(compact(once))).toEqual(shape(once));
  });

  test("does not change x or size", () => {
    const [out] = compact([item("a", 4, 9, 3, 2)]);
    expect([out.x, out.w, out.h]).toEqual([4, 3, 2]);
  });

  test("never mutates its input", () => {
    const input = [item("a", 0, 5, 3, 2)];
    compact(input);
    expect(input[0].y).toBe(5);
  });

  test("separates items that arrive overlapping", () => {
    // A stored layout is data from outside this module; it can be wrong.
    const out = compact([item("a", 0, 0, 4, 3), item("b", 2, 1, 4, 2)]);
    expect(overlapping(out)).toEqual([]);
    expect(shape(out)).toEqual(["a@0,0", "b@2,3"]);
  });
});

describe("moveItem", () => {
  test("an unobstructed move lands exactly where asked", () => {
    const out = moveItem(
      [item("a", 0, 0, 3, 2), item("b", 6, 0, 3, 2)],
      "a",
      3,
      0,
    );
    expect(shape(out)).toEqual(["a@3,0", "b@6,0"]);
  });

  test("the dragged item wins and the other is pushed down", () => {
    const out = moveItem(
      [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 2)],
      "b",
      0,
      0,
    );
    // b took the top; a yielded downward, then gravity pulled it to b's bottom.
    expect(shape(out)).toEqual(["a@0,2", "b@0,0"]);
  });

  test("the dragged item wins a row it shares with an item to its left", () => {
    // Gravity alone would also separate the pair, but in reading order, which
    // puts c (further left) first and pushes the dragged item down instead.
    // The explicit push is what makes rule 3 hold here.
    const out = moveItem(
      [item("c", 0, 0, 4, 2), item("a", 4, 0, 4, 2)],
      "a",
      2,
      0,
    );
    expect(shape(out)).toEqual(["a@2,0", "c@0,2"]);
  });

  test("displacement is downward, never sideways", () => {
    const out = moveItem(
      [item("a", 0, 0, 4, 2), item("b", 4, 0, 4, 2)],
      "a",
      2,
      0,
    );
    const b = out.find((i) => i.instanceId === "b");
    expect(b?.x).toBe(4);
    expect(b?.y).toBeGreaterThan(0);
  });

  test("a displacement cascade resolves", () => {
    const out = moveItem(
      [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 2), item("c", 0, 4, 4, 2)],
      "c",
      0,
      0,
    );
    expect(shape(out)).toEqual(["a@0,2", "b@0,4", "c@0,0"]);
  });

  test("x is clamped inside the grid", () => {
    expect(moveItem([item("a", 0, 0, 4, 2)], "a", 99, 0)[0].x).toBe(8);
    expect(moveItem([item("a", 4, 0, 4, 2)], "a", -5, 0)[0].x).toBe(0);
  });

  test("negative y is clamped to the top", () => {
    expect(moveItem([item("a", 0, 4, 3, 2)], "a", 0, -3)[0].y).toBe(0);
  });

  test("fractional coordinates snap to whole cells", () => {
    const [out] = moveItem([item("a", 0, 0, 3, 2)], "a", 2.6, 0.2);
    expect([out.x, out.y]).toEqual([3, 0]);
  });

  test("a non-finite coordinate is a no-op, not a NaN in the layout", () => {
    // A pointer event read before layout, or a division by a zero-size cell,
    // yields NaN; once one lands in the layout every later comparison is false.
    const input = [item("a", 2, 0, 3, 2)];
    for (const [x, y] of [
      [Number.NaN, 0],
      [0, Number.POSITIVE_INFINITY],
    ]) {
      expect(shape(moveItem(input, "a", x, y))).toEqual(["a@2,0"]);
    }
  });

  test("an unknown instanceId is a no-op, not a throw", () => {
    const input = [item("a", 0, 0, 3, 2)];
    expect(shape(moveItem(input, "ghost", 5, 5))).toEqual(shape(input));
  });

  test("result never contains an overlap", () => {
    const out = moveItem(
      [item("a", 0, 0, 6, 3), item("b", 6, 0, 6, 3), item("c", 0, 3, 12, 2)],
      "c",
      0,
      0,
    );
    expect(overlapping(out)).toEqual([]);
  });

  test("never mutates its input", () => {
    const input = [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 2)];
    moveItem(input, "b", 0, 0);
    expect(shape(input)).toEqual(["a@0,0", "b@0,2"]);
  });
});

describe("resizeItem", () => {
  const bounds = { minW: 2, minH: 2 };

  test("grows within the grid", () => {
    const [out] = resizeItem([item("a", 0, 0, 3, 2)], "a", 6, 4, bounds);
    expect([out.w, out.h]).toEqual([6, 4]);
  });

  test("clamps to the declared minimum", () => {
    const [out] = resizeItem([item("a", 0, 0, 6, 6)], "a", 1, 1, {
      minW: 3,
      minH: 2,
    });
    expect([out.w, out.h]).toEqual([3, 2]);
  });

  test("cannot grow past the right edge, and x does not shift", () => {
    const [out] = resizeItem([item("a", 8, 0, 4, 2)], "a", 9, 2, bounds);
    expect([out.x, out.w]).toEqual([8, 4]);
  });

  test("the grid edge wins over a minimum that cannot fit", () => {
    // Only reachable from a stored layout that already violates the minimum;
    // an item outside the grid is worse than one below its minimum width.
    const [out] = resizeItem([item("a", 10, 0, 2, 2)], "a", 2, 2, {
      minW: 4,
      minH: 2,
    });
    expect([out.x, out.w]).toEqual([10, 2]);
  });

  test("growing pushes the item below down", () => {
    const out = resizeItem(
      [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 2)],
      "a",
      4,
      5,
      bounds,
    );
    expect(shape(out)).toEqual(["a@0,0", "b@0,5"]);
  });

  test("shrinking lets the item below rise", () => {
    const out = resizeItem(
      [item("a", 0, 0, 4, 6), item("b", 0, 6, 4, 2)],
      "a",
      4,
      2,
      bounds,
    );
    expect(shape(out)).toEqual(["a@0,0", "b@0,2"]);
  });

  test("a non-finite size is a no-op", () => {
    const [out] = resizeItem(
      [item("a", 0, 0, 3, 2)],
      "a",
      Number.NaN,
      4,
      bounds,
    );
    expect([out.w, out.h]).toEqual([3, 2]);
  });

  test("an unknown instanceId is a no-op", () => {
    const input = [item("a", 0, 0, 3, 2)];
    expect(shape(resizeItem(input, "ghost", 6, 6, bounds))).toEqual(
      shape(input),
    );
  });
});

describe("cellFromPoint", () => {
  const METRICS = {
    left: 100,
    top: 50,
    cellWidth: 80,
    rowHeight: 40,
    gap: 16,
  };

  test("the grid origin is cell 0,0", () => {
    expect(cellFromPoint(100, 50, METRICS)).toEqual({ x: 0, y: 0 });
  });

  test("a point inside the first cell is still 0,0", () => {
    expect(cellFromPoint(150, 70, METRICS)).toEqual({ x: 0, y: 0 });
  });

  test("crossing a cell boundary advances one cell", () => {
    // cellWidth 80 + gap 16 = 96 per column; rowHeight 40 + gap 16 = 56 per row.
    expect(cellFromPoint(100 + 96, 50, METRICS).x).toBe(1);
    expect(cellFromPoint(100 + 95, 50, METRICS).x).toBe(0);
    expect(cellFromPoint(100, 50 + 56, METRICS).y).toBe(1);
  });

  test("a point left of or above the grid is clamped to 0", () => {
    expect(cellFromPoint(0, 0, METRICS)).toEqual({ x: 0, y: 0 });
    expect(cellFromPoint(-500, -500, METRICS)).toEqual({ x: 0, y: 0 });
  });

  test("x is clamped to the last column", () => {
    expect(cellFromPoint(99999, 50, METRICS).x).toBe(DASHBOARD_COLUMNS - 1);
  });

  test("x is clamped to a narrower grid's last column", () => {
    // The one-column collapse at narrow widths renders the same layout on a
    // grid with fewer columns.
    expect(cellFromPoint(99999, 50, METRICS, 1).x).toBe(0);
  });

  test("a zero-width cell does not produce NaN or Infinity", () => {
    // Guards the first paint, when the grid has been mounted but not laid out.
    const degenerate = { left: 0, top: 0, cellWidth: 0, rowHeight: 0, gap: 0 };
    expect(cellFromPoint(10, 10, degenerate)).toEqual({ x: 0, y: 0 });
  });
});

describe("layoutHeight", () => {
  test("is the lowest bottom edge", () => {
    expect(layoutHeight([item("a", 0, 0, 3, 2), item("b", 6, 4, 3, 3)])).toBe(
      7,
    );
  });

  test("an empty layout has height 0", () => {
    expect(layoutHeight([])).toBe(0);
  });
});

// The example tests above pin known cases. Drag and resize produce sequences
// no one writes by hand, so the engine's invariants are also checked across a
// long seeded run of random operations. Seeded, so a failure reproduces.
describe("invariants under random operations", () => {
  /** Mulberry32: tiny, deterministic, good enough to spread cases. */
  function rng(seed: number): () => number {
    let s = seed >>> 0;
    return () => {
      s = (s + 0x6d2b79f5) >>> 0;
      let t = s;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  test("no overlap, nothing lost, nothing outside the grid, input untouched", () => {
    const rand = rng(0xd6);
    const int = (lo: number, hi: number) =>
      lo + Math.floor(rand() * (hi - lo + 1));

    for (let round = 0; round < 200; round++) {
      let layout: LayoutItem[] = [];
      const count = int(1, 8);
      for (let n = 0; n < count; n++) {
        const w = int(1, 6);
        layout.push(
          item(
            `i${n}`,
            int(0, DASHBOARD_COLUMNS - w),
            int(0, 20),
            w,
            int(1, 5),
          ),
        );
      }
      layout = compact(layout);

      for (let step = 0; step < 25; step++) {
        const target = layout[int(0, layout.length - 1)].instanceId;
        const before = JSON.stringify(layout);
        const next =
          rand() < 0.5
            ? moveItem(layout, target, int(-3, 14), int(-3, 25))
            : resizeItem(layout, target, int(0, 14), int(0, 8), {
                minW: 1,
                minH: 1,
              });

        const where = `round ${round} step ${step}`;
        expect(JSON.stringify(layout), `${where}: input mutated`).toBe(before);
        expect(overlapping(next), `${where}: overlap`).toEqual([]);
        expect(
          next.map((i) => i.instanceId).sort(),
          `${where}: item set changed`,
        ).toEqual(layout.map((i) => i.instanceId).sort());
        for (const i of next) {
          expect(
            i.x >= 0 && i.y >= 0 && i.x + i.w <= DASHBOARD_COLUMNS,
            `${where}: ${i.instanceId} outside grid`,
          ).toBe(true);
        }
        expect(shape(compact(next)), `${where}: not compact`).toEqual(
          shape(next),
        );
        layout = next;
      }
    }
  });
});
