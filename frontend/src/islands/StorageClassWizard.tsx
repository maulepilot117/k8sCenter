/** @jsxImportSource react */
import { useEffect, useRef, useState } from "react";
import Field from "@/components/ui/form/Field.tsx";
import Select from "@/components/ui/form/Select.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Toggle from "@/components/ui/form/Toggle.tsx";
import { apiGet, apiPost } from "@/lib/api.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

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
  const params = f.parameters
    .filter((p) => p.key)
    .map((p) => `\n  ${p.key}: "${p.value}"`)
    .join("");
  const paramsBlock = params ? `\nparameters:${params}` : "";
  const opts = f.mountOptions.map((o) => o.value).filter(Boolean);
  const mountBlock =
    opts.length > 0
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

export default function StorageClassWizard({
  onClose,
}: {
  onClose?: () => void;
}) {
  // When rendered as a full-page route, no onClose is passed (Fresh cannot
  // serialize a function prop across the route→island boundary). Fall back to
  // browser history so the wizard chrome's Cancel/Done still work.
  const close = onClose ?? (() => globalThis.history.back());
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<FormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [presets, setPresets] = useState<Record<string, PresetInfo>>({});

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);
  const formRef = useLatest(form);

  useEffect(() => {
    apiGet<Record<string, PresetInfo>>("/v1/storage/presets")
      .then((resp) => {
        if (resp.data) setPresets(resp.data);
      })
      .catch(() => {});
  }, []);

  const updateField = (field: string, value: unknown) => {
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  const applyPreset = (driverName: string) => {
    const preset = presets[driverName];
    if (!preset) return;
    setForm((prev) => ({
      ...prev,
      provisioner: driverName,
      parameters: Object.entries(preset.parameters).map(([key, p]) => ({
        key,
        value: p.default,
      })),
    }));
  };

  const validateStep = (s: number): boolean => {
    const f = formRef.current;
    const errs: Record<string, string> = {};
    if (s === 0) {
      if (!f.name || !/^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(f.name)) {
        errs.name =
          "Must be a valid DNS subdomain (lowercase, hyphens, dots, max 253)";
      }
      if (!f.provisioner) errs.provisioner = "Required";
    }
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = formRef.current;
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

  const handleNext = async () => {
    if (!validateStep(step)) return;
    if (step === 1) {
      setStep(2);
      await fetchPreview();
    } else if (step < 2) {
      setStep(step + 1);
    } else {
      close();
    }
  };

  const presetNames = Object.keys(presets);
  const f = form;

  return (
    <WizardShell
      title="Create StorageClass"
      subtitle={`Step ${step + 1} of 3`}
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
          <ellipse cx="10" cy="6" rx="7" ry="3" />
          <path d="M3 6v4c0 1.66 3.13 3 7 3s7-1.34 7-3V6" />
          <path d="M3 10v4c0 1.66 3.13 3 7 3s7-1.34 7-3v-4" />
        </svg>
      }
      steps={STEPS}
      current={step}
      onStep={(i) => {
        if (i < step) setStep(i);
      }}
      onCancel={close}
      onBack={() => setStep((current) => Math.max(0, current - 1))}
      onNext={handleNext}
      nextLabel={step === 2 ? "Done" : "Continue"}
      yaml={step < 2 ? buildManifest(f) : undefined}
    >
      {step === 0 && (
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
                      background:
                        f.provisioner === driver
                          ? "var(--accent-dim)"
                          : "transparent",
                      color:
                        f.provisioner === driver
                          ? "var(--accent)"
                          : "var(--text-secondary)",
                      cursor: "pointer",
                      fontWeight: 600,
                      fontFamily: "inherit",
                      transition: "all 0.15s",
                    }}
                  >
                    {presets[driver].displayName}
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
            {errors.name && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.name}
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
            {errors.provisioner && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.provisioner}
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

      {step === 1 && (
        <ParametersStep form={f} presets={presets} onChange={updateField} />
      )}

      {step === 2 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/storage/overview"
        />
      )}
    </WizardShell>
  );
}

function ParametersStep({
  form,
  presets,
  onChange,
}: {
  form: FormState;
  presets: Record<string, PresetInfo>;
  onChange: (field: string, value: unknown) => void;
}) {
  const preset = presets[form.provisioner];
  const parameterKeys = useRowKeys(form.parameters.length);
  const mountOptionKeys = useRowKeys(form.mountOptions.length);

  const addParameter = () => {
    onChange("parameters", [...form.parameters, { key: "", value: "" }]);
  };

  const removeParameter = (idx: number) => {
    parameterKeys.removeAt(idx);
    onChange(
      "parameters",
      form.parameters.filter((_, i) => i !== idx),
    );
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
    mountOptionKeys.removeAt(idx);
    onChange(
      "mountOptions",
      form.mountOptions.filter((_, i) => i !== idx),
    );
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
                key={parameterKeys.keyAt(i)}
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
                    onChange={(e) =>
                      updateParameter(i, "key", e.currentTarget.value)
                    }
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
                  {presetParam?.options ? (
                    <select
                      value={p.value}
                      onChange={(e) =>
                        updateParameter(i, "value", e.currentTarget.value)
                      }
                      style={inputStyle}
                    >
                      {presetParam.options.map((opt) => (
                        <option key={opt} value={opt}>
                          {opt}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      type="text"
                      value={p.value}
                      onChange={(e) =>
                        updateParameter(i, "value", e.currentTarget.value)
                      }
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
            <div
              key={mountOptionKeys.keyAt(i)}
              style={{ display: "flex", gap: "8px" }}
            >
              <input
                type="text"
                value={o.value}
                onChange={(e) => updateMountOption(i, e.currentTarget.value)}
                placeholder="e.g. debug, noatime"
                style={{ ...inputStyle, flex: 1 }}
              />
              <button
                type="button"
                onClick={() => removeMountOption(i)}
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
