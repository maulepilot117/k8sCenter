import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, LabelEntry, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface PDBFormState {
  name: string;
  namespace: string;
  selectorLabels: LabelEntry[];
  budgetType: "minAvailable" | "maxUnavailable";
  budgetValue: string;
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

function initialState(): PDBFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  return {
    name: "",
    namespace: ns,
    selectorLabels: [{ key: "", value: "" }],
    budgetType: "minAvailable",
    budgetValue: "1",
  };
}

export default function PDBWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<PDBFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const updateLabel = useCallback(
    (index: number, field: "key" | "value", val: string) => {
      dirty.value = true;
      const selectorLabels = [...form.value.selectorLabels];
      selectorLabels[index] = { ...selectorLabels[index], [field]: val };
      form.value = { ...form.value, selectorLabels };
    },
    [],
  );

  const addLabel = useCallback(() => {
    dirty.value = true;
    form.value = {
      ...form.value,
      selectorLabels: [...form.value.selectorLabels, { key: "", value: "" }],
    };
  }, []);

  const removeLabel = useCallback((index: number) => {
    dirty.value = true;
    const selectorLabels = form.value.selectorLabels.filter(
      (_, i) => i !== index,
    );
    form.value = {
      ...form.value,
      selectorLabels: selectorLabels.length > 0
        ? selectorLabels
        : [{ key: "", value: "" }],
    };
  }, []);

  const validateStep = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";

    const hasValidLabel = f.selectorLabels.some((l) => l.key.trim() !== "");
    if (!hasValidLabel) {
      errs.selectorLabels = "At least one selector label with a non-empty key is required";
    }

    if (!f.budgetValue || !/^\d+%?$/.test(f.budgetValue.trim())) {
      errs.budgetValue =
        "Enter a number (e.g. 2) or percentage (e.g. 50%)";
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep()) return;
    currentStep.value = 1;
    await fetchPreview();
  };

  const goBack = () => {
    if (currentStep.value > 0) currentStep.value = 0;
  };

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    const payload: Record<string, unknown> = {
      name: f.name,
      namespace: f.namespace,
      selector: Object.fromEntries(
        f.selectorLabels.filter((l) => l.key).map((l) => [l.key, l.value]),
      ),
    };
    if (f.budgetType === "minAvailable") {
      payload.minAvailable = f.budgetValue;
    } else {
      payload.maxUnavailable = f.budgetValue;
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/pdb/preview",
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
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create PodDisruptionBudget
        </h1>
        <a
          href="/scaling/pdbs"
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
          <div class="mx-auto max-w-lg space-y-4">
            {/* Name */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Name <span class="text-danger">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class={WIZARD_INPUT_CLASS}
                placeholder="e.g. my-app-pdb"
              />
              {errors.value.name && (
                <p class="mt-1 text-xs text-danger">{errors.value.name}</p>
              )}
            </div>

            {/* Namespace */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Namespace <span class="text-danger">*</span>
              </label>
              <select
                value={form.value.namespace}
                onChange={(e) =>
                  updateField(
                    "namespace",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                {namespaces.value.map((ns) => (
                  <option key={ns} value={ns}>{ns}</option>
                ))}
              </select>
              {errors.value.namespace && (
                <p class="mt-1 text-xs text-danger">
                  {errors.value.namespace}
                </p>
              )}
            </div>

            {/* Pod Selector */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Pod Selector <span class="text-danger">*</span>
              </label>
              <p class="mb-2 text-xs text-slate-500 dark:text-slate-400">
                Labels used to select the pods this budget applies to.
              </p>
              <div class="mt-2 space-y-3">
                {form.value.selectorLabels.map((label, i) => (
                  <div key={i} class="flex gap-2 items-start">
                    <div class="flex-1">
                      <input
                        type="text"
                        value={label.key}
                        onInput={(e) =>
                          updateLabel(
                            i,
                            "key",
                            (e.target as HTMLInputElement).value,
                          )}
                        class={WIZARD_INPUT_CLASS}
                        placeholder="Key"
                      />
                    </div>
                    <div class="flex-1">
                      <input
                        type="text"
                        value={label.value}
                        onInput={(e) =>
                          updateLabel(
                            i,
                            "value",
                            (e.target as HTMLInputElement).value,
                          )}
                        class={WIZARD_INPUT_CLASS}
                        placeholder="Value"
                      />
                    </div>
                    <button
                      type="button"
                      onClick={() => removeLabel(i)}
                      class="mt-1 rounded p-2 text-slate-400 hover:bg-red-50 hover:text-danger dark:hover:bg-red-900/20"
                      title="Remove label"
                    >
                      <svg
                        class="h-4 w-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          stroke-width={2}
                          d="M6 18L18 6M6 6l12 12"
                        />
                      </svg>
                    </button>
                  </div>
                ))}
              </div>
              {errors.value.selectorLabels && (
                <p class="mt-1 text-xs text-danger">
                  {errors.value.selectorLabels}
                </p>
              )}
              <button
                type="button"
                onClick={addLabel}
                class="mt-2 text-sm text-brand hover:text-brand/80"
              >
                + Add label
              </button>
            </div>

            {/* Disruption Budget */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Disruption Budget <span class="text-danger">*</span>
              </label>
              <div class="mt-2 flex gap-4">
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="radio"
                    name="budgetType"
                    value="minAvailable"
                    checked={form.value.budgetType === "minAvailable"}
                    onChange={() => updateField("budgetType", "minAvailable")}
                    class="h-4 w-4 text-brand"
                  />
                  <span class="text-sm text-slate-700 dark:text-slate-300">
                    Min Available
                  </span>
                </label>
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="radio"
                    name="budgetType"
                    value="maxUnavailable"
                    checked={form.value.budgetType === "maxUnavailable"}
                    onChange={() => updateField("budgetType", "maxUnavailable")}
                    class="h-4 w-4 text-brand"
                  />
                  <span class="text-sm text-slate-700 dark:text-slate-300">
                    Max Unavailable
                  </span>
                </label>
              </div>
              <input
                type="text"
                value={form.value.budgetValue}
                onInput={(e) =>
                  updateField(
                    "budgetValue",
                    (e.target as HTMLInputElement).value,
                  )}
                class={WIZARD_INPUT_CLASS + " mt-2"}
                placeholder="e.g. 1 or 50%"
              />
              <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                Enter a number (e.g. 2) or percentage (e.g. 50%)
              </p>
              {errors.value.budgetValue && (
                <p class="mt-1 text-xs text-danger">
                  {errors.value.budgetValue}
                </p>
              )}
            </div>
          </div>
        )}

        {currentStep.value === 1 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/scaling/pdbs"
          />
        )}
      </div>

      {currentStep.value === 0 && (
        <div class="mt-8 flex justify-end">
          <Button variant="primary" onClick={goNext}>
            Preview YAML
          </Button>
        </div>
      )}

      {currentStep.value === 1 && !previewLoading.value &&
        previewError.value === null && (
        <div class="mt-4 flex justify-start">
          <Button variant="ghost" onClick={goBack}>
            Back
          </Button>
        </div>
      )}
    </div>
  );
}
