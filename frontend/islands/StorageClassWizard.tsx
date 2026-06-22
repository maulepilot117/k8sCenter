import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet, apiPost } from "@/lib/api.ts";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Select from "@/components/ui/form/Select.tsx";
import Toggle from "@/components/ui/form/Toggle.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";

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

const STEPS: WizardStep[] = [
  { label: "Basics", sub: "Name & provisioner" },
  { label: "Parameters", sub: "Driver config & options" },
  { label: "Review", sub: "Preview & apply" },
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

function buildManifest(f: FormState): string {
  const annotations = f.isDefault
    ? `\n  annotations:\n    storageclass.kubernetes.io/is-default-class: "true"`
    : "";
  const params = f.parameters.filter((p) => p.key).map((p) =>
    `\n  ${p.key}: "${p.value}"`
  ).join("");
  const paramsBlock = params ? `\nparameters:${params}` : "";
  const opts = f.mountOptions.map((o) => o.value).filter(Boolean);
  const mountBlock = opts.length > 0
    ? `\nmountOptions:\n${opts.map((o) => `  - ${o}`).join("\n")}`
    : "";
  return `apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ${f.name || "<name>"}${annotations}
provisioner: ${f.provisioner || "<provisioner>"}
reclaimPolicy: ${f.reclaimPolicy}
volumeBindingMode: ${f.volumeBindingMode}
allowVolumeExpansion: ${f.allowVolumeExpansion}${paramsBlock}${mountBlock}`;
}

export default function StorageClassWizard(
  { onClose }: { onClose?: () => void },
) {
  // When rendered as a full-page route, no onClose is passed (Fresh cannot
  // serialize a function prop across the route→island boundary). Fall back to
  // browser history so the wizard chrome's Cancel/Done still work.
  const close = onClose ?? (() => globalThis.history.back());
  const step = useSignal(0);
  const form = useSignal<FormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const presets = useSignal<Record<string, PresetInfo>>({});

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useEffect(() => {
    apiGet<Record<string, PresetInfo>>("/v1/storage/presets")
      .then((resp) => {
        if (resp.data) presets.value = resp.data;
      })
      .catch(() => {});
  }, []);

  const updateField = (field: string, value: unknown) => {
    form.value = { ...form.value, [field]: value };
  };

  const applyPreset = (driverName: string) => {
    const preset = presets.value[driverName];
    if (!preset) return;
    form.value = {
      ...form.value,
      provisioner: driverName,
      parameters: Object.entries(preset.parameters).map(([key, p]) => ({
        key,
        value: p.default,
      })),
    };
  };

  const validateStep = (s: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};
    if (s === 0) {
      if (!f.name || !/^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(f.name)) {
        errs.name =
          "Must be a valid DNS subdomain (lowercase, hyphens, dots, max 253)";
      }
      if (!f.provisioner) errs.provisioner = "Required";
    }
    errors.value = errs;
    return Object.keys(errs).length === 0;
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
    const params: Record<string, string> = {};
    for (const p of f.parameters) {
      if (p.key) params[p.key] = p.value;
    }
    if (Object.keys(params).length > 0) payload.parameters = params;
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

  const handleNext = async () => {
    if (!validateStep(step.value)) return;
    if (step.value === 1) {
      step.value = 2;
      await fetchPreview();
    } else if (step.value < 2) {
      step.value += 1;
    } else {
      close();
    }
  };

  const presetNames = Object.keys(presets.value);
  const f = form.value;

  return (
    <WizardShell
      title="Create StorageClass"
      subtitle={`Step ${step.value + 1} of 3`}
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
          <ellipse cx="10" cy="6" rx="7" ry="3" />
          <path d="M3 6v4c0 1.66 3.13 3 7 3s7-1.34 7-3V6" />
          <path d="M3 10v4c0 1.66 3.13 3 7 3s7-1.34 7-3v-4" />
        </svg>
      }
      steps={STEPS}
      current={step.value}
      onStep={(i) => {
        if (i < step.value) step.value = i;
      }}
      onCancel={close}
      onBack={() => (step.value = Math.max(0, step.value - 1))}
      onNext={handleNext}
      nextLabel={step.value === 2 ? "Done" : "Continue"}
      yaml={step.value < 2 ? buildManifest(f) : undefined}
    >
      {step.value === 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "18px",
            maxWidth: "480px",
          }}
        >
          {/* Preset quick-start */}
          {presetNames.length > 0 && (
            <div>
              <div
                style={{
                  fontSize: "12px",
                  fontWeight: 600,
                  color: "var(--text-secondary)",
                  marginBottom: "8px",
                  textTransform: "uppercase",
                  letterSpacing: "0.04em",
                }}
              >
                Quick Start (optional)
              </div>
              <div style={{ display: "flex", gap: "8px", flexWrap: "wrap" }}>
                {presetNames.map((driver) => (
                  <button
                    key={driver}
                    type="button"
                    onClick={() => applyPreset(driver)}
                    style={{
                      padding: "6px 14px",
                      fontSize: "12px",
                      borderRadius: "8px",
                      border: `1px solid ${
                        f.provisioner === driver
                          ? "var(--accent)"
                          : "var(--border-subtle)"
                      }`,
                      background: f.provisioner === driver
                        ? "var(--accent-dim)"
                        : "transparent",
                      color: f.provisioner === driver
                        ? "var(--accent)"
                        : "var(--text-secondary)",
                      cursor: "pointer",
                      fontWeight: 600,
                      fontFamily: "inherit",
                      transition: "all 0.15s",
                    }}
                  >
                    {presets.value[driver].displayName}
                  </button>
                ))}
              </div>
            </div>
          )}

          <Field label="Name">
            <TextField
              value={f.name}
              onInput={(v) => updateField("name", v)}
              placeholder="e.g. fast-storage"
            />
            {errors.value.name && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.value.name}
              </p>
            )}
          </Field>

          <Field label="Provisioner">
            <TextField
              value={f.provisioner}
              onInput={(v) => updateField("provisioner", v)}
              mono
              placeholder="e.g. ebs.csi.aws.com"
            />
            {errors.value.provisioner && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.value.provisioner}
              </p>
            )}
          </Field>

          <Field label="Reclaim Policy">
            <Select
              value={f.reclaimPolicy}
              options={["Delete", "Retain"]}
              onChange={(v) => updateField("reclaimPolicy", v)}
            />
          </Field>

          <Field label="Volume Binding Mode">
            <Select
              value={f.volumeBindingMode}
              options={["Immediate", "WaitForFirstConsumer"]}
              onChange={(v) => updateField("volumeBindingMode", v)}
            />
          </Field>

          <div
            style={{ display: "flex", flexDirection: "column", gap: "12px" }}
          >
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "16px",
                padding: "12px 14px",
                borderRadius: "10px",
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              <div>
                <div
                  style={{
                    fontSize: "13px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                  }}
                >
                  Allow Volume Expansion
                </div>
                <div
                  style={{
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    marginTop: "1px",
                  }}
                >
                  Permit resizing PVCs backed by this class
                </div>
              </div>
              <Toggle
                checked={f.allowVolumeExpansion}
                onChange={(v) => updateField("allowVolumeExpansion", v)}
              />
            </div>

            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "16px",
                padding: "12px 14px",
                borderRadius: "10px",
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              <div>
                <div
                  style={{
                    fontSize: "13px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                  }}
                >
                  Set as Default
                </div>
                <div
                  style={{
                    fontSize: "12px",
                    color: "var(--text-muted)",
                    marginTop: "1px",
                  }}
                >
                  Use this StorageClass when none is specified
                </div>
              </div>
              <Toggle
                checked={f.isDefault}
                onChange={(v) => updateField("isDefault", v)}
              />
            </div>
          </div>
        </div>
      )}

      {step.value === 1 && (
        <ParametersStep
          form={f}
          presets={presets.value}
          onChange={updateField}
        />
      )}

      {step.value === 2 && (
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
    </WizardShell>
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

  const inputStyle = {
    width: "100%",
    padding: "8px 12px",
    border: "1px solid var(--border-subtle)",
    borderRadius: "8px",
    background: "var(--bg-elevated)",
    color: "var(--text-primary)",
    fontSize: "13px",
    fontFamily: "inherit",
    outline: "none",
  };

  const removeBtn = {
    padding: "6px 10px",
    border: "none",
    background: "transparent",
    color: "var(--text-muted)",
    cursor: "pointer",
    fontSize: "16px",
    lineHeight: 1,
    borderRadius: "6px",
    fontFamily: "inherit",
  };

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "28px",
        maxWidth: "540px",
      }}
    >
      {/* Parameters */}
      <div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            marginBottom: "10px",
          }}
        >
          <span
            style={{
              fontSize: "13px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}
          >
            Parameters
          </span>
          <button
            type="button"
            onClick={addParameter}
            style={{
              padding: "5px 12px",
              fontSize: "12px",
              fontWeight: 600,
              border: "1px solid var(--border-subtle)",
              borderRadius: "7px",
              background: "transparent",
              color: "var(--text-secondary)",
              cursor: "pointer",
              fontFamily: "inherit",
            }}
          >
            + Add
          </button>
        </div>

        {form.parameters.length === 0 && (
          <p
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              padding: "8px 0",
            }}
          >
            {preset
              ? "Preset parameters were applied from Quick Start."
              : "No parameters yet. Add driver-specific key-value pairs."}
          </p>
        )}

        <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
          {form.parameters.map((p, i) => {
            const presetParam = preset?.parameters[p.key];
            return (
              <div
                key={i}
                style={{
                  display: "flex",
                  gap: "8px",
                  alignItems: "flex-start",
                }}
              >
                <div style={{ flex: 1 }}>
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
                    style={inputStyle}
                  />
                  {presetParam && (
                    <p
                      style={{
                        fontSize: "11px",
                        color: "var(--text-muted)",
                        marginTop: "3px",
                      }}
                    >
                      {presetParam.description}
                    </p>
                  )}
                </div>
                <div style={{ flex: 1 }}>
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
                        style={inputStyle}
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
                        style={inputStyle}
                      />
                    )}
                </div>
                <button
                  type="button"
                  onClick={() => removeParameter(i)}
                  style={removeBtn}
                >
                  ×
                </button>
              </div>
            );
          })}
        </div>
      </div>

      {/* Mount Options */}
      <div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            marginBottom: "10px",
          }}
        >
          <span
            style={{
              fontSize: "13px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}
          >
            Mount Options
          </span>
          <button
            type="button"
            onClick={addMountOption}
            style={{
              padding: "5px 12px",
              fontSize: "12px",
              fontWeight: 600,
              border: "1px solid var(--border-subtle)",
              borderRadius: "7px",
              background: "transparent",
              color: "var(--text-secondary)",
              cursor: "pointer",
              fontFamily: "inherit",
            }}
          >
            + Add
          </button>
        </div>

        {form.mountOptions.length === 0 && (
          <p
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              padding: "8px 0",
            }}
          >
            No mount options configured.
          </p>
        )}

        <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
          {form.mountOptions.map((o, i) => (
            <div key={i} style={{ display: "flex", gap: "8px" }}>
              <input
                type="text"
                value={o.value}
                onInput={(e) =>
                  updateMountOption(i, (e.target as HTMLInputElement).value)}
                placeholder="e.g. debug, noatime"
                style={{ ...inputStyle, flex: 1 }}
              />
              <button
                type="button"
                onClick={() =>
                  removeMountOption(i)}
                style={removeBtn}
              >
                ×
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
