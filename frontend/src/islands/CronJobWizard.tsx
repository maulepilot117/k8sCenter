/** @jsxImportSource react */
import { useCallback, useRef, useState } from "react";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import {
  DNS_LABEL_REGEX,
  RESTART_POLICY_OPTIONS,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

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

const STEPS: WizardStep[] = [
  { label: "Schedule", sub: "Name & cron expression" },
  { label: "Container", sub: "Image & resources" },
  { label: "Review", sub: "Preview & apply" },
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

function cronToHuman(cron: string): string {
  const trimmed = cron.trim();
  if (trimmed === "0 * * * *") return "Every hour, at minute 0";
  if (trimmed === "0 0 * * *") return "Every day at midnight";
  if (trimmed === "0 0 * * 0") return "Every Sunday at midnight";
  if (!trimmed) return "";
  return `Cron: ${trimmed}`;
}

function initialState(): CronJobFormState {
  const ns = initialNamespace();
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

const CRONJOB_ICON = (
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
    <rect x="2.5" y="4" width="15" height="13.5" rx="2" />
    <path d="M2.5 8.5h15M6.5 2.5v3M13.5 2.5v3" />
    <path d="M6 12h2.5l1.5-2 1.5 4 1-2H14" />
  </svg>
);

export default function CronJobWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<CronJobFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);

  const [namespaceList] = useNamespaces();

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);
  const formRef = useLatest(form);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    setDirty(true);
    setForm((prev) => ({ ...prev, [field]: value }));
  }, []);

  const updateContainerField = useCallback((field: string, value: unknown) => {
    setDirty(true);
    setForm((prev) => ({
      ...prev,
      container: { ...prev.container, [field]: value },
    }));
  }, []);

  const validateStep0 = (): boolean => {
    const f = formRef.current;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.schedule.trim()) errs.schedule = "Required";

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const validateStep1 = (): boolean => {
    const f = formRef.current;
    const errs: Record<string, string> = {};

    if (!f.container.image) errs.image = "Required";

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (currentStep === 0) {
      if (!validateStep0()) return;
      setCurrentStep(1);
    } else if (currentStep === 1) {
      if (!validateStep1()) return;
      setCurrentStep(2);
      await fetchPreview();
    }
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = formRef.current;

    // Build container payload matching backend ContainerInput
    const container: Record<string, unknown> = {
      image: f.container.image,
    };

    // Command: split comma-separated string into array
    if (f.container.command.trim()) {
      container.command = f.container.command.split(/\s+/).filter(Boolean);
    }
    if (f.container.args.trim()) {
      container.args = f.container.args
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
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
      f.container.requestCpu ||
      f.container.requestMemory ||
      f.container.limitCpu ||
      f.container.limitMemory
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
    if (!Number.isNaN(successLimit) && successLimit >= 0) {
      payload.successfulJobsHistoryLimit = successLimit;
    }
    const failLimit = parseInt(f.failedJobsHistoryLimit, 10);
    if (!Number.isNaN(failLimit) && failLimit >= 0) {
      payload.failedJobsHistoryLimit = failLimit;
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/cronjob/preview",
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

  const manifest = () => {
    const f = form;
    const name = f.name || "<name>";
    const ns = f.namespace || "default";
    return `apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: ${name}\n  namespace: ${ns}\nspec:\n  schedule: "${
      f.schedule || "0 0 * * *"
    }"\n  concurrencyPolicy: ${f.concurrencyPolicy}\n  suspend: ${f.suspend}\n  jobTemplate:\n    spec:\n      template:\n        spec:\n          restartPolicy: ${f.restartPolicy}\n          containers:\n            - name: ${name}\n              image: ${
      f.container.image || "<image>"
    }`;
  };

  const nextLabel = currentStep === 2 ? "Close" : "Continue";

  const handleNext = () => {
    if (currentStep === 2) {
      close();
    } else {
      goNext();
    }
  };

  return (
    <WizardShell
      title="Create CronJob"
      icon={CRONJOB_ICON}
      subtitle="Schedule recurring jobs"
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={() => setCurrentStep((step) => Math.max(0, step - 1))}
      onNext={handleNext}
      nextLabel={nextLabel}
      yaml={currentStep === 2 ? undefined : manifest()}
    >
      {/* Step 0: Basics & Schedule */}
      {currentStep === 0 && (
        <div className="mx-auto max-w-lg space-y-4">
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Name <span className="text-error">*</span>
            </label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
              placeholder="e.g. data-cleanup"
            />
            {errors.name && (
              <p className="mt-1 text-xs text-error">{errors.name}</p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Namespace <span className="text-error">*</span>
            </label>
            <select
              value={form.namespace}
              onChange={(e) => updateField("namespace", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
            >
              {namespaceList.map((ns) => (
                <option key={ns} value={ns}>
                  {ns}
                </option>
              ))}
            </select>
          </div>

          {/* Schedule with presets */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Schedule <span className="text-error">*</span>
            </label>
            <div className="mt-2 flex flex-wrap gap-2">
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
                  className={`rounded-md border px-3 py-1.5 text-sm transition-colors ${
                    (preset.value && form.schedulePreset === preset.value) ||
                    (!preset.value && form.schedulePreset === "")
                      ? "border-brand bg-brand/10 text-brand font-medium"
                      : "border-border-primary text-text-muted hover:border-border-primary"
                  }`}
                >
                  {preset.label}
                </button>
              ))}
            </div>
            {form.schedulePreset === "" && (
              <input
                type="text"
                value={form.schedule}
                onChange={(e) => updateField("schedule", e.currentTarget.value)}
                className={`${WIZARD_INPUT_CLASS} mt-2`}
                placeholder="e.g. */5 * * * *"
              />
            )}
            {form.schedule && (
              <p className="mt-1 text-xs text-text-muted">
                {cronToHuman(form.schedule)}
              </p>
            )}
            {errors.schedule && (
              <p className="mt-1 text-xs text-error">{errors.schedule}</p>
            )}
          </div>

          {/* Concurrency Policy */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Concurrency Policy
            </label>
            <select
              value={form.concurrencyPolicy}
              onChange={(e) =>
                updateField("concurrencyPolicy", e.currentTarget.value)
              }
              className={WIZARD_INPUT_CLASS}
            >
              {CONCURRENCY_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <p className="mt-1 text-xs text-text-muted">
              {form.concurrencyPolicy === "Allow" &&
                "Multiple jobs can run concurrently."}
              {form.concurrencyPolicy === "Forbid" &&
                "Skip new run if previous job is still running."}
              {form.concurrencyPolicy === "Replace" &&
                "Cancel the running job and start a new one."}
            </p>
          </div>

          {/* Restart Policy */}
          <div>
            <label className="block text-sm font-medium text-text-secondary">
              Restart Policy
            </label>
            <select
              value={form.restartPolicy}
              onChange={(e) =>
                updateField("restartPolicy", e.currentTarget.value)
              }
              className={WIZARD_INPUT_CLASS}
            >
              {RESTART_POLICY_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>

          {/* History limits */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-text-secondary">
                Successful History
              </label>
              <input
                type="number"
                min={0}
                max={100}
                value={form.successfulJobsHistoryLimit}
                onChange={(e) =>
                  updateField(
                    "successfulJobsHistoryLimit",
                    e.currentTarget.value,
                  )
                }
                className={WIZARD_INPUT_CLASS}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-text-secondary">
                Failed History
              </label>
              <input
                type="number"
                min={0}
                max={100}
                value={form.failedJobsHistoryLimit}
                onChange={(e) =>
                  updateField("failedJobsHistoryLimit", e.currentTarget.value)
                }
                className={WIZARD_INPUT_CLASS}
              />
            </div>
          </div>

          {/* Suspend toggle */}
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={form.suspend}
              onChange={(e) => updateField("suspend", e.currentTarget.checked)}
              className="rounded border-border-primary"
            />
            <span className="text-sm text-text-secondary">
              Create suspended (will not run until resumed)
            </span>
          </div>
        </div>
      )}

      {/* Step 1: Container */}
      {currentStep === 1 && (
        <ContainerForm
          image={form.container.image}
          command={form.container.command}
          args={form.container.args}
          ports={form.container.ports}
          envVars={form.container.envVars}
          requestCpu={form.container.requestCpu}
          requestMemory={form.container.requestMemory}
          limitCpu={form.container.limitCpu}
          limitMemory={form.container.limitMemory}
          errors={errors}
          onChange={updateContainerField}
        />
      )}

      {/* Step 2: Review */}
      {currentStep === 2 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/workloads/cronjobs"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
