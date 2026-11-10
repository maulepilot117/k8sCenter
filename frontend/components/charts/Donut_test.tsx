/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: Donut } = await import("./Donut.tsx");

test("segments become conic-gradient stops and the center sits in the hole", () => {
  const { container } = render(
    <Donut
      size={100}
      thickness={10}
      segments={[
        { value: 3, color: "red" },
        { value: 1, color: "blue" },
      ]}
      center={<span>4 pods</span>}
    />,
  );
  const outer = container.firstElementChild as HTMLElement;
  expect(outer.style.width).toBe("100px");
  expect(outer.style.height).toBe("100px");
  expect(outer.style.borderRadius).toBe("50%");
  expect(outer.style.background).toContain("conic-gradient");
  expect(outer.style.background).toContain("red 0% 75%");
  expect(outer.style.background).toContain("blue 75% 100%");
  const hole = outer.firstElementChild as HTMLElement;
  expect(hole.style.inset).toBe("10px");
  expect(hole.textContent).toBe("4 pods");
});

test("no segments still renders with default size", () => {
  const { container } = render(<Donut segments={[]} />);
  const outer = container.firstElementChild as HTMLElement;
  expect(outer.style.width).toBe("108px");
  expect((outer.firstElementChild as HTMLElement).style.inset).toBe("15px");
});
