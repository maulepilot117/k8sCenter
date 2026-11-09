/** @jsxImportSource react */
import { afterAll, afterEach, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The filter bar starts from the global namespace picker. The picker's
 * "all namespaces" value is the sentinel string "all", which is not a
 * namespace: copying it in queried `{namespace="all"}` and found nothing.
 * With no namespace and no other filter the selector must still match
 * something, because Loki rejects an empty `{}`.
 *
 * Fetch answers every label lookup with an empty list and is put back
 * afterwards.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

globalThis.fetch = (async () =>
  new Response(JSON.stringify({ data: [] }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })) as unknown as typeof fetch;

const { default: LogFilterBar } = await import("./LogFilterBar.tsx");
const { selectedNamespace } = await import("@/src/lib/namespace.ts");

afterAll(() => {
  globalThis.fetch = bunFetch;
});

afterEach(() => {
  selectedNamespace.value = "all";
});

function mountAndRun(): { query: string; select: HTMLSelectElement } {
  const queries: string[] = [];
  const { container } = render(
    <LogFilterBar
      onSearch={(q) => queries.push(q)}
      onLiveTail={() => {}}
      onStopTail={() => {}}
      isTailing={false}
      loading={false}
    />,
  );
  const run = [...container.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === "Run",
  );
  if (!run) throw new Error("Run button not rendered");
  act(() => run.click());
  return {
    query: queries[0] ?? "",
    select: container.querySelector("select") as HTMLSelectElement,
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
