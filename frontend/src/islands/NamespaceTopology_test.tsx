/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
// Static imports evaluate before this file's body, so the signal stores see
// the real IS_BROWSER (false) and register no browser-only effects that
// would outlive the stub below.
import "@/src/lib/namespace.ts";

/**
 * The topology API refuses a remote cluster selection with 501
 * unsupported_platform (#532): the graph is built from the local cluster's
 * informers. The island must say so plainly, not show a generic error with a
 * Retry that can never succeed, and not render an empty graph.
 *
 * The island only fetches when `IS_BROWSER` is true, and that constant is
 * fixed when `is-browser.ts` is first evaluated, which under `bun test` is
 * without a DOM. As in source-coverage-pages_test.tsx, this file stubs it to
 * true for its own island and puts the real value back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: NamespaceTopology, fitViewBox } = await import(
  "./NamespaceTopology.tsx"
);

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
});

function stubFetch(status: number, body: unknown) {
  originalFetch = globalThis.fetch;

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
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<NamespaceTopology namespace="foo" />, host as HTMLElement));
  // The fetch, the body read and the state update each take a turn of the
  // event loop; settle until the loading state is gone (bounded).
  for (let i = 0; i < 20 && host.textContent?.includes("Loading"); i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
  return host;
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
