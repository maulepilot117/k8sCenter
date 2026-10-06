/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * The diagnostics API refuses a remote cluster selection with 501
 * unsupported_platform (#532): the target, its pods and the blast radius are
 * resolved from the local cluster's informers. The workspace must say so
 * plainly rather than show the refusal as an error.
 *
 * The island only runs its mount effect when `IS_BROWSER` is true, and that
 * constant is fixed when `is-browser.ts` is first evaluated, which under
 * `bun test` is without a DOM. As in source-coverage-pages_test.tsx, this
 * file stubs it to true for its own island and puts the real value back
 * afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: DiagnosticWorkspace } = await import(
  "./DiagnosticWorkspace.tsx"
);

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
  globalThis.history.replaceState(null, "", "/");
});

function stubFetch(status: number, body: unknown) {
  originalFetch = globalThis.fetch;
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    )) as unknown as typeof globalThis.fetch;
}

/** Mounts the workspace with a full target in the URL, so it runs at once. */
async function mount(status: number, body: unknown) {
  stubFetch(status, body);
  setAccessToken("test-token");
  globalThis.history.replaceState(
    null,
    "",
    "/?namespace=team-a&kind=Pod&name=web",
  );
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<DiagnosticWorkspace />, host as HTMLElement));
  // The fetch, the body read and the state update each take a turn of the
  // event loop; settle until the running state is gone (bounded).
  for (let i = 0; i < 20; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    if (!host.textContent?.includes("Running diagnostics")) break;
  }
  return host;
}

const NOTICE = "not available for remote clusters";
const REFUSAL = "resource diagnostics are available for the local cluster only";

test("a remote refusal renders the unsupported notice, not an error", async () => {
  const root = await mount(501, {
    error: { code: 501, message: REFUSAL, reason: "unsupported_platform" },
  });
  expect(
    root.querySelector('[data-diagnostics-state="remote-unsupported"]'),
  ).not.toBeNull();
  expect(root.textContent).toContain(NOTICE);
  expect(root.textContent).not.toContain(REFUSAL);
});

test("any other failure still renders the error message", async () => {
  const root = await mount(500, {
    error: { code: 500, message: "failed to resolve resource" },
  });
  expect(root.textContent).toContain("failed to resolve resource");
  expect(root.textContent).not.toContain(NOTICE);
});

test("a local result renders without the remote notice", async () => {
  const root = await mount(200, {
    data: {
      target: { kind: "Pod", name: "web", namespace: "team-a" },
      results: [],
      blastRadius: { directlyAffected: [], potentiallyAffected: [] },
    },
  });
  expect(root.textContent).not.toContain(NOTICE);
  expect(
    root.querySelector('[data-diagnostics-state="remote-unsupported"]'),
  ).toBeNull();
  expect(root.textContent).not.toContain("Select a resource to investigate");
});

test("a local result offers capture to an incident beside Re-scan", async () => {
  const root = await mount(200, {
    data: {
      target: { kind: "Pod", name: "web", namespace: "team-a" },
      results: [],
      blastRadius: { directlyAffected: [], potentiallyAffected: [] },
    },
  });
  const capture = root.querySelector('[data-testid="capture-to-incident"]');
  expect(capture).not.toBeNull();
  expect(capture?.getAttribute("aria-disabled")).toBe("false");
  const banner = capture?.parentElement?.parentElement;
  expect(banner?.textContent).toContain("Re-scan");
});
