import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { ACCESS_MODES, DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useStorageClasses } from "@/lib/hooks/use-storage-classes.ts";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";
import Field from "@/components/ui/form/Field.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import Select from "@/components/ui/form/Select.tsx";
import Segmented from "@/components/ui/form/Segmented.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";

interface PVCFormState {
  name: string;
  namespace: string;
  storageClassName: string;
  sizeValue: string;
  sizeUnit: string;
  accessMode: string;
}

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Name, size & access" },
  { label: "Review", sub: "Preview & apply" },
];

const SIZE_UNITS = ["Mi", "Gi", "Ti"];
const ACCESS_MODE_VALUES = ACCESS_MODES.map((m) => m.value);

function initialState(): PVCFormState {
  return {
    name: "",
    namespace: initialNamespace(),
    storageClassName: "",
    sizeValue: "10",
    sizeUnit: "Gi",
    accessMode: "ReadWriteOnce",
  };
}

function buildManifest(f: PVCFormState): string {
  const accessMode = f.accessMode || "ReadWriteOnce";
  const storageClass = f.storageClassName
    ? `\n  storageClassName: ${f.storageClassName}`
    : "";
  return `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${f.name || "<name>"}
  namespace: ${f.namespace || "<namespace>"}
spec:
  accessModes:
    - ${accessMode}${storageClass}
  resources:
    requests:
      storage: ${f.sizeValue || "10"}${f.sizeUnit}`;
}

export default function PVCWizard({ onClose }: { onClose?: () => void }) {
  const close = onClose ?? (() => globalThis.history.back());
  const step = useSignal(0);
  const form = useSignal<PVCFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});

  const namespaces = useNamespaces();
  const storageClasses = useStorageClasses();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);
  const previewGen = useRef(0);

  // Auto-select first storage class when loaded
  useEffect(() => {
    if (storageClasses.value.length > 0 && !form.value.storageClassName) {
      form.value = {
        ...form.value,
        storageClassName: storageClasses.value[0].metadata.name,
      };
    }
  }, [storageClasses.value]);

  const updateField = (field: string, value: unknown) => {
    form.value = { ...form.value, [field]: value };
  };

  const validateStep = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};
    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.storageClassName) errs.storageClassName = "Required";
    const size = parseFloat(f.sizeValue);
    if (isNaN(size) || size <= 0) errs.sizeValue = "Must be a positive number";
    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    previewLoading.value = true;
    previewError.value = null;
    const f = form.value;
    try {
      const resp = await apiPost<{ yaml: string }>("/v1/wizards/pvc/preview", {
        name: f.name,
        namespace: f.namespace,
        storageClassName: f.storageClassName,
        size: `${f.sizeValue}${f.sizeUnit}`,
        accessMode: f.accessMode,
      });
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

  const handleNext = async () => {
    if (step.value === 0) {
      if (!validateStep()) return;
      step.value = 1;
      await fetchPreview();
    } else {
      close();
    }
  };

  const scNames = storageClasses.value.map((sc: StorageClassItem) =>
    sc.metadata.name
  );

  return (
    <WizardShell
      title="Create PVC"
      subtitle={`Step ${
        step.value + 1
      } of 2 · namespace ${form.value.namespace}`}
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
          <rect x="3" y="5" width="14" height="10" rx="2" />
          <path d="M7 10h6M10 7v6" />
        </svg>
      }
      steps={STEPS}
      current={step.value}
      onStep={(i) => {
        if (i < step.value) step.value = i;
      }}
      onCancel={close}
      onBack={() => (step.value = Math.max(0, step.value - 1))}
      onNext={handleNext}
      nextLabel={step.value === 0 ? "Continue" : "Done"}
      yaml={step.value === 0 ? buildManifest(form.value) : undefined}
    >
      {step.value === 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "18px",
            maxWidth: "440px",
          }}
        >
          <Field label="Name">
            <TextField
              value={form.value.name}
              onInput={(v) => updateField("name", v)}
              placeholder="e.g. my-data"
            />
            {errors.value.name && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.value.name}
              </p>
            )}
          </Field>

          <Field label="Namespace">
            <Select
              value={form.value.namespace}
              options={namespaces.value}
              onChange={(v) => updateField("namespace", v)}
            />
            {errors.value.namespace && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.value.namespace}
              </p>
            )}
          </Field>

          <Field label="Storage Class">
            <Select
              value={form.value.storageClassName}
              options={["", ...scNames]}
              onChange={(v) => updateField("storageClassName", v)}
            />
            {errors.value.storageClassName && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.value.storageClassName}
              </p>
            )}
          </Field>

          <Field label="Size">
            <div style={{ display: "flex", gap: "10px" }}>
              <TextField
                value={form.value.sizeValue}
                onInput={(v) => updateField("sizeValue", v)}
                mono
                width="100px"
                placeholder="10"
              />
              <Segmented
                value={form.value.sizeUnit}
                options={SIZE_UNITS}
                onChange={(v) => updateField("sizeUnit", v)}
              />
            </div>
            {errors.value.sizeValue && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.value.sizeValue}
              </p>
            )}
          </Field>

          <Field label="Access Mode">
            <Segmented
              value={form.value.accessMode}
              options={ACCESS_MODE_VALUES}
              onChange={(v) => updateField("accessMode", v)}
            />
          </Field>
        </div>
      )}

      {step.value === 1 && (
        <WizardReviewStep
          yaml={previewYaml.value}
          onYamlChange={(v) => {
            previewYaml.value = v;
          }}
          loading={previewLoading.value}
          error={previewError.value}
          detailBasePath="/storage/pvcs"
        />
      )}
    </WizardShell>
  );
}
