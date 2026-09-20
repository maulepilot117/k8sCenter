import { expect, test } from "bun:test";
import {
  applyChange,
  beginEdit,
  commit,
  discard,
  isDirty,
} from "./edit-session.ts";
import type { DashboardLayoutConfig, LayoutItem } from "./types.ts";

const BASE: DashboardLayoutConfig = {
  schemaVersion: 1,
  scope: "overview",
  columns: 12,
  items: [{ instanceId: "a", id: "cluster-health", x: 0, y: 0, w: 4, h: 4 }],
};

function at(x: number, y: number): LayoutItem[] {
  return [{ instanceId: "a", id: "cluster-health", x, y, w: 4, h: 4 }];
}

test("beginEdit: the working copy starts equal to the baseline and is not dirty", () => {
  const s = beginEdit(BASE, 3);
  expect(isDirty(s)).toBe(false);
  expect(s.revision).toBe(3);
});

test("beginEdit: the working copy is a deep copy", () => {
  // A shallow copy would let a drag mutate the baseline, and Cancel would then
  // "restore" the edited layout.
  const s = beginEdit(BASE, 1);
  s.working.items[0].x = 9;
  expect(BASE.items[0].x).toBe(0);
});

test("beginEdit: the baseline is a deep copy of the caller's config too", () => {
  // The caller's config is `layout.value` from the store, which a load can
  // replace but which nothing may mutate in place. A baseline aliasing it
  // would make the session's idea of "as loaded" change underneath it.
  const s = beginEdit(BASE, 1);
  s.baseline.items[0].x = 9;
  expect(BASE.items[0].x).toBe(0);
});

test("applyChange: a real change marks the session dirty", () => {
  const s = applyChange(beginEdit(BASE, 1), at(4, 0));
  expect(isDirty(s)).toBe(true);
});

test("applyChange: a change that restores the baseline is not dirty", () => {
  // Drag a widget away and back again and Save must not be offered -- an
  // enabled Save that writes nothing trains people to ignore it.
  const moved = applyChange(beginEdit(BASE, 1), at(4, 0));
  const back = applyChange(moved, at(0, 0));
  expect(isDirty(back)).toBe(false);
});

test("applyChange: item order does not affect dirtiness", () => {
  // The grid re-sorts during compaction, so a reordered-but-identical array is
  // not a change.
  const two: DashboardLayoutConfig = {
    ...BASE,
    items: [
      { instanceId: "a", id: "cluster-health", x: 0, y: 0, w: 4, h: 4 },
      { instanceId: "b", id: "node-list", x: 4, y: 0, w: 4, h: 4 },
    ],
  };
  const s = applyChange(beginEdit(two, 1), [two.items[1], two.items[0]]);
  expect(isDirty(s)).toBe(false);
});

test("applyChange: absent params and empty params are the same layout", () => {
  // The grid hands back whatever it is holding, and a widget with no
  // parameters can arrive as `undefined` from one path and `{}` from another.
  // Treating those as a change would arm Save over a layout nobody touched.
  const s = applyChange(beginEdit(BASE, 1), [
    {
      instanceId: "a",
      id: "cluster-health",
      x: 0,
      y: 0,
      w: 4,
      h: 4,
      params: {},
    },
  ]);
  expect(isDirty(s)).toBe(false);
});

test("applyChange: a changed param value is a change", () => {
  const withParam: DashboardLayoutConfig = {
    ...BASE,
    items: [{ ...BASE.items[0], params: { namespace: "prod" } }],
  };
  const s = applyChange(beginEdit(withParam, 1), [
    { ...BASE.items[0], params: { namespace: "staging" } },
  ]);
  expect(isDirty(s)).toBe(true);
});

test("applyChange: removing a widget is a change", () => {
  // D17 removes widgets through this same door, and an empty layout that read
  // as clean would leave Save disabled over the one edit hardest to redo.
  const s = applyChange(beginEdit(BASE, 1), []);
  expect(isDirty(s)).toBe(true);
});

test("applyChange: does not mutate the session it was given", () => {
  // The caller holds sessions in a signal and assigns the result. A mutating
  // applyChange would make the baseline follow the working copy, and Cancel
  // would restore the edits.
  const s = beginEdit(BASE, 1);
  applyChange(s, at(7, 2));
  expect(isDirty(s)).toBe(false);
  expect(s.working.items[0].x).toBe(0);
});

test("discard: returns the baseline, not the working copy", () => {
  const s = applyChange(beginEdit(BASE, 1), at(7, 2));
  expect(discard(s).items[0].x).toBe(0);
});

test("discard: hands back a copy the caller cannot use to corrupt the session", () => {
  const s = applyChange(beginEdit(BASE, 1), at(7, 2));
  discard(s).items[0].x = 5;
  expect(discard(s).items[0].x).toBe(0);
});

test("commit: produces the working copy and the revision it was loaded at", () => {
  const s = applyChange(beginEdit(BASE, 5), at(2, 0));
  const { config, revision } = commit(s);
  expect(config.items[0].x).toBe(2);
  expect(revision).toBe(5);
});

test("commit: preserves schemaVersion, scope and columns from the baseline", () => {
  // These are not the editor's to change, and a dropped scope would write a
  // layout addressed to the wrong dashboard.
  const { config } = commit(beginEdit(BASE, 1));
  expect(config.schemaVersion).toBe(1);
  expect(config.scope).toBe("overview");
  expect(config.columns).toBe(12);
});

test("commit: an applied change cannot rewrite scope, columns or schemaVersion", () => {
  // applyChange takes items only, so there is no door for this -- this test is
  // what keeps the signature from quietly growing one.
  const s = applyChange(beginEdit({ ...BASE, columns: 12 }, 1), at(1, 0));
  const { config } = commit(s);
  expect(config.columns).toBe(12);
  expect(config.scope).toBe("overview");
});
