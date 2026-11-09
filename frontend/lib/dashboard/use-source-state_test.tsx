/** @jsxImportSource react */
import { describe, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { dashboardData } = await import("@/lib/dashboard/data.ts");
const { useSourceState } = await import("@/lib/dashboard/use-source-state.ts");

const KEY = "use-source-state-test-a";
const OTHER = "use-source-state-test-b";

function resolved(data: unknown) {
  return {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
  };
}

describe("useSourceState", () => {
  test("re-renders when its own source resolves, not when another does", () => {
    let renders = 0;
    function Probe() {
      renders++;
      const s = useSourceState<{ n: number }>(KEY);
      return <span>{s.data ? `n=${s.data.n}` : "empty"}</span>;
    }

    const { container } = render(<Probe />);
    expect(container.textContent).toBe("empty");
    const afterMount = renders;

    act(() => {
      dashboardData.signalFor(OTHER).value = resolved({ n: 9 });
    });
    expect(renders).toBe(afterMount);

    act(() => {
      dashboardData.signalFor(KEY).value = resolved({ n: 3 });
    });
    expect(container.textContent).toBe("n=3");
    expect(renders).toBe(afterMount + 1);
  });
});
