/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The Flux provider list shares its search, paging, row menu and delete
 * behaviour with the alert and receiver lists through the notification
 * helpers: typing in the search box returns to the first page, opening one
 * row's menu closes another's, a click elsewhere closes it, and confirming a
 * delete sends one request however many times the button is hit.
 */
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

const { default: FluxProviders } = await import("./FluxProviders.tsx");

interface Call {
  method: string;
  url: string;
}
let calls: Call[] = [];

/** The live-update socket is not under test; it only has to stay quiet. */
class QuietWebSocket {
  static readonly OPEN = 1;
  readyState = 0;
  send() {}
  close() {}
  addEventListener() {}
  removeEventListener() {}
}

const provider = (i: number) => ({
  name: `prov-${String(i).padStart(3, "0")}`,
  namespace: "flux-system",
  type: "slack",
  address: "",
  channel: "ops",
  secretRef: "",
  suspend: false,
  status: "Ready",
  createdAt: "2026-01-01T00:00:00Z",
});

function stubApi(count: number) {
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({ method, url });
    const data = url.endsWith("/status")
      ? { available: true }
      : method === "GET"
        ? { providers: Array.from({ length: count }, (_, i) => provider(i)) }
        : {};
    return new Response(JSON.stringify({ data }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
}

beforeEach(() => {
  calls = [];
  globalThis.WebSocket = QuietWebSocket as unknown as typeof WebSocket;
  setAccessToken("t");
});
afterEach(() => {
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
  setAccessToken(null);
});

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

async function mount(count: number) {
  stubApi(count);
  const view = render(<FluxProviders />);
  await settle();
  return { container: view.container as HTMLElement };
}

const names = (root: HTMLElement) =>
  [...root.querySelectorAll('[role="cell"]')]
    .map((c) => c.textContent ?? "")
    .filter((t) => t.startsWith("prov-"))
    .map((t) => t.slice(0, 8));

const menuButtons = (root: HTMLElement) =>
  [...root.querySelectorAll("button")].filter((b) => b.textContent === "…");

test("typing in the search box filters the list and returns to page 1", async () => {
  const { container } = await mount(150);
  expect(names(container)).toHaveLength(100);

  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Next" }));
  });
  expect(container.textContent).toContain("Page 2 of 2");
  expect(names(container)).toHaveLength(50);

  const box = body().getByPlaceholderText(/Filter by name/) as HTMLInputElement;
  await act(async () => {
    fireEvent.change(box, { target: { value: "prov-007" } });
  });
  expect(names(container)).toEqual(["prov-007"]);
  expect(container.textContent).toContain("1 of 150 providers");

  await act(async () => {
    fireEvent.change(box, { target: { value: "" } });
  });
  expect(names(container)).toHaveLength(100);
  expect(names(container)[0]).toBe("prov-000");
});

test("paging moves between pages and stops at both ends", async () => {
  const { container } = await mount(150);
  const prev = body().getByRole("button", {
    name: "Previous",
  }) as HTMLButtonElement;
  const next = body().getByRole("button", {
    name: "Next",
  }) as HTMLButtonElement;
  expect(prev.disabled).toBe(true);

  await act(async () => {
    fireEvent.click(next);
  });
  expect(names(container)[0]).toBe("prov-100");
  expect(next.disabled).toBe(true);

  await act(async () => {
    fireEvent.click(prev);
  });
  expect(names(container)[0]).toBe("prov-000");
});

test("opening one row's menu closes another's, and a click elsewhere closes it", async () => {
  const { container } = await mount(3);
  const [first, second] = menuButtons(container);

  await act(async () => {
    fireEvent.click(first);
  });
  expect(body().queryAllByText("Suspend")).toHaveLength(1);

  await act(async () => {
    fireEvent.click(second);
  });
  expect(body().queryAllByText("Suspend")).toHaveLength(1);
  const rows = [...container.querySelectorAll('[role="row"]')];
  expect(rows[2].textContent).toContain("Suspend");

  await act(async () => {
    fireEvent.click(second);
  });
  expect(body().queryAllByText("Suspend")).toHaveLength(0);

  await act(async () => {
    fireEvent.click(first);
  });
  expect(body().queryAllByText("Suspend")).toHaveLength(1);
  await act(async () => {
    fireEvent.click(document.body);
  });
  expect(body().queryAllByText("Suspend")).toHaveLength(0);
});

test("confirming a delete sends a single request even when hit twice", async () => {
  const { container } = await mount(2);
  await act(async () => {
    fireEvent.click(menuButtons(container)[0]);
  });
  await act(async () => {
    fireEvent.click(body().getByText("Delete"));
  });
  const confirm = body()
    .getAllByRole("button", { name: "Delete" })
    .pop() as HTMLElement;
  await act(async () => {
    fireEvent.click(confirm);
    fireEvent.click(confirm);
  });
  await settle();
  const deletes = calls.filter((c) => c.method === "DELETE");
  expect(deletes).toHaveLength(1);
  expect(deletes[0].url).toContain("/providers/flux-system/prov-000");
});

test("submitting the create form twice sends one request", async () => {
  await mount(1);
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Create Provider" }));
  });
  const nameInput = body().getByPlaceholderText(
    "my-slack-provider",
  ) as HTMLInputElement;
  await act(async () => {
    fireEvent.change(nameInput, { target: { value: "new-one" } });
  });
  const submit = body().getByRole("button", { name: "Create" }) as HTMLElement;
  await act(async () => {
    fireEvent.click(submit);
    fireEvent.click(submit);
  });
  await settle();
  const posts = calls.filter((c) => c.method === "POST");
  expect(posts).toHaveLength(1);
});
