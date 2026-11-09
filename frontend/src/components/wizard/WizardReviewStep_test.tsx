/** @jsxImportSource react */
import { afterEach, expect, mock, test } from "bun:test";
import { signal } from "@preact/signals-core";
import type { ReactNode } from "react";
import { setAccessToken } from "@/lib/api.ts";
import type { WizardPrimaryAction } from "@/lib/wizard-primary-action.ts";
import {
  act,
  body,
  fireEvent,
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
let appliedBody: unknown = null;
let release: (() => void) | null = null;

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  applyCalls = 0;
  appliedBody = null;
  release = null;
});

function stubApply(failed: number, hold = false) {
  globalThis.fetch = (async (_input: unknown, init?: RequestInit) => {
    applyCalls++;
    appliedBody = init?.body;
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
  opts: {
    yaml?: string;
    loading?: boolean;
    onDirtyChange?: (dirty: boolean) => void;
    maskValues?: (yaml: string) => string;
    onYamlChange?: (yaml: string) => void;
  } = {},
) {
  setAccessToken("t");
  const primary = signal<WizardPrimaryAction | null>(null);
  const ui = (yaml: string): ReactNode => (
    <WizardPrimaryActionContext.Provider value={primary}>
      <WizardReviewStep
        yaml={yaml}
        onYamlChange={opts.onYamlChange ?? (() => {})}
        loading={opts.loading ?? false}
        error={null}
        detailBasePath="/config/configmaps"
        onDirtyChange={opts.onDirtyChange}
        maskValues={opts.maskValues}
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
  const dirtyChanges: boolean[] = [];
  const onDirtyChange = (d: boolean) => dirtyChanges.push(d);
  const { primary } = mount({ onDirtyChange });
  expect(primary.value?.label).toBe("Apply");
  expect(primary.value?.disabled).toBe(false);

  await act(async () => {
    primary.value?.onClick?.();
  });
  await waitFor(() =>
    expect(document.body.textContent).toContain("Applied successfully"),
  );
  expect(dirtyChanges).toEqual([false]);
  expect(wizardBusy.value).toBe(false);
  expect(primary.value?.label).toBe("View resource");
  expect(primary.value?.href).toBe("/config/configmaps/team-a/cm");
  expect(primary.value?.onClick).toBeUndefined();
  body().getByText("cm");
});

test("a partial failure shows failed rows, keeps dirty and offers Done", async () => {
  stubApply(1);
  const dirtyChanges: boolean[] = [];
  const onDirtyChange = (d: boolean) => dirtyChanges.push(d);
  const { primary } = mount({ onDirtyChange });

  await act(async () => {
    primary.value?.onClick?.();
  });
  await waitFor(() =>
    expect(document.body.textContent).toContain("Apply completed with errors"),
  );
  expect(document.body.textContent).not.toContain("Applied successfully");
  expect(document.body.textContent).toContain("(denied)");
  expect(dirtyChanges).toEqual([]);
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

const SECRET_YAML = "kind: Secret\nstringData:\n  password: hunter2\n";
const MASKED_YAML = "kind: Secret\nstringData:\n  password: '***'\n";
const maskSecret = (y: string) => y.replace("hunter2", "'***'");

function editor(): HTMLTextAreaElement {
  const ta = document.querySelector("textarea");
  if (!ta) throw new Error("no editor textarea");
  return ta;
}

test("while values are masked the editor is read-only and shows the masked text", async () => {
  mount({ yaml: SECRET_YAML, maskValues: maskSecret });
  await waitFor(() => expect(editor().value).toBe(MASKED_YAML));
  expect(editor().readOnly).toBe(true);
  expect(editor().value).not.toContain("hunter2");
  body().getByRole("button", { name: "Show values" });
});

test("toggling reveal and hide never calls onYamlChange and apply sends the real YAML", async () => {
  stubApply(0);
  const onYamlChange = mock((_yaml: string) => {});
  const { primary } = mount({
    yaml: SECRET_YAML,
    maskValues: maskSecret,
    onYamlChange,
  });
  await waitFor(() => expect(editor().value).toBe(MASKED_YAML));

  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Show values" }));
  });
  await waitFor(() => expect(editor().value).toBe(SECRET_YAML));
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Hide values" }));
  });
  await waitFor(() => expect(editor().value).toBe(MASKED_YAML));
  expect(onYamlChange).not.toHaveBeenCalled();

  await act(async () => {
    primary.value?.onClick?.();
  });
  await waitFor(() => expect(applyCalls).toBe(1));
  expect(appliedBody).toBe(SECRET_YAML);
  expect(appliedBody).not.toContain("'***'");
});

test("after revealing, the editor shows the real values and is editable", async () => {
  const onYamlChange = mock((_yaml: string) => {});
  mount({ yaml: SECRET_YAML, maskValues: maskSecret, onYamlChange });
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Show values" }));
  });
  await waitFor(() => expect(editor().value).toBe(SECRET_YAML));
  expect(editor().readOnly).toBe(false);

  const edited = "kind: Secret\nstringData:\n  password: changed\n";
  await act(async () => {
    fireEvent.change(editor(), { target: { value: edited } });
  });
  expect(onYamlChange).toHaveBeenCalledTimes(1);
  expect(onYamlChange).toHaveBeenCalledWith(edited);
});
