/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { useLatest } = await import("./use-latest.ts");

test("a ref captured on the first render reads the latest value", () => {
  const refs: { current: string }[] = [];
  function Probe({ value }: { value: string }) {
    refs.push(useLatest(value));
    return null;
  }
  const view = render(<Probe value="a" />);
  act(() => view.rerender(<Probe value="b" />));
  expect(refs[0].current).toBe("b");
});
