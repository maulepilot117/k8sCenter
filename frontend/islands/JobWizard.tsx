import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Select } from "@/components/ui/Select.tsx";
import type { EnvVarEntry, PortEntry } from "@/lib/wizard-types.ts";
import { RESTART_POLICY_OPTIONS } from "@/lib/wizard-constants.ts";

interface JobFormState {
  name: string;
  namespace: string;
  completions: string;
  parallelism: string;
  backoffLimit: string;
  restartPolicy: string;
  // Container fields
  image: string;
  command: string;
  args: string;
  ports: PortEntry[];
  envVars: EnvVarEntry[];
  requestCpu: string;
  requestMemory: string;
  limitCpu: string;
  limitMemory: string;
}

const STEPS = [
  { title: "Basics" },
  { title: "Container" },
  { title: "Review" },
];

function initialState(): JobFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    completions: "1",
    parallelism: "1",
    backoffLimit: "6",
    restartPolicy: "Never",
    image: "",
    command: "",
    args: "",
    ports: [],
    envVars: [],
    requestCpu: "",
    requestMemory: "",
    limitCpu: "",
    limitMemory: "",
  };
}

export default function JobWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<JobFormState>(initialState());
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

  const validateStep = (step: number): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (!f.namespace) errs.namespace = "Required";
      const completions = parseInt(f.completions);
      if (f.completions && (isNaN(completions) || completions < 0)) {
        errs.completions = "Must be a non-negative integer";
      }
      const parallelism = parseInt(f.parallelism);
      if (f.parallelism && (isNaN(parallelism) || parallelism < 0)) {
        errs.parallelism = "Must be a non-negative integer";
      }
      const backoffLimit = parseInt(f.backoffLimit);
      if (f.backoffLimit && (isNaN(backoffLimit) || backoffLimit < 0)) {
        errs.backoffLimit = "Must be a non-negative integer";
      }
    }

    if (step === 1) {
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

    // Command: split string by spaces into array
    if (f.command.trim()) {
      container.command = f.command.trim().split(/\s+/);
    }
    if (f.args.trim()) {
      container.args = f.args.trim().split(/\s+/);
    }

    // Ports: filter empty
    const ports = f.ports.filter((p) => p.containerPort > 0);
    if (ports.length > 0) container.ports = ports;

    // Env vars: convert to backend format
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

    // Resources
    if (f.requestCpu || f.requestMemory || f.limitCpu || f.limitMemory) {
      container.resources = {
        requestCpu: f.requestCpu || undefined,
        requestMemory: f.requestMemory || undefined,
        limitCpu: f.limitCpu || undefined,
        limitMemory: f.limitMemory || undefined,
      };
    }

    // Build top-level payload
    const payload: Record<string, unknown> = {
      name: f.name,
      namespace: f.namespace,
      container,
      restartPolicy: f.restartPolicy,
    };

    const completions = parseInt(f.completions);
    if (!isNaN(completions)) payload.completions = completions;

    const parallelism = parseInt(f.parallelism);
    if (!isNaN(parallelism)) payload.parallelism = parallelism;

    const backoffLimit = parseInt(f.backoffLimit);
    if (!isNaN(backoffLimit)) payload.backoffLimit = backoffLimit;

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/job/preview",
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
      // Moving to Review step
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
          Create Job
        </h1>
        <a
          href="/workloads/jobs"
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
          <div class="space-y-6 max-w-2xl">
            {/* Name */}
            <div class="space-y-1">
              <label class="block text-sm font-medium text-text-secondary">
                Job Name <span class="text-danger">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField(
                    "name",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="my-batch-job"
                class={WIZARD_INPUT_CLASS}
              />
              {errors.value.name && (
                <p class="text-sm text-danger">{errors.value.name}</p>
              )}
            </div>

            {/* Namespace */}
            <div class="space-y-1">
              <label class="block text-sm font-medium text-text-secondary">
                Namespace <span class="text-danger">*</span>
              </label>
              <select
                value={form.value.namespace}
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
                <p class="text-sm text-danger">{errors.value.namespace}</p>
              )}
            </div>

            {/* Completions */}
            <div class="space-y-1">
              <label class="block text-sm font-medium text-text-secondary">
                Completions
              </label>
              <input
                type="number"
                value={form.value.completions}
                onInput={(e) =>
                  updateField(
                    "completions",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="1"
                min={0}
                class={WIZARD_INPUT_CLASS}
              />
              {errors.value.completions && (
                <p class="text-sm text-danger">{errors.value.completions}</p>
              )}
              <p class="text-xs text-text-muted">
                Number of successful completions required.
              </p>
            </div>

            {/* Parallelism */}
            <div class="space-y-1">
              <label class="block text-sm font-medium text-text-secondary">
                Parallelism
              </label>
              <input
                type="number"
                value={form.value.parallelism}
                onInput={(e) =>
                  updateField(
                    "parallelism",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="1"
                min={0}
                class={WIZARD_INPUT_CLASS}
              />
              {errors.value.parallelism && (
                <p class="text-sm text-danger">{errors.value.parallelism}</p>
              )}
              <p class="text-xs text-text-muted">
                Maximum number of pods running in parallel.
              </p>
            </div>

            {/* Backoff Limit */}
            <div class="space-y-1">
              <label class="block text-sm font-medium text-text-secondary">
                Backoff Limit
              </label>
              <input
                type="number"
                value={form.value.backoffLimit}
                onInput={(e) =>
                  updateField(
                    "backoffLimit",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="6"
                min={0}
                class={WIZARD_INPUT_CLASS}
              />
              {errors.value.backoffLimit && (
                <p class="text-sm text-danger">{errors.value.backoffLimit}</p>
              )}
              <p class="text-xs text-text-muted">
                Number of retries before marking the job as failed.
              </p>
            </div>

            {/* Restart Policy */}
            <Select
              label="Restart Policy"
              value={form.value.restartPolicy}
              onChange={(e) =>
                updateField(
                  "restartPolicy",
                  (e.target as HTMLSelectElement).value,
                )}
              options={RESTART_POLICY_OPTIONS}
            />
          </div>
        )}

        {currentStep.value === 1 && (
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
        )}

        {currentStep.value === 2 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/workloads/jobs"
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
