/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  setBrowser,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The extensions hub lists the cluster's CRDs grouped by API group with the
 * number of instances of each, and narrows them as the operator types.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: ExtensionsHub } = await import("./ExtensionsHub.tsx");

const crds = {
  "widgets.example.com": [
    {
      group: "widgets.example.com",
      resource: "widgets",
      kind: "Widget",
      scope: "Namespaced",
    },
    {
      group: "widgets.example.com",
      resource: "gadgets",
      kind: "Gadget",
      scope: "Cluster",
    },
  ],
  "acme.io": [
    { group: "acme.io", resource: "boxes", kind: "Box", scope: "Namespaced" },
  ],
};
const counts = { "widgets.example.com/widgets": 4, "acme.io/boxes": 1 };

let urls: string[] = [];
let respond: (url: string) => Response;

beforeEach(() => {
  urls = [];
  respond = (url) => (url.endsWith("/counts") ? json(counts) : json(crds));
  setAccessToken("t");
  globalThis.fetch = (async (input: string | URL | Request) => {
    urls.push(String(input));
    return respond(String(input));
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

test("shows a skeleton while the CRDs load", () => {
  globalThis.fetch = (() =>
    new Promise<Response>(() => {})) as unknown as typeof globalThis.fetch;
  render(<ExtensionsHub />);
  expect(body().queryByText("Extensions")).toBeNull();
});

test("lists CRDs by group, sorted, with type and instance counts", async () => {
  render(<ExtensionsHub />);
  await settle();
  expect([...urls].sort()).toEqual([
    "/api/v1/extensions/crds",
    "/api/v1/extensions/crds/counts",
  ]);
  expect(body().getByText("3 CRDs across 2 API groups")).toBeTruthy();
  const groups = body().getAllByText(/^(acme\.io|widgets\.example\.com)$/);
  expect(groups.map((g: HTMLElement) => g.textContent)).toEqual([
    "acme.io",
    "widgets.example.com",
  ]);
  expect(body().getByText(/2 types/).textContent).toContain("4 instances");
  expect(body().getByText(/1 type /).textContent).toContain("1 instance");
  const card = body().getByText("Widget").closest("a") as HTMLAnchorElement;
  expect(card.getAttribute("href")).toBe(
    "/extensions/widgets.example.com/widgets",
  );
  expect(card.textContent).toContain("4");
  expect(card.textContent).toContain("widgets.widgets.example.com");
  expect(card.textContent).toContain("Namespaced");
  const gadget = body().getByText("Gadget").closest("a") as HTMLElement;
  expect(gadget.textContent).toContain("0");
  expect(gadget.textContent).toContain("Cluster");
});

test("search narrows by kind or resource name and counts what is left", async () => {
  render(<ExtensionsHub />);
  await settle();
  const box = body().getByPlaceholderText(
    "Search CRDs by kind or resource name…",
  );
  fireEvent.change(box, { target: { value: " GADGET " } });
  expect(body().getByText("1 CRDs across 1 API group")).toBeTruthy();
  expect(body().queryByText("Widget")).toBeNull();
  fireEvent.change(box, { target: { value: "boxes.acme" } });
  expect(body().getByText("1 CRDs across 1 API group")).toBeTruthy();
  expect(body().getByText("Box")).toBeTruthy();
  fireEvent.change(box, { target: { value: "nothing" } });
  expect(body().getByText("0 CRDs across 0 API groups")).toBeTruthy();
  expect(
    body().getByText("No custom resource definitions found in this cluster."),
  ).toBeTruthy();
});

test("a failed load shows the empty state", async () => {
  respond = () => json({}, 500);
  render(<ExtensionsHub />);
  await settle();
  expect(
    body().getByText("No custom resource definitions found in this cluster."),
  ).toBeTruthy();
});

test("the server render is a fixed-height placeholder and fetches nothing", async () => {
  setBrowser(false);
  try {
    const { container } = render(<ExtensionsHub />);
    await act(async () => {});
    expect(container.innerHTML).toBe('<div style="min-height: 400px;"></div>');
    expect(urls).toEqual([]);
  } finally {
    setBrowser(true);
  }
});
