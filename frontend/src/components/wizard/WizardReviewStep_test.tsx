/** @jsxImportSource react */
import { afterEach, expect, mock, test } from "bun:test";
import { type Signal, signal } from "@preact/signals-core";
import type { ReactNode } from "react";
import { setAccessToken } from "@/lib/api.ts";
import type { WizardPrimaryAction } from "@/lib/wizard-primary-action.ts";
import {
  act,
  body,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// The preview's editor loads Monaco from a CDN. Make creating the editor
// fail, so the preview shows its textarea fallback whatever the network or
// another test file's mock of the same URL (mock.module is process-wide)
// would do.
mock.module(
  "https://esm.sh/monaco-editor@0.52.2/esm/vs/editor/editor.api.js",
  () => ({
    editor: {
      create() {
        throw new Error("Monaco is not available in tests");
      },
    },
  }),
);

// Bun's own fetch, captured before happy-dom replaces it.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { WizardReviewStep } = await import("./WizardReviewStep.tsx");
const { WizardPrimaryActionContext } = await import(
  "@/lib/wizard-primary-action.ts"
);
const { wizardBusy } = await import("@/lib/wizard-busy.ts");

const YAML = "kind: ConfigMap\nmetadata:\n  name: cm\n";

let applyCalls = 0;
let release: (() => void) | null = null;

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  applyCalls = 0;
  release = null;
});

function stubApply(failed: number, hold = false) {
  globalThis.fetch = (async () => {
    applyCalls++;
    if (hold) await new Promise<void>((r) => (release = r));
    return new Response(
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
    );
  }) as unknown as typeof globalThis.fetch;
}

function mount(
  opts: { yaml?: string; loading?: boolean; dirty?: Signal<boolean> } = {},
) {
  setAccessToken("t");
  const primary = signal<WizardPrimaryAction | null>(null);
  const ui = (yaml: string): ReactNode => (
    <WizardPrimaryActionContext.Provider value={primary}>
      <WizardReviewStep
        yaml={yaml}
        onYamlChange={() => {}}
        loading={opts.loading ?? false}
        error={null}
        detailBasePath="/config/configmaps"
        dirty={opts.dirty}
      />
    </WizardPrimaryActionContext.Provider>
  );
  const view = render(ui(opts.yaml ?? YAML));
  return { primary, view, ui };
}

test("the preview shows the YAML", async () => {
  mount();
  await waitFor(() => {
    const ta = document.querySelector("textarea");
    expect(ta?.value).toBe(YAML);
  });
});

test("a clean apply shows the result, clears dirty and links to the resource", async () => {
  stubApply(0);
  const dirty = signal(true);
  const { primary } = mount({ dirty });
  expect(primary.value?.label).toBe("Apply");
  expect(primary.value?.disabled).toBe(false);

  await act(async () => {
    primary.value?.onClick?.();
  });
  await waitFor(() =>
    expect(document.body.textContent).toContain("Applied successfully"),
  );
  expect(dirty.value).toBe(false);
  expect(wizardBusy.value).toBe(false);
  expect(primary.value?.label).toBe("View resource");
  expect(primary.value?.href).toBe("/config/configmaps/team-a/cm");
  expect(primary.value?.onClick).toBeUndefined();
  body().getByText("cm");
});

test("a partial failure shows failed rows, keeps dirty and offers Done", async () => {
  stubApply(1);
  const dirty = signal(true);
  const { primary } = mount({ dirty });

  await act(async () => {
    primary.value?.onClick?.();
  });
  await waitFor(() =>
    expect(document.body.textContent).toContain("Apply completed with errors"),
  );
  expect(document.body.textContent).not.toContain("Applied successfully");
  expect(document.body.textContent).toContain("(denied)");
  expect(dirty.value).toBe(true);
  expect(primary.value?.label).toBe("Done");
  expect(primary.value?.href).toBeUndefined();
});

test("a double click applies once and the action reflects the in-flight apply", async () => {
  stubApply(0, true);
  const { primary } = mount();

  await act(async () => {
    primary.value?.onClick?.();
    primary.value?.onClick?.();
  });
  expect(applyCalls).toBe(1);
  expect(wizardBusy.value).toBe(true);
  expect(primary.value?.loading).toBe(true);
  expect(primary.value?.disabled).toBe(true);

  await act(async () => {
    release?.();
  });
  await waitFor(() =>
    expect(document.body.textContent).toContain("Applied successfully"),
  );
  expect(applyCalls).toBe(1);
  expect(wizardBusy.value).toBe(false);
});

test("the action is disabled without YAML and follows later YAML", async () => {
  stubApply(0);
  const { primary, view, ui } = mount({ yaml: "  " });
  expect(primary.value?.disabled).toBe(true);
  view.rerender(ui(YAML));
  await waitFor(() => expect(primary.value?.disabled).toBe(false));

  await act(async () => {
    primary.value?.onClick?.();
  });
  await waitFor(() => expect(applyCalls).toBe(1));
});

test("the action is cleared and busy reset when the step unmounts", async () => {
  stubApply(0, true);
  const { primary, view } = mount();
  await act(async () => {
    primary.value?.onClick?.();
  });
  expect(wizardBusy.value).toBe(true);
  act(() => view.unmount());
  expect(primary.value).toBeNull();
  expect(wizardBusy.value).toBe(false);
});
