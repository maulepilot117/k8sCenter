import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

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

export default function NamespaceLimitsWizard(
  { onClose }: { onClose?: () => void },
) {
  const close = onClose ?? (() => globalThis.history.back());
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

  const f = form.value;

  const nextLabel = currentStep.value === 2
    ? "Preview YAML"
    : currentStep.value === 3
    ? "Close"
    : "Next";

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
      {/* Step 0: Namespace & Preset */}
      {currentStep.value === 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "20px",
            maxWidth: "480px",
          }}
        >
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              Namespace <span style={{ color: "var(--danger)" }}>*</span>
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
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.namespace}
              </p>
            )}
          </div>

          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "8px",
              }}
            >
              Preset
            </label>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1fr 1fr",
                gap: "8px",
              }}
            >
              {(Object.keys(PRESETS) as PresetKey[]).map((key) => {
                const p = PRESETS[key];
                const isSelected = f.preset === key;
                return (
                  <button
                    key={key}
                    type="button"
                    onClick={() => applyPreset(key)}
                    style={{
                      borderRadius: "10px",
                      border: `1.5px solid ${
                        isSelected ? "var(--accent)" : "var(--border-subtle)"
                      }`,
                      padding: "10px 12px",
                      textAlign: "left",
                      cursor: "pointer",
                      background: isSelected
                        ? "var(--accent-dim)"
                        : "var(--bg-elevated)",
                      transition: "background 0.12s, border-color 0.12s",
                    }}
                  >
                    <div
                      style={{
                        fontSize: "13px",
                        fontWeight: 600,
                        color: "var(--text-primary)",
                      }}
                    >
                      {p.label}
                    </div>
                    <div
                      style={{
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginTop: "2px",
                      }}
                    >
                      {p.description}
                    </div>
                  </button>
                );
              })}
            </div>
          </div>

          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              ResourceQuota Name{" "}
              <span style={{ color: "var(--danger)" }}>*</span>
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
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.quotaName}
              </p>
            )}
          </div>

          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              LimitRange Name <span style={{ color: "var(--danger)" }}>*</span>
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
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.limitRangeName}
              </p>
            )}
          </div>
        </div>
      )}

      {/* Step 1: Quota Values */}
      {currentStep.value === 1 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "16px",
            maxWidth: "480px",
          }}
        >
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "12px",
            }}
          >
            <div>
              <label
                style={{
                  display: "block",
                  fontSize: "12.5px",
                  fontWeight: 600,
                  color: "var(--text-secondary)",
                  marginBottom: "5px",
                }}
              >
                CPU Hard Limit <span style={{ color: "var(--danger)" }}>*</span>
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
                <p
                  style={{
                    marginTop: "4px",
                    fontSize: "11px",
                    color: "var(--danger)",
                  }}
                >
                  {errors.value.cpuHard}
                </p>
              )}
            </div>
            <div>
              <label
                style={{
                  display: "block",
                  fontSize: "12.5px",
                  fontWeight: 600,
                  color: "var(--text-secondary)",
                  marginBottom: "5px",
                }}
              >
                Memory Hard Limit{" "}
                <span style={{ color: "var(--danger)" }}>*</span>
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
                <p
                  style={{
                    marginTop: "4px",
                    fontSize: "11px",
                    color: "var(--danger)",
                  }}
                >
                  {errors.value.memoryHard}
                </p>
              )}
            </div>
          </div>

          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              Max Pods <span style={{ color: "var(--danger)" }}>*</span>
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
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.podsHard}
              </p>
            )}
          </div>

          {/* Advanced toggle */}
          <button
            type="button"
            onClick={() => {
              showAdvancedQuota.value = !showAdvancedQuota.value;
            }}
            style={{
              alignSelf: "flex-start",
              display: "flex",
              alignItems: "center",
              gap: "5px",
              fontSize: "12.5px",
              color: "var(--accent)",
              background: "transparent",
              border: "none",
              cursor: "pointer",
              padding: 0,
              fontWeight: 600,
            }}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 20 20"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              style={{
                transform: showAdvancedQuota.value
                  ? "rotate(90deg)"
                  : "rotate(0deg)",
                transition: "transform 0.15s",
              }}
            >
              <path d="M7 5l5 5-5 5" />
            </svg>
            {showAdvancedQuota.value ? "Hide" : "Show"} advanced options
          </button>

          {showAdvancedQuota.value && (
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "14px",
                borderRadius: "10px",
                border: "1px solid var(--border-subtle)",
                padding: "14px",
              }}
            >
              <p
                style={{
                  margin: 0,
                  fontSize: "11.5px",
                  fontWeight: 600,
                  color: "var(--text-muted)",
                }}
              >
                Count Limits (Optional)
              </p>
              <div
                style={{
                  display: "grid",
                  gridTemplateColumns: "1fr 1fr",
                  gap: "10px",
                }}
              >
                {(
                  [
                    ["secretsHard", "Secrets"],
                    ["configMapsHard", "ConfigMaps"],
                    ["servicesHard", "Services"],
                    ["pvcsHard", "PVCs"],
                  ] as const
                ).map(([field, label]) => (
                  <div key={field}>
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
                      {label}
                    </label>
                    <input
                      type="number"
                      min={0}
                      value={f.quota[field] ?? ""}
                      onInput={(e) => {
                        const v = parseInt(
                          (e.target as HTMLInputElement).value,
                        );
                        updateQuota(field, isNaN(v) ? undefined : v);
                      }}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="No limit"
                    />
                  </div>
                ))}
              </div>

              <div>
                <label
                  style={{
                    display: "block",
                    fontSize: "11.5px",
                    fontWeight: 600,
                    color: "var(--text-muted)",
                    marginBottom: "5px",
                  }}
                >
                  GPU Limit
                </label>
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
              </div>

              <div>
                <label
                  style={{
                    display: "block",
                    fontSize: "11.5px",
                    fontWeight: 600,
                    color: "var(--text-muted)",
                    marginBottom: "8px",
                  }}
                >
                  Alert Thresholds (%)
                </label>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr 1fr",
                    gap: "10px",
                  }}
                >
                  <div>
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
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
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
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
            </div>
          )}
        </div>
      )}

      {/* Step 2: LimitRange Values */}
      {currentStep.value === 2 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "16px",
            maxWidth: "480px",
          }}
        >
          <h3
            style={{
              margin: 0,
              fontSize: "13.5px",
              fontWeight: 600,
              color: "var(--text-primary)",
            }}
          >
            Container Limits
          </h3>

          <div
            style={{
              borderRadius: "10px",
              border: "1px solid var(--border-subtle)",
              padding: "14px",
              display: "flex",
              flexDirection: "column",
              gap: "14px",
            }}
          >
            {(
              [
                [
                  "containerDefault",
                  "Default Limits",
                  "Applied to containers without explicit limits",
                  true,
                ],
                ["containerDefaultRequest", "Default Requests", null, false],
                ["containerMax", "Maximum Limits", null, false],
                ["containerMin", "Minimum Limits", null, false],
              ] as const
            ).map(([key, title, hint, required]) => (
              <div key={key}>
                <label
                  style={{
                    display: "block",
                    fontSize: "12.5px",
                    fontWeight: 600,
                    color: "var(--text-secondary)",
                    marginBottom: hint ? "2px" : "6px",
                  }}
                >
                  {title}
                  {required && (
                    <span style={{ color: "var(--danger)" }}>*</span>
                  )}
                </label>
                {hint && (
                  <p
                    style={{
                      margin: "0 0 6px",
                      fontSize: "11px",
                      color: "var(--text-muted)",
                    }}
                  >
                    {hint}
                  </p>
                )}
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr 1fr",
                    gap: "10px",
                  }}
                >
                  <div>
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
                      CPU
                    </label>
                    <input
                      type="text"
                      value={(f.limits[key] as ResourcePair | undefined)?.cpu ??
                        ""}
                      onInput={(e) =>
                        updateResourcePair(
                          key,
                          "cpu",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 250m"
                    />
                    {key === "containerDefault" &&
                      errors.value.containerDefaultCpu && (
                      <p
                        style={{
                          marginTop: "3px",
                          fontSize: "10px",
                          color: "var(--danger)",
                        }}
                      >
                        {errors.value.containerDefaultCpu}
                      </p>
                    )}
                  </div>
                  <div>
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
                      Memory
                    </label>
                    <input
                      type="text"
                      value={(f.limits[key] as ResourcePair | undefined)
                        ?.memory ?? ""}
                      onInput={(e) =>
                        updateResourcePair(
                          key,
                          "memory",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="e.g. 256Mi"
                    />
                    {key === "containerDefault" &&
                      errors.value.containerDefaultMemory && (
                      <p
                        style={{
                          marginTop: "3px",
                          fontSize: "10px",
                          color: "var(--danger)",
                        }}
                      >
                        {errors.value.containerDefaultMemory}
                      </p>
                    )}
                  </div>
                </div>
              </div>
            ))}
          </div>

          {/* Advanced toggle */}
          <button
            type="button"
            onClick={() => {
              showAdvancedLimits.value = !showAdvancedLimits.value;
            }}
            style={{
              alignSelf: "flex-start",
              display: "flex",
              alignItems: "center",
              gap: "5px",
              fontSize: "12.5px",
              color: "var(--accent)",
              background: "transparent",
              border: "none",
              cursor: "pointer",
              padding: 0,
              fontWeight: 600,
            }}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 20 20"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              style={{
                transform: showAdvancedLimits.value
                  ? "rotate(90deg)"
                  : "rotate(0deg)",
                transition: "transform 0.15s",
              }}
            >
              <path d="M7 5l5 5-5 5" />
            </svg>
            {showAdvancedLimits.value ? "Hide" : "Show"} advanced options
          </button>

          {showAdvancedLimits.value && (
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "14px",
                borderRadius: "10px",
                border: "1px solid var(--border-subtle)",
                padding: "14px",
              }}
            >
              <div>
                <p
                  style={{
                    margin: "0 0 8px",
                    fontSize: "11.5px",
                    fontWeight: 600,
                    color: "var(--text-muted)",
                  }}
                >
                  Pod Limits (Optional)
                </p>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr 1fr",
                    gap: "10px",
                  }}
                >
                  <div>
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
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
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
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
              </div>

              <div>
                <p
                  style={{
                    margin: "0 0 8px",
                    fontSize: "11.5px",
                    fontWeight: 600,
                    color: "var(--text-muted)",
                  }}
                >
                  PVC Storage Limits (Optional)
                </p>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr 1fr",
                    gap: "10px",
                  }}
                >
                  <div>
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
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
                    <label
                      style={{
                        display: "block",
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginBottom: "3px",
                      }}
                    >
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
            </div>
          )}
        </div>
      )}

      {/* Step 3: Review */}
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
