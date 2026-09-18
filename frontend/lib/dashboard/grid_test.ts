import { describe, expect, test } from "bun:test";
import {
  cellFromPoint,
  compact,
  dragTarget,
  layoutHeight,
  metricsFrom,
  moveItem,
  overlaps,
  resizeItem,
  resizeItemBy,
  resizeItemToCell,
  resolveRenderable,
  stepItem,
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

  test("returns items in reading order, not input/processing order", () => {
    // a is processed first (smaller pre-compaction y), but both land at y=0,
    // where b (smaller x) reads first. Array order is asserted directly
    // because shape() sorts and would hide an ordering regression.
    const out = compact([item("a", 6, 0, 3, 2), item("b", 0, 5, 3, 2)]);
    expect(out.map((i) => i.instanceId)).toEqual(["b", "a"]);
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

  test("returns items in reading order, not processing order", () => {
    // z is untouched and never overlaps the mover, but the mover (a) is
    // requested well below z's row; both settle back to y 0 since neither
    // blocks the other, and a's smaller x must sort it first.
    const out = moveItem(
      [item("z", 8, 0, 4, 2), item("a", 0, 5, 4, 2)],
      "a",
      0,
      10,
    );
    expect(out.map((i) => i.instanceId)).toEqual(["a", "z"]);
  });

  describe("a downward move into an occupied column", () => {
    // a(0,0,4,2) sits above b(0,2,4,3). Moving a down by one cell overlaps b,
    // b is pushed underneath a during the push phase, and gravity lifts a
    // back on top during compaction -- the move is undone. Only once the
    // requested y reaches b's bottom edge (2 + 3 = 5) does a actually land
    // below b.
    const layout = [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 3)];

    test("a smaller downward move settles back", () => {
      expect(shape(moveItem(layout, "a", 0, 1))).toEqual(["a@0,0", "b@0,2"]);
    });

    test("reaching the item's bottom edge passes it", () => {
      expect(shape(moveItem(layout, "a", 0, 5))).toEqual(["a@0,3", "b@0,0"]);
    });

    test("is stable under re-application", () => {
      const once = moveItem(layout, "a", 0, 5);
      expect(shape(moveItem(once, "a", 0, 5))).toEqual(shape(once));
    });
  });

  test("re-applying the same move to its own result changes nothing", () => {
    // A drag calls moveItem on the current layout at every pointermove, so a
    // result that is not a fixed point makes the widget jump while the
    // pointer is still. Here a single resolve pass lifts b above c and a, and
    // a second pass would push it back down to row 2.
    const layout = [
      item("b", 8, 0, 3, 1),
      item("c", 7, 1, 2, 1),
      item("a", 5, 2, 3, 1),
    ];
    const once = moveItem(layout, "b", 4, 2);
    expect(moveItem(once, "b", 4, 2)).toEqual(once);
  });

  test("dragging away and back need not restore the original layout", () => {
    // Documented behavior, pinned so a change to it is deliberate: each step
    // resolves against the current layout, so neighbors displaced on the way
    // keep their new order. A caller that must restore holds the pre-drag
    // layout (every function leaves its input untouched).
    // Dragging b up one row lifts it above a; dragging it back to row 2 is a
    // downward move short of a's new bottom edge, so it settles on top.
    const start = [item("a", 8, 0, 3, 2), item("b", 10, 2, 1, 1)];
    const away = moveItem(start, "b", 10, 1);
    expect(shape(away)).toEqual(["a@8,1", "b@10,0"]);
    const back = moveItem(away, "b", 10, 2);
    expect(shape(back)).toEqual(["a@8,1", "b@10,0"]);
    expect(shape(back)).not.toEqual(shape(start));
  });
});

describe("stepItem", () => {
  /** The stepped item, so a test can read the size a move must not change. */
  const only = (items: readonly LayoutItem[], instanceId: string) => {
    const found = items.find((i) => i.instanceId === instanceId);
    if (!found) throw new Error(`no item ${instanceId}`);
    return found;
  };

  // Rows are where gravity left them, not where a test wished them: a lone
  // item always compacts to row 0, so these layouts state the row they end in.
  test("an arrow moves one cell", () => {
    const layout = [item("a", 4, 0, 3, 2)];
    expect(shape(stepItem(layout, "a", 1, 0))).toEqual(["a@5,0"]);
    expect(shape(stepItem(layout, "a", -1, 0))).toEqual(["a@3,0"]);
  });

  test("a step never resizes what it moves", () => {
    const moved = only(stepItem([item("a", 4, 0, 3, 2)], "a", 1, 0), "a");
    expect([moved.w, moved.h]).toEqual([3, 2]);
  });

  test("the grid's edges stop a step rather than wrapping it", () => {
    // Left edge, right edge (12 columns, so a 3-wide item stops at 9), top.
    expect(shape(stepItem([item("a", 0, 0, 3, 2)], "a", -1, 0))).toEqual([
      "a@0,0",
    ]);
    expect(shape(stepItem([item("a", 9, 0, 3, 2)], "a", 1, 0))).toEqual([
      "a@9,0",
    ]);
    expect(shape(stepItem([item("a", 4, 0, 3, 2)], "a", 0, -1))).toEqual([
      "a@4,0",
    ]);
  });

  test("a sideways step displaces the neighbour it lands on downward", () => {
    const layout = [item("a", 0, 0, 3, 2), item("b", 3, 0, 3, 2)];
    expect(shape(stepItem(layout, "a", 1, 0))).toEqual(["a@1,0", "b@3,2"]);
  });

  test("an upward step passes the item above", () => {
    const layout = [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 3)];
    expect(shape(stepItem(layout, "b", 0, -1))).toEqual(["a@0,3", "b@0,0"]);
  });

  describe("a downward step", () => {
    // The case moveItem documents as absorbed: asking for one row down puts b
    // under a during the push, and gravity lifts a straight back on top. A
    // drag survives that -- the next pointermove asks again from further down
    // -- but a key press has no next event, so stepItem has to find the row
    // that actually lands.
    test("passes the item below instead of doing nothing", () => {
      const layout = [item("a", 0, 0, 4, 2), item("b", 0, 2, 4, 3)];
      expect(shape(moveItem(layout, "a", 0, 1))).toEqual(["a@0,0", "b@0,2"]);
      expect(shape(stepItem(layout, "a", 0, 1))).toEqual(["a@0,3", "b@0,0"]);
    });

    test("passes one neighbour, not the whole stack", () => {
      const layout = [
        item("a", 0, 0, 4, 2),
        item("b", 0, 2, 4, 2),
        item("c", 0, 4, 4, 2),
      ];
      expect(shape(stepItem(layout, "a", 0, 1))).toEqual([
        "a@0,2",
        "b@0,0",
        "c@0,4",
      ]);
    });

    test("does nothing when there is nothing below to pass", () => {
      const layout = [item("a", 0, 0, 4, 2), item("b", 6, 0, 4, 2)];
      expect(shape(stepItem(layout, "a", 0, 1))).toEqual(["a@0,0", "b@6,0"]);
    });

    test("only passes a neighbour that shares a column", () => {
      // b is directly below a's rows but two columns clear of it, so a has
      // nothing to pass and gravity holds it at the top.
      const layout = [item("a", 0, 0, 4, 2), item("b", 6, 2, 4, 2)];
      expect(shape(stepItem(layout, "a", 0, 1))).toEqual(["a@0,0", "b@6,0"]);
    });
  });

  test("an unknown item leaves the layout alone", () => {
    const layout = [item("a", 0, 0, 3, 2)];
    const out = stepItem(layout, "nope", 1, 0);
    expect(out).toEqual(layout);
    expect(out).not.toBe(layout);
  });

  test("a non-finite step leaves the layout alone", () => {
    const layout = [item("a", 0, 0, 3, 2)];
    expect(shape(stepItem(layout, "a", Number.NaN, 0))).toEqual(["a@0,0"]);
    expect(shape(stepItem(layout, "a", 0, Number.POSITIVE_INFINITY))).toEqual([
      "a@0,0",
    ]);
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
    for (const [w, h] of [
      [Number.NaN, 4],
      [Number.POSITIVE_INFINITY, 4],
      [4, Number.NaN],
      [4, Number.POSITIVE_INFINITY],
    ]) {
      const [out] = resizeItem([item("a", 0, 0, 3, 2)], "a", w, h, bounds);
      expect([out.w, out.h], `w=${w} h=${h}`).toEqual([3, 2]);
    }
  });

  test("returns items in reading order, not processing order", () => {
    // Shrinking a lets b rise to row 0, where b's larger x reads after c.
    const out = resizeItem(
      [item("a", 0, 0, 4, 4), item("c", 0, 4, 2, 2), item("b", 2, 4, 2, 2)],
      "a",
      2,
      2,
      bounds,
    );
    expect(out.map((i) => i.instanceId)).toEqual(["a", "b", "c"]);
  });

  test("a resize can change the item's row, so handlers must read it back", () => {
    // Narrowing a off the item it rested on lets gravity lift it. A resize
    // handler that computes height from a y captured at drag start would then
    // stop tracking the pointer; it must use the resized item's current y.
    const out = resizeItem(
      [item("base", 0, 0, 2, 3), item("a", 0, 3, 6, 2)],
      "a",
      4,
      2,
      bounds,
    );
    expect(out.find((i) => i.instanceId === "a")?.y).toBe(3);
    const narrowed = resizeItem(out, "a", 4, 2, bounds);
    expect(narrowed).toEqual(out);
    const off = resizeItem(
      [item("base", 0, 0, 2, 3), item("a", 2, 3, 6, 2)],
      "a",
      4,
      2,
      bounds,
    );
    expect(off.find((i) => i.instanceId === "a")?.y).toBe(0);
  });

  test("an unknown instanceId is a no-op", () => {
    const input = [item("a", 0, 0, 3, 2)];
    expect(shape(resizeItem(input, "ghost", 6, 6, bounds))).toEqual(
      shape(input),
    );
  });
});

describe("resizeItemBy", () => {
  const size = (items: readonly LayoutItem[], instanceId: string) => {
    const found = items.find((i) => i.instanceId === instanceId);
    if (!found) throw new Error(`no item ${instanceId}`);
    return [found.w, found.h];
  };

  test("grows and shrinks by whole cells", () => {
    const layout = [item("a", 0, 0, 3, 2)];
    const bounds = { minW: 1, minH: 1 };
    expect(size(resizeItemBy(layout, "a", 1, 0, bounds), "a")).toEqual([4, 2]);
    expect(size(resizeItemBy(layout, "a", 0, 1, bounds), "a")).toEqual([3, 3]);
    expect(size(resizeItemBy(layout, "a", -1, -1, bounds), "a")).toEqual([
      2, 1,
    ]);
  });

  test("stops at the declared minimum", () => {
    const layout = [item("a", 0, 0, 2, 3)];
    const bounds = { minW: 2, minH: 3 };
    expect(size(resizeItemBy(layout, "a", -1, 0, bounds), "a")).toEqual([2, 3]);
    expect(size(resizeItemBy(layout, "a", 0, -1, bounds), "a")).toEqual([2, 3]);
  });

  test("stops at the grid's right edge", () => {
    const layout = [item("a", 9, 0, 3, 2)];
    expect(size(resizeItemBy(layout, "a", 1, 0, { minW: 1, minH: 1 }), "a"))
      // 12 columns, starting at 9: three is all there is.
      .toEqual([3, 2]);
  });

  test("an unknown item leaves the layout alone", () => {
    const layout = [item("a", 0, 0, 3, 2)];
    const out = resizeItemBy(layout, "nope", 1, 1, { minW: 1, minH: 1 });
    expect(out).toEqual(layout);
    expect(out).not.toBe(layout);
  });
});

describe("resizeItemToCell", () => {
  const bounds = { minW: 1, minH: 1 };
  /** Where the item's bottom-right corner ended up. */
  const corner = (items: readonly LayoutItem[], id: string) => {
    const it = items.find((i) => i.instanceId === id)!;
    return { x: it.x + it.w - 1, y: it.y + it.h - 1 };
  };

  test("the size is inclusive of the cell under the corner", () => {
    const out = resizeItemToCell(
      [item("a", 0, 0, 1, 1)],
      "a",
      { x: 2, y: 3 },
      bounds,
    );
    const a = out.find((i) => i.instanceId === "a")!;
    expect([a.w, a.h]).toEqual([3, 4]);
  });

  test("the corner cell an item already occupies leaves it unchanged", () => {
    // `a` needs something above it, or gravity moves it and "unchanged" would
    // be testing compaction rather than the resize.
    const input = [item("top", 2, 0, 3, 1), item("a", 2, 1, 3, 2)];
    const out = resizeItemToCell(input, "a", { x: 4, y: 2 }, bounds);
    const a = out.find((i) => i.instanceId === "a")!;
    expect([a.x, a.y, a.w, a.h]).toEqual([2, 1, 3, 2]);
  });

  test("a corner above and left of the item clamps to its minimum", () => {
    const out = resizeItemToCell(
      [item("a", 4, 4, 5, 5)],
      "a",
      { x: 0, y: 0 },
      {
        minW: 2,
        minH: 3,
      },
    );
    const a = out.find((i) => i.instanceId === "a")!;
    expect([a.x, a.w, a.h]).toEqual([4, 2, 3]);
  });

  test("an item gravity lifts mid-resize still lands its corner on the cell", () => {
    // `target` rests at y=4 under `blocker` (columns 2-5). Narrowing it to
    // columns 0-1 frees it, so gravity lifts it to the top -- and the height
    // that was just applied was measured from the y it had before the lift.
    // Applying once leaves the corner at row 1 with the pointer on row 5;
    // whether the user got that or the right answer would then depend on
    // whether one more pointermove arrived before they let go.
    const out = resizeItemToCell(
      [item("blocker", 2, 0, 4, 4), item("target", 0, 4, 6, 2)],
      "target",
      { x: 1, y: 5 },
      bounds,
    );
    expect(corner(out, "target")).toEqual({ x: 1, y: 5 });
    expect(shape(out)).toEqual(["blocker@2,0", "target@0,0"]);
  });

  test("re-aiming at the same cell changes nothing", () => {
    const input = [item("blocker", 2, 0, 4, 4), item("target", 0, 4, 6, 2)];
    const cell = { x: 1, y: 5 };
    const once = resizeItemToCell(input, "target", cell, bounds);
    expect(resizeItemToCell(once, "target", cell, bounds)).toEqual(once);
  });

  test("an unknown instanceId is a no-op", () => {
    const input = [item("a", 0, 0, 3, 2)];
    expect(
      shape(resizeItemToCell(input, "ghost", { x: 9, y: 9 }, bounds)),
    ).toEqual(shape(input));
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

  test("a zero-width cell does not produce NaN or Infinity", () => {
    // Guards the first paint, when the grid has been mounted but not laid out.
    const degenerate = { left: 0, top: 0, cellWidth: 0, rowHeight: 0, gap: 0 };
    expect(cellFromPoint(10, 10, degenerate)).toEqual({ x: 0, y: 0 });
  });

  test("a zero cellWidth with a nonzero gap does not produce a nonzero cell", () => {
    // colStride = cellWidth + gap = 16, which is > 0 even though the grid has
    // no width -- the guard must check cellWidth itself, not the stride.
    const degenerate = { left: 0, top: 0, cellWidth: 0, rowHeight: 0, gap: 16 };
    expect(cellFromPoint(50, 50, degenerate)).toEqual({ x: 0, y: 0 });
  });

  test("a negative cellWidth from a container narrower than its gaps is guarded", () => {
    // (0 - 16 * 11) / 12, the width a zero-width grid computes per cell.
    const degenerate = {
      left: 0,
      top: 0,
      cellWidth: -14.67,
      rowHeight: 40,
      gap: 16,
    };
    expect(cellFromPoint(50, 50, degenerate)).toEqual({ x: 0, y: 0 });
  });

  test("an infinite measured size is guarded", () => {
    for (const bad of [
      { cellWidth: Number.POSITIVE_INFINITY, rowHeight: 40 },
      { cellWidth: 80, rowHeight: Number.POSITIVE_INFINITY },
    ]) {
      const metrics = { left: 0, top: 0, gap: 16, ...bad };
      expect(cellFromPoint(500, 500, metrics)).toEqual({ x: 0, y: 0 });
    }
  });

  test("a point in the gap below a row still belongs to that row", () => {
    // rowHeight 40 + gap 16 = 56; y offsets 40..55 are the gap under row 0.
    expect(cellFromPoint(100, 50 + 55, METRICS).y).toBe(0);
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

describe("metricsFrom", () => {
  test("columns share what the gaps leave", () => {
    // 12 columns, 11 gaps of 16 = 176px of gutter.
    const m = metricsFrom({ left: 40, top: 100, width: 1076 });
    expect(m.cellWidth).toBe(75);
    expect([m.left, m.top, m.rowHeight, m.gap]).toEqual([40, 100, 40, 16]);
  });

  test("a container narrower than its gaps reports a negative cell", () => {
    // Left as measured: cellFromPoint owns the "not laid out yet" decision,
    // and clamping here would hide a zero-size grid from it.
    expect(metricsFrom({ left: 0, top: 0, width: 0 }).cellWidth).toBeLessThan(
      0,
    );
  });

  test("feeds cellFromPoint: the grid's own corner is cell 0,0", () => {
    const m = metricsFrom({ left: 40, top: 100, width: 1076 });
    expect(cellFromPoint(40, 100, m)).toEqual({ x: 0, y: 0 });
    // One column stride right, one row stride down.
    expect(cellFromPoint(40 + 75 + 16, 100 + 40 + 16, m)).toEqual({
      x: 1,
      y: 1,
    });
  });
});

describe("dragTarget", () => {
  test("translates the item by the pointer's travel, not to the pointer", () => {
    // Grabbed at cell (5,3) on an item at (4,2): two cells right and one down
    // moves the item to (6,3), keeping the grab offset.
    expect(dragTarget({ x: 4, y: 2 }, { x: 5, y: 3 }, { x: 7, y: 4 })).toEqual({
      x: 6,
      y: 3,
    });
  });

  test("no travel leaves the item where it started", () => {
    expect(dragTarget({ x: 4, y: 2 }, { x: 5, y: 3 }, { x: 5, y: 3 })).toEqual({
      x: 4,
      y: 2,
    });
  });

  test("travel up and left is negative, for moveItem to clamp", () => {
    // Off the top-left corner: moveItem clamps, so this stays raw.
    expect(dragTarget({ x: 0, y: 0 }, { x: 1, y: 1 }, { x: 0, y: 0 })).toEqual({
      x: -1,
      y: -1,
    });
  });
});

describe("resolveRenderable", () => {
  // `item()` above builds ids as `w-<instanceId>`, so a lookup that answers
  // for "w-a" resolves the item created as item("a", ...).
  const lookup = (known: string[]) => (id: string) =>
    known.includes(id) ? { id } : undefined;

  test("pairs each item with the definition its id resolves to", () => {
    const resolved = resolveRenderable(
      [item("a", 0, 0, 3, 2), item("b", 3, 0, 3, 2)],
      lookup(["w-a", "w-b"]),
    );

    expect(resolved.map((r) => r.item.instanceId)).toEqual(["a", "b"]);
    expect(resolved.map((r) => r.def.id)).toEqual(["w-a", "w-b"]);
  });

  test("skips an item whose id no definition answers for", () => {
    const resolved = resolveRenderable(
      [item("a", 0, 0, 3, 2), item("gone", 3, 0, 3, 2)],
      lookup(["w-a"]),
    );

    expect(resolved.map((r) => r.item.instanceId)).toEqual(["a"]);
  });

  test("a skipped item leaves no rows behind", () => {
    // "gone" occupies rows 2-3 between two known widgets. Dropping it has to
    // reclaim those rows, or the grid renders an empty band where it was.
    const resolved = resolveRenderable(
      [
        item("top", 0, 0, 3, 2),
        item("gone", 0, 2, 3, 2),
        item("bot", 0, 4, 3, 2),
      ],
      lookup(["w-top", "w-bot"]),
    );

    expect(shape(resolved.map((r) => r.item))).toEqual(["bot@0,2", "top@0,0"]);
    expect(layoutHeight(resolved.map((r) => r.item))).toBe(4);
  });

  test("skipping every item yields an empty layout of height 0", () => {
    const resolved = resolveRenderable([item("a", 0, 0, 3, 2)], lookup([]));

    expect(resolved).toEqual([]);
    expect(layoutHeight(resolved.map((r) => r.item))).toBe(0);
  });

  test("returns reading order, whatever order it is given", () => {
    const resolved = resolveRenderable(
      [item("c", 0, 4, 3, 2), item("b", 4, 0, 3, 2), item("a", 0, 0, 3, 2)],
      lookup(["w-a", "w-b", "w-c"]),
    );

    expect(resolved.map((r) => r.item.instanceId)).toEqual(["a", "b", "c"]);
  });

  test("does not mutate its input", () => {
    const items = [item("a", 0, 2, 3, 2), item("gone", 0, 0, 3, 2)];
    const before = structuredClone(items);

    resolveRenderable(items, lookup(["w-a"]));

    expect(items).toEqual(before);
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

  test("no overlap, nothing lost, in-grid, input untouched, stable when re-applied", () => {
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
        const isMove = rand() < 0.5;
        const requestedX = int(-3, 14);
        const requestedY = int(-3, 25);
        const requestedW = int(0, 14);
        const requestedH = int(0, 8);
        const bounds = { minW: 1, minH: 1 };
        const before_ = layout.find((i) => i.instanceId === target);
        const targetW = before_?.w ?? 0;
        const targetX = before_?.x ?? 0;
        // The same call a drag or resize handler repeats on every pointermove.
        const apply = (from: readonly LayoutItem[]) =>
          isMove
            ? moveItem(from, target, requestedX, requestedY)
            : resizeItem(from, target, requestedW, requestedH, bounds);
        const next = apply(layout);

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
        const changed = next.find((i) => i.instanceId === target);
        if (isMove) {
          // Rule 3: the moved item keeps its requested column.
          const expectedX = Math.min(
            DASHBOARD_COLUMNS - targetW,
            Math.max(0, Math.round(requestedX)),
          );
          expect(changed?.x, `${where}: moved item x`).toBe(expectedX);
        } else {
          // A resize applies the requested size, clamped, and never moves x.
          const expectedW = Math.max(
            1,
            Math.min(
              DASHBOARD_COLUMNS - targetX,
              Math.max(bounds.minW, requestedW),
            ),
          );
          const expectedH = Math.max(1, bounds.minH, requestedH);
          expect(
            [changed?.x, changed?.w, changed?.h],
            `${where}: resized item`,
          ).toEqual([targetX, expectedW, expectedH]);
        }
        expect(
          next.map((i) => i.instanceId),
          `${where}: not in reading order`,
        ).toEqual(
          [...next]
            .sort((p, q) => p.y - q.y || p.x - q.x)
            .map((i) => i.instanceId),
        );
        expect(apply(next), `${where}: unstable when re-applied`).toEqual(next);
        layout = next;
      }
    }
  });
});
