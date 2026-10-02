/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * Compliance snapshots are recorded for the local cluster only, so under a
 * remote selection the history route answers 501 `remote_history_unsupported`
 * (#530). The trend card must say that, not report a failed read with a Retry
 * that can never succeed, and must keep any other failure an error.
 *
 * The island fetches only when `IS_BROWSER` is true, which is false under
 * `bun test`; this file stubs it for its own island and restores it after,
 * as source-coverage-pages_test.tsx does.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: ComplianceTrendChart } = await import(
  "./ComplianceTrendChart.tsx"
);

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
const originalFetch = globalThis.fetch;

beforeEach(() => setAccessToken("test-token"));

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  globalThis.fetch = originalFetch;
  setAccessToken(null);
});

function serve(status: number, body: unknown) {
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    )) as unknown as typeof globalThis.fetch;
}

async function mountUntil(text: string): Promise<HTMLElement> {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<ComplianceTrendChart />, host as HTMLElement));
  for (let i = 0; i < 50 && !host.textContent?.includes(text); i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
  return host;
}

test("a remote cluster's refusal says history is local-only, with no retry", async () => {
  serve(501, {
    error: {
      code: 501,
      message: "compliance history is recorded for the local cluster only",
      reason: "remote_history_unsupported",
    },
  });
  const root = await mountUntil("local cluster only");

  expect(
    root.querySelector('[data-testid="compliance-trend-remote-unsupported"]'),
  ).not.toBeNull();
  expect(root.textContent).not.toContain("Failed to load");
  expect(root.textContent).not.toContain("Retry");
});

test("any other failure is still a failed read with a retry", async () => {
  serve(500, { error: { code: 500, message: "boom" } });
  const root = await mountUntil("Failed to load");

  expect(root.textContent).toContain("Failed to load compliance trend data");
  expect(root.textContent).toContain("Retry");
  expect(
    root.querySelector('[data-testid="compliance-trend-remote-unsupported"]'),
  ).toBeNull();
});
