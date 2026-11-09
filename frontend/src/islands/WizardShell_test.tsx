/** @jsxImportSource react */
import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * On a wizard's review step the footer's primary button is the Apply, and
 * after a clean apply it leads to the new resource. The step reports a clean
 * apply so the wizard can drop its unsaved-changes guard.
 */

setupDom({ browser: true });

const { default: WizardShell } = await import("./WizardShell.tsx");
const { WizardReviewStep } = await import(
  "@/src/components/wizard/WizardReviewStep.tsx"
);

let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
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

const YAML = "kind: ConfigMap\nmetadata:\n  name: cm\n";

function mount(opts: {
  onNext: () => void;
  yaml?: string;
  maskValues?: (yaml: string) => string;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  setAccessToken("t");
  const ui = (yaml: string, withReview = true) => (
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
      {withReview ? (
        <WizardReviewStep
          yaml={yaml}
          onYamlChange={() => {}}
          loading={false}
          error={null}
          detailBasePath="/config/configmaps"
          maskValues={opts.maskValues}
          onDirtyChange={opts.onDirtyChange}
        />
      ) : (
        <p>plain step</p>
      )}
    </WizardShell>
  );
  const view = render(ui(opts.yaml ?? YAML));
  return Object.assign(view.container as HTMLElement, {
    rerenderYaml: (yaml: string) => view.rerender(ui(yaml)),
    clearStep: () => view.rerender(ui(YAML, false)),
  });
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
  const root = mount({ onNext: () => {} });
  expect(buttons(root, "Close")).toHaveLength(0);
  expect(buttons(root, "Apply")).toHaveLength(1);
});

test("a clean apply clears the unsaved-changes flag and links to the resource", async () => {
  stubApply(0);
  const dirtyChanges: boolean[] = [];
  const onDirtyChange = (d: boolean) => dirtyChanges.push(d);
  const root = mount({ onNext: () => {}, onDirtyChange });

  await act(async () => buttons(root, "Apply")[0].click());
  await flush();

  expect(dirtyChanges).toEqual([false]);
  expect(root.textContent).toContain("Applied successfully");
  const link = [...root.querySelectorAll("a")].find(
    (a) => a.textContent?.trim() === "View resource",
  );
  expect(link?.getAttribute("href")).toBe("/config/configmaps/team-a/cm");
});

test("an apply with failures keeps the guard and offers Done", async () => {
  stubApply(1);
  const dirtyChanges: boolean[] = [];
  const onDirtyChange = (d: boolean) => dirtyChanges.push(d);
  let next = 0;
  const root = mount({ onNext: () => next++, onDirtyChange });

  await act(async () => buttons(root, "Apply")[0].click());
  await flush();

  expect(dirtyChanges).toEqual([]);
  expect(root.textContent).toContain("Apply completed with errors");
  const done = buttons(root, "Done");
  expect(done).toHaveLength(1);
  act(() => done[0].click());
  expect(next).toBe(1);
});

test("a masked review applies the real YAML and can show the values", async () => {
  stubApply(0);
  const sent: string[] = [];
  const stubbed = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    sent.push(String(init?.body ?? ""));
    return stubbed(input, init);
  }) as typeof globalThis.fetch;
  const yaml = "kind: Secret\nstringData:\n  password: hunter2\n";
  const root = mount({
    onNext: () => {},
    yaml,
    maskValues: (y) => y.replace("hunter2", "***"),
  });

  expect(root.textContent).toContain("Values are hidden");
  const show = buttons(root, "Show values");
  expect(show).toHaveLength(1);
  act(() => show[0].click());
  expect(buttons(root, "Hide values")).toHaveLength(1);
  expect(root.textContent).not.toContain("Values are hidden");
  act(() => buttons(root, "Hide values")[0].click());

  await act(async () => buttons(root, "Apply")[0].click());
  await flush();
  expect(sent).toEqual([yaml]);
});

test("a review with no mask has no values toggle", () => {
  const root = mount({ onNext: () => {} });
  expect(buttons(root, "Show values")).toHaveLength(0);
});

test("registering replaces the footer's own button, clearing restores it", () => {
  let next = 0;
  const root = mount({ onNext: () => next++ });
  expect(buttons(root, "Close")).toHaveLength(0);
  expect(buttons(root, "Apply")).toHaveLength(1);

  act(() => root.clearStep());
  expect(buttons(root, "Apply")).toHaveLength(0);
  expect(buttons(root, "Close")).toHaveLength(1);
  act(() => buttons(root, "Close")[0].click());
  expect(next).toBe(1);
});

test("the footer button runs the latest handler, so Apply sends the current YAML", async () => {
  stubApply(0);
  const sent: string[] = [];
  const stubbed = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    sent.push(String(init?.body ?? ""));
    return stubbed(input, init);
  }) as typeof globalThis.fetch;
  const root = mount({ onNext: () => {} });
  const edited = "kind: ConfigMap\nmetadata:\n  name: edited\n";

  act(() => root.rerenderYaml(edited));
  expect(buttons(root, "Apply")).toHaveLength(1);
  await act(async () => buttons(root, "Apply")[0].click());
  await flush();
  expect(sent).toEqual([edited]);
});

test("an empty YAML disables the footer button until YAML arrives", () => {
  const root = mount({ onNext: () => {}, yaml: "  " });
  const apply = buttons(root, "Apply")[0] as HTMLButtonElement;
  expect(apply.disabled).toBe(true);
  act(() => root.rerenderYaml(YAML));
  expect((buttons(root, "Apply")[0] as HTMLButtonElement).disabled).toBe(false);
});
