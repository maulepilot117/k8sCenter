import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  MAX_NODE_PORT,
  MAX_PORT,
  MIN_NODE_PORT,
  PORT_NAME_REGEX,
} from "@/lib/wizard-constants.ts";
import { ServiceBasicsStep } from "@/components/wizard/ServiceBasicsStep.tsx";
import { ServicePortsStep } from "@/components/wizard/ServicePortsStep.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

interface ServiceFormState {
  name: string;
  namespace: string;
  type: "ClusterIP" | "NodePort" | "LoadBalancer";
  labels: Array<{ key: string; value: string }>;
  selector: Array<{ key: string; value: string }>;
  ports: Array<{
    name: string;
    port: number;
    targetPort: number;
    protocol: string;
    nodePort: number;
  }>;
}

const STEPS: WizardStep[] = [
  { label: "Basics", sub: "Name, type & labels" },
  { label: "Ports & Selector", sub: "Routing config" },
  { label: "Review", sub: "Preview & apply" },
];

function initialState(): ServiceFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    type: "ClusterIP",
    labels: [{ key: "app", value: "" }],
    selector: [{ key: "app", value: "" }],
    ports: [{
      name: "",
      port: 80,
      targetPort: 8080,
      protocol: "TCP",
      nodePort: 0,
    }],
  };
}

function buildManifest(f: ServiceFormState): string {
  const firstPort = f.ports[0];
  const selEntry = f.selector[0];
  return `apiVersion: v1\nkind: Service\nmetadata:\n  name: ${
    f.name || "<name>"
  }\n  namespace: ${f.namespace}\nspec:\n  type: ${f.type}\n  selector:\n    ${
    selEntry?.key || "app"
  }: "${selEntry?.value || ""}"\n  ports:\n    - port: ${
    firstPort?.port ?? 80
  }\n      targetPort: ${firstPort?.targetPort ?? 8080}\n      protocol: ${
    firstPort?.protocol ?? "TCP"
  }`;
}

export default function ServiceWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const currentStep = useSignal(0);
  const form = useSignal<ServiceFormState>(initialState());
  const namespaces = useNamespaces();
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    const f = { ...form.value, [field]: value };
    // Auto-sync app label/selector with name
    if (field === "name") {
      const nameVal = value as string;
      const labelIdx = f.labels.findIndex(
        (l: { key: string }) => l.key === "app",
      );
      if (labelIdx >= 0) {
        const updated = [...f.labels];
        updated[labelIdx] = { ...updated[labelIdx], value: nameVal };
        f.labels = updated;
      }
      const selIdx = f.selector.findIndex(
        (s: { key: string }) => s.key === "app",
      );
      if (selIdx >= 0) {
        const updated = [...f.selector];
        updated[selIdx] = { ...updated[selIdx], value: nameVal };
        f.selector = updated;
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
    }

    if (step === 1) {
      const validSelectors = f.selector.filter((s) => s.key);
      if (validSelectors.length === 0) {
        errs.selector = "At least one selector is required";
      }
      const validPorts = f.ports.filter((p) => p.port > 0);
      if (validPorts.length === 0) {
        errs.ports = "At least one port is required";
      }
      f.ports.forEach((p, i) => {
        if (p.name && !PORT_NAME_REGEX.test(p.name)) {
          errs[`ports[${i}].name`] =
            "Must be a valid IANA service name (lowercase, alphanumeric + hyphens, max 15 chars)";
        }
        if (p.port && (p.port < 1 || p.port > MAX_PORT)) {
          errs[`ports[${i}].port`] = `Must be 1-${MAX_PORT}`;
        }
        if (p.targetPort && (p.targetPort < 1 || p.targetPort > MAX_PORT)) {
          errs[`ports[${i}].targetPort`] = `Must be 1-${MAX_PORT}`;
        }
        if (
          p.nodePort &&
          (f.type === "NodePort" || f.type === "LoadBalancer") &&
          (p.nodePort < MIN_NODE_PORT || p.nodePort > MAX_NODE_PORT)
        ) {
          errs[`ports[${i}].nodePort`] =
            `Must be ${MIN_NODE_PORT}-${MAX_NODE_PORT}`;
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
    const labelsMap: Record<string, string> = {};
    for (const l of f.labels) {
      if (l.key) labelsMap[l.key] = l.value;
    }

    const selectorMap: Record<string, string> = {};
    for (const s of f.selector) {
      if (s.key) selectorMap[s.key] = s.value;
    }

    const ports = f.ports
      .filter((p) => p.port > 0)
      .map((p) => ({
        name: p.name || undefined,
        port: p.port,
        targetPort: p.targetPort || p.port,
        protocol: p.protocol || "TCP",
        nodePort: p.nodePort || undefined,
      }));

    const payload = {
      name: f.name,
      namespace: f.namespace,
      type: f.type,
      labels: Object.keys(labelsMap).length > 0 ? labelsMap : undefined,
      selector: selectorMap,
      ports,
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/service/preview",
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

  const f = form.value;

  return (
    <WizardShell
      title="Create Service"
      subtitle={`Step ${currentStep.value + 1} of 3 · namespace ${f.namespace}`}
      icon={
        <svg
          width="21"
          height="21"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <circle cx="10" cy="10" r="7.2" />
          <path d="M2.8 10h14.4M10 2.8v14.4" />
          <path d="M5 5c2.4 2 8.6 2 10 0M5 15c1.4-2 7.6-2 10 0" />
        </svg>
      }
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i < currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={goBack}
      onNext={goNext}
      nextLabel={currentStep.value === 1
        ? "Preview YAML"
        : currentStep.value === 2
        ? "Close"
        : "Next"}
      yaml={currentStep.value < 2 ? buildManifest(f) : undefined}
    >
      {currentStep.value === 0 && (
        <ServiceBasicsStep
          name={f.name}
          namespace={f.namespace}
          type={f.type}
          labels={f.labels}
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
        <ServicePortsStep
          ports={f.ports}
          selector={f.selector}
          serviceType={f.type}
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
          detailBasePath="/networking/services"
        />
      )}
    </WizardShell>
  );
}
