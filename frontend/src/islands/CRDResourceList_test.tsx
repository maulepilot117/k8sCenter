/** @jsxImportSource react */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import "react";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// A stub fetch replaces Bun's; capture the original before setupDom swaps in
// happy-dom's and put exactly that back afterwards.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const toasts: Array<{ message: string; type: string }> = [];
// bun shares modules across test files; keep the real store and put it back.
const realToastStore = {
  ...(await import("@/src/lib/toast-store.ts")),
};
mock.module("@/src/lib/toast-store.ts", () => ({
  ...realToastStore,
  showToast: (message: string, type = "info") => {
    toasts.push({ message, type });
  },
}));

const { default: CRDResourceList } = await import("./CRDResourceList.tsx");
const { selectedNamespace } = await import("@/src/lib/namespace.ts");

afterAll(() => {
  mock.module("@/src/lib/toast-store.ts", () => realToastStore);
  globalThis.fetch = bunFetch;
});

interface Call {
  method: string;
  path: string;
}

let calls: Call[] = [];

const meta = {
  info: {
    group: "example.io",
    version: "v1",
    kind: "Widget",
    scope: "Namespaced",
    served: true,
    additionalPrinterColumns: [
      { name: "Size", type: "string", jsonPath: ".spec.size" },
    ],
  },
};

function item(name: string, ns: string, ready: "True" | "False" | null) {
  return {
    metadata: {
      name,
      namespace: ns,
      uid: `uid-${name}`,
      creationTimestamp: new Date().toISOString(),
    },
    spec: { size: `size-${name}` },
    status: ready ? { conditions: [{ type: "Ready", status: ready }] } : {},
  };
}

function stubFetch() {
  calls = [];
  toasts.length = 0;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname + url.search;
    const method = init?.method ?? "GET";
    calls.push({ method, path });
    let payload: unknown = { data: null };
    if (method === "DELETE") {
      payload = { data: {} };
    } else if (path.includes("/extensions/crds/")) {
      payload = { data: meta };
    } else if (path.includes("/extensions/resources/")) {
      payload = {
        data: path.includes("/team-b?")
          ? [item("gamma", "team-b", "True")]
          : [item("alpha", "team-a", "True"), item("beta", "team-a", "False")],
      };
    }
    return new Response(JSON.stringify(payload), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof fetch;
}

afterEach(() => {
  act(() => {
    selectedNamespace.value = "all";
  });
});

test("renders a row per instance with its columns and links", async () => {
  stubFetch();
  render(<CRDResourceList group="example.io" resource="widgets" />);
  await waitFor(() => body().getByText("alpha"));

  expect(body().getByText("beta")).toBeTruthy();
  // namespace + printer column value
  expect(body().getAllByText("team-a").length).toBe(2);
  expect(body().getByText("size-alpha")).toBeTruthy();
  expect(body().getByText("Size")).toBeTruthy();
  // filter tab + badge each
  expect(body().getAllByText("Ready").length).toBe(2);
  expect(body().getAllByText("Not Ready").length).toBe(2);
  expect(body().getAllByText("Widgets").length).toBe(2); // breadcrumb + heading
  const link = body().getByText("alpha").closest("a");
  expect(link?.getAttribute("href")).toBe(
    "/extensions/example.io/widgets/team-a/alpha",
  );
  expect(
    calls.some((c) => c.path === "/api/v1/extensions/crds/example.io/widgets"),
  ).toBe(true);
  expect(
    calls.some(
      (c) =>
        c.path === "/api/v1/extensions/resources/example.io/widgets?limit=100",
    ),
  ).toBe(true);
});

test("the search box filters rows by name", async () => {
  stubFetch();
  render(<CRDResourceList group="example.io" resource="widgets" />);
  await waitFor(() => body().getByText("alpha"));

  const input = body().getByPlaceholderText("Search widgets...");
  (input as HTMLInputElement).value = "alp";
  fireEvent.input(input);
  await waitFor(() => expect(body().queryByText("beta")).toBeNull());
  expect(body().getByText("alpha")).toBeTruthy();

  (input as HTMLInputElement).value = "zzz";
  fireEvent.input(input);
  await waitFor(() => body().getByText("No matching results"));
});

test("refetches with the namespace path when the selected namespace changes", async () => {
  stubFetch();
  render(<CRDResourceList group="example.io" resource="widgets" />);
  await waitFor(() => body().getByText("alpha"));

  act(() => {
    selectedNamespace.value = "team-b";
  });
  await waitFor(() => body().getByText("gamma"));
  expect(body().queryByText("alpha")).toBeNull();
  expect(
    calls.some(
      (c) =>
        c.path ===
        "/api/v1/extensions/resources/example.io/widgets/team-b?limit=100",
    ),
  ).toBe(true);
});

test("confirming a delete sends one DELETE, drops the row and toasts", async () => {
  stubFetch();
  render(<CRDResourceList group="example.io" resource="widgets" />);
  await waitFor(() => body().getByText("alpha"));

  const kebabs = body().getAllByText("⋮");
  fireEvent.click(kebabs[0]);
  fireEvent.click(body().getByText("Delete"));
  body().getByText(/Are you sure you want to delete/);

  const confirm = body()
    .getAllByText("Delete")
    .find(
      (el: HTMLElement) => el.tagName === "BUTTON" && el.closest("p") === null,
    );
  expect(confirm).toBeTruthy();
  const target = confirm as HTMLElement;
  // A double click must still send a single request.
  fireEvent.click(target);
  fireEvent.click(target);

  await waitFor(() => expect(body().queryByText("alpha")).toBeNull());
  const deletes = calls.filter((c) => c.method === "DELETE");
  expect(deletes.length).toBe(1);
  expect(deletes[0].path).toBe(
    "/api/v1/extensions/resources/example.io/widgets/team-a/alpha",
  );
  expect(toasts).toEqual([{ message: "Deleted alpha", type: "success" }]);
  expect(body().queryByText(/Are you sure/)).toBeNull();
});
