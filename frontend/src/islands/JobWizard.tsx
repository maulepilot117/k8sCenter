/** @jsxImportSource react */
import { useCallback, useRef, useState } from "react";
import { Select } from "@/components/ui/Select.tsx";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import {
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
  RESTART_POLICY_OPTIONS,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import type { EnvVarEntry, PortEntry } from "@/lib/wizard-types.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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

const STEPS: WizardStep[] = [
  { label: "Basics", sub: "Name & job config" },
  { label: "Container", sub: "Image & resources" },
  { label: "Review", sub: "Preview & apply" },
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

export default function JobWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<JobFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);

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

  const validateStep = (step: number): boolean => {
    const f = formRef.current;
    const errs: Record<string, string> = {};

    if (step === 0) {
      if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
        errs.name =
          "Must be lowercase alphanumeric with hyphens, 1-63 characters";
      }
      if (!f.namespace) errs.namespace = "Required";
      const completions = parseInt(f.completions, 10);
      if (f.completions && (Number.isNaN(completions) || completions < 0)) {
        errs.completions = "Must be a non-negative integer";
      }
      const parallelism = parseInt(f.parallelism, 10);
      if (f.parallelism && (Number.isNaN(parallelism) || parallelism < 0)) {
        errs.parallelism = "Must be a non-negative integer";
      }
      const backoffLimit = parseInt(f.backoffLimit, 10);
      if (f.backoffLimit && (Number.isNaN(backoffLimit) || backoffLimit < 0)) {
        errs.backoffLimit = "Must be a non-negative integer";
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

    const completions = parseInt(f.completions, 10);
    if (!Number.isNaN(completions)) payload.completions = completions;

    const parallelism = parseInt(f.parallelism, 10);
    if (!Number.isNaN(parallelism)) payload.parallelism = parallelism;

    const backoffLimit = parseInt(f.backoffLimit, 10);
    if (!Number.isNaN(backoffLimit)) payload.backoffLimit = backoffLimit;

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/job/preview",
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
      // Moving to Review step
      setCurrentStep(2);
      await fetchPreview();
    } else {
      setCurrentStep(currentStep + 1);
    }
  };

  const manifest = () => {
    const f = form;
    const name = f.name || "<name>";
    const ns = f.namespace || "default";
    return `apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: ${name}\n  namespace: ${ns}\nspec:\n  completions: ${
      f.completions || 1
    }\n  parallelism: ${f.parallelism || 1}\n  backoffLimit: ${
      f.backoffLimit || 6
    }\n  template:\n    spec:\n      restartPolicy: ${f.restartPolicy}\n      containers:\n        - name: ${name}\n          image: ${
      f.image || "<image>"
    }`;
  };

  const nextLabel = currentStep === 2 ? "Close" : "Continue";

  return (
    <WizardShell
      title="Create Job"
      subtitle={form.namespace || "default"}
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
          <circle cx="10" cy="10" r="7.5" />
          <path d="M10 6v4l2.5 2.5" />
          <path d="M7.5 2.5l5 0" />
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
      nextLabel={nextLabel}
      yaml={currentStep < 2 ? manifest() : previewYaml}
    >
      {currentStep === 0 && (
        <div className="space-y-6 max-w-2xl">
          {/* Name */}
          <div className="space-y-1">
            <label className="block text-sm font-medium text-text-secondary">
              Job Name <span className="text-danger">*</span>
            </label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              placeholder="my-batch-job"
              className={WIZARD_INPUT_CLASS}
            />
            {errors.name && (
              <p className="text-sm text-danger">{errors.name}</p>
            )}
          </div>

          {/* Namespace */}
          <div className="space-y-1">
            <label className="block text-sm font-medium text-text-secondary">
              Namespace <span className="text-danger">*</span>
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
            {errors.namespace && (
              <p className="text-sm text-danger">{errors.namespace}</p>
            )}
          </div>

          {/* Completions */}
          <div className="space-y-1">
            <label className="block text-sm font-medium text-text-secondary">
              Completions
            </label>
            <input
              type="number"
              value={form.completions}
              onChange={(e) =>
                updateField("completions", e.currentTarget.value)
              }
              placeholder="1"
              min={0}
              className={WIZARD_INPUT_CLASS}
            />
            {errors.completions && (
              <p className="text-sm text-danger">{errors.completions}</p>
            )}
            <p className="text-xs text-text-muted">
              Number of successful completions required.
            </p>
          </div>

          {/* Parallelism */}
          <div className="space-y-1">
            <label className="block text-sm font-medium text-text-secondary">
              Parallelism
            </label>
            <input
              type="number"
              value={form.parallelism}
              onChange={(e) =>
                updateField("parallelism", e.currentTarget.value)
              }
              placeholder="1"
              min={0}
              className={WIZARD_INPUT_CLASS}
            />
            {errors.parallelism && (
              <p className="text-sm text-danger">{errors.parallelism}</p>
            )}
            <p className="text-xs text-text-muted">
              Maximum number of pods running in parallel.
            </p>
          </div>

          {/* Backoff Limit */}
          <div className="space-y-1">
            <label className="block text-sm font-medium text-text-secondary">
              Backoff Limit
            </label>
            <input
              type="number"
              value={form.backoffLimit}
              onChange={(e) =>
                updateField("backoffLimit", e.currentTarget.value)
              }
              placeholder="6"
              min={0}
              className={WIZARD_INPUT_CLASS}
            />
            {errors.backoffLimit && (
              <p className="text-sm text-danger">{errors.backoffLimit}</p>
            )}
            <p className="text-xs text-text-muted">
              Number of retries before marking the job as failed.
            </p>
          </div>

          {/* Restart Policy */}
          <Select
            label="Restart Policy"
            value={form.restartPolicy}
            onChange={(e) =>
              updateField("restartPolicy", e.currentTarget.value)
            }
            options={RESTART_POLICY_OPTIONS}
          />
        </div>
      )}

      {currentStep === 1 && (
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
      )}

      {currentStep === 2 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/workloads/jobs"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
