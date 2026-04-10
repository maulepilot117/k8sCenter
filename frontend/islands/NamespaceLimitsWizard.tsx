import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

// Preset configurations
const PRESETS = {
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

type PresetKey = keyof typeof PRESETS;

interface ResourcePair {
  cpu: string;
  memory: string;
}

interface QuotaConfig {
  cpuHard: string;
  memoryHard: string;
  podsHard: number;
  // Advanced
  secretsHard?: number;
  configMapsHard?: number;
  servicesHard?: number;
  pvcsHard?: number;
  gpuHard?: string;
  warnThreshold?: number;
  criticalThreshold?: number;
}

interface LimitConfig {
  containerDefault: ResourcePair;
  containerDefaultRequest: ResourcePair;
  containerMax: ResourcePair;
  containerMin: ResourcePair;
  // Advanced
  podMax?: ResourcePair;
  pvcMinStorage?: string;
  pvcMaxStorage?: string;
}

interface FormState {
  namespace: string;
  preset: PresetKey;
  quotaName: string;
  limitRangeName: string;
  quota: QuotaConfig;
  limits: LimitConfig;
}

const STEPS = [
  { title: "Namespace & Preset" },
  { title: "Quota Values" },
  { title: "LimitRange Values" },
  { title: "Review" },
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

export default function NamespaceLimitsWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<FormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const showAdvancedQuota = useSignal(false);
  const showAdvancedLimits = useSignal(false);

  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

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

  const f = form.value;

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-text-primary">
          Create Namespace Limits
        </h1>
        <a
          href="/config/namespace-limits"
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
        {/* Step 1: Namespace & Preset */}
        {currentStep.value === 0 && (
          <div class="mx-auto max-w-lg space-y-6">
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Namespace <span class="text-error">*</span>
              </label>
              <select
                value={f.namespace}
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
                <p class="mt-1 text-xs text-error">{errors.value.namespace}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Preset
              </label>
              <div class="mt-2 grid grid-cols-2 gap-3">
                {(Object.keys(PRESETS) as PresetKey[]).map((key) => {
                  const p = PRESETS[key];
                  const isSelected = f.preset === key;
                  return (
                    <button
                      key={key}
                      type="button"
                      onClick={() => applyPreset(key)}
                      class={`rounded-lg border p-3 text-left transition-colors ${
                        isSelected
                          ? "border-brand bg-brand/5"
                          : "border-border-primary hover:border-text-muted"
                      }`}
                    >
                      <div class="font-medium text-text-primary">{p.label}</div>
                      <div class="text-xs text-text-muted">{p.description}</div>
                    </button>
                  );
                })}
              </div>
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                ResourceQuota Name <span class="text-error">*</span>
              </label>
              <input
                type="text"
                value={f.quotaName}
                onInput={(e) =>
                  updateField(
                    "quotaName",
                    (e.target as HTMLInputElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
                placeholder="e.g. default-quota"
              />
              {errors.value.quotaName && (
                <p class="mt-1 text-xs text-error">{errors.value.quotaName}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                LimitRange Name <span class="text-error">*</span>
              </label>
              <input
                type="text"
                value={f.limitRangeName}
                onInput={(e) =>
                  updateField(
                    "limitRangeName",
                    (e.target as HTMLInputElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
                placeholder="e.g. default-limits"
              />
              {errors.value.limitRangeName && (
                <p class="mt-1 text-xs text-error">
                  {errors.value.limitRangeName}
                </p>
              )}
            </div>
          </div>
        )}

        {/* Step 2: Quota Values */}
        {currentStep.value === 1 && (
          <div class="mx-auto max-w-lg space-y-4">
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="block text-sm font-medium text-text-secondary">
                  CPU Hard Limit <span class="text-error">*</span>
                </label>
                <input
                  type="text"
                  value={f.quota.cpuHard}
                  onInput={(e) =>
                    updateQuota(
                      "cpuHard",
                      (e.target as HTMLInputElement).value,
                    )}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="e.g. 8 or 8000m"
                />
                {errors.value.cpuHard && (
                  <p class="mt-1 text-xs text-error">{errors.value.cpuHard}</p>
                )}
              </div>

              <div>
                <label class="block text-sm font-medium text-text-secondary">
                  Memory Hard Limit <span class="text-error">*</span>
                </label>
                <input
                  type="text"
                  value={f.quota.memoryHard}
                  onInput={(e) =>
                    updateQuota(
                      "memoryHard",
                      (e.target as HTMLInputElement).value,
                    )}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="e.g. 16Gi"
                />
                {errors.value.memoryHard && (
                  <p class="mt-1 text-xs text-error">
                    {errors.value.memoryHard}
                  </p>
                )}
              </div>
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Max Pods <span class="text-error">*</span>
              </label>
              <input
                type="number"
                min={1}
                max={1000}
                value={f.quota.podsHard}
                onInput={(e) =>
                  updateQuota(
                    "podsHard",
                    parseInt((e.target as HTMLInputElement).value) || 1,
                  )}
                class={WIZARD_INPUT_CLASS}
              />
              {errors.value.podsHard && (
                <p class="mt-1 text-xs text-error">{errors.value.podsHard}</p>
              )}
            </div>

            <button
              type="button"
              onClick={() => {
                showAdvancedQuota.value = !showAdvancedQuota.value;
              }}
              class="text-sm text-brand hover:text-brand/80 flex items-center gap-1"
            >
              <svg
                class={`w-4 h-4 transition-transform ${
                  showAdvancedQuota.value ? "rotate-90" : ""
                }`}
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M9 5l7 7-7 7"
                />
              </svg>
              {showAdvancedQuota.value ? "Hide" : "Show"} advanced options
            </button>

            {showAdvancedQuota.value && (
              <div class="space-y-4 rounded-lg border border-border-primary p-4">
                <h4 class="text-sm font-medium text-text-muted">
                  Count Limits (Optional)
                </h4>
                <div class="grid grid-cols-2 gap-4">
                  <div>
                    <label class="block text-xs text-text-muted">Secrets</label>
                    <input
                      type="number"
                      min={0}
                      value={f.quota.secretsHard ?? ""}
                      onInput={(e) => {
                        const v = parseInt(
                          (e.target as HTMLInputElement).value,
                        );
                        updateQuota("secretsHard", isNaN(v) ? undefined : v);
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="No limit"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">
                      ConfigMaps
                    </label>
                    <input
                      type="number"
                      min={0}
                      value={f.quota.configMapsHard ?? ""}
                      onInput={(e) => {
                        const v = parseInt(
                          (e.target as HTMLInputElement).value,
                        );
                        updateQuota("configMapsHard", isNaN(v) ? undefined : v);
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="No limit"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">
                      Services
                    </label>
                    <input
                      type="number"
                      min={0}
                      value={f.quota.servicesHard ?? ""}
                      onInput={(e) => {
                        const v = parseInt(
                          (e.target as HTMLInputElement).value,
                        );
                        updateQuota("servicesHard", isNaN(v) ? undefined : v);
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="No limit"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">PVCs</label>
                    <input
                      type="number"
                      min={0}
                      value={f.quota.pvcsHard ?? ""}
                      onInput={(e) => {
                        const v = parseInt(
                          (e.target as HTMLInputElement).value,
                        );
                        updateQuota("pvcsHard", isNaN(v) ? undefined : v);
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="No limit"
                    />
                  </div>
                </div>

                <h4 class="text-sm font-medium text-text-muted mt-4">
                  GPU Limit
                </h4>
                <input
                  type="text"
                  value={f.quota.gpuHard ?? ""}
                  onInput={(e) =>
                    updateQuota(
                      "gpuHard",
                      (e.target as HTMLInputElement).value || undefined,
                    )}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="e.g. 1 (nvidia.com/gpu)"
                />

                <h4 class="text-sm font-medium text-text-muted mt-4">
                  Alert Thresholds (%)
                </h4>
                <div class="grid grid-cols-2 gap-4">
                  <div>
                    <label class="block text-xs text-text-muted">
                      Warning (default: 80)
                    </label>
                    <input
                      type="number"
                      min={1}
                      max={100}
                      value={f.quota.warnThreshold ?? ""}
                      onInput={(e) => {
                        const v = parseInt(
                          (e.target as HTMLInputElement).value,
                        );
                        updateQuota("warnThreshold", isNaN(v) ? undefined : v);
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="80"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">
                      Critical (default: 95)
                    </label>
                    <input
                      type="number"
                      min={1}
                      max={100}
                      value={f.quota.criticalThreshold ?? ""}
                      onInput={(e) => {
                        const v = parseInt(
                          (e.target as HTMLInputElement).value,
                        );
                        updateQuota(
                          "criticalThreshold",
                          isNaN(v) ? undefined : v,
                        );
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="95"
                    />
                  </div>
                </div>
              </div>
            )}
          </div>
        )}

        {/* Step 3: LimitRange Values */}
        {currentStep.value === 2 && (
          <div class="mx-auto max-w-lg space-y-4">
            <h3 class="text-sm font-semibold text-text-primary">
              Container Limits
            </h3>

            <div class="rounded-lg border border-border-primary p-4 space-y-4">
              <div>
                <label class="block text-sm font-medium text-text-secondary">
                  Default Limits (applied to containers without explicit limits)
                </label>
                <div class="grid grid-cols-2 gap-4 mt-2">
                  <div>
                    <label class="block text-xs text-text-muted">CPU</label>
                    <input
                      type="text"
                      value={f.limits.containerDefault.cpu}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerDefault",
                          "cpu",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 250m"
                    />
                    {errors.value.containerDefaultCpu && (
                      <p class="mt-1 text-xs text-error">
                        {errors.value.containerDefaultCpu}
                      </p>
                    )}
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">Memory</label>
                    <input
                      type="text"
                      value={f.limits.containerDefault.memory}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerDefault",
                          "memory",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 256Mi"
                    />
                    {errors.value.containerDefaultMemory && (
                      <p class="mt-1 text-xs text-error">
                        {errors.value.containerDefaultMemory}
                      </p>
                    )}
                  </div>
                </div>
              </div>

              <div>
                <label class="block text-sm font-medium text-text-secondary">
                  Default Requests
                </label>
                <div class="grid grid-cols-2 gap-4 mt-2">
                  <div>
                    <label class="block text-xs text-text-muted">CPU</label>
                    <input
                      type="text"
                      value={f.limits.containerDefaultRequest.cpu}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerDefaultRequest",
                          "cpu",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 100m"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">Memory</label>
                    <input
                      type="text"
                      value={f.limits.containerDefaultRequest.memory}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerDefaultRequest",
                          "memory",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 128Mi"
                    />
                  </div>
                </div>
              </div>

              <div>
                <label class="block text-sm font-medium text-text-secondary">
                  Maximum Limits
                </label>
                <div class="grid grid-cols-2 gap-4 mt-2">
                  <div>
                    <label class="block text-xs text-text-muted">CPU</label>
                    <input
                      type="text"
                      value={f.limits.containerMax.cpu}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerMax",
                          "cpu",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 2"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">Memory</label>
                    <input
                      type="text"
                      value={f.limits.containerMax.memory}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerMax",
                          "memory",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 4Gi"
                    />
                  </div>
                </div>
              </div>

              <div>
                <label class="block text-sm font-medium text-text-secondary">
                  Minimum Limits
                </label>
                <div class="grid grid-cols-2 gap-4 mt-2">
                  <div>
                    <label class="block text-xs text-text-muted">CPU</label>
                    <input
                      type="text"
                      value={f.limits.containerMin.cpu}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerMin",
                          "cpu",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 10m"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">Memory</label>
                    <input
                      type="text"
                      value={f.limits.containerMin.memory}
                      onInput={(e) =>
                        updateResourcePair(
                          "containerMin",
                          "memory",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 8Mi"
                    />
                  </div>
                </div>
              </div>
            </div>

            <button
              type="button"
              onClick={() => {
                showAdvancedLimits.value = !showAdvancedLimits.value;
              }}
              class="text-sm text-brand hover:text-brand/80 flex items-center gap-1"
            >
              <svg
                class={`w-4 h-4 transition-transform ${
                  showAdvancedLimits.value ? "rotate-90" : ""
                }`}
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M9 5l7 7-7 7"
                />
              </svg>
              {showAdvancedLimits.value ? "Hide" : "Show"} advanced options
            </button>

            {showAdvancedLimits.value && (
              <div class="space-y-4 rounded-lg border border-border-primary p-4">
                <h4 class="text-sm font-medium text-text-muted">
                  Pod Limits (Optional)
                </h4>
                <div class="grid grid-cols-2 gap-4">
                  <div>
                    <label class="block text-xs text-text-muted">
                      Max CPU per Pod
                    </label>
                    <input
                      type="text"
                      value={f.limits.podMax?.cpu ?? ""}
                      onInput={(e) => {
                        const cpu = (e.target as HTMLInputElement).value;
                        const current = f.limits.podMax ?? {
                          cpu: "",
                          memory: "",
                        };
                        updateLimits(
                          "podMax",
                          cpu || current.memory
                            ? { ...current, cpu }
                            : undefined,
                        );
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="No limit"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">
                      Max Memory per Pod
                    </label>
                    <input
                      type="text"
                      value={f.limits.podMax?.memory ?? ""}
                      onInput={(e) => {
                        const memory = (e.target as HTMLInputElement).value;
                        const current = f.limits.podMax ?? {
                          cpu: "",
                          memory: "",
                        };
                        updateLimits(
                          "podMax",
                          memory || current.cpu
                            ? { ...current, memory }
                            : undefined,
                        );
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="No limit"
                    />
                  </div>
                </div>

                <h4 class="text-sm font-medium text-text-muted mt-4">
                  PVC Storage Limits (Optional)
                </h4>
                <div class="grid grid-cols-2 gap-4">
                  <div>
                    <label class="block text-xs text-text-muted">
                      Min Storage
                    </label>
                    <input
                      type="text"
                      value={f.limits.pvcMinStorage ?? ""}
                      onInput={(e) =>
                        updateLimits(
                          "pvcMinStorage",
                          (e.target as HTMLInputElement).value || undefined,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 1Gi"
                    />
                  </div>
                  <div>
                    <label class="block text-xs text-text-muted">
                      Max Storage
                    </label>
                    <input
                      type="text"
                      value={f.limits.pvcMaxStorage ?? ""}
                      onInput={(e) =>
                        updateLimits(
                          "pvcMaxStorage",
                          (e.target as HTMLInputElement).value || undefined,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 100Gi"
                    />
                  </div>
                </div>
              </div>
            )}
          </div>
        )}

        {/* Step 4: Review */}
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
      </div>

      {/* Navigation buttons */}
      {currentStep.value < 3 && (
        <div class="mt-8 flex justify-between">
          {currentStep.value > 0
            ? (
              <Button variant="ghost" onClick={goBack}>
                Back
              </Button>
            )
            : <div />}
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === 2 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === 3 && !previewLoading.value &&
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
