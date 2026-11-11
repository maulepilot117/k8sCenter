import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { act, render, setupDom, waitFor } from "@/src/test-utils/render.tsx";

/**
 * The topology API refuses a remote cluster selection with 501
 * unsupported_platform (#532): the graph is built from the local cluster's
 * informers. The island must say so plainly, not show a generic error with a
 * Retry that can never succeed, and not render an empty graph.
 *
 * The island only fetches when `IS_BROWSER` is true, which setupDom sets for
 * this file and restores afterwards.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: NamespaceTopology, fitViewBox } = await import(
  "./NamespaceTopology.tsx"
);

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

function stubFetch(status: number, body: unknown) {
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    )) as unknown as typeof globalThis.fetch;
}

async function mount(status: number, body: unknown) {
  stubFetch(status, body);
  setAccessToken("test-token");
  const { container } = render(<NamespaceTopology namespace="foo" />);
  // The fetch, the body read and the state update each take a turn of the
  // event loop; settle until the loading state is gone (bounded).
  for (let i = 0; i < 20 && container.textContent?.includes("Loading"); i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
  return container as HTMLElement;
}

const NOTICE = "not available for remote clusters";

test("a remote refusal renders the unsupported state, with no Retry", async () => {
  const root = await mount(501, {
    error: {
      code: 501,
      message:
        "the resource topology graph is available for the local cluster only",
      reason: "unsupported_platform",
    },
  });
  expect(
    root.querySelector('[data-topology-state="remote-unsupported"]'),
  ).not.toBeNull();
  expect(root.textContent).toContain(NOTICE);
  expect(root.textContent).not.toContain("Retry");
});

test("any other failure still renders the error with Retry", async () => {
  const root = await mount(500, {
    error: { code: 500, message: "failed to build topology graph" },
  });
  expect(root.textContent).toContain("failed to build topology graph");
  expect(root.textContent).toContain("Retry");
  expect(root.textContent).not.toContain(NOTICE);
});

test("a local graph renders without the remote notice", async () => {
  const root = await mount(200, {
    data: {
      nodes: [
        {
          id: "uid-a",
          kind: "Service",
          name: "svc-a",
          namespace: "foo",
          health: "healthy",
          summary: "",
        },
      ],
      edges: [],
      computedAt: new Date().toISOString(),
    },
  });
  expect(root.textContent).not.toContain(NOTICE);
  expect(root.textContent).toContain("svc-a");
});

// The layout centres each column on y = 0, so half of a tall graph sits at
// negative y. A viewBox fixed at (0, 0) showed the graph's lower half, or
// nothing at all, and "Fit to view" reset to that same box.
test("fit to view frames every node, including those above y = 0", () => {
  const nodes = [
    { x: 130, y: -1048 },
    { x: 350, y: -900 },
    { x: 570, y: 400 },
  ];
  const v = fitViewBox(nodes);
  const w = 1200 / v.zoom;
  const h = 800 / v.zoom;
  for (const n of nodes) {
    expect(n.x - 90).toBeGreaterThanOrEqual(v.panX);
    expect(n.x + 90).toBeLessThanOrEqual(v.panX + w);
    expect(n.y - 28).toBeGreaterThanOrEqual(v.panY);
    expect(n.y + 28).toBeLessThanOrEqual(v.panY + h);
  }
});

test("fit to view does not zoom a small graph past 1x", () => {
  const v = fitViewBox([{ x: 130, y: 0 }]);
  expect(v.zoom).toBe(1);
  // The single node is centred.
  expect(v.panX + 1200 / 2).toBeCloseTo(130);
  expect(v.panY + 800 / 2).toBeCloseTo(0);
});

test("fit to view of an empty graph is the default view", () => {
  expect(fitViewBox([])).toEqual({ zoom: 1, panX: 0, panY: 0 });
});

const GRAPH = {
  nodes: [
    {
      id: "uid-a",
      kind: "Service",
      name: "svc-a",
      namespace: "foo",
      health: "healthy",
      summary: "",
    },
    {
      id: "uid-b",
      kind: "Pod",
      name: "pod-b",
      namespace: "foo",
      health: "healthy",
      summary: "",
    },
  ],
  edges: [{ source: "uid-a", target: "uid-b", type: "selector" }],
  computedAt: new Date().toISOString(),
};

function viewBox(svg: Element): number[] {
  return (svg.getAttribute("viewBox") ?? "").split(" ").map(Number);
}

async function mountGraph() {
  const root = await mount(200, { data: GRAPH });
  const svg = root.querySelector(
    'svg[aria-label="Namespace resource topology graph"]',
  ) as SVGSVGElement;
  expect(svg).not.toBeNull();
  // happy-dom lays nothing out; the pan and zoom maths needs a size.
  svg.getBoundingClientRect = () =>
    ({ left: 0, top: 0, width: 1200, height: 800 }) as DOMRect;
  return svg;
}

test("the wheel listener is native and non-passive, so preventDefault stops the page scrolling", async () => {
  const registered: Array<{
    target: unknown;
    type: string;
    options: unknown;
  }> = [];
  // addEventListener lives on whichever prototype owns it for an SVG element.
  let proto: object | null = Object.getPrototypeOf(
    document.createElementNS("http://www.w3.org/2000/svg", "svg"),
  );
  while (proto && !Object.hasOwn(proto, "addEventListener")) {
    proto = Object.getPrototypeOf(proto);
  }
  if (!proto) throw new Error("addEventListener not found");
  const target = proto as EventTarget;
  const original = target.addEventListener;
  target.addEventListener = function (
    this: EventTarget,
    type: string,
    listener: unknown,
    options?: unknown,
  ) {
    registered.push({ target: this, type, options });
    return (original as (...a: unknown[]) => void).call(
      this,
      type,
      listener,
      options,
    );
  } as typeof target.addEventListener;
  let svg: SVGSVGElement;
  try {
    svg = await mountGraph();
  } finally {
    target.addEventListener = original;
  }
  const wheel = registered.filter(
    (r) => r.target === svg && r.type === "wheel",
  );
  expect(wheel.length).toBeGreaterThan(0);
  expect(
    wheel.every((r) => (r.options as { passive?: boolean })?.passive === false),
  ).toBe(true);
});

test("a wheel event over the graph zooms it and has its default prevented", async () => {
  const svg = await mountGraph();
  const before = viewBox(svg);
  const event = new WheelEvent("wheel", {
    deltaY: -100,
    clientX: 600,
    clientY: 400,
    bubbles: true,
    cancelable: true,
  });
  await act(async () => {
    svg.dispatchEvent(event);
  });
  expect(event.defaultPrevented).toBe(true);
  const after = viewBox(svg);
  // Zoom in: the visible window shrinks.
  expect(after[2]).toBeLessThan(before[2]);
  expect(after[3]).toBeLessThan(before[3]);
});

test("two wheel events before a render compound", async () => {
  const svg = await mountGraph();
  const before = viewBox(svg);
  await act(async () => {
    for (let i = 0; i < 2; i++) {
      svg.dispatchEvent(
        new WheelEvent("wheel", {
          deltaY: -100,
          clientX: 600,
          clientY: 400,
          bubbles: true,
          cancelable: true,
        }),
      );
    }
  });
  // Zoom 1 -> 1.2: the window is 1200 / 1.2 wide.
  expect(viewBox(svg)[2]).toBeCloseTo(1200 / 1.2, 5);
  expect(before[2]).toBeCloseTo(1200, 5);
});

test("a pan across several moves in one session ends at the final pointer position", async () => {
  const svg = await mountGraph();
  const [panX, panY, w] = viewBox(svg);
  expect(w).toBeCloseTo(1200, 5);
  const at = (x: number, y: number) => ({ clientX: x, clientY: y, button: 0 });
  await act(async () => {
    svg.dispatchEvent(
      new MouseEvent("mousedown", { ...at(100, 100), bubbles: true }),
    );
  });
  for (const [x, y] of [
    [150, 130],
    [220, 160],
    [300, 180],
  ]) {
    await act(async () => {
      svg.dispatchEvent(
        new MouseEvent("mousemove", { ...at(x, y), bubbles: true }),
      );
    });
  }
  // 1200x800 view on a 1200x800 box: one screen pixel is one unit.
  const end = viewBox(svg);
  expect(end[0]).toBeCloseTo(panX - 200, 5);
  expect(end[1]).toBeCloseTo(panY - 80, 5);
  await act(async () => {
    svg.dispatchEvent(new MouseEvent("mouseup", { bubbles: true }));
    svg.dispatchEvent(
      new MouseEvent("mousemove", { ...at(900, 900), bubbles: true }),
    );
  });
  await waitFor(() => expect(viewBox(svg)[0]).toBeCloseTo(panX - 200, 5));
});
