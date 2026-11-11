import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { act, type ReactNode } from "react";
import { createRoot, hydrateRoot } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { setAccessToken } from "@/lib/api.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

/**
 * Render-level coverage for the resource detail page.
 *
 * Hydration: the server render and the first browser render must have the
 * same element structure, because hydration adopts the server's elements
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

// mock.module replaces a module for every test file in the run, so keep the
// real ones and put them back in afterAll; a later file importing the ws
// store, the viewer or the editor must get the real module, not these stubs.
const realModules = {
  "./MetricsRail.tsx": { ...(await import("./MetricsRail.tsx")) },
  "../lib/ws.ts": { ...(await import("../lib/ws.ts")) },
  "../components/ui/CodeMirrorEditor.tsx": {
    ...(await import("../components/ui/CodeMirrorEditor.tsx")),
  },
  "./LogViewer.tsx": { ...(await import("./LogViewer.tsx")) },
  "./PodTerminal.tsx": { ...(await import("./PodTerminal.tsx")) },
};

const env = { browser: false };
const mockIsBrowser = () =>
  mock.module("../lib/is-browser.ts", () => ({ IS_BROWSER: env.browser }));
mockIsBrowser();

// The real rail is SSR-safe in the same way: nothing on the server or during
// hydration, a card once the browser has taken over (useIsClient).
mock.module("./MetricsRail.tsx", () => ({
  default: () => (useIsClient() ? <aside data-testid="rail" /> : null),
}));
mock.module("../lib/ws.ts", () => ({
  EVENT_DELETED: "DELETED",
  EVENT_MODIFIED: "MODIFIED",
  EVENT_RESYNC: "RESYNC",
  subscribe: () => () => {},
}));
// CodeMirror needs layout a test DOM does not have; a textarea honours the
// same value/onChange/readOnly contract.
function CodeMirrorEditorStub(props: {
  value: string;
  onChange?: (v: string) => void;
  readOnly?: boolean;
}) {
  return (
    <textarea
      data-testid="yaml"
      value={props.value}
      readOnly={props.readOnly}
      onInput={(e) =>
        props.onChange?.((e.currentTarget as HTMLTextAreaElement).value)
      }
    />
  );
}
mock.module("../components/ui/CodeMirrorEditor.tsx", () => ({
  CodeMirrorEditor: CodeMirrorEditorStub,
}));

// The pod tabs open sockets; a marker is enough to see which tab is active.
// The viewer streams the containers it mounts with, so record them.
const logViewerContainers: string[][] = [];
mock.module("./LogViewer.tsx", () => ({
  default: (props: { containers: string[] }) => {
    logViewerContainers.push(props.containers);
    return <div data-testid="log-viewer" />;
  },
}));
const terminalContainers: string[][] = [];
mock.module("./PodTerminal.tsx", () => ({
  default: (props: { containers: string[] }) => {
    terminalContainers.push(props.containers);
    return <div data-testid="pod-terminal" />;
  },
}));

const { default: ResourceDetail } = await import("./ResourceDetail.tsx");

const PROPS = {
  kind: "deployments",
  title: "Deployment",
  namespace: "default",
  name: "web",
};

let host: HTMLElement | null = null;
const roots = new WeakMap<HTMLElement, ReturnType<typeof createRoot>>();

function mount(el: HTMLElement, node: ReactNode) {
  const root = createRoot(el);
  roots.set(el, root);
  act(() => root.render(node));
}

function unmount(el: HTMLElement) {
  const root = roots.get(el);
  if (!root) return;
  roots.delete(el);
  act(() => root.unmount());
}
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
  if (host) {
    unmount(host);
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
});

afterAll(() => {
  mock.module("../lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  for (const [path, real] of Object.entries(realModules)) {
    mock.module(path, () => real);
  }
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
  act(() => {
    roots.set(
      host as HTMLElement,
      hydrateRoot(host as HTMLElement, <ResourceDetail {...PROPS} />),
    );
  });

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
  mount(host, <ResourceDetail {...PROPS} />);
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

function tabLabels(root: HTMLElement): string[] {
  return [...root.querySelectorAll("[role=tab]")].map(
    (t) => t.textContent?.trim() ?? "",
  );
}

async function renderDetail(props: typeof PROPS, hash = "") {
  enterBrowser();
  setAccessToken("test-token");
  location.hash = hash;
  stubFetch((url) =>
    url.includes(`/v1/resources/${props.kind}/`)
      ? {
          apiVersion: "v1",
          kind: props.title,
          metadata: { name: props.name, namespace: props.namespace, uid: "u" },
          spec: { containers: [{ name: "app" }] },
        }
      : [],
  );
  host = document.createElement("div");
  document.body.appendChild(host);
  mount(host, <ResourceDetail {...props} />);
  await flush();
  return host;
}

test("a kind with metric queries has a Metrics tab; one without does not", async () => {
  const deployment = await renderDetail(PROPS);
  expect(tabLabels(deployment)).toContain("Metrics");
  unmount(deployment);

  const configMap = await renderDetail({
    kind: "configmaps",
    title: "ConfigMap",
    namespace: "default",
    name: "settings",
  });
  expect(tabLabels(configMap)).not.toContain("Metrics");
});

test("a #logs link opens a pod on its Logs tab", async () => {
  logViewerContainers.length = 0;
  const root = await renderDetail(
    { kind: "pods", title: "Pod", namespace: "default", name: "web-0" },
    "#logs",
  );
  const active = root.querySelector("[role=tab][aria-selected=true]");
  expect(active?.textContent?.trim()).toBe("Logs");
  expect(root.querySelector("[data-testid=log-viewer]")).not.toBeNull();
  // Never mounted with a placeholder before the pod loaded: the viewer
  // would stream a container named "default" that the pod does not have.
  expect(logViewerContainers.length).toBeGreaterThan(0);
  expect(logViewerContainers.every((c) => c.join() === "app")).toBe(true);
});

test("a #terminal link waits for the pod's containers before connecting", async () => {
  terminalContainers.length = 0;
  const root = await renderDetail(
    { kind: "pods", title: "Pod", namespace: "default", name: "web-0" },
    "#terminal",
  );
  const active = root.querySelector("[role=tab][aria-selected=true]");
  expect(active?.textContent?.trim()).toBe("Terminal");
  expect(root.querySelector("[data-testid=pod-terminal]")).not.toBeNull();
  expect(terminalContainers.length).toBeGreaterThan(0);
  expect(terminalContainers.every((c) => c.join() === "app")).toBe(true);
});

/**
 * Wraps the stubbed fetch so requests whose URL matches are held until the
 * test releases them. Counts the held requests.
 */
function holdRequests(match: string) {
  const inner = globalThis.fetch;
  const held: Array<() => void> = [];
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    if (!String(input).includes(match)) return inner(input, init);
    return new Promise<Response>((resolve) => {
      held.push(() =>
        resolve(
          new Response(JSON.stringify({ data: applyResponse(false) }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );
    });
  }) as typeof globalThis.fetch;
  return held;
}

test("a double-clicked Apply sends one apply request", async () => {
  enterBrowser();
  setAccessToken("test-token");
  location.hash = "#yaml";
  stubFetch((url) =>
    url.includes("/v1/resources/deployments/default/web") ? DEPLOYMENT : [],
  );
  const held = holdRequests("/v1/yaml/apply");

  host = document.createElement("div");
  document.body.appendChild(host);
  mount(host, <ResourceDetail {...PROPS} />);
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

  // Both clicks land before the first request resolves and before any
  // re-render could disable the button.
  const apply = button(host, "Apply");
  act(() => {
    apply.click();
    apply.click();
  });
  await flush();
  expect(held).toHaveLength(1);

  // Releasing the held request must not produce a second one.
  held[0]();
  await flush();
  expect(held).toHaveLength(1);
});

test("a double-clicked confirm sends one restart request", async () => {
  enterBrowser();
  setAccessToken("test-token");
  location.hash = "";
  stubFetch((url) =>
    url.includes("/v1/resources/deployments/default/web") ? DEPLOYMENT : [],
  );
  const held = holdRequests("/web/restart");

  host = document.createElement("div");
  document.body.appendChild(host);
  mount(host, <ResourceDetail {...PROPS} />);
  await flush();
  act(() => button(host as HTMLElement, "Restart").click());
  await flush();

  const dialog = host.querySelector("[role=dialog]") as HTMLElement;
  expect(dialog).not.toBeNull();
  const confirm = button(dialog, "Restart");
  act(() => {
    confirm.click();
    confirm.click();
  });
  await flush();
  expect(held).toHaveLength(1);

  held[0]();
  await flush();
  expect(held).toHaveLength(1);
});
