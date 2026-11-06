/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { signal } from "@preact/signals";
import { render } from "preact";
import { act } from "preact/test-utils";

/**
 * The filter bar starts from the global namespace picker. The picker's
 * "all namespaces" value is the sentinel string "all", which is not a
 * namespace: copying it in queried `{namespace="all"}` and found nothing.
 * With no namespace and no other filter the selector must still match
 * something, because Loki rejects an empty `{}`.
 *
 * IS_BROWSER is stubbed to true and put back afterwards, and fetch answers
 * every label lookup with an empty list.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const realFetch = globalThis.fetch;
globalThis.fetch = (async () =>
  new Response(JSON.stringify({ data: [] }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })) as unknown as typeof fetch;

const { default: LogFilterBar } = await import("./LogFilterBar.tsx");
const { selectedNamespace } = await import("@/src/lib/namespace.ts");

afterAll(() => {
  globalThis.fetch = realFetch;
  selectedNamespace.value = "all";
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
});

function mountAndRun(): { query: string; select: HTMLSelectElement } {
  const queries: string[] = [];
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <LogFilterBar
        onSearch={(q) => queries.push(q)}
        onLiveTail={() => {}}
        onStopTail={() => {}}
        isTailing={signal(false)}
        loading={signal(false)}
      />,
      host as HTMLElement,
    ),
  );
  const run = [...host.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === "Run",
  );
  if (!run) throw new Error("Run button not rendered");
  act(() => run.click());
  return {
    query: queries[0] ?? "",
    select: host.querySelector("select") as HTMLSelectElement,
  };
}

test("all namespaces queries every namespace, not one named 'all'", () => {
  selectedNamespace.value = "all";
  const { query, select } = mountAndRun();
  expect(query).not.toContain('"all"');
  expect(query).toBe('{namespace=~".+"}');
  expect(select.value).toBe("");
});

test("a selected namespace is carried into the query", () => {
  selectedNamespace.value = "team-a";
  const { query } = mountAndRun();
  expect(query).toBe('{namespace="team-a"}');
});
