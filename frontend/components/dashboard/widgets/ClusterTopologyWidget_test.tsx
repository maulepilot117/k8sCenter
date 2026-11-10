/** @jsxImportSource react */
import { afterEach, describe, expect, spyOn, test } from "bun:test";
import { act, body, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { dashboardData } = await import("@/lib/dashboard/data.ts");
const { getWidget } = await import("@/lib/dashboard/registry.ts");
// The single widget file, not the index: other widgets could read the same
// signals and mask a missing subscription in this one.
await import("@/components/dashboard/widgets/ClusterTopologyWidget.tsx");

const KEYS = [
  "nodes-list",
  "services-list",
  "deployments-list",
  "statefulsets-list",
  "daemonsets-list",
  "pods-list",
  "pvcs-list",
];

function seed(key: string, data: unknown): void {
  dashboardData.signalFor(key).value = {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
  };
}

const page = (items: unknown[], total = items.length) => ({
  items,
  total,
  continue: "",
});

const pod = (name: string) => ({
  metadata: { name, namespace: "ns" },
  spec: { containers: [{ name: "app" }] },
  status: { phase: "Running" },
});

function seedAll(pods: unknown[]): void {
  for (const key of KEYS) seed(key, page([]));
  seed("pods-list", page(pods));
}

/** happy-dom's WheelEvent drops modifier keys from the init dictionary. */
function wheel(deltaY: number, ctrlKey: boolean): WheelEvent {
  const e = new WheelEvent("wheel", {
    deltaY,
    cancelable: true,
    bubbles: true,
  });
  Object.defineProperty(e, "ctrlKey", { value: ctrlKey });
  return e;
}

function mount() {
  const def = getWidget("cluster-topology");
  if (def === undefined) throw new Error("cluster-topology is not registered");
  return render(<>{def.render({ mode: "normal", params: {} })}</>);
}

afterEach(() => {
  for (const key of KEYS) {
    dashboardData.signalFor(key).value = {
      data: null,
      error: null,
      errorKind: null,
      loading: false,
      range: "1h",
    };
  }
});

describe("ClusterTopologyWidget", () => {
  test("draws the pods the payload holds", () => {
    seedAll([pod("alpha"), pod("beta")]);
    mount();
    const svg = body().getByRole("group");
    expect(svg.getAttribute("aria-label")).toContain("2 resources");
    expect(body().getByLabelText("ns/alpha (Running)")).toBeTruthy();
    expect(body().getByLabelText("ns/beta (Running)")).toBeTruthy();
  });

  test("an empty readable cluster says there is nothing to draw", () => {
    seedAll([]);
    mount();
    expect(body().getByTestId("cluster-topology-empty")).toBeTruthy();
  });

  test("a live update shows the new payload", () => {
    seedAll([pod("alpha")]);
    mount();
    expect(body().queryByLabelText("ns/gamma (Running)")).toBeNull();
    act(() => {
      seed("pods-list", page([pod("alpha"), pod("gamma")]));
    });
    expect(body().getByLabelText("ns/gamma (Running)")).toBeTruthy();
  });

  test("a coverage note follows a live source update", () => {
    seedAll([pod("alpha")]);
    mount();
    expect(body().queryByTestId("cluster-topology-coverage")).toBeNull();
    act(() => {
      dashboardData.signalFor("nodes-list").value = {
        data: null,
        error: "denied",
        errorKind: "permission",
        loading: false,
        range: "1h",
      };
    });
    expect(
      body().getByTestId("cluster-topology-coverage").textContent,
    ).toContain("Nodes (not permitted for this account)");
  });

  test("ctrl+wheel zooms and is prevented; plain wheel is left alone", () => {
    seedAll([pod("alpha")]);
    mount();
    const svg = body().getByRole("group");
    expect(body().queryByText("100%")).toBeNull();

    const plain = wheel(-100, false);
    act(() => {
      svg.dispatchEvent(plain);
    });
    expect(plain.defaultPrevented).toBe(false);
    const before = svg.getAttribute("viewBox");

    const zoom = wheel(-100, true);
    act(() => {
      svg.dispatchEvent(zoom);
    });
    expect(zoom.defaultPrevented).toBe(true);
    expect(svg.getAttribute("viewBox")).not.toBe(before);
    expect(body().getByText(/^\d+%$/).textContent).not.toBe("100%");

    // A ctrl+wheel the other way steps back from the zoom the first produced.
    const first = svg.getAttribute("viewBox");
    act(() => {
      svg.dispatchEvent(wheel(100, true));
    });
    expect(svg.getAttribute("viewBox")).not.toBe(first);
  });
});

describe("ClusterTopologyWidget wheel registration", () => {
  test("the wheel listener is native and non-passive", () => {
    const spy = spyOn(HTMLElement.prototype, "addEventListener");
    const svgSpy = spyOn(Element.prototype, "addEventListener");
    try {
      seedAll([pod("alpha")]);
      mount();
      // React registers its own wheel listener on the root container; only
      // the one the widget puts on its own svg is under test.
      const svg = body().getByRole("group");
      const calls = [spy, svgSpy].flatMap((s) =>
        s.mock.calls.filter(
          (c, i) => c[0] === "wheel" && s.mock.contexts[i] === svg,
        ),
      );
      expect(calls.length).toBeGreaterThan(0);
      for (const c of calls) {
        expect((c[2] as AddEventListenerOptions).passive).toBe(false);
      }
    } finally {
      spy.mockRestore();
      svgSpy.mockRestore();
    }
  });
});
