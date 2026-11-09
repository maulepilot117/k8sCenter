/** @jsxImportSource react */
import { useCallback, useRef, useState } from "react";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type { LabelEntry } from "@/lib/wizard-types.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useSignal } from "@/src/lib/signal-hooks.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface PDBFormState {
  name: string;
  namespace: string;
  selectorLabels: LabelEntry[];
  budgetType: "minAvailable" | "maxUnavailable";
  budgetValue: string;
}

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Name, selector & budget" },
  { label: "Review", sub: "Preview & apply" },
];

const PDB_ICON = (
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
    <path d="M10 2.5L3 5.5v5c0 4 3 6.5 7 7.5 4-1 7-3.5 7-7.5v-5L10 2.5Z" />
    <path d="M7 10l2 2 4-4" />
  </svg>
);

function initialState(): PDBFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    selectorLabels: [{ key: "", value: "" }],
    budgetType: "minAvailable",
    budgetValue: "1",
  };
}

export default function PDBWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<PDBFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const dirty = useSignal(false);
  const labelKeys = useRowKeys(form.selectorLabels.length);

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  useDirtyGuard(dirty);

  const updateField = useCallback(
    (field: string, value: unknown) => {
      dirty.value = true;
      setForm((prev) => ({ ...prev, [field]: value }));
    },
    [dirty],
  );

  const updateLabel = useCallback(
    (index: number, field: "key" | "value", val: string) => {
      dirty.value = true;
      setForm((prev) => {
        const selectorLabels = [...prev.selectorLabels];
        selectorLabels[index] = { ...selectorLabels[index], [field]: val };
        return { ...prev, selectorLabels };
      });
    },
    [dirty],
  );

  const addLabel = useCallback(() => {
    dirty.value = true;
    setForm((prev) => ({
      ...prev,
      selectorLabels: [...prev.selectorLabels, { key: "", value: "" }],
    }));
  }, [dirty]);

  const removeLabel = useCallback(
    (index: number) => {
      dirty.value = true;
      labelKeys.removeAt(index);
      setForm((prev) => {
        const selectorLabels = prev.selectorLabels.filter(
          (_, i) => i !== index,
        );
        return {
          ...prev,
          selectorLabels:
            selectorLabels.length > 0
              ? selectorLabels
              : [{ key: "", value: "" }],
        };
      });
    },
    [dirty, labelKeys],
  );

  const validateStep = (): boolean => {
    const f = form;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";

    const hasValidLabel = f.selectorLabels.some((l) => l.key.trim() !== "");
    if (!hasValidLabel) {
      errs.selectorLabels =
        "At least one selector label with a non-empty key is required";
    }

    if (!f.budgetValue || !/^\d+%?$/.test(f.budgetValue.trim())) {
      errs.budgetValue = "Enter a number (e.g. 2) or percentage (e.g. 50%)";
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
    const selectorEntries =
      f.selectorLabels
        .filter((l) => l.key)
        .map((l) => `      ${l.key}: "${l.value}"`)
        .join("\n") || "      app: <app>";
    const budgetKey =
      f.budgetType === "minAvailable" ? "minAvailable" : "maxUnavailable";
    return `apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n  name: ${name}\n  namespace: ${ns}\nspec:\n  ${budgetKey}: ${
      f.budgetValue || 1
    }\n  selector:\n    matchLabels:\n${selectorEntries}`;
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
      title="Create PodDisruptionBudget"
      icon={PDB_ICON}
      subtitle="Define disruption budget for pods"
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
              placeholder="e.g. my-app-pdb"
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

          {/* Pod Selector */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Pod Selector <span className="text-danger">*</span>
            </label>
            <p className="mb-2 text-xs text-text-muted">
              Labels used to select the pods this budget applies to.
            </p>
            <div className="mt-2 space-y-3">
              {form.selectorLabels.map((label, i) => (
                <div
                  key={labelKeys.keyAt(i)}
                  className="flex gap-2 items-start"
                >
                  <div className="flex-1">
                    <input
                      type="text"
                      value={label.key}
                      onChange={(e) =>
                        updateLabel(i, "key", e.currentTarget.value)
                      }
                      className={WIZARD_INPUT_CLASS}
                      placeholder="Key"
                    />
                  </div>
                  <div className="flex-1">
                    <input
                      type="text"
                      value={label.value}
                      onChange={(e) =>
                        updateLabel(i, "value", e.currentTarget.value)
                      }
                      className={WIZARD_INPUT_CLASS}
                      placeholder="Value"
                    />
                  </div>
                  <button
                    type="button"
                    onClick={() => removeLabel(i)}
                    className="mt-1 rounded p-2 text-text-muted hover:bg-danger-dim hover:text-danger"
                    title="Remove label"
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
              ))}
            </div>
            {errors.selectorLabels && (
              <p className="mt-1 text-xs text-danger">
                {errors.selectorLabels}
              </p>
            )}
            <button
              type="button"
              onClick={addLabel}
              className="mt-2 text-sm text-brand hover:text-brand/80"
            >
              + Add label
            </button>
          </div>

          {/* Disruption Budget */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Disruption Budget <span className="text-danger">*</span>
            </label>
            <div className="mt-2 flex gap-4">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="budgetType"
                  value="minAvailable"
                  checked={form.budgetType === "minAvailable"}
                  onChange={() => updateField("budgetType", "minAvailable")}
                  className="h-4 w-4 text-brand"
                />
                <span className="text-sm text-text-secondary">
                  Min Available
                </span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="budgetType"
                  value="maxUnavailable"
                  checked={form.budgetType === "maxUnavailable"}
                  onChange={() => updateField("budgetType", "maxUnavailable")}
                  className="h-4 w-4 text-brand"
                />
                <span className="text-sm text-text-secondary">
                  Max Unavailable
                </span>
              </label>
            </div>
            <input
              type="text"
              value={form.budgetValue}
              onChange={(e) =>
                updateField("budgetValue", e.currentTarget.value)
              }
              className={`${WIZARD_INPUT_CLASS} mt-2`}
              placeholder="e.g. 1 or 50%"
            />
            <p className="mt-1 text-xs text-text-muted">
              Enter a number (e.g. 2) or percentage (e.g. 50%)
            </p>
            {errors.budgetValue && (
              <p className="mt-1 text-xs text-danger">{errors.budgetValue}</p>
            )}
          </div>
        </div>
      )}

      {currentStep === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/scaling/pdbs"
          dirty={dirty}
        />
      )}
    </WizardShell>
  );
}
