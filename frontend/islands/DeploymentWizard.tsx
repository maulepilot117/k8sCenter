import { useSignal } from "@preact/signals";
import { useCallback, useRef } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
  MAX_REPLICAS,
} from "@/lib/wizard-constants.ts";
import { DeploymentBasicsStep } from "@/components/wizard/DeploymentBasicsStep.tsx";
import { DeploymentNetworkStep } from "@/components/wizard/DeploymentNetworkStep.tsx";
import { DeploymentResourcesStep } from "@/components/wizard/DeploymentResourcesStep.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import type { ProbeState } from "@/lib/wizard-types.ts";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

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

const STEPS: WizardStep[] = [
  { label: "Basics", sub: "Name, image & replicas" },
  { label: "Networking", sub: "Ports & env vars" },
  { label: "Resources", sub: "CPU, memory & probes" },
  { label: "Review", sub: "Preview & apply" },
];

function initialState(): DeploymentFormState {
  const ns = initialNamespace();
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

export default function DeploymentWizard(
  { onClose }: { onClose?: () => void },
) {
  const close = onClose ?? (() => globalThis.history.back());
  const currentStep = useSignal(0);
  const form = useSignal<DeploymentFormState>(initialState());
  const namespaces = useNamespaces();
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  // Review step state
  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);
  const previewGen = useRef(0);

  useDirtyGuard(dirty);

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

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
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
      if (gen !== previewGen.current) return;
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      if (gen !== previewGen.current) return;
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      if (gen === previewGen.current) previewLoading.value = false;
    }
  };

  const manifest = () => {
    const f = form.value;
    const name = f.name || "<name>";
    const ns = f.namespace || "default";
    const img = f.image || "<image>";
    return `apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: ${name}\n  namespace: ${ns}\nspec:\n  replicas: ${f.replicas}\n  selector:\n    matchLabels:\n      app: ${name}\n  template:\n    metadata:\n      labels:\n        app: ${name}\n    spec:\n      containers:\n        - name: ${name}\n          image: ${img}`;
  };

  return (
    <WizardShell
      title="Create Deployment"
      subtitle={`${form.value.namespace || "default"}`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linejoin="round"
        >
          <path d="M10 2.5 17 6v8l-7 3.5L3 14V6l7-3.5Z" />
          <path d="M3 6l7 3.5L17 6" />
          <path d="M10 9.5V17" />
        </svg>
      }
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i <= currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={() => (currentStep.value = Math.max(0, currentStep.value - 1))}
      onNext={() => {
        if (currentStep.value < 3) goNext();
        else close();
      }}
      nextLabel={currentStep.value < 3 ? "Continue" : "Close"}
      yaml={currentStep.value < 3 ? manifest() : previewYaml.value}
    >
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
          onNamespaceCreated={(ns) => {
            if (!namespaces.value.includes(ns)) {
              namespaces.value = [...namespaces.value, ns].sort();
            }
          }}
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
    </WizardShell>
  );
}
