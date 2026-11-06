/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * On a wizard's review step the footer's primary button is the Apply, and
 * after a clean apply it leads to the new resource. The step reports a clean
 * apply so the wizard can drop its unsaved-changes guard.
 */

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

const { default: WizardShell } = await import("./WizardShell.tsx");
const { WizardReviewStep } = await import(
  "@/src/components/wizard/WizardReviewStep.tsx"
);

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

function stubApply(failed: number) {
  originalFetch = globalThis.fetch;
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(
        JSON.stringify({
          data: {
            results: [
              {
                kind: "ConfigMap",
                name: "cm",
                namespace: "team-a",
                action: failed ? "failed" : "created",
                error: failed ? "denied" : undefined,
              },
            ],
            summary: {
              created: failed ? 0 : 1,
              configured: 0,
              unchanged: 0,
              failed,
            },
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    )) as unknown as typeof globalThis.fetch;
}

function mount(opts: { onApplied: () => void; onNext: () => void }) {
  setAccessToken("t");
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <WizardShell
        title="Create ConfigMap"
        steps={[{ label: "Basics" }, { label: "Review" }]}
        current={1}
        onStep={() => {}}
        onCancel={() => {}}
        onBack={() => {}}
        onNext={opts.onNext}
        nextLabel="Close"
      >
        <WizardReviewStep
          yaml={"kind: ConfigMap\nmetadata:\n  name: cm\n"}
          onYamlChange={() => {}}
          loading={false}
          error={null}
          detailBasePath="/config/configmaps"
          onApplied={opts.onApplied}
        />
      </WizardShell>,
      host as HTMLElement,
    ),
  );
  return host as HTMLElement;
}

const buttons = (root: HTMLElement, label: string) =>
  [...root.querySelectorAll("button")].filter(
    (b) => b.textContent?.trim() === label,
  );

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

test("the review step's footer button is Apply, not Close", () => {
  const root = mount({ onApplied: () => {}, onNext: () => {} });
  expect(buttons(root, "Close")).toHaveLength(0);
  expect(buttons(root, "Apply")).toHaveLength(1);
});

test("a clean apply reports itself and links to the resource", async () => {
  stubApply(0);
  let applied = 0;
  const root = mount({ onApplied: () => applied++, onNext: () => {} });

  await act(async () => buttons(root, "Apply")[0].click());
  await flush();

  expect(applied).toBe(1);
  expect(root.textContent).toContain("Applied successfully");
  const link = [...root.querySelectorAll("a")].find(
    (a) => a.textContent?.trim() === "View resource",
  );
  expect(link?.getAttribute("href")).toBe("/config/configmaps/team-a/cm");
});

test("an apply with failures keeps the guard and offers Done", async () => {
  stubApply(1);
  let applied = 0;
  let next = 0;
  const root = mount({ onApplied: () => applied++, onNext: () => next++ });

  await act(async () => buttons(root, "Apply")[0].click());
  await flush();

  expect(applied).toBe(0);
  expect(root.textContent).toContain("Apply completed with errors");
  const done = buttons(root, "Done");
  expect(done).toHaveLength(1);
  act(() => done[0].click());
  expect(next).toBe(1);
});
