/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { hydrate, render } from "preact";
import { act } from "preact/test-utils";
import { renderToString } from "preact-render-to-string";
import { setAccessToken } from "@/lib/api.ts";

/**
 * Render-level coverage for the resource detail page.
 *
 * Hydration: the server render and the first browser render must have the
 * same element structure. Preact's hydration adopts the server's elements
 * positionally and does not re-apply their props. When the metrics rail was
 * gated on IS_BROWSER, the server emitted DetailShell's single-column body
 * while the browser rendered the two-column grid, so every element below it
 * was adopted one level off: the grid kept the content card's styles, and the
 * loaded tab ended up inside the loading spinner's centred flex box. The YAML
 * editor in that box sized itself to whichever lines CodeMirror had rendered,
 * so its width changed as the page scrolled.
 *
 * YAML apply: the page sends the edited manifest to /yaml/apply, which
 * answers 200 even when the server-side apply rejects a document and reports
 * the failure in the per-document results. The page must show that as an
 * error and keep the edit, never as a success.
 */

const env = { browser: false };
const mockIsBrowser = () =>
  mock.module("../lib/is-browser.ts", () => ({ IS_BROWSER: env.browser }));
mockIsBrowser();

// The real rail is SSR-safe in the same way: nothing on the server, a card in
// the browser.
mock.module("./MetricsRail.tsx", () => ({
  default: () => (env.browser ? <aside data-testid="rail" /> : null),
}));
mock.module("../lib/ws.ts", () => ({
  EVENT_DELETED: "DELETED",
  EVENT_MODIFIED: "MODIFIED",
  EVENT_RESYNC: "RESYNC",
  subscribe: () => () => {},
}));
// CodeMirror needs layout a test DOM does not have; a textarea honours the
// same value/onChange/readOnly contract.
mock.module("../components/ui/CodeMirrorEditor.tsx", () => ({
  CodeMirrorEditor: (props: {
    value: string;
    onChange?: (v: string) => void;
    readOnly?: boolean;
  }) => (
    <textarea
      data-testid="yaml"
      value={props.value}
      readOnly={props.readOnly}
      onInput={(e) =>
        props.onChange?.((e.currentTarget as HTMLTextAreaElement).value)
      }
    />
  ),
}));

const { default: ResourceDetail } = await import("./ResourceDetail.tsx");

const PROPS = {
  kind: "deployments",
  title: "Deployment",
  namespace: "default",
  name: "web",
};

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
});

afterAll(() => {
  mock.module("../lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  if (GlobalRegistrator.isRegistered) GlobalRegistrator.unregister();
});

function enterBrowser() {
  if (!GlobalRegistrator.isRegistered) {
    GlobalRegistrator.register({
      url: "http://localhost/workloads/deployments/default/web",
    });
  }
  env.browser = true;
  mockIsBrowser();
}

function stubFetch(handler: (url: string, init?: RequestInit) => unknown) {
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const data = handler(String(input), init);
    if (data === undefined) return new Promise(() => {});
    return Promise.resolve(
      new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
}

const flush = () =>
  act(async () => {
    for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
  });

function button(root: HTMLElement, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === label,
  );
  if (!found) throw new Error(`no "${label}" button`);
  return found;
}

test("hydration keeps the rail grid and the content card on their own elements", () => {
  env.browser = false;
  mockIsBrowser();
  const ssr = renderToString(<ResourceDetail {...PROPS} />);

  enterBrowser();
  // Keep the page in its loading state: nothing the effects ask for answers.
  stubFetch(() => undefined);

  host = document.createElement("div");
  host.innerHTML = ssr;
  document.body.appendChild(host);
  act(() => hydrate(<ResourceDetail {...PROPS} />, host as HTMLElement));

  const divs = [...host.querySelectorAll("div")];
  const grid = divs.find((d) =>
    (d.getAttribute("style") ?? "").includes("grid-template-columns"),
  );
  expect(grid).toBeDefined();

  // The content card sits in the grid's first column, not in the grid's own
  // element.
  const card = divs.find((d) =>
    (d.getAttribute("style") ?? "").includes("var(--bg-surface)"),
  );
  expect(card).toBeDefined();
  expect(card).not.toBe(grid);
  expect(card?.parentElement?.parentElement).toBe(grid ?? null);
  expect(grid?.querySelector("[data-testid=rail]")).not.toBeNull();
});

// A Deployment as the GET now serves it: with apiVersion and kind, and with
// the managedFields the API server always attaches.
const DEPLOYMENT = {
  apiVersion: "apps/v1",
  kind: "Deployment",
  metadata: {
    name: "web",
    namespace: "default",
    uid: "uid-1",
    resourceVersion: "7",
    managedFields: [{ manager: "kubectl", operation: "Update" }],
  },
  spec: { replicas: 2 },
};

function applyResponse(failed: boolean) {
  return {
    results: [
      {
        index: 0,
        kind: "Deployment",
        name: "web",
        namespace: "default",
        action: failed ? "failed" : "configured",
        ...(failed
          ? { error: "field ownership conflict: spec.replicas. Use force." }
          : {}),
      },
    ],
    summary: {
      total: 1,
      created: 0,
      configured: failed ? 0 : 1,
      unchanged: 0,
      failed: failed ? 1 : 0,
    },
  };
}

/** Opens the YAML tab, edits replicas, and presses Apply. */
async function editAndApply(failed: boolean) {
  enterBrowser();
  setAccessToken("test-token");
  location.hash = "#yaml";
  const applied: string[] = [];
  stubFetch((url, init) => {
    if (url.includes("/v1/yaml/apply")) {
      applied.push(String(init?.body ?? ""));
      return applyResponse(failed);
    }
    if (url.includes("/v1/resources/deployments/default/web")) {
      return DEPLOYMENT;
    }
    return [];
  });

  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<ResourceDetail {...PROPS} />, host as HTMLElement));
  await flush();

  act(() => button(host as HTMLElement, "Edit").click());
  await flush();

  const editor = host.querySelector(
    "[data-testid=yaml]",
  ) as HTMLTextAreaElement;
  act(() => {
    editor.value = editor.value.replace("replicas: 2", "replicas: 3");
    editor.dispatchEvent(new Event("input", { bubbles: true }));
  });
  act(() => button(host as HTMLElement, "Apply").click());
  await flush();

  return { root: host, applied };
}

test("Apply sends a manifest with apiVersion and kind and without managedFields", async () => {
  const { applied } = await editAndApply(false);

  expect(applied).toHaveLength(1);
  expect(applied[0]).toContain("apiVersion: apps/v1");
  expect(applied[0]).toContain("kind: Deployment");
  expect(applied[0]).toContain("replicas: 3");
  expect(applied[0]).not.toContain("managedFields");
});

test("a 200 apply that reports a failed document shows the error and keeps the edit", async () => {
  const { root } = await editAndApply(true);

  expect(root.textContent).toContain("Apply failed");
  expect(root.textContent).toContain("field ownership conflict");
  expect(root.textContent).not.toContain("Changes applied successfully");
  // Still in edit mode, with the operator's change intact.
  expect(() => button(root, "Apply")).not.toThrow();
  const editor = root.querySelector(
    "[data-testid=yaml]",
  ) as HTMLTextAreaElement;
  expect(editor.value).toContain("replicas: 3");
});

test("a successful apply shows the success banner and leaves edit mode", async () => {
  const { root } = await editAndApply(false);

  expect(root.textContent).toContain("Changes applied successfully");
  expect(() => button(root, "Edit")).not.toThrow();
});
