/** @jsxImportSource react */
import { expect, mock, test } from "bun:test";
import { signal } from "@preact/signals-core";
import type {
  ExternalSecretWizardForm,
  ExternalSecretWizardStoreOption,
} from "@/src/islands/ExternalSecretWizard.tsx";
import type {
  FormState,
  LimitConfig,
  QuotaConfig,
} from "@/src/islands/NamespaceLimitsWizard.tsx";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { LimitRangeValuesStep } = await import("./LimitRangeValuesStep.tsx");
const { NamespacePresetStep } = await import("./NamespacePresetStep.tsx");
const { QuotaValuesStep } = await import("./QuotaValuesStep.tsx");
const { ExternalSecretForm } = await import("./ExternalSecretForm.tsx");
const { default: StepRail } = await import("./StepRail.tsx");

const pair = (cpu: string, memory: string) => ({ cpu, memory });

function limits(): LimitConfig {
  return {
    containerDefault: pair("250m", "256Mi"),
    containerDefaultRequest: pair("100m", "128Mi"),
    containerMax: pair("", ""),
    containerMin: pair("", ""),
  };
}

function quota(): QuotaConfig {
  return { cpuHard: "8", memoryHard: "16Gi", podsHard: 20 };
}

test("LimitRangeValuesStep shows values, reports edits and shows errors pushed after mount", async () => {
  const errors = signal<Record<string, string>>({});
  const onUpdateResourcePair = mock();
  const onUpdateLimits = mock();
  const view = render(
    <LimitRangeValuesStep
      limits={limits()}
      errors={errors}
      onUpdateLimits={onUpdateLimits}
      onUpdateResourcePair={onUpdateResourcePair}
    />,
  );

  expect(view.getByText("Container Limits")).toBeTruthy();
  const cpu = view.getAllByPlaceholderText("e.g. 250m")[0] as HTMLInputElement;
  expect(cpu.value).toBe("250m");
  expect(view.queryByText("cpu is required")).toBeNull();

  fireEvent.input(cpu, { target: { value: "500m" } });
  expect(onUpdateResourcePair).toHaveBeenCalledWith(
    "containerDefault",
    "cpu",
    "500m",
  );

  await act(async () => {
    errors.value = {
      containerDefaultCpu: "cpu is required",
      containerDefaultMemory: "memory is required",
    };
  });
  expect(view.getByText("cpu is required")).toBeTruthy();
  expect(view.getByText("memory is required")).toBeTruthy();

  // Advanced options are hidden until toggled, and the toggle works both ways.
  expect(view.queryByText("Pod Limits (Optional)")).toBeNull();
  fireEvent.click(view.getByText(/Show advanced options/));
  expect(view.getByText("Pod Limits (Optional)")).toBeTruthy();
  fireEvent.input(view.getAllByPlaceholderText("No limit")[0], {
    target: { value: "4" },
  });
  expect(onUpdateLimits).toHaveBeenCalledWith("podMax", {
    cpu: "4",
    memory: "",
  });
  fireEvent.input(view.getByPlaceholderText("e.g. 1Gi"), {
    target: { value: "2Gi" },
  });
  expect(onUpdateLimits).toHaveBeenCalledWith("pvcMinStorage", "2Gi");
  fireEvent.click(view.getByText(/Hide advanced options/));
  expect(view.queryByText("Pod Limits (Optional)")).toBeNull();
});

test("QuotaValuesStep shows values, reports edits and shows errors pushed after mount", async () => {
  const errors = signal<Record<string, string>>({});
  const onUpdateQuota = mock();
  const view = render(
    <QuotaValuesStep
      quota={quota()}
      errors={errors}
      onUpdateQuota={onUpdateQuota}
    />,
  );

  expect(
    (view.getByPlaceholderText("e.g. 16Gi") as HTMLInputElement).value,
  ).toBe("16Gi");
  fireEvent.input(view.getByPlaceholderText("e.g. 8 or 8000m"), {
    target: { value: "12" },
  });
  expect(onUpdateQuota).toHaveBeenCalledWith("cpuHard", "12");

  await act(async () => {
    errors.value = { cpuHard: "bad cpu", podsHard: "too many pods" };
  });
  expect(view.getByText("bad cpu")).toBeTruthy();
  expect(view.getByText("too many pods")).toBeTruthy();

  fireEvent.click(view.getByText(/Show advanced options/));
  fireEvent.input(view.getByPlaceholderText("e.g. 1 (nvidia.com/gpu)"), {
    target: { value: "2" },
  });
  expect(onUpdateQuota).toHaveBeenCalledWith("gpuHard", "2");
  fireEvent.input(view.getByPlaceholderText("80"), { target: { value: "70" } });
  expect(onUpdateQuota).toHaveBeenCalledWith("warnThreshold", 70);
});

test("NamespacePresetStep lists namespaces, selects presets and shows errors pushed after mount", async () => {
  const errors = signal<Record<string, string>>({});
  const namespaces = ["default", "team-a"];
  const onUpdateField = mock();
  const onApplyPreset = mock();
  const form = {
    namespace: "team-a",
    preset: "standard",
    quotaName: "default-quota",
    limitRangeName: "default-limits",
    quota: quota(),
    limits: limits(),
  } as FormState;
  const step = (list: string[]) => (
    <NamespacePresetStep
      form={form}
      errors={errors}
      namespaces={list}
      onUpdateField={onUpdateField}
      onApplyPreset={onApplyPreset}
    />
  );
  const view = render(step(namespaces));

  const select = view.getByLabelText(/Namespace/) as HTMLSelectElement;
  expect(Array.from(select.options).map((o) => o.value)).toEqual([
    "default",
    "team-a",
  ]);
  expect(select.value).toBe("team-a");
  fireEvent.change(select, { target: { value: "default" } });
  expect(onUpdateField).toHaveBeenCalledWith("namespace", "default");

  fireEvent.input(view.getByLabelText(/ResourceQuota Name/), {
    target: { value: "q2" },
  });
  expect(onUpdateField).toHaveBeenCalledWith("quotaName", "q2");
  fireEvent.input(view.getByLabelText(/LimitRange Name/), {
    target: { value: "l2" },
  });
  expect(onUpdateField).toHaveBeenCalledWith("limitRangeName", "l2");

  const presetButtons = view
    .getAllByRole("button")
    .filter((b) => b.textContent);
  expect(presetButtons.length).toBeGreaterThan(1);
  fireEvent.click(presetButtons[0]);
  expect(onApplyPreset).toHaveBeenCalledTimes(1);

  await act(async () => {
    errors.value = { namespace: "pick a namespace", quotaName: "name taken" };
    view.rerender(step([...namespaces, "team-b"]));
  });
  expect(view.getByText("pick a namespace")).toBeTruthy();
  expect(view.getByText("name taken")).toBeTruthy();
  expect(
    Array.from(
      (view.getByLabelText(/Namespace/) as HTMLSelectElement).options,
    ).map((o) => o.value),
  ).toContain("team-b");
});

function esForm(
  overrides: Partial<ExternalSecretWizardForm> = {},
): ExternalSecretWizardForm {
  return {
    name: "app",
    namespace: "default",
    storeRefName: "",
    storeRefKind: "SecretStore",
    refreshInterval: "1h",
    targetSecretName: "app",
    targetSecretNameTouched: false,
    data: [{ secretKey: "A", key: "path/a", property: "", pathTouched: false }],
    ...overrides,
  };
}

function esHandlers() {
  return {
    onUpdate: mock(),
    onUpdateData: mock(),
    onAddDataItem: mock(),
    onRemoveDataItem: mock(),
    onPathFieldTouched: mock(),
  };
}

test("ExternalSecretForm reports edits, add and remove, and shows errors", () => {
  const h = esHandlers();
  const view = render(
    <ExternalSecretForm
      form={esForm({
        data: [
          { secretKey: "A", key: "p/a", property: "", pathTouched: false },
          { secretKey: "B", key: "p/b", property: "", pathTouched: false },
        ],
      })}
      errors={{
        name: "name required",
        "data[1].remoteRef.key": "remote key required",
      }}
      namespaces={["default"]}
      stores={[]}
      storesLoading={false}
      {...h}
    />,
  );

  expect(view.getByText("name required")).toBeTruthy();
  expect(view.getByText("remote key required")).toBeTruthy();
  expect(view.getByText("Data item 2")).toBeTruthy();

  fireEvent.input(view.getByLabelText("Name"), { target: { value: "web" } });
  expect(h.onUpdate).toHaveBeenCalledWith("name", "web");
  // Target name follows the name until the user edits it.
  expect(h.onUpdate).toHaveBeenCalledWith("targetSecretName", "web");

  fireEvent.input(view.getByLabelText("Refresh interval"), {
    target: { value: "5m" },
  });
  expect(h.onUpdate).toHaveBeenCalledWith("refreshInterval", "5m");

  fireEvent.change(view.getByLabelText("Store kind"), {
    target: { value: "ClusterSecretStore" },
  });
  expect(h.onUpdate).toHaveBeenCalledWith("storeRefKind", "ClusterSecretStore");

  const secretKeys = view.getAllByLabelText("Secret key");
  fireEvent.input(secretKeys[1], { target: { value: "B2" } });
  expect(h.onUpdateData).toHaveBeenCalledWith(1, "secretKey", "B2");

  const remote = view.getAllByLabelText(/Remote key/);
  fireEvent.input(remote[0], { target: { value: "p/new" } });
  expect(h.onUpdateData).toHaveBeenCalledWith(0, "key", "p/new");
  expect(h.onPathFieldTouched).toHaveBeenCalledWith(0);

  fireEvent.click(view.getByText("+ Add data item"));
  expect(h.onAddDataItem).toHaveBeenCalledTimes(1);
  fireEvent.click(view.getAllByText("Remove")[1]);
  expect(h.onRemoveDataItem).toHaveBeenCalledWith(1);
});

test("ExternalSecretForm keeps a row's input mounted while its content changes", () => {
  const h = esHandlers();
  const items = (a: string) => [
    { secretKey: a, key: "", property: "", pathTouched: false },
    { secretKey: "", key: "", property: "", pathTouched: false },
  ];
  const props = {
    errors: {},
    namespaces: ["default"],
    stores: [],
    storesLoading: false,
    ...h,
  };
  const view = render(
    <ExternalSecretForm form={esForm({ data: items("") })} {...props} />,
  );
  const first = view.getAllByLabelText("Secret key")[0];
  view.rerender(
    <ExternalSecretForm form={esForm({ data: items("AB") })} {...props} />,
  );
  expect(view.getAllByLabelText("Secret key")[0]).toBe(first);
});

test("ExternalSecretForm lists discovered paths for a SecretStore", async () => {
  const store: ExternalSecretWizardStoreOption = {
    name: "k8s-store",
    namespace: "default",
    kind: "SecretStore",
    provider: "kubernetes",
  };
  const realFetch = globalThis.fetch;
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(
        JSON.stringify({
          data: { supported: true, provider: "kubernetes", paths: ["a", "b"] },
        }),
        { headers: { "content-type": "application/json" } },
      ),
    )) as unknown as typeof fetch;
  try {
    const h = esHandlers();
    const view = render(
      <ExternalSecretForm
        form={esForm({ storeRefName: "k8s-store" })}
        errors={{}}
        namespaces={["default"]}
        stores={[store]}
        storesLoading={false}
        {...h}
      />,
    );
    await waitFor(() =>
      expect(view.getByText("2 paths available")).toBeTruthy(),
    );
    expect(view.getByPlaceholderText("Start typing to search…")).toBeTruthy();
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("StepRail vertical shows labels, marks the current step and reports clicks", () => {
  const onStep = mock();
  const view = render(
    <StepRail
      steps={[
        { label: "One", sub: "first" },
        { label: "Two" },
        { label: "Three" },
      ]}
      current={1}
      onStep={onStep}
    />,
  );
  for (const l of ["One", "Two", "Three", "first"]) {
    expect(view.getByText(l)).toBeTruthy();
  }
  const buttons = view.getAllByRole("button");
  expect(buttons.map((b) => b.getAttribute("aria-current"))).toEqual([
    null,
    "step",
    null,
  ]);
  fireEvent.click(buttons[2]);
  expect(onStep).toHaveBeenCalledWith(2);
});

test("StepRail horizontal only lets completed steps be clicked", () => {
  const onStep = mock();
  const view = render(
    <StepRail
      orientation="horizontal"
      steps={[{ label: "One" }, { label: "Two" }, { label: "Three" }]}
      current={1}
      onStep={onStep}
    />,
  );
  const done = view.getByLabelText("Step 1: One (done)") as HTMLButtonElement;
  const current = view.getByLabelText("Step 2: Two") as HTMLButtonElement;
  const upcoming = view.getByLabelText("Step 3: Three") as HTMLButtonElement;
  expect(current.getAttribute("aria-current")).toBe("step");
  expect(done.disabled).toBe(false);
  expect(current.disabled).toBe(true);
  expect(upcoming.disabled).toBe(true);
  fireEvent.click(done);
  expect(onStep).toHaveBeenCalledWith(0);
  fireEvent.click(upcoming);
  expect(onStep).toHaveBeenCalledTimes(1);
  expect(body().getAllByRole("navigation").length).toBe(1);
});
