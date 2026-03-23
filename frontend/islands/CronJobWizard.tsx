import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface ContainerState {
  image: string;
  command: string;
  args: string;
  ports: Array<{ containerPort: number; protocol: string }>;
  envVars: Array<{
    name: string;
    value: string;
    configMapRef: string;
    secretRef: string;
    key: string;
  }>;
  requestCpu: string;
  requestMemory: string;
  limitCpu: string;
  limitMemory: string;
}

interface CronJobFormState {
  name: string;
  namespace: string;
  schedulePreset: string;
  schedule: string;
  concurrencyPolicy: string;
  restartPolicy: string;
  successfulJobsHistoryLimit: string;
  failedJobsHistoryLimit: string;
  suspend: boolean;
  container: ContainerState;
}

const STEPS = [
  { title: "Basics & Schedule" },
  { title: "Container" },
  { title: "Review" },
];

const SCHEDULE_PRESETS: { label: string; value: string }[] = [
  { label: "Every hour", value: "0 * * * *" },
  { label: "Daily midnight", value: "0 0 * * *" },
  { label: "Weekly Sunday", value: "0 0 * * 0" },
  { label: "Custom", value: "" },
];

const CONCURRENCY_OPTIONS = [
  { value: "Allow", label: "Allow" },
  { value: "Forbid", label: "Forbid" },
  { value: "Replace", label: "Replace" },
];

const RESTART_POLICY_OPTIONS = [
  { value: "Never", label: "Never" },
  { value: "OnFailure", label: "OnFailure" },
];

function cronToHuman(cron: string): string {
  const trimmed = cron.trim();
  if (trimmed === "0 * * * *") return "Every hour, at minute 0";
  if (trimmed === "0 0 * * *") return "Every day at midnight";
  if (trimmed === "0 0 * * 0") return "Every Sunday at midnight";
  if (!trimmed) return "";
  return `Cron: ${trimmed}`;
}

function initialState(): CronJobFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  return {
    name: "",
    namespace: ns,
    schedulePreset: "0 0 * * *",
    schedule: "0 0 * * *",
    concurrencyPolicy: "Allow",
    restartPolicy: "Never",
    successfulJobsHistoryLimit: "3",
    failedJobsHistoryLimit: "1",
    suspend: false,
    container: {
      image: "",
      command: "",
      args: "",
      ports: [],
      envVars: [],
      requestCpu: "",
      requestMemory: "",
      limitCpu: "",
      limitMemory: "",
    },
  };
}

export default function CronJobWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<CronJobFormState>(initialState());
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

  const updateContainerField = useCallback(
    (field: string, value: unknown) => {
      dirty.value = true;
      form.value = {
        ...form.value,
        container: { ...form.value.container, [field]: value },
      };
    },
    [],
  );

  const validateStep0 = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.schedule.trim()) errs.schedule = "Required";

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const validateStep1 = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.container.image) errs.image = "Required";

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (currentStep.value === 0) {
      if (!validateStep0()) return;
      currentStep.value = 1;
    } else if (currentStep.value === 1) {
      if (!validateStep1()) return;
      currentStep.value = 2;
      await fetchPreview();
    }
  };

  const goBack = () => {
    if (currentStep.value > 0) {
      currentStep.value = currentStep.value - 1;
    }
  };

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;

    // Build container payload matching backend ContainerInput
    const container: Record<string, unknown> = {
      image: f.container.image,
    };

    // Command: split comma-separated string into array
    if (f.container.command.trim()) {
      container.command = f.container.command.split(/\s+/).filter(Boolean);
    }
    if (f.container.args.trim()) {
      container.args = f.container.args.split(",").map((s) => s.trim()).filter(
        Boolean,
      );
    }

    // Ports
    const ports = f.container.ports.filter((p) => p.containerPort > 0);
    if (ports.length > 0) container.ports = ports;

    // Env vars: convert to backend format
    const envVars = f.container.envVars
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
    if (
      f.container.requestCpu || f.container.requestMemory ||
      f.container.limitCpu || f.container.limitMemory
    ) {
      container.resources = {
        requestCpu: f.container.requestCpu || undefined,
        requestMemory: f.container.requestMemory || undefined,
        limitCpu: f.container.limitCpu || undefined,
        limitMemory: f.container.limitMemory || undefined,
      };
    }

    const payload: Record<string, unknown> = {
      name: f.name,
      namespace: f.namespace,
      schedule: f.schedule,
      container,
      restartPolicy: f.restartPolicy,
      concurrencyPolicy: f.concurrencyPolicy,
      suspend: f.suspend,
    };

    // History limits
    const successLimit = parseInt(f.successfulJobsHistoryLimit, 10);
    if (!isNaN(successLimit) && successLimit >= 0) {
      payload.successfulJobsHistoryLimit = successLimit;
    }
    const failLimit = parseInt(f.failedJobsHistoryLimit, 10);
    if (!isNaN(failLimit) && failLimit >= 0) {
      payload.failedJobsHistoryLimit = failLimit;
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/cronjob/preview",
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

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create CronJob
        </h1>
        <a
          href="/workloads/cronjobs"
          class="text-sm text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
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
        {/* Step 0: Basics & Schedule */}
        {currentStep.value === 0 && (
          <div class="mx-auto max-w-lg space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Name <span class="text-red-500">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class={WIZARD_INPUT_CLASS}
                placeholder="e.g. data-cleanup"
              />
              {errors.value.name && (
                <p class="mt-1 text-xs text-red-500">{errors.value.name}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Namespace <span class="text-red-500">*</span>
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
            </div>

            {/* Schedule with presets */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Schedule <span class="text-red-500">*</span>
              </label>
              <div class="mt-2 flex flex-wrap gap-2">
                {SCHEDULE_PRESETS.map((preset) => (
                  <button
                    key={preset.label}
                    type="button"
                    onClick={() => {
                      if (preset.value) {
                        updateField("schedule", preset.value);
                        updateField("schedulePreset", preset.value);
                      } else {
                        updateField("schedulePreset", "");
                      }
                    }}
                    class={`rounded-md border px-3 py-1.5 text-sm transition-colors ${
                      (preset.value &&
                          form.value.schedulePreset === preset.value) ||
                        (!preset.value && form.value.schedulePreset === "")
                        ? "border-brand bg-brand/10 text-brand font-medium"
                        : "border-slate-300 text-slate-600 hover:border-slate-400 dark:border-slate-600 dark:text-slate-400"
                    }`}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
              {form.value.schedulePreset === "" && (
                <input
                  type="text"
                  value={form.value.schedule}
                  onInput={(e) =>
                    updateField(
                      "schedule",
                      (e.target as HTMLInputElement).value,
                    )}
                  class={`${WIZARD_INPUT_CLASS} mt-2`}
                  placeholder="e.g. */5 * * * *"
                />
              )}
              {form.value.schedule && (
                <p class="mt-1 text-xs text-slate-500">
                  {cronToHuman(form.value.schedule)}
                </p>
              )}
              {errors.value.schedule && (
                <p class="mt-1 text-xs text-red-500">
                  {errors.value.schedule}
                </p>
              )}
            </div>

            {/* Concurrency Policy */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Concurrency Policy
              </label>
              <select
                value={form.value.concurrencyPolicy}
                onChange={(e) =>
                  updateField(
                    "concurrencyPolicy",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                {CONCURRENCY_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>{opt.label}</option>
                ))}
              </select>
              <p class="mt-1 text-xs text-slate-500">
                {form.value.concurrencyPolicy === "Allow" &&
                  "Multiple jobs can run concurrently."}
                {form.value.concurrencyPolicy === "Forbid" &&
                  "Skip new run if previous job is still running."}
                {form.value.concurrencyPolicy === "Replace" &&
                  "Cancel the running job and start a new one."}
              </p>
            </div>

            {/* Restart Policy */}
            <div>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Restart Policy
              </label>
              <select
                value={form.value.restartPolicy}
                onChange={(e) =>
                  updateField(
                    "restartPolicy",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                {RESTART_POLICY_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>{opt.label}</option>
                ))}
              </select>
            </div>

            {/* History limits */}
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  Successful History
                </label>
                <input
                  type="number"
                  min={0}
                  max={100}
                  value={form.value.successfulJobsHistoryLimit}
                  onInput={(e) =>
                    updateField(
                      "successfulJobsHistoryLimit",
                      (e.target as HTMLInputElement).value,
                    )}
                  class={WIZARD_INPUT_CLASS}
                />
              </div>
              <div>
                <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  Failed History
                </label>
                <input
                  type="number"
                  min={0}
                  max={100}
                  value={form.value.failedJobsHistoryLimit}
                  onInput={(e) =>
                    updateField(
                      "failedJobsHistoryLimit",
                      (e.target as HTMLInputElement).value,
                    )}
                  class={WIZARD_INPUT_CLASS}
                />
              </div>
            </div>

            {/* Suspend toggle */}
            <div class="flex items-center gap-2">
              <input
                type="checkbox"
                checked={form.value.suspend}
                onChange={(e) =>
                  updateField(
                    "suspend",
                    (e.target as HTMLInputElement).checked,
                  )}
                class="rounded border-slate-300 dark:border-slate-600"
              />
              <span class="text-sm text-slate-700 dark:text-slate-300">
                Create suspended (will not run until resumed)
              </span>
            </div>
          </div>
        )}

        {/* Step 1: Container */}
        {currentStep.value === 1 && (
          <ContainerForm
            image={form.value.container.image}
            command={form.value.container.command}
            args={form.value.container.args}
            ports={form.value.container.ports}
            envVars={form.value.container.envVars}
            requestCpu={form.value.container.requestCpu}
            requestMemory={form.value.container.requestMemory}
            limitCpu={form.value.container.limitCpu}
            limitMemory={form.value.container.limitMemory}
            errors={errors.value}
            onChange={updateContainerField}
          />
        )}

        {/* Step 2: Review */}
        {currentStep.value === 2 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/workloads/cronjobs"
          />
        )}
      </div>

      {/* Navigation buttons */}
      {currentStep.value < 2 && (
        <div class="mt-8 flex justify-between">
          {currentStep.value > 0
            ? (
              <Button variant="ghost" onClick={goBack}>
                Back
              </Button>
            )
            : <div />}
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === 1 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === 2 && !previewLoading.value &&
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
