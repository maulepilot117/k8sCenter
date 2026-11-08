/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";
import type { RowKeys } from "./use-row-keys.ts";

setupDom({ browser: true });
const { useRowKeys } = await import("./use-row-keys.ts");

function harness(initial: number) {
  let rows!: RowKeys;
  let length = initial;
  function Probe({ n }: { n: number }) {
    rows = useRowKeys(n);
    return null;
  }
  const view = render(<Probe n={length} />);
  const set = (n: number) => {
    length = n;
    act(() => view.rerender(<Probe n={n} />));
  };
  const keys = () => Array.from({ length }, (_, i) => rows.keyAt(i));
  return { rows: () => rows, set, keys };
}

test("rows keep their keys across renders and new rows get fresh ones", () => {
  const h = harness(2);
  const first = h.keys();
  h.set(2);
  expect(h.keys()).toEqual(first);
  h.set(3);
  const grown = h.keys();
  expect(grown.slice(0, 2)).toEqual(first);
  expect(new Set(grown).size).toBe(3);
});

test("removing a middle row keeps the keys of the rows after it", () => {
  const h = harness(3);
  const [a, , c] = h.keys();
  h.rows().removeAt(1);
  h.set(2);
  expect(h.keys()).toEqual([a, c]);
  h.set(3);
  expect(h.keys().slice(0, 2)).toEqual([a, c]);
  expect([a, c]).not.toContain(h.keys()[2]);
});
