import { useEffect, useRef, useState } from "react";
import Field from "@/components/ui/form/Field.tsx";
import Segmented from "@/components/ui/form/Segmented.tsx";
import Select from "@/components/ui/form/Select.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import { apiPost } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useStorageClasses } from "@/lib/hooks/use-storage-classes.ts";
import { ACCESS_MODES, DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import {
  DEFAULT_NAMESPACE,
  useAdoptSelectedNamespace,
} from "@/src/lib/namespace.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

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
    namespace: DEFAULT_NAMESPACE,
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
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<PVCFormState>(initialState);
  useAdoptSelectedNamespace((namespace) =>
    setForm((prev) => ({ ...prev, namespace })),
  );
  const [errors, setErrors] = useState<Record<string, string>>({});

  const [namespaceList] = useNamespaces();
  const storageClasses = useStorageClasses();

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);
  const formRef = useLatest(form);

  // Auto-select first storage class when loaded
  useEffect(() => {
    if (storageClasses.length > 0) {
      const first = storageClasses[0].metadata.name;
      setForm((prev) =>
        prev.storageClassName ? prev : { ...prev, storageClassName: first },
      );
    }
  }, [storageClasses]);

  const updateField = (field: string, value: unknown) => {
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  const validateStep = (): boolean => {
    const f = form;
    const errs: Record<string, string> = {};
    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.storageClassName) errs.storageClassName = "Required";
    const size = parseFloat(f.sizeValue);
    if (Number.isNaN(size) || size <= 0)
      errs.sizeValue = "Must be a positive number";
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = formRef.current;
    try {
      const resp = await apiPost<{ yaml: string }>("/v1/wizards/pvc/preview", {
        name: f.name,
        namespace: f.namespace,
        storageClassName: f.storageClassName,
        size: `${f.sizeValue}${f.sizeUnit}`,
        accessMode: f.accessMode,
      });
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

  const handleNext = async () => {
    if (step === 0) {
      if (!validateStep()) return;
      setStep(1);
      await fetchPreview();
    } else {
      close();
    }
  };

  const scNames = storageClasses.map(
    (sc: StorageClassItem) => sc.metadata.name,
  );

  return (
    <WizardShell
      title="Create PVC"
      subtitle={`Step ${step + 1} of 2 · namespace ${form.namespace}`}
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
          <rect x="3" y="5" width="14" height="10" rx="2" />
          <path d="M7 10h6M10 7v6" />
        </svg>
      }
      steps={STEPS}
      current={step}
      onStep={(i) => {
        if (i < step) setStep(i);
      }}
      onCancel={close}
      onBack={() => setStep((prev) => Math.max(0, prev - 1))}
      onNext={handleNext}
      nextLabel={step === 0 ? "Continue" : "Done"}
      yaml={step === 0 ? buildManifest(form) : undefined}
    >
      {step === 0 && (
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
              value={form.name}
              onInput={(v) => updateField("name", v)}
              placeholder="e.g. my-data"
            />
            {errors.name && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.name}
              </p>
            )}
          </Field>

          <Field label="Namespace">
            <Select
              value={form.namespace}
              options={namespaceList}
              onChange={(v) => updateField("namespace", v)}
            />
            {errors.namespace && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.namespace}
              </p>
            )}
          </Field>

          <Field label="Storage Class">
            <Select
              value={form.storageClassName}
              options={["", ...scNames]}
              onChange={(v) => updateField("storageClassName", v)}
            />
            {errors.storageClassName && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.storageClassName}
              </p>
            )}
          </Field>

          <Field label="Size">
            <div style={{ display: "flex", gap: "10px" }}>
              <TextField
                value={form.sizeValue}
                onInput={(v) => updateField("sizeValue", v)}
                mono
                width="100px"
                placeholder="10"
              />
              <Segmented
                value={form.sizeUnit}
                options={SIZE_UNITS}
                onChange={(v) => updateField("sizeUnit", v)}
              />
            </div>
            {errors.sizeValue && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.sizeValue}
              </p>
            )}
          </Field>

          <Field label="Access Mode">
            <Segmented
              value={form.accessMode}
              options={ACCESS_MODE_VALUES}
              onChange={(v) => updateField("accessMode", v)}
            />
          </Field>
        </div>
      )}

      {step === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/storage/pvcs"
        />
      )}
    </WizardShell>
  );
}
