import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface PVCFormState {
  name: string;
  namespace: string;
  storageClassName: string;
  sizeValue: string;
  sizeUnit: string;
  accessMode: string;
}

interface StorageClassItem {
  metadata: { name: string };
  provisioner?: string;
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

const ACCESS_MODES = [
  {
    value: "ReadWriteOnce",
    label: "ReadWriteOnce",
    desc: "Single node read-write",
  },
  {
    value: "ReadWriteMany",
    label: "ReadWriteMany",
    desc: "Multi-node read-write",
  },
  {
    value: "ReadOnlyMany",
    label: "ReadOnlyMany",
    desc: "Multi-node read-only",
  },
  {
    value: "ReadWriteOncePod",
    label: "ReadWriteOncePod",
    desc: "Single pod read-write",
  },
];

function initialState(): PVCFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  return {
    name: "",
    namespace: ns,
    storageClassName: "",
    sizeValue: "10",
    sizeUnit: "Gi",
    accessMode: "ReadWriteOnce",
  };
}

export default function PVCWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<PVCFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useSignal<string[]>(["default"]);
  const storageClasses = useSignal<StorageClassItem[]>([]);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          namespaces.value = resp.data.map((ns) => ns.metadata.name).sort();
        }
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<StorageClassItem[]>("/v1/resources/storageclasses?limit=500")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          storageClasses.value = resp.data;
          if (resp.data.length > 0 && !form.value.storageClassName) {
            form.value = {
              ...form.value,
              storageClassName: resp.data[0].metadata.name,
            };
          }
        }
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty.value) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const validateStep = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.storageClassName) errs.storageClassName = "Required";

    const size = parseFloat(f.sizeValue);
    if (isNaN(size) || size <= 0) {
      errs.sizeValue = "Must be a positive number";
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
    const payload = {
      name: f.name,
      namespace: f.namespace,
      storageClassName: f.storageClassName,
      size: `${f.sizeValue}${f.sizeUnit}`,
      accessMode: f.accessMode,
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/pvc/preview",
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

  const inputClass =
    "mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white";

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create Persistent Volume Claim
        </h1>
        <a
          href="/storage/pvcs"
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
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Name <span class="text-red-500">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class={inputClass}
                placeholder="e.g. my-data"
              />
              {errors.value.name && (
                <p class="mt-1 text-xs text-red-500">{errors.value.name}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Namespace <span class="text-red-500">*</span>
              </label>
              <select
                value={form.value.namespace}
                onChange={(e) =>
                  updateField(
                    "namespace",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={inputClass}
              >
                {namespaces.value.map((ns) => (
                  <option key={ns} value={ns}>{ns}</option>
                ))}
              </select>
            </div>

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Storage Class <span class="text-red-500">*</span>
              </label>
              <select
                value={form.value.storageClassName}
                onChange={(e) =>
                  updateField(
                    "storageClassName",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={inputClass}
              >
                <option value="">Select a storage class...</option>
                {storageClasses.value.map((sc) => (
                  <option key={sc.metadata.name} value={sc.metadata.name}>
                    {sc.metadata.name}
                  </option>
                ))}
              </select>
              {errors.value.storageClassName && (
                <p class="mt-1 text-xs text-red-500">
                  {errors.value.storageClassName}
                </p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Size <span class="text-red-500">*</span>
              </label>
              <div class="mt-1 flex gap-2">
                <input
                  type="number"
                  min="1"
                  value={form.value.sizeValue}
                  onInput={(e) =>
                    updateField(
                      "sizeValue",
                      (e.target as HTMLInputElement).value,
                    )}
                  class="w-32 rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                />
                <select
                  value={form.value.sizeUnit}
                  onChange={(e) =>
                    updateField(
                      "sizeUnit",
                      (e.target as HTMLSelectElement).value,
                    )}
                  class="rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                >
                  <option value="Mi">Mi</option>
                  <option value="Gi">Gi</option>
                  <option value="Ti">Ti</option>
                </select>
              </div>
              {errors.value.sizeValue && (
                <p class="mt-1 text-xs text-red-500">
                  {errors.value.sizeValue}
                </p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Access Mode
              </label>
              <div class="mt-2 space-y-2">
                {ACCESS_MODES.map((mode) => (
                  <label
                    key={mode.value}
                    class="flex items-center gap-3 rounded-md border border-slate-200 px-3 py-2 cursor-pointer hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800/50"
                  >
                    <input
                      type="radio"
                      name="accessMode"
                      value={mode.value}
                      checked={form.value.accessMode === mode.value}
                      onChange={() => updateField("accessMode", mode.value)}
                      class="text-brand focus:ring-brand"
                    />
                    <div>
                      <span class="text-sm font-medium text-slate-700 dark:text-slate-300">
                        {mode.label}
                      </span>
                      <span class="ml-2 text-xs text-slate-400">
                        {mode.desc}
                      </span>
                    </div>
                  </label>
                ))}
              </div>
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
            detailBasePath="/storage/pvcs"
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
