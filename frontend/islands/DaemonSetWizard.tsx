import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
} from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import type { EnvVarEntry, PortEntry } from "@/lib/wizard-types.ts";

interface NodeSelectorEntry {
  key: string;
  value: string;
}

interface DaemonSetFormState {
  name: string;
  namespace: string;
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
  // DaemonSet-specific
  nodeSelector: NodeSelectorEntry[];
  maxUnavailable: string;
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

function initialState(): DaemonSetFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    image: "",
    command: "",
    args: "",
    ports: [],
    envVars: [],
    requestCpu: "",
    requestMemory: "",
    limitCpu: "",
    limitMemory: "",
    nodeSelector: [],
    maxUnavailable: "",
  };
}

export default function DaemonSetWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<DaemonSetFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const validateStep = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.image) errs.image = "Required";

    f.ports.forEach((p, i) => {
      if (
        p.containerPort && (p.containerPort < 1 || p.containerPort > MAX_PORT)
      ) {
        errs[`ports[${i}].containerPort`] = `Must be 1-${MAX_PORT}`;
      }
    });
    f.envVars.forEach((e, i) => {
      if (e.name && !ENV_VAR_NAME_REGEX.test(e.name)) {
        errs[`envVars[${i}].name`] = "Invalid env var name";
      }
    });

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

    const payload: Record<string, unknown> = {
      name: f.name,
      namespace: f.namespace,
      container,
    };

    // Node selector: convert array to map
    const nsMap: Record<string, string> = {};
    for (const entry of f.nodeSelector) {
      if (entry.key) nsMap[entry.key] = entry.value;
    }
    if (Object.keys(nsMap).length > 0) payload.nodeSelector = nsMap;

    if (f.maxUnavailable) payload.maxUnavailable = f.maxUnavailable;

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/daemonset/preview",
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
    if (!validateStep()) return;
    currentStep.value = 1;
    await fetchPreview();
  };

  const goBack = () => {
    if (currentStep.value > 0) currentStep.value = 0;
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">
          Create DaemonSet
        </h1>
        <a
          href="/workloads/daemonsets"
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
        {currentStep.value === 0 && (
          <div class="space-y-8">
            {/* Name & Namespace */}
            <div class="max-w-lg space-y-4">
              <Input
                label="Name"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                placeholder="my-daemonset"
                error={errors.value.name}
                required
              />

              <NamespaceSelect
                value={form.value.namespace}
                namespaces={namespaces.value}
                error={errors.value.namespace}
                onChange={(ns) => updateField("namespace", ns)}
              />
            </div>

            {/* Container Form */}
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

            {/* Node Selector */}
            <div class="max-w-2xl space-y-3">
              <label class="block text-sm font-medium text-text-secondary">
                Node Selector
              </label>
              <p class="text-xs text-text-muted">
                Optional. Constrain the DaemonSet to nodes matching these
                labels.
              </p>
              {form.value.nodeSelector.map((entry, i) => (
                <div key={i} class="flex items-end gap-2">
                  <div class="flex-1">
                    <Input
                      label={i === 0 ? "Key" : undefined}
                      value={entry.key}
                      onInput={(e) => {
                        const updated = [...form.value.nodeSelector];
                        updated[i] = {
                          ...updated[i],
                          key: (e.target as HTMLInputElement).value,
                        };
                        updateField("nodeSelector", updated);
                      }}
                      placeholder="kubernetes.io/os"
                    />
                  </div>
                  <div class="flex-1">
                    <Input
                      label={i === 0 ? "Value" : undefined}
                      value={entry.value}
                      onInput={(e) => {
                        const updated = [...form.value.nodeSelector];
                        updated[i] = {
                          ...updated[i],
                          value: (e.target as HTMLInputElement).value,
                        };
                        updateField("nodeSelector", updated);
                      }}
                      placeholder="linux"
                    />
                  </div>
                  <RemoveButton
                    onClick={() =>
                      updateField(
                        "nodeSelector",
                        form.value.nodeSelector.filter((_, idx) =>
                          idx !== i
                        ),
                      )}
                    title="Remove selector"
                    class="p-2 mb-1"
                  />
                </div>
              ))}
              {form.value.nodeSelector.length < 50 && (
                <button
                  type="button"
                  onClick={() =>
                    updateField("nodeSelector", [
                      ...form.value.nodeSelector,
                      { key: "", value: "" },
                    ])}
                  class="text-sm text-brand hover:text-brand/80"
                >
                  + Add Node Selector
                </button>
              )}
            </div>

            {/* Max Unavailable */}
            <div class="max-w-lg">
              <Input
                label="Max Unavailable"
                value={form.value.maxUnavailable}
                onInput={(e) =>
                  updateField(
                    "maxUnavailable",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="1 or 25%"
              />
              <p class="mt-1 text-xs text-text-muted">
                Maximum number of pods that can be unavailable during a rolling
                update. Accepts an integer or percentage.
              </p>
            </div>
          </div>
        )}

        {currentStep.value === 1 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/workloads/daemonsets"
          />
        )}
      </div>

      {currentStep.value === 0 && (
        <div class="mt-8 flex justify-end">
          <Button variant="primary" onClick={goNext}>
            Preview YAML
          </Button>
        </div>
      )}

      {currentStep.value === 1 && !previewLoading.value &&
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
