/** @jsxImportSource react */
import { useCallback, useRef, useState } from "react";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import {
  DEFAULT_NAMESPACE,
  useAdoptSelectedNamespace,
} from "@/src/lib/namespace.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

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

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Target, replicas & metrics" },
  { label: "Review", sub: "Preview & apply" },
];

const HPA_ICON = (
  <svg
    width="21"
    height="21"
    viewBox="0 0 20 20"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.6"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M3 10h14M13 6l4 4-4 4M7 14l-4-4 4-4" />
  </svg>
);

function initialState(): HPAFormState {
  return {
    name: "",
    namespace: DEFAULT_NAMESPACE,
    targetKind: "Deployment",
    targetName: "",
    minReplicas: 1,
    maxReplicas: 10,
    metrics: [
      {
        type: "Resource",
        resourceName: "cpu",
        targetType: "Utilization",
        targetAverageValue: 80,
      },
    ],
  };
}

export default function HPAWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<HPAFormState>(initialState);
  useAdoptSelectedNamespace((namespace) =>
    setForm((prev) => ({ ...prev, namespace })),
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const metricKeys = useRowKeys(form.metrics.length);

  const [namespaceList] = useNamespaces();

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    setDirty(true);
    setForm((prev) => ({ ...prev, [field]: value }));
  }, []);

  const updateMetric = useCallback(
    (index: number, field: keyof HPAMetricState, val: unknown) => {
      setDirty(true);
      setForm((prev) => {
        const metrics = [...prev.metrics];
        metrics[index] = { ...metrics[index], [field]: val };
        return { ...prev, metrics };
      });
    },
    [],
  );

  const addMetric = useCallback(() => {
    setDirty(true);
    setForm((prev) => ({
      ...prev,
      metrics: [
        ...prev.metrics,
        {
          type: "Resource",
          resourceName: "cpu",
          targetType: "Utilization",
          targetAverageValue: 80,
        },
      ],
    }));
  }, []);

  const removeMetric = useCallback(
    (index: number) => {
      setDirty(true);
      metricKeys.removeAt(index);
      setForm((prev) => {
        const metrics = prev.metrics.filter((_, i) => i !== index);
        return {
          ...prev,
          metrics:
            metrics.length > 0
              ? metrics
              : [
                  {
                    type: "Resource",
                    resourceName: "cpu",
                    targetType: "Utilization",
                    targetAverageValue: 80,
                  },
                ],
        };
      });
    },
    [metricKeys],
  );

  const validateStep = (): boolean => {
    const f = form;
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

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep()) return;
    setCurrentStep(1);
    await fetchPreview();
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = form;
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
      if (gen !== previewGen.current) return;
      setPreviewYaml(resp.data.yaml);
    } catch (err) {
      if (gen !== previewGen.current) return;
      setPreviewError(
        err instanceof Error ? err.message : "Failed to generate preview",
      );
    } finally {
      if (gen === previewGen.current) setPreviewLoading(false);
    }
  };

  const manifest = () => {
    const f = form;
    const name = f.name || "<name>";
    const ns = f.namespace || "default";
    return `apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: ${name}\n  namespace: ${ns}\nspec:\n  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: ${f.targetKind}\n    name: ${
      f.targetName || "<target>"
    }\n  minReplicas: ${f.minReplicas}\n  maxReplicas: ${f.maxReplicas}`;
  };

  const nextLabel = currentStep === 0 ? "Continue" : "Close";

  const handleNext = () => {
    if (currentStep === 1) {
      close();
    } else {
      goNext();
    }
  };

  return (
    <WizardShell
      title="Create HorizontalPodAutoscaler"
      icon={HPA_ICON}
      subtitle="Automatically scale workload replicas"
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={() => {
        if (currentStep > 0) setCurrentStep(0);
      }}
      onNext={handleNext}
      nextLabel={nextLabel}
      yaml={currentStep === 1 ? undefined : manifest()}
    >
      {currentStep === 0 && (
        <div className="mx-auto max-w-lg space-y-4">
          {/* Name */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Name <span className="text-danger">*</span>
            </label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
              placeholder="e.g. my-app-hpa"
            />
            {errors.name && (
              <p className="mt-1 text-xs text-danger">{errors.name}</p>
            )}
          </div>

          {/* Namespace */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Namespace <span className="text-danger">*</span>
            </label>
            <select
              value={form.namespace}
              onChange={(e) => updateField("namespace", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
            >
              {namespaceList.map((ns) => (
                <option key={ns} value={ns}>
                  {ns}
                </option>
              ))}
            </select>
            {errors.namespace && (
              <p className="mt-1 text-xs text-danger">{errors.namespace}</p>
            )}
          </div>

          {/* Scale Target */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Scale Target
            </label>
            <div className="mt-2 grid grid-cols-2 gap-3">
              <div>
                <label className="block text-xs text-text-muted mb-1">
                  Kind
                </label>
                <select
                  value={form.targetKind}
                  onChange={(e) =>
                    updateField("targetKind", e.currentTarget.value)
                  }
                  className={WIZARD_INPUT_CLASS}
                >
                  <option value="Deployment">Deployment</option>
                  <option value="StatefulSet">StatefulSet</option>
                  <option value="ReplicaSet">ReplicaSet</option>
                </select>
              </div>
              <div>
                <label className="block text-xs text-text-muted mb-1">
                  Name <span className="text-danger">*</span>
                </label>
                <input
                  type="text"
                  value={form.targetName}
                  onChange={(e) =>
                    updateField("targetName", e.currentTarget.value)
                  }
                  className={WIZARD_INPUT_CLASS}
                  placeholder="e.g. my-app"
                />
                {errors.targetName && (
                  <p className="mt-1 text-xs text-danger">
                    {errors.targetName}
                  </p>
                )}
              </div>
            </div>
          </div>

          {/* Replicas */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Replicas
            </label>
            <div className="mt-2 grid grid-cols-2 gap-3">
              <div>
                <label className="block text-xs text-text-muted mb-1">
                  Min Replicas
                </label>
                <input
                  type="number"
                  min={1}
                  value={form.minReplicas}
                  onChange={(e) =>
                    updateField(
                      "minReplicas",
                      parseInt(e.currentTarget.value, 10) || 1,
                    )
                  }
                  className={WIZARD_INPUT_CLASS}
                />
                {errors.minReplicas && (
                  <p className="mt-1 text-xs text-danger">
                    {errors.minReplicas}
                  </p>
                )}
              </div>
              <div>
                <label className="block text-xs text-text-muted mb-1">
                  Max Replicas <span className="text-danger">*</span>
                </label>
                <input
                  type="number"
                  min={1}
                  value={form.maxReplicas}
                  onChange={(e) =>
                    updateField(
                      "maxReplicas",
                      parseInt(e.currentTarget.value, 10) || 1,
                    )
                  }
                  className={WIZARD_INPUT_CLASS}
                />
                {errors.maxReplicas && (
                  <p className="mt-1 text-xs text-danger">
                    {errors.maxReplicas}
                  </p>
                )}
              </div>
            </div>
          </div>

          {/* Metrics */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Metrics
            </label>
            <div className="mt-2 space-y-3">
              {form.metrics.map((metric, i) => (
                <div
                  key={metricKeys.keyAt(i)}
                  className="rounded-md border border-border-primary p-3 space-y-3"
                >
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-medium text-text-muted">
                      Metric {i + 1}
                    </span>
                    <button
                      type="button"
                      onClick={() => removeMetric(i)}
                      className="rounded p-1 text-text-muted hover:bg-danger-dim hover:text-danger"
                      title="Remove metric"
                    >
                      <svg
                        className="h-4 w-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth={2}
                          d="M6 18L18 6M6 6l12 12"
                        />
                      </svg>
                    </button>
                  </div>
                  <div className="grid grid-cols-3 gap-2">
                    <div>
                      <label className="block text-xs text-text-muted mb-1">
                        Resource
                      </label>
                      <select
                        value={metric.resourceName}
                        onChange={(e) =>
                          updateMetric(i, "resourceName", e.currentTarget.value)
                        }
                        className={WIZARD_INPUT_CLASS}
                      >
                        <option value="cpu">CPU</option>
                        <option value="memory">Memory</option>
                      </select>
                    </div>
                    <div>
                      <label className="block text-xs text-text-muted mb-1">
                        Target Type
                      </label>
                      <select
                        value={metric.targetType}
                        onChange={(e) =>
                          updateMetric(i, "targetType", e.currentTarget.value)
                        }
                        className={WIZARD_INPUT_CLASS}
                      >
                        <option value="Utilization">Utilization</option>
                        <option value="AverageValue">AverageValue</option>
                      </select>
                    </div>
                    <div>
                      <label className="block text-xs text-text-muted mb-1">
                        Target Value <span className="text-danger">*</span>
                      </label>
                      <input
                        type="number"
                        min={1}
                        value={metric.targetAverageValue}
                        onChange={(e) =>
                          updateMetric(
                            i,
                            "targetAverageValue",
                            parseInt(e.currentTarget.value, 10) || 1,
                          )
                        }
                        className={WIZARD_INPUT_CLASS}
                        placeholder="80"
                      />
                      {errors[`metrics_${i}_targetAverageValue`] && (
                        <p className="mt-1 text-xs text-danger">
                          {errors[`metrics_${i}_targetAverageValue`]}
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
              className="mt-2 text-sm text-brand hover:text-brand/80"
            >
              + Add metric
            </button>
          </div>
        </div>
      )}

      {currentStep === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/scaling/hpas"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
