/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { useLatest } = await import("./use-latest.ts");

function LatestProbe({
  value,
  refs,
}: {
  value: string;
  refs: { current: string }[];
}) {
  refs.push(useLatest(value));
  return null;
}

test("a ref captured on the first render reads the latest value", () => {
  const refs: { current: string }[] = [];
  const view = render(<LatestProbe value="a" refs={refs} />);
  act(() => view.rerender(<LatestProbe value="b" refs={refs} />));
  expect(refs[0].current).toBe("b");
});
