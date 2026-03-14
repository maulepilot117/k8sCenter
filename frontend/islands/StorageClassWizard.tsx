import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";

interface PresetParam {
  default: string;
  description: string;
  type: string;
  required?: boolean;
  options?: string[];
}

interface PresetInfo {
  displayName: string;
  parameters: Record<string, PresetParam>;
}

interface FormState {
  name: string;
  provisioner: string;
  reclaimPolicy: string;
  volumeBindingMode: string;
  allowVolumeExpansion: boolean;
  isDefault: boolean;
  parameters: Array<{ key: string; value: string }>;
  mountOptions: Array<{ value: string }>;
}

const STEPS = [
  { title: "Basics" },
  { title: "Parameters" },
  { title: "Review" },
];

function initialState(): FormState {
  return {
    name: "",
    provisioner: "",
    reclaimPolicy: "Delete",
    volumeBindingMode: "Immediate",
    allowVolumeExpansion: false,
    isDefault: false,
    parameters: [],
    mountOptions: [],
  };
}

export default function StorageClassWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<FormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const presets = useSignal<Record<string, PresetInfo>>({});
  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);
  const dirty = useSignal(false);

  // Fetch presets
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Record<string, PresetInfo>>("/v1/storage/presets")
      .then((resp) => {
        if (resp.data) presets.value = resp.data;
      })
      .catch(() => {});
  }, []);

  // beforeunload guard
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty.value) e.preventDefault();
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const applyPreset = (driverName: string) => {
    const preset = presets.value[driverName];
    if (!preset) return;
    const params = Object.entries(preset.parameters).map(([key, p]) => ({
      key,
      value: p.default,
    }));
    form.value = {
      ...form.value,
      provisioner: driverName,
      parameters: params,
    };
    dirty.value = true;
  };

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (
        !f.name ||
        !/^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(f.name)
      ) {
        errs.name =
          "Must be a valid DNS subdomain (lowercase, hyphens, dots, max 253)";
      }
      if (!f.provisioner) errs.provisioner = "Required";
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep(currentStep.value)) return;

    if (currentStep.value === 1) {
      currentStep.value = 2;
      await fetchPreview();
    } else {
      currentStep.value = currentStep.value + 1;
    }
  };

  const goBack = () => {
    if (currentStep.value > 0) {
      currentStep.value = currentStep.value - 1;
    }
  };

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    const payload: Record<string, unknown> = {
      name: f.name,
      provisioner: f.provisioner,
      reclaimPolicy: f.reclaimPolicy,
      volumeBindingMode: f.volumeBindingMode,
      allowVolumeExpansion: f.allowVolumeExpansion,
      isDefault: f.isDefault,
    };

    // Convert parameters array to map
    const params: Record<string, string> = {};
    for (const p of f.parameters) {
      if (p.key) params[p.key] = p.value;
    }
    if (Object.keys(params).length > 0) payload.parameters = params;

    // Mount options
    const opts = f.mountOptions.map((o) => o.value).filter(Boolean);
    if (opts.length > 0) payload.mountOptions = opts;

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/storageclass/preview",
        payload,
      );
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      previewLoading.value = false;
    }
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create StorageClass
        </h1>
        <a
          href="/storage/overview"
          class="text-sm text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
        >
          Cancel
        </a>
      </div>

      <WizardStepper
        steps={STEPS}
        currentStep={currentStep.value}
        onStepClick={(step) => {
          if (step < currentStep.value) currentStep.value = step;
        }}
      />

      <div class="mt-6">
        {currentStep.value === 0 && (
          <BasicsStep
            form={form.value}
            errors={errors.value}
            presets={presets.value}
            onChange={updateField}
            onApplyPreset={applyPreset}
          />
        )}

        {currentStep.value === 1 && (
          <ParametersStep
            form={form.value}
            presets={presets.value}
            onChange={updateField}
          />
        )}

        {currentStep.value === 2 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/storage/overview"
          />
        )}
      </div>

      {currentStep.value < 2 && (
        <div class="flex justify-between mt-8">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === 1 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === 2 && !previewLoading.value &&
        previewError.value === null && (
        <div class="flex justify-start mt-4">
          <Button variant="ghost" onClick={goBack}>
            Back
          </Button>
        </div>
      )}
    </div>
  );
}

function BasicsStep(
  { form, errors, presets, onChange, onApplyPreset }: {
    form: FormState;
    errors: Record<string, string>;
    presets: Record<string, PresetInfo>;
    onChange: (field: string, value: unknown) => void;
    onApplyPreset: (driver: string) => void;
  },
) {
  const presetNames = Object.keys(presets);

  return (
    <div class="space-y-6 max-w-xl">
      {/* Preset selector */}
      {presetNames.length > 0 && (
        <div>
          <label class="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
            Quick Start (optional)
          </label>
          <div class="flex gap-2 flex-wrap">
            {presetNames.map((driver) => (
              <button
                key={driver}
                type="button"
                onClick={() => onApplyPreset(driver)}
                class={`px-3 py-1.5 text-xs rounded-lg border transition-colors ${
                  form.provisioner === driver
                    ? "border-brand bg-brand/10 text-brand"
                    : "border-slate-300 dark:border-slate-600 text-slate-600 dark:text-slate-400 hover:border-brand/50"
                }`}
              >
                {presets[driver].displayName}
              </button>
            ))}
          </div>
        </div>
      )}

      <Input
        label="Name"
        value={form.name}
        error={errors.name}
        onInput={(e) => onChange("name", (e.target as HTMLInputElement).value)}
        placeholder="e.g., fast-storage"
      />

      <Input
        label="Provisioner"
        value={form.provisioner}
        error={errors.provisioner}
        onInput={(e) =>
          onChange("provisioner", (e.target as HTMLInputElement).value)}
        placeholder="e.g., ebs.csi.aws.com"
      />

      <div>
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-1">
          Reclaim Policy
        </label>
        <select
          value={form.reclaimPolicy}
          onChange={(e) =>
            onChange(
              "reclaimPolicy",
              (e.target as HTMLSelectElement).value,
            )}
          class="w-full px-3 py-2 border border-slate-300 dark:border-slate-600 rounded-lg bg-white dark:bg-slate-800 text-slate-800 dark:text-white text-sm"
        >
          <option value="Delete">Delete</option>
          <option value="Retain">Retain</option>
        </select>
      </div>

      <div>
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-1">
          Volume Binding Mode
        </label>
        <select
          value={form.volumeBindingMode}
          onChange={(e) =>
            onChange(
              "volumeBindingMode",
              (e.target as HTMLSelectElement).value,
            )}
          class="w-full px-3 py-2 border border-slate-300 dark:border-slate-600 rounded-lg bg-white dark:bg-slate-800 text-slate-800 dark:text-white text-sm"
        >
          <option value="Immediate">Immediate</option>
          <option value="WaitForFirstConsumer">WaitForFirstConsumer</option>
        </select>
      </div>

      <div class="flex gap-6">
        <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
          <input
            type="checkbox"
            checked={form.allowVolumeExpansion}
            onChange={(e) =>
              onChange(
                "allowVolumeExpansion",
                (e.target as HTMLInputElement).checked,
              )}
            class="rounded border-slate-300 dark:border-slate-600"
          />
          Allow Volume Expansion
        </label>

        <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
          <input
            type="checkbox"
            checked={form.isDefault}
            onChange={(e) =>
              onChange(
                "isDefault",
                (e.target as HTMLInputElement).checked,
              )}
            class="rounded border-slate-300 dark:border-slate-600"
          />
          Set as Default StorageClass
        </label>
      </div>
    </div>
  );
}

function ParametersStep(
  { form, presets, onChange }: {
    form: FormState;
    presets: Record<string, PresetInfo>;
    onChange: (field: string, value: unknown) => void;
  },
) {
  const preset = presets[form.provisioner];

  const addParameter = () => {
    onChange("parameters", [...form.parameters, { key: "", value: "" }]);
  };

  const removeParameter = (idx: number) => {
    onChange("parameters", form.parameters.filter((_, i) => i !== idx));
  };

  const updateParameter = (
    idx: number,
    field: "key" | "value",
    val: string,
  ) => {
    const updated = [...form.parameters];
    updated[idx] = { ...updated[idx], [field]: val };
    onChange("parameters", updated);
  };

  const addMountOption = () => {
    onChange("mountOptions", [...form.mountOptions, { value: "" }]);
  };

  const removeMountOption = (idx: number) => {
    onChange("mountOptions", form.mountOptions.filter((_, i) => i !== idx));
  };

  const updateMountOption = (idx: number, val: string) => {
    const updated = [...form.mountOptions];
    updated[idx] = { value: val };
    onChange("mountOptions", updated);
  };

  return (
    <div class="space-y-6 max-w-2xl">
      {/* Parameters */}
      <div>
        <div class="flex items-center justify-between mb-2">
          <label class="text-sm font-medium text-slate-700 dark:text-slate-300">
            Parameters
          </label>
          <Button variant="ghost" onClick={addParameter}>
            + Add Parameter
          </Button>
        </div>

        {form.parameters.length === 0 && (
          <p class="text-sm text-slate-400 dark:text-slate-500 py-2">
            No parameters configured.
            {preset
              ? " Preset parameters were applied from the quick start selection."
              : " Add driver-specific parameters as needed."}
          </p>
        )}

        <div class="space-y-2">
          {form.parameters.map((p, i) => {
            const presetParam = preset?.parameters[p.key];
            return (
              <div key={i} class="flex gap-2 items-start">
                <div class="flex-1">
                  <input
                    type="text"
                    value={p.key}
                    onInput={(e) =>
                      updateParameter(
                        i,
                        "key",
                        (e.target as HTMLInputElement).value,
                      )}
                    placeholder="Key"
                    class="w-full px-3 py-2 border border-slate-300 dark:border-slate-600 rounded-lg bg-white dark:bg-slate-800 text-slate-800 dark:text-white text-sm"
                  />
                  {presetParam && (
                    <p class="text-xs text-slate-400 mt-0.5">
                      {presetParam.description}
                    </p>
                  )}
                </div>
                <div class="flex-1">
                  {presetParam?.options
                    ? (
                      <select
                        value={p.value}
                        onChange={(e) =>
                          updateParameter(
                            i,
                            "value",
                            (e.target as HTMLSelectElement).value,
                          )}
                        class="w-full px-3 py-2 border border-slate-300 dark:border-slate-600 rounded-lg bg-white dark:bg-slate-800 text-slate-800 dark:text-white text-sm"
                      >
                        {presetParam.options.map((opt) => (
                          <option key={opt} value={opt}>{opt}</option>
                        ))}
                      </select>
                    )
                    : (
                      <input
                        type="text"
                        value={p.value}
                        onInput={(e) =>
                          updateParameter(
                            i,
                            "value",
                            (e.target as HTMLInputElement).value,
                          )}
                        placeholder="Value"
                        class="w-full px-3 py-2 border border-slate-300 dark:border-slate-600 rounded-lg bg-white dark:bg-slate-800 text-slate-800 dark:text-white text-sm"
                      />
                    )}
                </div>
                <button
                  type="button"
                  onClick={() => removeParameter(i)}
                  class="px-2 py-2 text-slate-400 hover:text-red-500"
                >
                  x
                </button>
              </div>
            );
          })}
        </div>
      </div>

      {/* Mount Options */}
      <div>
        <div class="flex items-center justify-between mb-2">
          <label class="text-sm font-medium text-slate-700 dark:text-slate-300">
            Mount Options
          </label>
          <Button variant="ghost" onClick={addMountOption}>
            + Add Mount Option
          </Button>
        </div>

        {form.mountOptions.length === 0 && (
          <p class="text-sm text-slate-400 dark:text-slate-500 py-2">
            No mount options configured.
          </p>
        )}

        <div class="space-y-2">
          {form.mountOptions.map((o, i) => (
            <div key={i} class="flex gap-2">
              <input
                type="text"
                value={o.value}
                onInput={(e) =>
                  updateMountOption(
                    i,
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="e.g., debug, noatime"
                class="flex-1 px-3 py-2 border border-slate-300 dark:border-slate-600 rounded-lg bg-white dark:bg-slate-800 text-slate-800 dark:text-white text-sm"
              />
              <button
                type="button"
                onClick={() => removeMountOption(i)}
                class="px-2 py-2 text-slate-400 hover:text-red-500"
              >
                x
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
