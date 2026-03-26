import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import {
  DNS_LABEL_REGEX,
  MAX_NODE_PORT,
  MAX_PORT,
  MIN_NODE_PORT,
  PORT_NAME_REGEX,
} from "@/lib/wizard-constants.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { ServiceBasicsStep } from "@/components/wizard/ServiceBasicsStep.tsx";
import { ServicePortsStep } from "@/components/wizard/ServicePortsStep.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

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

const STEPS = [
  { title: "Basics" },
  { title: "Ports & Selector" },
  { title: "Review" },
];

function initialState(): ServiceFormState {
  const ns = IS_BROWSER && selectedNamespace.value !== "all"
    ? selectedNamespace.value
    : "default";
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

export default function ServiceWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<ServiceFormState>(initialState());
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
      .catch(() => {});
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

  const fetchPreview = async () => {
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    // Build labels map
    const labelsMap: Record<string, string> = {};
    for (const l of f.labels) {
      if (l.key) labelsMap[l.key] = l.value;
    }

    // Build selector map
    const selectorMap: Record<string, string> = {};
    for (const s of f.selector) {
      if (s.key) selectorMap[s.key] = s.value;
    }

    // Build ports
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

  if (!IS_BROWSER) {
    return <div class="p-6">Loading wizard...</div>;
  }

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">
          Create Service
        </h1>
        <a
          href="/networking/services"
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
          <ServiceBasicsStep
            name={form.value.name}
            namespace={form.value.namespace}
            type={form.value.type}
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
          <ServicePortsStep
            ports={form.value.ports}
            selector={form.value.selector}
            serviceType={form.value.type}
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
