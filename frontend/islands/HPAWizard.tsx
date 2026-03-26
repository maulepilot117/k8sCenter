import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface HPAMetricState {
  type: "Resource";
  resourceName: "cpu" | "memory";
  targetType: "Utilization" | "AverageValue";
  targetAverageValue: number;
}

interface HPAFormState {
  name: string;
  namespace: string;
  targetKind: "Deployment" | "StatefulSet" | "ReplicaSet";
  targetName: string;
  minReplicas: number;
  maxReplicas: number;
  metrics: HPAMetricState[];
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

function initialState(): HPAFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  return {
    name: "",
    namespace: ns,
    targetKind: "Deployment",
    targetName: "",
    minReplicas: 1,
    maxReplicas: 10,
    metrics: [{
      type: "Resource",
      resourceName: "cpu",
      targetType: "Utilization",
      targetAverageValue: 80,
    }],
  };
}

export default function HPAWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<HPAFormState>(initialState());
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

  const updateMetric = useCallback(
    (index: number, field: keyof HPAMetricState, val: unknown) => {
      dirty.value = true;
      const metrics = [...form.value.metrics];
      metrics[index] = { ...metrics[index], [field]: val };
      form.value = { ...form.value, metrics };
    },
    [],
  );

  const addMetric = useCallback(() => {
    dirty.value = true;
    form.value = {
      ...form.value,
      metrics: [
        ...form.value.metrics,
        {
          type: "Resource",
          resourceName: "cpu",
          targetType: "Utilization",
          targetAverageValue: 80,
        },
      ],
    };
  }, []);

  const removeMetric = useCallback((index: number) => {
    dirty.value = true;
    const metrics = form.value.metrics.filter((_, i) => i !== index);
    form.value = {
      ...form.value,
      metrics: metrics.length > 0 ? metrics : [{
        type: "Resource",
        resourceName: "cpu",
        targetType: "Utilization",
        targetAverageValue: 80,
      }],
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
    if (!f.targetName || !DNS_LABEL_REGEX.test(f.targetName)) {
      errs.targetName =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (f.maxReplicas < 1 || f.maxReplicas > 1000) {
      errs.maxReplicas = "Must be between 1 and 1000";
    }
    if (f.minReplicas > f.maxReplicas) {
      errs.minReplicas = "Min replicas cannot exceed max replicas";
    }

    for (let i = 0; i < f.metrics.length; i++) {
      const m = f.metrics[i];
      if (
        m.targetType === "Utilization" &&
        (m.targetAverageValue < 1 || m.targetAverageValue > 100)
      ) {
        errs[`metrics_${i}_targetAverageValue`] =
          "Must be between 1 and 100 for Utilization (percentage)";
      } else if (m.targetAverageValue < 1) {
        errs[`metrics_${i}_targetAverageValue`] = "Must be at least 1";
      }
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
      targetKind: f.targetKind,
      targetName: f.targetName,
      minReplicas: f.minReplicas > 0 ? f.minReplicas : undefined,
      maxReplicas: f.maxReplicas,
      metrics: f.metrics.map((m) => ({
        type: m.type,
        resourceName: m.resourceName,
        targetType: m.targetType,
        targetAverageValue: m.targetAverageValue,
      })),
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/hpa/preview",
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
        <h1 class="text-2xl font-bold text-text-primary">
          Create HorizontalPodAutoscaler
        </h1>
        <a
          href="/scaling/hpas"
          class="text-sm text-text-muted hover:text-text-primary"
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
              <label class="block text-sm font-medium text-text-secondary">
                Name <span class="text-danger">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class={WIZARD_INPUT_CLASS}
                placeholder="e.g. my-app-hpa"
              />
              {errors.value.name && (
                <p class="mt-1 text-xs text-danger">{errors.value.name}</p>
              )}
            </div>

            {/* Namespace */}
            <div>
              <label class="block text-sm font-medium text-text-secondary">
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

            {/* Scale Target */}
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Scale Target
              </label>
              <div class="mt-2 grid grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs text-text-muted mb-1">
                    Kind
                  </label>
                  <select
                    value={form.value.targetKind}
                    onChange={(e) =>
                      updateField(
                        "targetKind",
                        (e.target as HTMLSelectElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                  >
                    <option value="Deployment">Deployment</option>
                    <option value="StatefulSet">StatefulSet</option>
                    <option value="ReplicaSet">ReplicaSet</option>
                  </select>
                </div>
                <div>
                  <label class="block text-xs text-text-muted mb-1">
                    Name <span class="text-danger">*</span>
                  </label>
                  <input
                    type="text"
                    value={form.value.targetName}
                    onInput={(e) =>
                      updateField(
                        "targetName",
                        (e.target as HTMLInputElement).value,
                      )}
                    class={WIZARD_INPUT_CLASS}
                    placeholder="e.g. my-app"
                  />
                  {errors.value.targetName && (
                    <p class="mt-1 text-xs text-danger">
                      {errors.value.targetName}
                    </p>
                  )}
                </div>
              </div>
            </div>

            {/* Replicas */}
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Replicas
              </label>
              <div class="mt-2 grid grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs text-text-muted mb-1">
                    Min Replicas
                  </label>
                  <input
                    type="number"
                    min={1}
                    value={form.value.minReplicas}
                    onInput={(e) =>
                      updateField(
                        "minReplicas",
                        parseInt((e.target as HTMLInputElement).value, 10) || 1,
                      )}
                    class={WIZARD_INPUT_CLASS}
                  />
                  {errors.value.minReplicas && (
                    <p class="mt-1 text-xs text-danger">
                      {errors.value.minReplicas}
                    </p>
                  )}
                </div>
                <div>
                  <label class="block text-xs text-text-muted mb-1">
                    Max Replicas <span class="text-danger">*</span>
                  </label>
                  <input
                    type="number"
                    min={1}
                    value={form.value.maxReplicas}
                    onInput={(e) =>
                      updateField(
                        "maxReplicas",
                        parseInt((e.target as HTMLInputElement).value, 10) || 1,
                      )}
                    class={WIZARD_INPUT_CLASS}
                  />
                  {errors.value.maxReplicas && (
                    <p class="mt-1 text-xs text-danger">
                      {errors.value.maxReplicas}
                    </p>
                  )}
                </div>
              </div>
            </div>

            {/* Metrics */}
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Metrics
              </label>
              <div class="mt-2 space-y-3">
                {form.value.metrics.map((metric, i) => (
                  <div
                    key={i}
                    class="rounded-md border border-border-primary p-3 space-y-3"
                  >
                    <div class="flex items-center justify-between">
                      <span class="text-xs font-medium text-text-muted">
                        Metric {i + 1}
                      </span>
                      <button
                        type="button"
                        onClick={() => removeMetric(i)}
                        class="rounded p-1 text-text-muted hover:bg-danger-dim hover:text-danger"
                        title="Remove metric"
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
                    <div class="grid grid-cols-3 gap-2">
                      <div>
                        <label class="block text-xs text-text-muted mb-1">
                          Resource
                        </label>
                        <select
                          value={metric.resourceName}
                          onChange={(e) =>
                            updateMetric(
                              i,
                              "resourceName",
                              (e.target as HTMLSelectElement).value,
                            )}
                          class={WIZARD_INPUT_CLASS}
                        >
                          <option value="cpu">CPU</option>
                          <option value="memory">Memory</option>
                        </select>
                      </div>
                      <div>
                        <label class="block text-xs text-text-muted mb-1">
                          Target Type
                        </label>
                        <select
                          value={metric.targetType}
                          onChange={(e) =>
                            updateMetric(
                              i,
                              "targetType",
                              (e.target as HTMLSelectElement).value,
                            )}
                          class={WIZARD_INPUT_CLASS}
                        >
                          <option value="Utilization">Utilization</option>
                          <option value="AverageValue">AverageValue</option>
                        </select>
                      </div>
                      <div>
                        <label class="block text-xs text-text-muted mb-1">
                          Target Value <span class="text-danger">*</span>
                        </label>
                        <input
                          type="number"
                          min={1}
                          value={metric.targetAverageValue}
                          onInput={(e) =>
                            updateMetric(
                              i,
                              "targetAverageValue",
                              parseInt(
                                (e.target as HTMLInputElement).value,
                                10,
                              ) || 1,
                            )}
                          class={WIZARD_INPUT_CLASS}
                          placeholder="80"
                        />
                        {errors.value[`metrics_${i}_targetAverageValue`] && (
                          <p class="mt-1 text-xs text-danger">
                            {errors.value[`metrics_${i}_targetAverageValue`]}
                          </p>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
              <button
                type="button"
                onClick={addMetric}
                class="mt-2 text-sm text-brand hover:text-brand/80"
              >
                + Add metric
              </button>
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
            detailBasePath="/scaling/hpas"
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
