/** @jsxImportSource react */
import { useCallback, useRef, useState } from "react";
import { Input } from "@/components/ui/Input.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { Select } from "@/components/ui/Select.tsx";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useStorageClasses } from "@/lib/hooks/use-storage-classes.ts";
import {
  ACCESS_MODES,
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
  MAX_REPLICAS,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import type {
  EnvVarEntry,
  PortEntry,
  StorageClassItem,
} from "@/lib/wizard-types.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useSignal } from "@/src/lib/signal-hooks.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface VolumeClaimEntry {
  name: string;
  storageClassName: string;
  sizeValue: string;
  sizeUnit: string;
  accessMode: string;
}

interface StatefulSetFormState {
  name: string;
  namespace: string;
  serviceName: string;
  replicas: number;
  podManagementPolicy: string;
  // Container fields (flat, matching ContainerForm props)
  image: string;
  command: string;
  args: string;
  ports: PortEntry[];
  envVars: EnvVarEntry[];
  requestCpu: string;
  requestMemory: string;
  limitCpu: string;
  limitMemory: string;
  // Volume claim templates
  volumeClaimTemplates: VolumeClaimEntry[];
}

const STEPS: WizardStep[] = [
  { label: "Basics", sub: "Name & service" },
  { label: "Container & Volumes", sub: "Image, ports & PVCs" },
  { label: "Review", sub: "Preview & apply" },
];

const POD_MANAGEMENT_OPTIONS = [
  { value: "OrderedReady", label: "OrderedReady (default)" },
  { value: "Parallel", label: "Parallel" },
];

function initialState(): StatefulSetFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    serviceName: "",
    replicas: 1,
    podManagementPolicy: "OrderedReady",
    image: "",
    command: "",
    args: "",
    ports: [],
    envVars: [],
    requestCpu: "",
    requestMemory: "",
    limitCpu: "",
    limitMemory: "",
    volumeClaimTemplates: [],
  };
}

export default function StatefulSetWizard({
  onClose,
}: {
  onClose?: () => void;
}) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<StatefulSetFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);
  const storageClasses = useStorageClasses();

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);
  const formRef = useRef(form);
  formRef.current = form;
  const vctKeys = useRowKeys(form.volumeClaimTemplates.length);

  useDirtyGuard(dirty);

  const updateField = useCallback(
    (field: string, value: unknown) => {
      dirty.value = true;
      setForm((prev) => {
        const f = { ...prev, [field]: value };
        // Auto-sync serviceName with name when it is empty or still the
        // default derived from the previous name
        if (field === "name") {
          const newName = value as string;
          if (
            !f.serviceName ||
            f.serviceName === prev.name ||
            f.serviceName === `${prev.name}-headless`
          ) {
            f.serviceName = newName ? `${newName}-headless` : "";
          }
        }
        return f;
      });
    },
    [dirty],
  );

  const validateStep = (step: number): boolean => {
    const f = form;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (!f.namespace) errs.namespace = "Required";
      if (!f.serviceName || !DNS_LABEL_REGEX.test(f.serviceName)) {
        errs.serviceName =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (f.replicas < 0 || f.replicas > MAX_REPLICAS) {
        errs.replicas = `Must be between 0 and ${MAX_REPLICAS}`;
      }
    }

    if (step === 1) {
      if (!f.image) errs.image = "Required";

      f.ports.forEach((p, i) => {
        if (
          p.containerPort &&
          (p.containerPort < 1 || p.containerPort > MAX_PORT)
        ) {
          errs[`ports[${i}].containerPort`] = `Must be 1-${MAX_PORT}`;
        }
      });
      f.envVars.forEach((e, i) => {
        if (e.name && !ENV_VAR_NAME_REGEX.test(e.name)) {
          errs[`envVars[${i}].name`] = "Invalid env var name";
        }
      });

      // Validate volume claim templates
      f.volumeClaimTemplates.forEach((vct, i) => {
        if (!vct.name || !DNS_LABEL_REGEX.test(vct.name)) {
          errs[`vct[${i}].name`] =
            "Must be lowercase alphanumeric with hyphens, 1-63 characters";
        }
        const size = parseFloat(vct.sizeValue);
        if (Number.isNaN(size) || size <= 0) {
          errs[`vct[${i}].size`] = "Must be a positive number";
        }
      });
    }

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = formRef.current;

    // Build the container sub-object
    const container: Record<string, unknown> = {
      image: f.image,
    };

    if (f.command.trim()) {
      container.command = f.command.trim().split(/\s+/);
    }
    if (f.args.trim()) {
      container.args = f.args.trim().split(/\s+/);
    }

    const ports = f.ports.filter((p) => p.containerPort > 0);
    if (ports.length > 0) container.ports = ports;

    const envVars = f.envVars
      .filter((e) => e.name)
      .map((e) => {
        if (e.configMapRef) {
          return { name: e.name, configMapRef: e.configMapRef, key: e.key };
        }
        if (e.secretRef) {
          return { name: e.name, secretRef: e.secretRef, key: e.key };
        }
        return { name: e.name, value: e.value };
      });
    if (envVars.length > 0) container.envVars = envVars;

    if (f.requestCpu || f.requestMemory || f.limitCpu || f.limitMemory) {
      container.resources = {
        requestCpu: f.requestCpu || undefined,
        requestMemory: f.requestMemory || undefined,
        limitCpu: f.limitCpu || undefined,
        limitMemory: f.limitMemory || undefined,
      };
    }

    // Add volume mounts for each VCT
    if (f.volumeClaimTemplates.length > 0) {
      container.volumeMounts = f.volumeClaimTemplates.map((vct) => ({
        name: vct.name,
        mountPath: `/data/${vct.name}`,
      }));
    }

    const payload: Record<string, unknown> = {
      name: f.name,
      namespace: f.namespace,
      serviceName: f.serviceName,
      replicas: f.replicas,
      container,
    };

    if (f.podManagementPolicy && f.podManagementPolicy !== "OrderedReady") {
      payload.podManagementPolicy = f.podManagementPolicy;
    }

    // Volume claim templates
    if (f.volumeClaimTemplates.length > 0) {
      payload.volumeClaimTemplates = f.volumeClaimTemplates.map((vct) => ({
        name: vct.name,
        storageClassName: vct.storageClassName,
        size: `${vct.sizeValue}${vct.sizeUnit}`,
        accessMode: vct.accessMode,
      }));
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/statefulset/preview",
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

  const goNext = async () => {
    if (!validateStep(currentStep)) return;

    if (currentStep === 1) {
      setCurrentStep(2);
      await fetchPreview();
    } else {
      setCurrentStep((step) => step + 1);
    }
  };

  const manifest = () => {
    const f = form;
    const name = f.name || "<name>";
    const ns = f.namespace || "default";
    return `apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: ${name}\n  namespace: ${ns}\nspec:\n  serviceName: ${
      f.serviceName || `${name}-headless`
    }\n  replicas: ${f.replicas}\n  selector:\n    matchLabels:\n      app: ${name}\n  template:\n    metadata:\n      labels:\n        app: ${name}\n    spec:\n      containers:\n        - name: ${name}\n          image: ${
      f.image || "<image>"
    }`;
  };

  return (
    <WizardShell
      title="Create StatefulSet"
      subtitle={`${form.namespace || "default"}`}
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
          <ellipse cx="10" cy="6" rx="7" ry="2.5" />
          <path d="M3 6v4c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5V6" />
          <path d="M3 10v4c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5v-4" />
        </svg>
      }
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i <= currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={() => setCurrentStep((step) => Math.max(0, step - 1))}
      onNext={() => {
        if (currentStep < 2) goNext();
        else close();
      }}
      nextLabel={currentStep < 2 ? "Continue" : "Close"}
      yaml={currentStep < 2 ? manifest() : previewYaml}
    >
      {/* Step 1: Basics */}
      {currentStep === 0 && (
        <div className="max-w-lg space-y-4">
          <Input
            label="Name"
            value={form.name}
            onChange={(e) => updateField("name", e.currentTarget.value)}
            placeholder="my-statefulset"
            error={errors.name}
            required
          />

          <NamespaceSelect
            value={form.namespace}
            namespaces={namespaceList}
            error={errors.namespace}
            onChange={(ns) => updateField("namespace", ns)}
          />

          <Input
            label="Headless Service Name"
            value={form.serviceName}
            onChange={(e) => updateField("serviceName", e.currentTarget.value)}
            placeholder="my-statefulset-headless"
            error={errors.serviceName}
            required
          />
          <p className="-mt-2 text-xs text-text-muted">
            The headless Service that governs this StatefulSet. Must exist or be
            created separately.
          </p>

          <Input
            label="Replicas"
            type="number"
            value={String(form.replicas)}
            onChange={(e) =>
              updateField("replicas", parseInt(e.currentTarget.value, 10) || 0)
            }
            min={0}
            max={1000}
            error={errors.replicas}
          />

          <Select
            label="Pod Management Policy"
            value={form.podManagementPolicy}
            onChange={(e) =>
              updateField("podManagementPolicy", e.currentTarget.value)
            }
            options={POD_MANAGEMENT_OPTIONS}
          />
        </div>
      )}

      {/* Step 2: Container & Volumes */}
      {currentStep === 1 && (
        <div className="space-y-8">
          <ContainerForm
            image={form.image}
            command={form.command}
            args={form.args}
            ports={form.ports}
            envVars={form.envVars}
            requestCpu={form.requestCpu}
            requestMemory={form.requestMemory}
            limitCpu={form.limitCpu}
            limitMemory={form.limitMemory}
            errors={errors}
            onChange={updateField}
          />

          {/* Volume Claim Templates */}
          <div className="max-w-2xl space-y-4">
            <div>
              <h3 className="text-sm font-medium text-text-secondary">
                Volume Claim Templates
              </h3>
              <p className="mt-1 text-xs text-text-muted">
                Persistent storage for each pod replica. Each pod gets its own
                PVC.
              </p>
            </div>

            {form.volumeClaimTemplates.map((vct, i) => (
              <div
                key={vctKeys.keyAt(i)}
                className="rounded-lg border border-border-primary p-4 space-y-3"
              >
                <div className="flex items-center justify-between">
                  <span className="text-sm font-medium text-text-secondary">
                    Volume {i + 1}
                  </span>
                  <RemoveButton
                    onClick={() => {
                      vctKeys.removeAt(i);
                      updateField(
                        "volumeClaimTemplates",
                        form.volumeClaimTemplates.filter((_, idx) => idx !== i),
                      );
                    }}
                    title="Remove volume"
                    className="p-1"
                  />
                </div>

                <Input
                  label="Volume Name"
                  value={vct.name}
                  onChange={(e) => {
                    const updated = [...form.volumeClaimTemplates];
                    updated[i] = {
                      ...updated[i],
                      name: e.currentTarget.value,
                    };
                    updateField("volumeClaimTemplates", updated);
                  }}
                  placeholder="data"
                  error={errors[`vct[${i}].name`]}
                  required
                />

                <div>
                  <label className="block text-sm font-medium text-text-secondary">
                    Storage Class
                  </label>
                  <select
                    value={vct.storageClassName}
                    onChange={(e) => {
                      const updated = [...form.volumeClaimTemplates];
                      updated[i] = {
                        ...updated[i],
                        storageClassName: e.currentTarget.value,
                      };
                      updateField("volumeClaimTemplates", updated);
                    }}
                    className={WIZARD_INPUT_CLASS}
                  >
                    <option value="">Default (cluster default)</option>
                    {storageClasses.map((sc: StorageClassItem) => (
                      <option key={sc.metadata.name} value={sc.metadata.name}>
                        {sc.metadata.name}
                      </option>
                    ))}
                  </select>
                </div>

                <div>
                  <label className="block text-sm font-medium text-text-secondary">
                    Size <span className="text-error">*</span>
                  </label>
                  <div className="mt-1 flex gap-2">
                    <input
                      type="number"
                      min="1"
                      value={vct.sizeValue}
                      onChange={(e) => {
                        const updated = [...form.volumeClaimTemplates];
                        updated[i] = {
                          ...updated[i],
                          sizeValue: e.currentTarget.value,
                        };
                        updateField("volumeClaimTemplates", updated);
                      }}
                      className="w-32 rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                    />
                    <select
                      value={vct.sizeUnit}
                      onChange={(e) => {
                        const updated = [...form.volumeClaimTemplates];
                        updated[i] = {
                          ...updated[i],
                          sizeUnit: e.currentTarget.value,
                        };
                        updateField("volumeClaimTemplates", updated);
                      }}
                      className="rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
                    >
                      <option value="Mi">Mi</option>
                      <option value="Gi">Gi</option>
                      <option value="Ti">Ti</option>
                    </select>
                  </div>
                  {errors[`vct[${i}].size`] && (
                    <p className="mt-1 text-xs text-error">
                      {errors[`vct[${i}].size`]}
                    </p>
                  )}
                </div>

                <div>
                  <label className="block text-sm font-medium text-text-secondary">
                    Access Mode
                  </label>
                  <div className="mt-2 space-y-1">
                    {ACCESS_MODES.map((mode) => (
                      <label
                        key={mode.value}
                        className="flex items-center gap-3 rounded-md border border-border-primary px-3 py-1.5 cursor-pointer hover:bg-surface /50"
                      >
                        <input
                          type="radio"
                          name={`vct-${i}-accessMode`}
                          value={mode.value}
                          checked={vct.accessMode === mode.value}
                          onChange={() => {
                            const updated = [...form.volumeClaimTemplates];
                            updated[i] = {
                              ...updated[i],
                              accessMode: mode.value,
                            };
                            updateField("volumeClaimTemplates", updated);
                          }}
                          className="text-brand focus:ring-brand"
                        />
                        <div>
                          <span className="text-sm font-medium text-text-secondary">
                            {mode.label}
                          </span>
                          <span className="ml-2 text-xs text-text-muted">
                            {mode.desc}
                          </span>
                        </div>
                      </label>
                    ))}
                  </div>
                </div>
              </div>
            ))}

            {form.volumeClaimTemplates.length < 20 && (
              <button
                type="button"
                onClick={() =>
                  updateField("volumeClaimTemplates", [
                    ...form.volumeClaimTemplates,
                    {
                      name: "",
                      storageClassName:
                        storageClasses.length > 0
                          ? storageClasses[0].metadata.name
                          : "",
                      sizeValue: "10",
                      sizeUnit: "Gi",
                      accessMode: "ReadWriteOnce",
                    },
                  ])
                }
                className="text-sm text-brand hover:text-brand/80"
              >
                + Add Volume Claim Template
              </button>
            )}
          </div>
        </div>
      )}

      {/* Step 3: Review */}
      {currentStep === 2 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/workloads/statefulsets"
          dirty={dirty}
        />
      )}
    </WizardShell>
  );
}
