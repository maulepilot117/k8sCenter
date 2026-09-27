/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import { switchCluster } from "@/src/lib/cluster.ts";

/**
 * Render-level coverage for the template editor's Validate outcome. The page
 * is mounted into a happy-dom document with fetch stubbed, so what is asserted
 * is what an operator would see after pressing Validate.
 *
 * The editor island loads Monaco from a CDN, which a test cannot do; it is
 * replaced by a plain textarea that honours the same value/onChange contract.
 */
mock.module("./YamlEditor.tsx", () => ({
  default: (props: { value: string; onChange?: (v: string) => void }) => (
    <textarea
      data-testid="yaml"
      value={props.value}
      onInput={(e) =>
        props.onChange?.((e.currentTarget as HTMLTextAreaElement).value)
      }
    />
  ),
}));

const { default: SecretStoreFromTemplateEditor } = await import(
  "./SecretStoreFromTemplateEditor.tsx"
);

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

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

function stubValidate(payload: unknown) {
  const urls: string[] = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request) => {
    urls.push(String(input));
    return Promise.resolve(
      new Response(JSON.stringify({ data: payload }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
  return urls;
}

function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <SecretStoreFromTemplateEditor provider="akeyless" />,
      host as HTMLElement,
    ),
  );
  return host;
}

function button(root: HTMLElement, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === label,
  );
  if (!found) throw new Error(`no "${label}" button`);
  return found;
}

async function click(el: HTMLElement) {
  await act(async () => {
    el.click();
    await new Promise((r) => setTimeout(r, 0));
  });
}

function validateBody(valid: boolean) {
  return {
    documents: [
      {
        index: 0,
        kind: "SecretStore",
        name: "akeyless-store",
        namespace: "default",
        valid,
        ...(valid ? {} : { errors: [{ message: "spec.provider is invalid" }] }),
      },
    ],
    valid,
    targetCluster: "local",
    targetGeneration: "local",
  };
}

test("a passing Validate shows that every document is valid", async () => {
  switchCluster("local", "local");
  const urls = stubValidate(validateBody(true));
  const root = mount();

  await click(button(root, "Validate"));

  expect(urls).toEqual(["/api/v1/yaml/validate"]);
  expect(root.textContent).toContain("1 resource validated: all valid");
});

test("a failing Validate names the document and its error", async () => {
  switchCluster("local", "local");
  stubValidate(validateBody(false));
  const root = mount();

  await click(button(root, "Validate"));

  expect(root.textContent).toContain("1 with errors");
  expect(root.textContent).toContain("SecretStore/akeyless-store");
  expect(root.textContent).toContain("spec.provider is invalid");
});

test("editing after Validate clears the verdict it no longer describes", async () => {
  switchCluster("local", "local");
  stubValidate(validateBody(true));
  const root = mount();

  await click(button(root, "Validate"));
  expect(root.textContent).toContain("all valid");

  const editor = root.querySelector<HTMLTextAreaElement>("[data-testid=yaml]");
  if (!editor) throw new Error("editor stub not rendered");
  await act(async () => {
    editor.value = `${editor.value}\n# edited`;
    editor.dispatchEvent(new Event("input", { bubbles: true }));
  });

  expect(root.textContent).not.toContain("all valid");
});
