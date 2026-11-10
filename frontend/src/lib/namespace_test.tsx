/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { useState } from "react";
import { hydrate } from "@/src/test-utils/hydrate.tsx";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

// Bun's own fetch, captured before happy-dom replaces it.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const {
  DEFAULT_NAMESPACE,
  selectedNamespace,
  selectedNamespaceOrDefault,
  useAdoptSelectedNamespace,
} = await import("./namespace.ts");
const { default: ConfigMapWizard } = await import(
  "@/src/islands/ConfigMapWizard.tsx"
);

beforeEach(() => {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ data: [] }), {
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof fetch;
});

afterEach(() => {
  selectedNamespace.value = "all";
});

afterAll(() => {
  globalThis.fetch = bunFetch;
});

function Form({ enabled = true }: { enabled?: boolean }) {
  const [namespace, setNamespace] = useState(DEFAULT_NAMESPACE);
  useAdoptSelectedNamespace(setNamespace, enabled);
  return <output>{namespace}</output>;
}

test("a form takes the picker's namespace once mounted", () => {
  selectedNamespace.value = "team-a";
  const view = render(<Form />);
  expect(view.container.textContent).toBe("team-a");
});

test("a form stays on the default namespace when the picker shows all, or adoption is off", () => {
  expect(render(<Form />).container.textContent).toBe(DEFAULT_NAMESPACE);
  selectedNamespace.value = "team-a";
  expect(render(<Form enabled={false} />).container.textContent).toBe(
    DEFAULT_NAMESPACE,
  );
});

test("a later picker change does not overwrite the form", async () => {
  selectedNamespace.value = "team-a";
  const view = render(<Form />);
  await act(async () => {
    selectedNamespace.value = "team-b";
  });
  expect(view.container.textContent).toBe("team-a");
});

test("selectedNamespaceOrDefault maps all to the default namespace", () => {
  expect(selectedNamespaceOrDefault()).toBe(DEFAULT_NAMESPACE);
  selectedNamespace.value = "team-a";
  expect(selectedNamespaceOrDefault()).toBe("team-a");
});

test("a create wizard hydrates without an error when the browser restored a namespace", async () => {
  const { html, container, errors, done } = await hydrate(
    () => <ConfigMapWizard onClose={() => {}} />,
    {
      restore: () => {
        selectedNamespace.value = "team-a";
      },
    },
  );
  expect(html).toContain(`namespace ${DEFAULT_NAMESPACE}`);
  expect(errors).toEqual([]);
  expect(container.textContent).toContain("namespace team-a");
  done();
});
