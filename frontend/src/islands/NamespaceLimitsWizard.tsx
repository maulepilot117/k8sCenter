import { useCallback, useRef, useState } from "react";
import { LimitRangeValuesStep } from "@/components/wizard/LimitRangeValuesStep.tsx";
import { NamespacePresetStep } from "@/components/wizard/NamespacePresetStep.tsx";
import { QuotaValuesStep } from "@/components/wizard/QuotaValuesStep.tsx";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import {
  DEFAULT_NAMESPACE,
  useAdoptSelectedNamespace,
} from "@/src/lib/namespace.ts";

// ---------------------------------------------------------------------------
// Shared types & constants — exported for step components
// ---------------------------------------------------------------------------

// PRESETS and PresetKey moved to lib/namespace-presets.ts so the shared
// preset step can import them without importing this island back.

import { PRESETS, type PresetKey } from "@/lib/namespace-presets.ts";

export interface ResourcePair {
  cpu: string;
  memory: string;
}

export interface QuotaConfig {
  cpuHard: string;
  memoryHard: string;
  podsHard: number;
  secretsHard?: number;
  configMapsHard?: number;
  servicesHard?: number;
  pvcsHard?: number;
  gpuHard?: string;
  warnThreshold?: number;
  criticalThreshold?: number;
}

export interface LimitConfig {
  containerDefault: ResourcePair;
  containerDefaultRequest: ResourcePair;
  containerMax: ResourcePair;
  containerMin: ResourcePair;
  podMax?: ResourcePair;
  pvcMinStorage?: string;
  pvcMaxStorage?: string;
}

export interface FormState {
  namespace: string;
  preset: PresetKey;
  quotaName: string;
  limitRangeName: string;
  quota: QuotaConfig;
  limits: LimitConfig;
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

const STEPS: WizardStep[] = [
  { label: "Namespace & Preset", sub: "Scope & baseline" },
  { label: "Quota Values", sub: "CPU, memory & pods" },
  { label: "LimitRange Values", sub: "Container limits" },
  { label: "Review", sub: "Preview & apply" },
];

function initialState(): FormState {
  const preset = PRESETS.standard;
  return {
    namespace: DEFAULT_NAMESPACE,
    preset: "standard",
    quotaName: "default-quota",
    limitRangeName: "default-limits",
    quota: { ...preset.quota },
    limits: {
      containerDefault: { ...preset.limits.containerDefault },
      containerDefaultRequest: { ...preset.limits.containerDefaultRequest },
      containerMax: { ...preset.limits.containerMax },
      containerMin: { ...preset.limits.containerMin },
    },
  };
}

function buildManifest(f: FormState): string {
  return `apiVersion: v1\nkind: ResourceQuota\nmetadata:\n  name: ${f.quotaName}\n  namespace: ${f.namespace}\nspec:\n  hard:\n    cpu: "${f.quota.cpuHard}"\n    memory: "${f.quota.memoryHard}"\n    pods: "${f.quota.podsHard}"\n---\napiVersion: v1\nkind: LimitRange\nmetadata:\n  name: ${f.limitRangeName}\n  namespace: ${f.namespace}\nspec:\n  limits:\n    - type: Container\n      default:\n        cpu: "${f.limits.containerDefault.cpu}"\n        memory: "${f.limits.containerDefault.memory}"\n      defaultRequest:\n        cpu: "${f.limits.containerDefaultRequest.cpu}"\n        memory: "${f.limits.containerDefaultRequest.memory}"`;
}

// ---------------------------------------------------------------------------
// Island
// ---------------------------------------------------------------------------

export default function NamespaceLimitsWizard({
  onClose,
}: {
  onClose?: () => void;
}) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<FormState>(initialState);
  useAdoptSelectedNamespace((namespace) =>
    setForm((prev) => ({ ...prev, namespace })),
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);

  const [namespaces] = useNamespaces();

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  useDirtyGuard(dirty);

  const updateField = useCallback(
    <K extends keyof FormState>(field: K, value: FormState[K]) => {
      setDirty(true);
      setForm((prev) => ({ ...prev, [field]: value }));
    },
    [],
  );

  const updateQuota = useCallback(
    <K extends keyof QuotaConfig>(field: K, value: QuotaConfig[K]) => {
      setDirty(true);
      setForm((prev) => ({
        ...prev,
        quota: { ...prev.quota, [field]: value },
      }));
    },
    [],
  );

  const updateLimits = useCallback(
    <K extends keyof LimitConfig>(field: K, value: LimitConfig[K]) => {
      setDirty(true);
      setForm((prev) => ({
        ...prev,
        limits: { ...prev.limits, [field]: value },
      }));
    },
    [],
  );

  const updateResourcePair = useCallback(
    (
      configKey: keyof LimitConfig,
      resourceKey: "cpu" | "memory",
      value: string,
    ) => {
      setDirty(true);
      setForm((prev) => {
        const currentPair = prev.limits[configKey] as ResourcePair | undefined;
        const newPair = { ...(currentPair ?? { cpu: "", memory: "" }) };
        newPair[resourceKey] = value;
        return {
          ...prev,
          limits: { ...prev.limits, [configKey]: newPair },
        };
      });
    },
    [],
  );

  const applyPreset = useCallback((preset: PresetKey) => {
    setDirty(true);
    const p = PRESETS[preset];
    setForm((prev) => ({
      ...prev,
      preset,
      quota: { ...p.quota },
      limits: {
        containerDefault: { ...p.limits.containerDefault },
        containerDefaultRequest: { ...p.limits.containerDefaultRequest },
        containerMax: { ...p.limits.containerMax },
        containerMin: { ...p.limits.containerMin },
      },
    }));
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.namespace) errs.namespace = "Namespace is required";
      if (!f.quotaName || !DNS_LABEL_REGEX.test(f.quotaName)) {
        errs.quotaName =
          "Must be lowercase alphanumeric with hyphens, 1-63 chars";
      }
      if (!f.limitRangeName || !DNS_LABEL_REGEX.test(f.limitRangeName)) {
        errs.limitRangeName =
          "Must be lowercase alphanumeric with hyphens, 1-63 chars";
      }
    }

    if (step === 1) {
      if (!f.quota.cpuHard) errs.cpuHard = "CPU limit is required";
      if (!f.quota.memoryHard) errs.memoryHard = "Memory limit is required";
      if (f.quota.podsHard < 1) errs.podsHard = "Must be at least 1";
    }

    if (step === 2) {
      if (!f.limits.containerDefault.cpu) {
        errs.containerDefaultCpu = "Required";
      }
      if (!f.limits.containerDefault.memory) {
        errs.containerDefaultMemory = "Required";
      }
    }

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep(currentStep)) return;

    if (currentStep === 2) {
      setCurrentStep(3);
      await fetchPreview();
    } else {
      setCurrentStep(currentStep + 1);
    }
  };

  const goBack = () => {
    if (currentStep > 0) setCurrentStep((step) => step - 1);
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = form;
    const payload = {
      namespace: f.namespace,
      quotaName: f.quotaName,
      limitRangeName: f.limitRangeName,
      quota: {
        cpuHard: f.quota.cpuHard,
        memoryHard: f.quota.memoryHard,
        podsHard: f.quota.podsHard,
        ...(f.quota.secretsHard && { secretsHard: f.quota.secretsHard }),
        ...(f.quota.configMapsHard && {
          configMapsHard: f.quota.configMapsHard,
        }),
        ...(f.quota.servicesHard && { servicesHard: f.quota.servicesHard }),
        ...(f.quota.pvcsHard && { pvcsHard: f.quota.pvcsHard }),
        ...(f.quota.gpuHard && { gpuHard: f.quota.gpuHard }),
        ...(f.quota.warnThreshold && { warnThreshold: f.quota.warnThreshold }),
        ...(f.quota.criticalThreshold && {
          criticalThreshold: f.quota.criticalThreshold,
        }),
      },
      limits: {
        containerDefault: f.limits.containerDefault,
        containerDefaultRequest: f.limits.containerDefaultRequest,
        containerMax: f.limits.containerMax,
        containerMin: f.limits.containerMin,
        ...(f.limits.podMax && { podMax: f.limits.podMax }),
        ...(f.limits.pvcMinStorage && {
          pvcMinStorage: f.limits.pvcMinStorage,
        }),
        ...(f.limits.pvcMaxStorage && {
          pvcMaxStorage: f.limits.pvcMaxStorage,
        }),
      },
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/namespace-limits/preview",
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

  const f = form;
  const nextLabel = currentStep === 3 ? "Close" : "Continue";

  return (
    <WizardShell
      title="Create Namespace Limits"
      subtitle={`Step ${currentStep + 1} of 4 · namespace ${f.namespace}`}
      icon={
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
          <path d="M4 4h12v4H4zM4 10h5v6H4zM11 10h5v6h-5z" />
        </svg>
      }
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={currentStep === 3 ? close : goNext}
      nextLabel={nextLabel}
      yaml={currentStep < 3 ? buildManifest(f) : undefined}
    >
      {currentStep === 0 && (
        <NamespacePresetStep
          form={f}
          errors={errors}
          namespaces={namespaces}
          onUpdateField={updateField}
          onApplyPreset={applyPreset}
        />
      )}

      {currentStep === 1 && (
        <QuotaValuesStep
          quota={f.quota}
          errors={errors}
          onUpdateQuota={updateQuota}
        />
      )}

      {currentStep === 2 && (
        <LimitRangeValuesStep
          limits={f.limits}
          errors={errors}
          onUpdateLimits={updateLimits}
          onUpdateResourcePair={updateResourcePair}
        />
      )}

      {currentStep === 3 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/config/namespace-limits"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
