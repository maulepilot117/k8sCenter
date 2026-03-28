import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import {
  ACCESS_MODES,
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
  MAX_REPLICAS,
  type StorageClassItem,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useStorageClasses } from "@/lib/hooks/use-storage-classes.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { Select } from "@/components/ui/Select.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";

interface PortEntry {
  containerPort: number;
  protocol: string;
}

interface EnvVarEntry {
  name: string;
  value: string;
  configMapRef: string;
  secretRef: string;
  key: string;
}

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

const STEPS = [
  { title: "Basics" },
  { title: "Container & Volumes" },
  { title: "Review" },
];

const POD_MANAGEMENT_OPTIONS = [
  { value: "OrderedReady", label: "OrderedReady (default)" },
  { value: "Parallel", label: "Parallel" },
];

function initialState(): StatefulSetFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
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

export default function StatefulSetWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<StatefulSetFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  const storageClasses = useStorageClasses();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  // Auto-sync serviceName with name when name changes and serviceName is empty or matches old name
  const prevName = useSignal("");

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    const f = { ...form.value, [field]: value };
    // Auto-sync serviceName with name
    if (field === "name") {
      const newName = value as string;
      if (
        !f.serviceName ||
        f.serviceName === prevName.value ||
        f.serviceName === `${prevName.value}-headless`
      ) {
        f.serviceName = newName ? `${newName}-headless` : "";
      }
      prevName.value = newName;
    }
    form.value = f;
  }, []);

  const validateStep = (step: number): boolean => {
    const f = form.value;
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
        if (isNaN(size) || size <= 0) {
          errs[`vct[${i}].size`] = "Must be a positive number";
        }
      });
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;

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
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      previewLoading.value = false;
    }
  };

  const goNext = async () => {
    if (!validateStep(currentStep.value)) return;

    if (currentStep.value === 1) {
      currentStep.value = 2;
      await fetchPreview();
    } else {
      currentStep.value = currentStep.value + 1;
    }
  };

  const goBack = () => {
    if (currentStep.value > 0) {
      currentStep.value = currentStep.value - 1;
    }
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">
          Create StatefulSet
        </h1>
        <a
          href="/workloads/statefulsets"
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
        {/* Step 1: Basics */}
        {currentStep.value === 0 && (
          <div class="max-w-lg space-y-4">
            <Input
              label="Name"
              value={form.value.name}
              onInput={(e) =>
                updateField("name", (e.target as HTMLInputElement).value)}
              placeholder="my-statefulset"
              error={errors.value.name}
              required
            />

            <NamespaceSelect
              value={form.value.namespace}
              namespaces={namespaces.value}
              error={errors.value.namespace}
              onChange={(ns) => updateField("namespace", ns)}
            />

            <Input
              label="Headless Service Name"
              value={form.value.serviceName}
              onInput={(e) =>
                updateField(
                  "serviceName",
                  (e.target as HTMLInputElement).value,
                )}
              placeholder="my-statefulset-headless"
              error={errors.value.serviceName}
              required
            />
            <p class="-mt-2 text-xs text-text-muted">
              The headless Service that governs this StatefulSet. Must exist or
              be created separately.
            </p>

            <Input
              label="Replicas"
              type="number"
              value={String(form.value.replicas)}
              onInput={(e) =>
                updateField(
                  "replicas",
                  parseInt((e.target as HTMLInputElement).value) || 0,
                )}
              min={0}
              max={1000}
              error={errors.value.replicas}
            />

            <Select
              label="Pod Management Policy"
              value={form.value.podManagementPolicy}
              onChange={(e) =>
                updateField(
                  "podManagementPolicy",
                  (e.target as HTMLSelectElement).value,
                )}
              options={POD_MANAGEMENT_OPTIONS}
            />
          </div>
        )}

        {/* Step 2: Container & Volumes */}
        {currentStep.value === 1 && (
          <div class="space-y-8">
            <ContainerForm
              image={form.value.image}
              command={form.value.command}
              args={form.value.args}
              ports={form.value.ports}
              envVars={form.value.envVars}
              requestCpu={form.value.requestCpu}
              requestMemory={form.value.requestMemory}
              limitCpu={form.value.limitCpu}
              limitMemory={form.value.limitMemory}
              errors={errors.value}
              onChange={updateField}
            />

            {/* Volume Claim Templates */}
            <div class="max-w-2xl space-y-4">
              <div>
                <h3 class="text-sm font-medium text-text-secondary">
                  Volume Claim Templates
                </h3>
                <p class="mt-1 text-xs text-text-muted">
                  Persistent storage for each pod replica. Each pod gets its own
                  PVC.
                </p>
              </div>

              {form.value.volumeClaimTemplates.map((vct, i) => (
                <div
                  key={i}
                  class="rounded-lg border border-border-primary p-4 space-y-3"
                >
                  <div class="flex items-center justify-between">
                    <span class="text-sm font-medium text-text-secondary">
                      Volume {i + 1}
                    </span>
                    <RemoveButton
                      onClick={() =>
                        updateField(
                          "volumeClaimTemplates",
                          form.value.volumeClaimTemplates.filter((_, idx) =>
                            idx !== i
                          ),
                        )}
                      title="Remove volume"
                      class="p-1"
                    />
                  </div>

                  <Input
                    label="Volume Name"
                    value={vct.name}
                    onInput={(e) => {
                      const updated = [...form.value.volumeClaimTemplates];
                      updated[i] = {
                        ...updated[i],
                        name: (e.target as HTMLInputElement).value,
                      };
                      updateField("volumeClaimTemplates", updated);
                    }}
                    placeholder="data"
                    error={errors.value[`vct[${i}].name`]}
                    required
                  />

                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
                      Storage Class
                    </label>
                    <select
                      value={vct.storageClassName}
                      onChange={(e) => {
                        const updated = [...form.value.volumeClaimTemplates];
                        updated[i] = {
                          ...updated[i],
                          storageClassName:
                            (e.target as HTMLSelectElement).value,
                        };
                        updateField("volumeClaimTemplates", updated);
                      }}
                      class={WIZARD_INPUT_CLASS}
                    >
                      <option value="">Default (cluster default)</option>
                      {storageClasses.value.map((sc: StorageClassItem) => (
                        <option key={sc.metadata.name} value={sc.metadata.name}>
                          {sc.metadata.name}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
                      Size <span class="text-error">*</span>
                    </label>
                    <div class="mt-1 flex gap-2">
                      <input
                        type="number"
                        min="1"
                        value={vct.sizeValue}
                        onInput={(e) => {
                          const updated = [...form.value.volumeClaimTemplates];
                          updated[i] = {
                            ...updated[i],
                            sizeValue: (e.target as HTMLInputElement).value,
                          };
                          updateField("volumeClaimTemplates", updated);
                        }}
                        class="w-32 rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                      />
                      <select
                        value={vct.sizeUnit}
                        onChange={(e) => {
                          const updated = [...form.value.volumeClaimTemplates];
                          updated[i] = {
                            ...updated[i],
                            sizeUnit: (e.target as HTMLSelectElement).value,
                          };
                          updateField("volumeClaimTemplates", updated);
                        }}
                        class="rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
                      >
                        <option value="Mi">Mi</option>
                        <option value="Gi">Gi</option>
                        <option value="Ti">Ti</option>
                      </select>
                    </div>
                    {errors.value[`vct[${i}].size`] && (
                      <p class="mt-1 text-xs text-error">
                        {errors.value[`vct[${i}].size`]}
                      </p>
                    )}
                  </div>

                  <div>
                    <label class="block text-sm font-medium text-text-secondary">
                      Access Mode
                    </label>
                    <div class="mt-2 space-y-1">
                      {ACCESS_MODES.map((mode) => (
                        <label
                          key={mode.value}
                          class="flex items-center gap-3 rounded-md border border-border-primary px-3 py-1.5 cursor-pointer hover:bg-surface /50"
                        >
                          <input
                            type="radio"
                            name={`vct-${i}-accessMode`}
                            value={mode.value}
                            checked={vct.accessMode === mode.value}
                            onChange={() => {
                              const updated = [
                                ...form.value.volumeClaimTemplates,
                              ];
                              updated[i] = {
                                ...updated[i],
                                accessMode: mode.value,
                              };
                              updateField("volumeClaimTemplates", updated);
                            }}
                            class="text-brand focus:ring-brand"
                          />
                          <div>
                            <span class="text-sm font-medium text-text-secondary">
                              {mode.label}
                            </span>
                            <span class="ml-2 text-xs text-text-muted">
                              {mode.desc}
                            </span>
                          </div>
                        </label>
                      ))}
                    </div>
                  </div>
                </div>
              ))}

              {form.value.volumeClaimTemplates.length < 20 && (
                <button
                  type="button"
                  onClick={() =>
                    updateField("volumeClaimTemplates", [
                      ...form.value.volumeClaimTemplates,
                      {
                        name: "",
                        storageClassName: storageClasses.value.length > 0
                          ? storageClasses.value[0].metadata.name
                          : "",
                        sizeValue: "10",
                        sizeUnit: "Gi",
                        accessMode: "ReadWriteOnce",
                      },
                    ])}
                  class="text-sm text-brand hover:text-brand/80"
                >
                  + Add Volume Claim Template
                </button>
              )}
            </div>
          </div>
        )}

        {/* Step 3: Review */}
        {currentStep.value === 2 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/workloads/statefulsets"
          />
        )}
      </div>

      {/* Navigation buttons */}
      {currentStep.value < 2 && (
        <div class="flex justify-between mt-8">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === 1 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === 2 && !previewLoading.value &&
        previewError.value === null && (
        <div class="flex justify-start mt-4">
          <Button variant="ghost" onClick={goBack}>
            Back
          </Button>
        </div>
      )}
    </div>
  );
}
