/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { type ReadonlySignal, type Signal, signal } from "@preact/signals-core";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { useComputed, useSignal, useSignalEffect } = await import(
  "./signal-hooks.ts"
);

test("useSignal keeps one signal across renders, starting at its initial value", () => {
  const seen: Signal<number>[] = [];
  function Owner({ tick }: { tick: number }) {
    seen.push(useSignal(tick));
    return null;
  }
  const r = render(<Owner tick={1} />);
  r.rerender(<Owner tick={2} />);

  expect(seen).toHaveLength(2);
  expect(seen[1]).toBe(seen[0]);
  expect(seen[0].value).toBe(1);
});

test("useComputed follows the signals it reads, with the latest compute", async () => {
  const base = signal(2);
  let derived: ReadonlySignal<number> | undefined;
  function Owner({ factor }: { factor: number }) {
    derived = useComputed(() => base.value * factor);
    return null;
  }
  const r = render(<Owner factor={10} />);
  expect(derived?.value).toBe(20);

  r.rerender(<Owner factor={100} />);
  await act(() => {
    base.value = 3;
  });

  expect(derived?.value).toBe(300);
});

test("useSignalEffect re-runs on a signal write and cleans up on unmount", async () => {
  const count = signal(0);
  const runs: number[] = [];
  let cleanups = 0;
  function Owner() {
    useSignalEffect(() => {
      runs.push(count.value);
      return () => {
        cleanups++;
      };
    });
    return null;
  }
  const r = render(<Owner />);
  await act(() => {
    count.value = 1;
  });
  expect(runs).toEqual([0, 1]);
  expect(cleanups).toBe(1);

  await act(() => {
    r.unmount();
  });
  expect(cleanups).toBe(2);
  await act(() => {
    count.value = 2;
  });
  expect(runs).toEqual([0, 1]);
});
