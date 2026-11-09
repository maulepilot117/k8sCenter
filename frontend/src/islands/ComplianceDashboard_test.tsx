/** @jsxImportSource react */
import { afterAll, afterEach, expect, test } from "bun:test";
import { act, render, settle, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The compliance page scores the namespace the global picker is on. A change
 * of the picker refetches the score with that namespace; the violations list
 * is fetched once and filtered on the page.
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

const { default: ComplianceDashboard } = await import(
  "./ComplianceDashboard.tsx"
);
const { selectedNamespace } = await import("@/src/lib/namespace.ts");

afterEach(() => {
  globalThis.fetch = realFetch;
  selectedNamespace.value = "all";
});
afterAll(() => {
  globalThis.WebSocket = realWebSocket;
});

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const violation = (namespace: string, name: string) => ({
  policy: "require-labels",
  severity: "medium",
  action: "audit",
  message: "label missing",
  namespace,
  kind: "Pod",
  name,
  engine: "kyverno",
  blocking: false,
});

function stubApi(urls: string[]) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    if (url.includes("/policies/violations")) {
      return json({
        data: [violation("team-a", "a-1"), violation("team-b", "b-1")],
      });
    }
    if (url.includes("/policies/compliance/history")) {
      return json({ data: [], gaps: [] });
    }
    const scoped = url.includes("namespace=team-a");
    return json({
      data: {
        scope: scoped ? "team-a" : "",
        score: scoped ? 50 : 90,
        pass: scoped ? 1 : 9,
        fail: scoped ? 1 : 1,
        warn: 0,
        total: scoped ? 2 : 10,
      },
    });
  }) as unknown as typeof globalThis.fetch;
}

test("the score follows the global namespace picker", async () => {
  const urls: string[] = [];
  stubApi(urls);
  const { container } = render(<ComplianceDashboard />);
  await settle();
  expect(container.textContent).toContain("9/10 passing");
  expect(container.textContent).toContain("Weighted cluster-wide compliance");

  await act(async () => {
    selectedNamespace.value = "team-a";
  });
  await settle();

  expect(urls.some((u) => u.includes("compliance?namespace=team-a"))).toBe(
    true,
  );
  expect(container.textContent).toContain("1/2 passing");
  expect(container.textContent).toContain('namespace "team-a"');
  // Only team-a's violation is previewed, and it came from the one fetch.
  expect(container.textContent).toContain("Pod/a-1");
  expect(container.textContent).not.toContain("Pod/b-1");
  expect(urls.filter((u) => u.includes("/policies/violations")).length).toBe(1);
});
