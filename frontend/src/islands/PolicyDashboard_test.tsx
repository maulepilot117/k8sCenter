/** @jsxImportSource react */
import { afterAll, afterEach, expect, test } from "bun:test";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The policy list narrows by engine, severity and mode together, and a
 * search box filters by text on top of them.
 */
setupDom({ browser: true });

const realFetch = globalThis.fetch;
const realWebSocket = globalThis.WebSocket;

class InertSocket {
  onopen = null;
  onmessage = null;
  onclose = null;
  onerror = null;
  readyState = 0;
  send() {}
  close() {}
}
globalThis.WebSocket = InertSocket as unknown as typeof WebSocket;

const { default: PolicyDashboard } = await import("./PolicyDashboard.tsx");

afterEach(() => {
  globalThis.fetch = realFetch;
});
afterAll(() => {
  globalThis.WebSocket = realWebSocket;
});

const policy = (
  name: string,
  engine: string,
  severity: string,
  blocking: boolean,
) => ({
  id: `${engine}::${name}`,
  name,
  kind: "ClusterPolicy",
  engine,
  severity,
  blocking,
  ready: true,
  violationCount: 0,
  targetKinds: ["Pod"],
});

const POLICIES = [
  policy("kv-high-enforce", "kyverno", "high", true),
  policy("kv-low-audit", "kyverno", "low", false),
  policy("gk-high-audit", "gatekeeper", "high", false),
];

function stubApi() {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    const data = url.includes("/policies/status")
      ? { detected: "both", lastChecked: "2026-11-09T00:00:00Z" }
      : POLICIES;
    return new Response(JSON.stringify({ data }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof globalThis.fetch;
}

async function mount() {
  stubApi();
  const view = render(<PolicyDashboard />);
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
  return view.container;
}

test("engine, severity and mode filters narrow the list together", async () => {
  const el = await mount();
  expect(el.textContent).toContain("3 of 3 policies");
  const [engine, severity, mode] = [...el.querySelectorAll("select")];
  const change = async (select: HTMLSelectElement, value: string) => {
    await act(async () => {
      fireEvent.change(select, { target: { value } });
    });
  };

  await change(severity, "high");
  expect(el.textContent).toContain("2 of 3 policies");

  await change(engine, "kyverno");
  expect(el.textContent).toContain("1 of 3 policies");
  expect(el.textContent).toContain("kv-high-enforce");
  expect(el.textContent).not.toContain("gk-high-audit");

  await change(mode, "audit");
  expect(el.textContent).toContain("0 of 3 policies");
  expect(el.textContent).toContain("No policies match your filters.");

  await change(mode, "all");
  await change(engine, "all");
  expect(el.textContent).toContain("2 of 3 policies");
  expect(el.textContent).toContain("gk-high-audit");
});

test("typing in the search box filters by name", async () => {
  const el = await mount();
  const input = el.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.change(input, { target: { value: "low" } });
  });
  expect(el.textContent).toContain("1 of 3 policies");
  expect(el.textContent).toContain("kv-low-audit");
});
