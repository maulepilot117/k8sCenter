import { expect, test } from "bun:test";
import type { ReactNode } from "react";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { useStateRef } = await import("./use-state-ref.ts");

function StateRefProbe<T>({
  initial,
  show,
  onRender,
}: {
  initial: T;
  show: (v: T) => ReactNode;
  onRender: (api: [T, (n: T) => void, { current: T }]) => void;
}) {
  const api = useStateRef<T>(initial);
  onRender(api);
  return <p>{show(api[0])}</p>;
}

test("the ref holds a new value before the next render, and the render shows it", () => {
  let api!: [number, (n: number) => void, { current: number }];
  const view = render(
    <StateRefProbe
      initial={1}
      show={(v) => v}
      onRender={(a) => {
        api = a;
      }}
    />,
  );
  const [, set, ref] = api;
  let seenInHandler = -1;
  act(() => {
    set(2);
    seenInHandler = ref.current;
  });
  expect(seenInHandler).toBe(2);
  expect(view.container.textContent).toBe("2");
  expect(api[1]).toBe(set);
  expect(api[2]).toBe(ref);
});

test("a function value is stored as the value, not called as an updater", () => {
  type Fn = () => string;
  const first: Fn = () => "a";
  const second: Fn = () => "b";
  let api!: [Fn, (n: Fn) => void, { current: Fn }];
  const view = render(
    <StateRefProbe<Fn>
      initial={first}
      show={(v) => v()}
      onRender={(a) => {
        api = a;
      }}
    />,
  );
  act(() => api[1](second));
  expect(view.container.textContent).toBe("b");
  expect(api[2].current).toBe(second);
});
