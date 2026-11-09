/** @jsxImportSource react */
import { afterAll, afterEach, expect, test } from "bun:test";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The violations list had two namespace filters: the global picker, applied
 * silently, and the page's own select, which kept reading "All Namespaces".
 * With the global picker on a namespace the page showed "0 of 53
 * violations" under a select that claimed everything was shown. There is now
 * one filter: the select shows and sets the global namespace.
 *
 * fetch answers with violations; WebSocket is inert.
 */
setupDom({ browser: true });

const violation = (
  namespace: string | undefined,
  name: string,
  severity = "medium",
  engine = "kyverno",
) => ({
  policy: "require-labels",
  severity,
  action: "audit",
  message: "label missing",
  namespace,
  kind: "Pod",
  name,
  engine,
  blocking: false,
});
const DEFAULT_VIOLATIONS = [
  violation("team-a", "a-1"),
  violation("team-b", "b-1"),
  violation(undefined, "cluster-1"),
];

let VIOLATIONS: ReturnType<typeof violation>[] = DEFAULT_VIOLATIONS;

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
});

afterEach(() => {
  selectedNamespace.value = "all";
  VIOLATIONS = DEFAULT_VIOLATIONS;
  globalThis.history.replaceState({}, "", "/security/violations");
});

async function mount(): Promise<Element> {
  const view = render(<ViolationBrowser />);
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
  return view.container;
}

const namespaceSelect = (el: Element) =>
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
    fireEvent.change(select, { target: { value: "all" } });
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

test("severity and engine filters narrow the list together and widen when one is cleared", async () => {
  VIOLATIONS = [
    violation("team-a", "crit-kyverno", "critical", "kyverno"),
    violation("team-a", "crit-gatekeeper", "critical", "gatekeeper"),
    violation("team-a", "low-kyverno", "low", "kyverno"),
    violation("team-a", "low-gatekeeper", "low", "gatekeeper"),
  ];
  const el = await mount();
  const [, severity, engine] = [...el.querySelectorAll("select")];
  const change = async (select: HTMLSelectElement, value: string) => {
    await act(async () => {
      fireEvent.change(select, { target: { value } });
    });
  };
  expect(el.textContent).toContain("4 of 4 violations");

  await change(severity as HTMLSelectElement, "critical");
  expect(el.textContent).toContain("2 of 4 violations");

  await change(engine as HTMLSelectElement, "gatekeeper");
  expect(el.textContent).toContain("1 of 4 violations");
  expect(el.textContent).toContain("Pod/crit-gatekeeper");
  expect(el.textContent).not.toContain("Pod/crit-kyverno");
  expect(el.textContent).not.toContain("Pod/low-gatekeeper");

  await change(severity as HTMLSelectElement, "all");
  expect(el.textContent).toContain("2 of 4 violations");
  expect(el.textContent).toContain("Pod/low-gatekeeper");
  expect(el.textContent).not.toContain("Pod/low-kyverno");
});

test("a list that shrinks under the reader's page and grows back stays on the clamped page", async () => {
  VIOLATIONS = [
    ...Array.from({ length: 150 }, (_, i) => violation("team-a", `a-${i}`)),
    violation("team-b", "b-1"),
  ];
  const el = await mount();
  const next = [...el.querySelectorAll("button")].find(
    (b) => b.textContent === "Next",
  ) as HTMLButtonElement;
  await act(async () => {
    fireEvent.click(next);
  });
  expect(el.textContent).toContain("Page 2 of 2");

  // The top bar narrows the namespace, so one page is left.
  await act(async () => {
    selectedNamespace.value = "team-b";
  });
  expect(el.textContent).toContain("1 of 151 violations");
  expect(el.textContent).toContain("Pod/b-1");

  await act(async () => {
    selectedNamespace.value = "all";
  });
  expect(el.textContent).toContain("Page 1 of 2");
});
