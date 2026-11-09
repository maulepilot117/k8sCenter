/** @jsxImportSource react */
import { afterEach, beforeEach, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * useNamespaces starts from "default", replaces it with the cluster's
 * namespaces sorted by name, and lets a wizard add a namespace it just
 * created so the select can show it.
 */
setupDom({ browser: true });

const { useNamespaces } = await import("./use-namespaces.ts");
const { setAccessToken } = await import("@/lib/api.ts");

const realFetch = globalThis.fetch;
let respond: () => Response;

beforeEach(() => {
  setAccessToken("test-token");
  globalThis.fetch = (async () => respond()) as unknown as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
  setAccessToken(null);
});

const namespaceList = (...names: string[]) =>
  new Response(
    JSON.stringify({ data: names.map((name) => ({ metadata: { name } })) }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );

let latest: ReturnType<typeof useNamespaces> | null = null;
function Probe() {
  latest = useNamespaces();
  return <span>{latest[0].join(",")}</span>;
}

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

test("starts from default, then shows the cluster's namespaces sorted", async () => {
  respond = () => namespaceList("team-b", "default", "team-a");
  const view = render(<Probe />);
  expect(view.container.textContent).toBe("default");
  await settle();
  expect(view.container.textContent).toBe("default,team-a,team-b");
});

test("a created namespace joins the list in order", async () => {
  respond = () => namespaceList("default", "team-c");
  const view = render(<Probe />);
  await settle();
  await act(async () => {
    latest?.[1]((prev) => [...prev, "team-b"].sort());
  });
  expect(view.container.textContent).toBe("default,team-b,team-c");
});

test("a failed read keeps default", async () => {
  respond = () =>
    new Response(JSON.stringify({ error: { code: 500, message: "boom" } }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  const view = render(<Probe />);
  await settle();
  expect(view.container.textContent).toBe("default");
});
