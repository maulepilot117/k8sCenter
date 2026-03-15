import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
  MAX_REPLICAS,
} from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { DeploymentBasicsStep } from "@/components/wizard/DeploymentBasicsStep.tsx";
import { DeploymentNetworkStep } from "@/components/wizard/DeploymentNetworkStep.tsx";
import { DeploymentResourcesStep } from "@/components/wizard/DeploymentResourcesStep.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";
import type { ProbeState } from "@/lib/wizard-types.ts";

interface DeploymentFormState {
  name: string;
  namespace: string;
  image: string;
  replicas: number;
  labels: Array<{ key: string; value: string }>;
  ports: Array<{ name: string; containerPort: number; protocol: string }>;
  envVars: Array<{
    name: string;
    type: "literal" | "configmap" | "secret";
    value: string;
    ref: string;
    key: string;
  }>;
  cpuRequest: string;
  memoryRequest: string;
  cpuLimit: string;
  memoryLimit: string;
  livenessProbe: ProbeState | null;
  readinessProbe: ProbeState | null;
  strategy: { type: string; maxSurge: string; maxUnavailable: string };
}

const STEPS = [
  { title: "Basics" },
  { title: "Networking" },
  { title: "Resources" },
  { title: "Review" },
];

function initialState(): DeploymentFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
  return {
    name: "",
    namespace: ns,
    image: "",
    replicas: 1,
    labels: [{ key: "app", value: "" }],
    ports: [],
    envVars: [],
    cpuRequest: "",
    memoryRequest: "",
    cpuLimit: "",
    memoryLimit: "",
    livenessProbe: null,
    readinessProbe: null,
    strategy: { type: "RollingUpdate", maxSurge: "25%", maxUnavailable: "25%" },
  };
}

export default function DeploymentWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<DeploymentFormState>(initialState());
  const namespaces = useSignal<string[]>(["default"]);
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  // Review step state
  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  // Fetch namespaces
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Array<{ metadata: { name: string } }>>("/v1/resources/namespaces")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          namespaces.value = resp.data.map((ns) => ns.metadata.name).sort();
        }
      })
      .catch(() => {
        // Keep default
      });
  }, []);

  // beforeunload guard
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty.value) {
        e.preventDefault();
      }
    };
    globalThis.addEventListener("beforeunload", handler);
    return () => globalThis.removeEventListener("beforeunload", handler);
  }, []);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    const f = { ...form.value, [field]: value };
    // Auto-sync app label value with name
    if (field === "name") {
      const idx = f.labels.findIndex((l: { key: string }) => l.key === "app");
      if (idx >= 0) {
        const updated = [...f.labels];
        updated[idx] = { ...updated[idx], value: value as string };
        f.labels = updated;
      }
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
      if (!f.image) errs.image = "Required";
      if (f.replicas < 0 || f.replicas > MAX_REPLICAS) {
        errs.replicas = `Must be between 0 and ${MAX_REPLICAS}`;
      }
    }

    if (step === 1) {
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

  const goNext = async () => {
    if (!validateStep(currentStep.value)) return;

    if (currentStep.value === 2) {
      // Moving to Review step — fetch preview
      currentStep.value = 3;
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

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    // Build the backend payload
    const payload: Record<string, unknown> = {
      name: f.name,
      namespace: f.namespace,
      image: f.image,
      replicas: f.replicas,
    };

    // Labels: convert array to map, filter empty keys
    const labelsMap: Record<string, string> = {};
    for (const l of f.labels) {
      if (l.key) labelsMap[l.key] = l.value;
    }
    if (Object.keys(labelsMap).length > 0) payload.labels = labelsMap;

    // Ports: filter empty entries
    const ports = f.ports.filter((p) => p.containerPort > 0);
    if (ports.length > 0) payload.ports = ports;

    // Env vars: convert to backend format, filter empty names
    const envVars = f.envVars
      .filter((e) => e.name)
      .map((e) => {
        if (e.type === "configmap") {
          return { name: e.name, configMapRef: e.ref, key: e.key };
        }
        if (e.type === "secret") {
          return { name: e.name, secretRef: e.ref, key: e.key };
        }
        return { name: e.name, value: e.value };
      });
    if (envVars.length > 0) payload.envVars = envVars;

    // Resources
    if (f.cpuRequest || f.memoryRequest || f.cpuLimit || f.memoryLimit) {
      payload.resources = {
        requestCpu: f.cpuRequest || undefined,
        requestMemory: f.memoryRequest || undefined,
        limitCpu: f.cpuLimit || undefined,
        limitMemory: f.memoryLimit || undefined,
      };
    }

    // Probes
    if (f.livenessProbe || f.readinessProbe) {
      payload.probes = {
        liveness: f.livenessProbe || undefined,
        readiness: f.readinessProbe || undefined,
      };
    }

    // Strategy
    if (f.strategy.type) {
      payload.strategy = f.strategy;
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/deployment/preview",
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
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white">
          Create Deployment
        </h1>
        <a
          href="/workloads/deployments"
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
        {currentStep.value === 0 && (
          <DeploymentBasicsStep
            name={form.value.name}
            namespace={form.value.namespace}
            image={form.value.image}
            replicas={form.value.replicas}
            labels={form.value.labels}
            namespaces={namespaces.value}
            errors={errors.value}
            onChange={updateField}
          />
        )}

        {currentStep.value === 1 && (
          <DeploymentNetworkStep
            ports={form.value.ports}
            envVars={form.value.envVars}
            errors={errors.value}
            onChange={updateField}
          />
        )}

        {currentStep.value === 2 && (
          <DeploymentResourcesStep
            cpuRequest={form.value.cpuRequest}
            memoryRequest={form.value.memoryRequest}
            cpuLimit={form.value.cpuLimit}
            memoryLimit={form.value.memoryLimit}
            livenessProbe={form.value.livenessProbe}
            readinessProbe={form.value.readinessProbe}
            strategy={form.value.strategy}
            errors={errors.value}
            onChange={updateField}
          />
        )}

        {currentStep.value === 3 && (
          <WizardReviewStep
            yaml={previewYaml.value}
            onYamlChange={(v) => {
              previewYaml.value = v;
            }}
            loading={previewLoading.value}
            error={previewError.value}
            detailBasePath="/workloads/deployments"
          />
        )}
      </div>

      {/* Navigation buttons */}
      {currentStep.value < 3 && (
        <div class="flex justify-between mt-8">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            {currentStep.value === 2 ? "Preview YAML" : "Next"}
          </Button>
        </div>
      )}

      {currentStep.value === 3 && !previewLoading.value &&
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
