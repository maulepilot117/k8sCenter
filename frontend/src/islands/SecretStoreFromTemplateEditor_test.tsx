import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { switchCluster } from "@/src/lib/cluster.ts";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * Render-level coverage for the template editor's Validate outcome. The page
 * is mounted into a happy-dom document with fetch stubbed, so what is asserted
 * is what an operator would see after pressing Validate.
 *
 * The editor island loads Monaco from a CDN, which a test cannot do; it is
 * replaced by a plain textarea that honours the same value/onChange contract.
 */
const realYamlEditor = { ...(await import("./YamlEditor.tsx")) };
mock.module("./YamlEditor.tsx", () => ({
  default: (props: { value: string; onChange?: (v: string) => void }) => (
    <textarea
      data-testid="yaml"
      value={props.value}
      onChange={(e) => props.onChange?.(e.currentTarget.value)}
    />
  ),
}));

setupDom({ browser: true });

const { default: SecretStoreFromTemplateEditor } = await import(
  "./SecretStoreFromTemplateEditor.tsx"
);

afterAll(() => {
  mock.module("./YamlEditor.tsx", () => realYamlEditor);
});

let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
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
  return render(<SecretStoreFromTemplateEditor provider="akeyless" />)
    .container;
}

function button(root: Element, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === label,
  );
  if (!found) throw new Error(`no "${label}" button`);
  return found;
}

async function click(el: Element) {
  await act(async () => {
    (el as HTMLElement).click();
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
    fireEvent.input(editor, {
      target: {
        value: `${editor.value}
# edited`,
      },
    });
  });

  expect(root.textContent).not.toContain("all valid");
});

test("Validate then Apply sends both requests and shows the apply summary", async () => {
  switchCluster("local", "local");
  const urls: string[] = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    const data = url.endsWith("/yaml/apply")
      ? {
          summary: {
            total: 1,
            created: 0,
            configured: 0,
            unchanged: 0,
            failed: 1,
          },
          results: [
            {
              index: 0,
              kind: "SecretStore",
              name: "akeyless-store",
              namespace: "default",
              action: "failed",
              error: "placeholder rejected",
            },
          ],
        }
      : validateBody(true);
    return Promise.resolve(
      new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  }) as typeof globalThis.fetch;
  const root = mount();

  await click(button(root, "Validate"));
  await click(button(root, "Apply"));

  expect(urls).toEqual(["/api/v1/yaml/validate", "/api/v1/yaml/apply"]);
  expect(root.textContent).toContain("1 resource processed: 1 failed");
  expect(root.textContent).toContain("failed: placeholder rejected");
});
