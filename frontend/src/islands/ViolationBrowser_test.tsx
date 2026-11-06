/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";

/**
 * The violations list had two namespace filters: the global picker, applied
 * silently, and the page's own select, which kept reading "All Namespaces".
 * With the global picker on a namespace the page showed "0 of 53
 * violations" under a select that claimed everything was shown. There is now
 * one filter: the select shows and sets the global namespace.
 *
 * fetch answers with three violations; WebSocket is inert. IS_BROWSER is
 * stubbed to true and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/security/violations" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const violation = (namespace: string | undefined, name: string) => ({
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
const VIOLATIONS = [
  violation("team-a", "a-1"),
  violation("team-b", "b-1"),
  violation(undefined, "cluster-1"),
];

const realFetch = globalThis.fetch;
globalThis.fetch = (async () =>
  new Response(JSON.stringify({ data: VIOLATIONS }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })) as unknown as typeof fetch;

class InertSocket {
  onopen = null;
  onmessage = null;
  onclose = null;
  onerror = null;
  readyState = 0;
  send() {}
  close() {}
}
const realWebSocket = globalThis.WebSocket;
globalThis.WebSocket = InertSocket as unknown as typeof WebSocket;

const { default: ViolationBrowser } = await import("./ViolationBrowser.tsx");
const { selectedNamespace } = await import("@/src/lib/namespace.ts");

afterAll(() => {
  globalThis.fetch = realFetch;
  globalThis.WebSocket = realWebSocket;
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
  globalThis.history.replaceState({}, "", "/security/violations");
});

async function mount(): Promise<HTMLElement> {
  host = document.createElement("div");
  document.body.appendChild(host);
  await act(async () => {
    render(<ViolationBrowser />, host as HTMLElement);
  });
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
  return host;
}

const namespaceSelect = (el: HTMLElement) =>
  [...el.querySelectorAll("select")].find((s) =>
    s.textContent?.includes("All Namespaces"),
  ) as HTMLSelectElement;

test("the namespace select shows the global namespace it filters by", async () => {
  selectedNamespace.value = "team-a";
  const el = await mount();
  expect(namespaceSelect(el).value).toBe("team-a");
  // team-a's violation plus the cluster-scoped one, which every namespace shows.
  expect(el.textContent).toContain("2 of 3 violations");
});

test("choosing All Namespaces in the select clears the global filter", async () => {
  selectedNamespace.value = "team-a";
  const el = await mount();
  const select = namespaceSelect(el);
  await act(async () => {
    select.value = "all";
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  expect(selectedNamespace.value).toBe("all");
  expect(el.textContent).toContain("3 of 3 violations");
});

test("a ?namespace= link selects that namespace", async () => {
  selectedNamespace.value = "all";
  globalThis.history.replaceState(
    {},
    "",
    "/security/violations?namespace=team-b",
  );
  const el = await mount();
  expect(selectedNamespace.value).toBe("team-b");
  expect(namespaceSelect(el).value).toBe("team-b");
  expect(el.textContent).toContain("2 of 3 violations");
});
