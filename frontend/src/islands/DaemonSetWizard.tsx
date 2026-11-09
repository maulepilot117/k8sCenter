/** @jsxImportSource react */
import { useCallback, useRef, useState } from "react";
import { Input } from "@/components/ui/Input.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { ContainerForm } from "@/components/wizard/ContainerForm.tsx";
import { apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import {
  DNS_LABEL_REGEX,
  ENV_VAR_NAME_REGEX,
  MAX_PORT,
} from "@/lib/wizard-constants.ts";
import type { EnvVarEntry, PortEntry } from "@/lib/wizard-types.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useSignal } from "@/src/lib/signal-hooks.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Container, nodes & update" },
  { label: "Review", sub: "Preview & apply" },
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

export default function DaemonSetWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<DaemonSetFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  const nodeSelectorKeys = useRowKeys(form.nodeSelector.length);

  useDirtyGuard(dirty);

  const updateField = useCallback(
    (field: string, value: unknown) => {
      dirty.value = true;
      setForm((prev) => ({ ...prev, [field]: value }));
    },
    [dirty],
  );

  const updateNodeSelector = (
    index: number,
    field: "key" | "value",
    val: string,
  ) => {
    dirty.value = true;
    setForm((prev) => {
      const nodeSelector = [...prev.nodeSelector];
      nodeSelector[index] = { ...nodeSelector[index], [field]: val };
      return { ...prev, nodeSelector };
    });
  };

  const addNodeSelector = () => {
    dirty.value = true;
    setForm((prev) => ({
      ...prev,
      nodeSelector: [...prev.nodeSelector, { key: "", value: "" }],
    }));
  };

  const removeNodeSelector = (index: number) => {
    dirty.value = true;
    nodeSelectorKeys.removeAt(index);
    setForm((prev) => ({
      ...prev,
      nodeSelector: prev.nodeSelector.filter((_, idx) => idx !== index),
    }));
  };

  const validateStep = (): boolean => {
    const f = form;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
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

    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const f = form;

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
    if (!validateStep()) return;
    setCurrentStep(1);
    await fetchPreview();
  };

  const manifest = () => {
    const f = form;
    const name = f.name || "<name>";
    const ns = f.namespace || "default";
    return `apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: ${name}\n  namespace: ${ns}\nspec:\n  selector:\n    matchLabels:\n      app: ${name}\n  template:\n    metadata:\n      labels:\n        app: ${name}\n    spec:\n      containers:\n        - name: ${name}\n          image: ${
      f.image || "<image>"
    }`;
  };

  return (
    <WizardShell
      title="Create DaemonSet"
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
          <circle cx="10" cy="10" r="2.5" />
          <path d="M10 3v3M10 14v3M3 10h3M14 10h3" />
          <path d="M5.6 5.6l2.1 2.1M12.3 12.3l2.1 2.1M5.6 14.4l2.1-2.1M12.3 7.7l2.1-2.1" />
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
        if (currentStep === 0) goNext();
        else close();
      }}
      nextLabel={currentStep === 0 ? "Continue" : "Close"}
      yaml={currentStep === 0 ? manifest() : previewYaml}
    >
      {currentStep === 0 && (
        <div className="space-y-8">
          {/* Name & Namespace */}
          <div className="max-w-lg space-y-4">
            <Input
              label="Name"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              placeholder="my-daemonset"
              error={errors.name}
              required
            />

            <NamespaceSelect
              value={form.namespace}
              namespaces={namespaceList}
              error={errors.namespace}
              onChange={(ns) => updateField("namespace", ns)}
            />
          </div>

          {/* Container Form */}
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

          {/* Node Selector */}
          <div className="max-w-2xl space-y-3">
            <label className="block text-sm font-medium text-text-secondary">
              Node Selector
            </label>
            <p className="text-xs text-text-muted">
              Optional. Constrain the DaemonSet to nodes matching these labels.
            </p>
            {form.nodeSelector.map((entry, i) => (
              <div
                key={nodeSelectorKeys.keyAt(i)}
                className="flex items-end gap-2"
              >
                <div className="flex-1">
                  <Input
                    label={i === 0 ? "Key" : undefined}
                    value={entry.key}
                    onChange={(e) =>
                      updateNodeSelector(i, "key", e.currentTarget.value)
                    }
                    placeholder="kubernetes.io/os"
                  />
                </div>
                <div className="flex-1">
                  <Input
                    label={i === 0 ? "Value" : undefined}
                    value={entry.value}
                    onChange={(e) =>
                      updateNodeSelector(i, "value", e.currentTarget.value)
                    }
                    placeholder="linux"
                  />
                </div>
                <RemoveButton
                  onClick={() => removeNodeSelector(i)}
                  title="Remove selector"
                  className="p-2 mb-1"
                />
              </div>
            ))}
            {form.nodeSelector.length < 50 && (
              <button
                type="button"
                onClick={addNodeSelector}
                className="text-sm text-brand hover:text-brand/80"
              >
                + Add Node Selector
              </button>
            )}
          </div>

          {/* Max Unavailable */}
          <div className="max-w-lg">
            <Input
              label="Max Unavailable"
              value={form.maxUnavailable}
              onChange={(e) =>
                updateField("maxUnavailable", e.currentTarget.value)
              }
              placeholder="1 or 25%"
            />
            <p className="mt-1 text-xs text-text-muted">
              Maximum number of pods that can be unavailable during a rolling
              update. Accepts an integer or percentage.
            </p>
          </div>
        </div>
      )}

      {currentStep === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/workloads/daemonsets"
          dirty={dirty}
        />
      )}
    </WizardShell>
  );
}
