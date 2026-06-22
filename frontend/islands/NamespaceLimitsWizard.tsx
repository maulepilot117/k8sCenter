import { useSignal } from "@preact/signals";
import { useCallback, useRef } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { NamespacePresetStep } from "@/components/wizard/NamespacePresetStep.tsx";
import { QuotaValuesStep } from "@/components/wizard/QuotaValuesStep.tsx";
import { LimitRangeValuesStep } from "@/components/wizard/LimitRangeValuesStep.tsx";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

// ---------------------------------------------------------------------------
// Shared types & constants — exported for step components
// ---------------------------------------------------------------------------

export const PRESETS = {
  small: {
    label: "Small",
    description: "For development or small workloads",
    quota: { cpuHard: "2", memoryHard: "4Gi", podsHard: 10 },
    limits: {
      containerDefault: { cpu: "100m", memory: "128Mi" },
      containerDefaultRequest: { cpu: "50m", memory: "64Mi" },
      containerMax: { cpu: "1", memory: "2Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
  standard: {
    label: "Standard",
    description: "For typical production workloads",
    quota: { cpuHard: "8", memoryHard: "16Gi", podsHard: 20 },
    limits: {
      containerDefault: { cpu: "250m", memory: "256Mi" },
      containerDefaultRequest: { cpu: "100m", memory: "128Mi" },
      containerMax: { cpu: "2", memory: "4Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
  large: {
    label: "Large",
    description: "For resource-intensive workloads",
    quota: { cpuHard: "32", memoryHard: "64Gi", podsHard: 100 },
    limits: {
      containerDefault: { cpu: "500m", memory: "512Mi" },
      containerDefaultRequest: { cpu: "250m", memory: "256Mi" },
      containerMax: { cpu: "4", memory: "8Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
  custom: {
    label: "Custom",
    description: "Configure all values manually",
    quota: { cpuHard: "4", memoryHard: "8Gi", podsHard: 20 },
    limits: {
      containerDefault: { cpu: "200m", memory: "256Mi" },
      containerDefaultRequest: { cpu: "100m", memory: "128Mi" },
      containerMax: { cpu: "2", memory: "4Gi" },
      containerMin: { cpu: "10m", memory: "8Mi" },
    },
  },
} as const;

export type PresetKey = keyof typeof PRESETS;

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
  const ns = initialNamespace();
  const preset = PRESETS.standard;
  return {
    namespace: ns,
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

export default function NamespaceLimitsWizard(
  { onClose }: { onClose?: () => void },
) {
  const close = onClose ?? (() => globalThis.history.back());
  const currentStep = useSignal(0);
  const form = useSignal<FormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);
  const previewGen = useRef(0);

  useDirtyGuard(dirty);

  const updateField = useCallback(
    <K extends keyof FormState>(field: K, value: FormState[K]) => {
      dirty.value = true;
      form.value = { ...form.value, [field]: value };
    },
    [],
  );

  const updateQuota = useCallback(
    <K extends keyof QuotaConfig>(field: K, value: QuotaConfig[K]) => {
      dirty.value = true;
      form.value = {
        ...form.value,
        quota: { ...form.value.quota, [field]: value },
      };
    },
    [],
  );

  const updateLimits = useCallback(
    <K extends keyof LimitConfig>(field: K, value: LimitConfig[K]) => {
      dirty.value = true;
      form.value = {
        ...form.value,
        limits: { ...form.value.limits, [field]: value },
      };
    },
    [],
  );

  const updateResourcePair = useCallback(
    (
      configKey: keyof LimitConfig,
      resourceKey: "cpu" | "memory",
      value: string,
    ) => {
      dirty.value = true;
      const currentPair = form.value.limits[configKey] as
        | ResourcePair
        | undefined;
      const newPair = { ...(currentPair ?? { cpu: "", memory: "" }) };
      newPair[resourceKey] = value;
      form.value = {
        ...form.value,
        limits: { ...form.value.limits, [configKey]: newPair },
      };
    },
    [],
  );

  const applyPreset = useCallback((preset: PresetKey) => {
    dirty.value = true;
    const p = PRESETS[preset];
    form.value = {
      ...form.value,
      preset,
      quota: { ...p.quota },
      limits: {
        containerDefault: { ...p.limits.containerDefault },
        containerDefaultRequest: { ...p.limits.containerDefaultRequest },
        containerMax: { ...p.limits.containerMax },
        containerMin: { ...p.limits.containerMin },
      },
    };
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form.value;
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

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep(currentStep.value)) return;

    if (currentStep.value === 2) {
      currentStep.value = 3;
      await fetchPreview();
    } else {
      currentStep.value++;
    }
  };

  const goBack = () => {
    if (currentStep.value > 0) currentStep.value--;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    const payload = {
      namespace: f.namespace,
      quotaName: f.quotaName,
      limitRangeName: f.limitRangeName,
      quota: {
        cpuHard: f.quota.cpuHard,
        memoryHard: f.quota.memoryHard,
        podsHard: f.quota.podsHard,
        ...(f.quota.secretsHard && { secretsHard: f.quota.secretsHard }),
        ...(f.quota.configMapsHard &&
          { configMapsHard: f.quota.configMapsHard }),
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
        ...(f.limits.pvcMinStorage &&
          { pvcMinStorage: f.limits.pvcMinStorage }),
        ...(f.limits.pvcMaxStorage &&
          { pvcMaxStorage: f.limits.pvcMaxStorage }),
      },
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/namespace-limits/preview",
        payload,
      );
      if (gen !== previewGen.current) return;
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      if (gen !== previewGen.current) return;
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      if (gen === previewGen.current) previewLoading.value = false;
    }
  };

  const f = form.value;
  const nextLabel = currentStep.value === 3 ? "Close" : "Continue";

  return (
    <WizardShell
      title="Create Namespace Limits"
      subtitle={`Step ${currentStep.value + 1} of 4 · namespace ${f.namespace}`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M4 4h12v4H4zM4 10h5v6H4zM11 10h5v6h-5z" />
        </svg>
      }
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i < currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={goBack}
      onNext={currentStep.value === 3 ? close : goNext}
      nextLabel={nextLabel}
      yaml={currentStep.value < 3 ? buildManifest(f) : undefined}
    >
      {currentStep.value === 0 && (
        <NamespacePresetStep
          form={f}
          errors={errors}
          namespaces={namespaces}
          onUpdateField={updateField}
          onApplyPreset={applyPreset}
        />
      )}

      {currentStep.value === 1 && (
        <QuotaValuesStep
          quota={f.quota}
          errors={errors}
          onUpdateQuota={updateQuota}
        />
      )}

      {currentStep.value === 2 && (
        <LimitRangeValuesStep
          limits={f.limits}
          errors={errors}
          onUpdateLimits={updateLimits}
          onUpdateResourcePair={updateResourcePair}
        />
      )}

      {currentStep.value === 3 && (
        <WizardReviewStep
          yaml={previewYaml.value}
          onYamlChange={(v) => {
            previewYaml.value = v;
          }}
          loading={previewLoading.value}
          error={previewError.value}
          detailBasePath="/config/namespace-limits"
        />
      )}
    </WizardShell>
  );
}
