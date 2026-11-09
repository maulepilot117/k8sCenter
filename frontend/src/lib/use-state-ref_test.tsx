/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { useStateRef } = await import("./use-state-ref.ts");

test("the ref holds a new value before the next render, and the render shows it", () => {
  let api!: [number, (n: number) => void, { current: number }];
  function Probe() {
    api = useStateRef(1);
    return <p>{api[0]}</p>;
  }
  const view = render(<Probe />);
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
  function Probe() {
    api = useStateRef<Fn>(first);
    return <p>{api[0]()}</p>;
  }
  const view = render(<Probe />);
  act(() => api[1](second));
  expect(view.container.textContent).toBe("b");
  expect(api[2].current).toBe(second);
});
