import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: PromQLQuery } = await import("./PromQLQuery.tsx");

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

function stub(urls: string[]) {
  setAccessToken("t");
  globalThis.fetch = (async (input: string | URL | Request) => {
    urls.push(String(input));
    return json({
      resultType: "vector",
      result: [{ metric: { job: "api" }, value: [1, "42"] }],
    });
  }) as unknown as typeof globalThis.fetch;
}

function type(text: string) {
  const box = body().getByPlaceholderText('up{job="prometheus"}');
  fireEvent.input(box, { target: { value: text } });
  return box;
}

test("Run Query is disabled until there is an expression, then shows the result", async () => {
  const urls: string[] = [];
  stub(urls);
  const view = render(<PromQLQuery />);
  const run = body().getByRole("button", { name: "Run Query" });
  expect((run as HTMLButtonElement).disabled).toBe(true);

  type("up == 1");
  expect((run as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(run);
  await settle();

  expect(urls.length).toBe(1);
  expect(urls[0]).toContain("/v1/monitoring/query?query=up%20%3D%3D%201");
  expect(view.container.textContent).toContain("1 result(s)");
  expect(view.container.textContent).toContain('{job="api"}');
  expect(view.container.textContent).toContain("42");
});

test("Ctrl+Enter runs the query", async () => {
  const urls: string[] = [];
  stub(urls);
  render(<PromQLQuery />);
  const box = type("up");
  fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
  await settle();
  expect(urls.length).toBe(1);
  expect(urls[0]).toContain("query=up");
});

test("Range sends a query_range request for the chosen window", async () => {
  const urls: string[] = [];
  stub(urls);
  render(<PromQLQuery />);
  fireEvent.click(body().getByRole("button", { name: "Range" }));
  fireEvent.change(body().getByRole("combobox"), { target: { value: "6h" } });
  type("up");
  fireEvent.click(body().getByRole("button", { name: "Run Query" }));
  await settle();

  expect(urls.length).toBe(1);
  const u = new URL(urls[0] as string, "http://localhost");
  expect(u.pathname).toContain("/v1/monitoring/query_range");
  const span =
    Date.parse(u.searchParams.get("end") as string) -
    Date.parse(u.searchParams.get("start") as string);
  expect(Math.round(span / 3600000)).toBe(6);
  expect(u.searchParams.get("step")).toBe("108s");
});

test("a failed query shows the error and clears the old result", async () => {
  setAccessToken("t");
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ error: { code: 403, message: "nope" } }), {
      status: 403,
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof globalThis.fetch;
  const view = render(<PromQLQuery />);
  type("up");
  fireEvent.click(body().getByRole("button", { name: "Run Query" }));
  await settle();
  expect(view.container.textContent).not.toContain("result(s)");
  expect(view.container.textContent).toContain("nope");
});
