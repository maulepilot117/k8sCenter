import { afterEach, describe, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

// Registers every real widget.
await import("@/components/dashboard/widgets/index.ts");
const { default: WidgetHost } = await import(
  "@/components/dashboard/WidgetHost.tsx"
);
const { allWidgets } = await import("@/lib/dashboard/registry.ts");
const { dashboardData } = await import("@/lib/dashboard/data.ts");

/**
 * Every registered widget mounts through the host and settles.
 *
 * The host subscribes to the set of source keys a widget's state depends on.
 * A widget that names the summary as its own source and also headlines one of
 * its sections put the same key in that set twice, the cached snapshot never
 * matched, and the host re-rendered forever: the dashboard hung on its first
 * card. The host tests use fixture widgets, so only a pass over the real ones
 * shows a declaration like that.
 */
const SUMMARY = "dashboard-summary";

afterEach(() => {
  dashboardData.signalFor(SUMMARY).value = {
    data: null,
    error: null,
    errorKind: null,
    loading: false,
    range: null,
  };
});

describe("every registered widget renders through WidgetHost", () => {
  const defs = allWidgets().filter((d) => !d.id.startsWith("fixture-"));

  test("there are real widgets to render", () => {
    expect(defs.length).toBeGreaterThan(30);
  });

  test("each mounts with no data, then settles when the summary lands", () => {
    const mounted: string[] = [];
    for (const def of defs) {
      const r = render(<WidgetHost def={def} params={{}} />);
      act(() => {
        dashboardData.signalFor(SUMMARY).value = {
          data: null,
          error: null,
          errorKind: null,
          loading: true,
          range: "1h",
        };
      });
      mounted.push(
        r.container
          .querySelector("[data-testid='widget-host']")
          ?.getAttribute("data-widget-id") ?? "",
      );
      r.unmount();
    }
    expect(mounted).toEqual(defs.map((d) => d.id));
  });
});
