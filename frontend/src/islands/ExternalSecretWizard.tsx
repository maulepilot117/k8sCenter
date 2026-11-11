import { useEffect, useRef, useState } from "react";
import { ExternalSecretForm } from "@/components/wizard/ExternalSecretForm.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import {
  DEFAULT_NAMESPACE,
  useAdoptSelectedNamespace,
} from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

export interface ExternalSecretWizardDataItem {
  secretKey: string;
  key: string;
  property: string;
  /** True once the user has typed in this row's path field. */
  pathTouched: boolean;
}

export interface ExternalSecretWizardForm {
  name: string;
  namespace: string;
  storeRefName: string;
  storeRefKind: "SecretStore" | "ClusterSecretStore";
  refreshInterval: string;
  targetSecretName: string;
  /** True once the user explicitly edited targetSecretName themselves. */
  targetSecretNameTouched: boolean;
  data: ExternalSecretWizardDataItem[];
}

export interface ExternalSecretWizardStoreOption {
  name: string;
  /** Empty string for ClusterSecretStore. */
  namespace?: string;
  kind: "SecretStore" | "ClusterSecretStore";
  provider: string;
}

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Store & data" },
  { label: "Review", sub: "Preview & apply" },
];

function newDataItem(): ExternalSecretWizardDataItem {
  return { secretKey: "", key: "", property: "", pathTouched: false };
}

function initialForm(): ExternalSecretWizardForm {
  return {
    name: "",
    namespace: DEFAULT_NAMESPACE,
    storeRefName: "",
    storeRefKind: "SecretStore",
    refreshInterval: "1h",
    targetSecretName: "",
    targetSecretNameTouched: false,
    data: [newDataItem()],
  };
}

export interface ExternalSecretWizardProps {
  onClose?: () => void;
}

export default function ExternalSecretWizard({
  onClose,
}: ExternalSecretWizardProps) {
  const close = onClose ?? (() => globalThis.history.back());
  const isClient = useIsClient();
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<ExternalSecretWizardForm>(initialForm);
  useAdoptSelectedNamespace((namespace) =>
    setForm((prev) => ({ ...prev, namespace })),
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const [namespaceList] = useNamespaces();

  const [stores, setStores] = useState<ExternalSecretWizardStoreOption[]>([]);
  const [storesLoading, setStoresLoading] = useState(true);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewSeq = useRef(0);

  useDirtyGuard(dirty);

  // Load both Namespaced and Cluster stores once so the dropdown can switch
  // between them without an extra round-trip. allSettled ensures a single
  // failure (e.g. user lacks ClusterSecretStore permission) doesn't discard
  // the other slice.
  useEffect(() => {
    let cancelled = false;
    Promise.allSettled([esoApi.listStores(), esoApi.listClusterStores()])
      .then(([nsResult, clResult]) => {
        if (cancelled) return;
        const list: ExternalSecretWizardStoreOption[] = [];
        if (nsResult.status === "fulfilled") {
          for (const s of nsResult.value.data ?? []) {
            list.push({
              name: s.name,
              namespace: s.namespace ?? "",
              kind: "SecretStore",
              provider: s.provider ?? "",
            });
          }
        } else {
          console.warn("Failed to load SecretStores:", nsResult.reason);
        }
        if (clResult.status === "fulfilled") {
          for (const s of clResult.value.data ?? []) {
            list.push({
              name: s.name,
              kind: "ClusterSecretStore",
              provider: s.provider ?? "",
            });
          }
        } else {
          console.warn("Failed to load ClusterSecretStores:", clResult.reason);
        }
        setStores(list);
      })
      .finally(() => {
        if (!cancelled) setStoresLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function updateField(field: keyof ExternalSecretWizardForm, value: unknown) {
    setDirty(true);
    // Switching kind clears the selected store so a stale selection doesn't
    // bleed into the new kind's option list.
    if (field === "storeRefKind") {
      setForm((prev) => ({
        ...prev,
        storeRefKind: value as "SecretStore" | "ClusterSecretStore",
        storeRefName: "",
      }));
      return;
    }
    setForm(
      (prev) => ({ ...prev, [field]: value }) as ExternalSecretWizardForm,
    );
  }

  function updateData(
    index: number,
    field: "secretKey" | "key" | "property",
    value: string,
  ) {
    setDirty(true);
    setForm((prev) => ({
      ...prev,
      data: prev.data.map((item, i) =>
        i === index ? { ...item, [field]: value } : item,
      ),
    }));
  }

  function markPathTouched(index: number) {
    setForm((prev) => {
      if (prev.data[index].pathTouched) return prev;
      return {
        ...prev,
        data: prev.data.map((item, i) =>
          i === index ? { ...item, pathTouched: true } : item,
        ),
      };
    });
  }

  function addDataItem() {
    setDirty(true);
    setForm((prev) => ({ ...prev, data: [...prev.data, newDataItem()] }));
  }

  function removeDataItem(index: number) {
    setDirty(true);
    setForm((prev) => ({
      ...prev,
      data: prev.data.filter((_, i) => i !== index),
    }));
  }

  function validate(): boolean {
    const f = form;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name = "Must be a valid DNS label";
    }
    if (!f.namespace || !DNS_LABEL_REGEX.test(f.namespace)) {
      errs.namespace = "Must be a valid DNS label";
    }
    if (!f.storeRefName) {
      errs["storeRef.name"] = "Select a store";
    } else if (!DNS_LABEL_REGEX.test(f.storeRefName)) {
      errs["storeRef.name"] = "Store name must be a valid DNS label";
    }
    if (!f.targetSecretName || !DNS_LABEL_REGEX.test(f.targetSecretName)) {
      errs.targetSecretName = "Must be a valid DNS label";
    }
    if (
      f.refreshInterval &&
      !/^(0|([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$/i.test(f.refreshInterval)
    ) {
      errs.refreshInterval = "Must be a Go duration (e.g. 1h, 30m, 1h30m)";
    }
    if (!f.data.length) {
      errs.data = "At least one data item is required";
    }
    f.data.forEach((item, i) => {
      if (!item.secretKey) {
        errs[`data[${i}].secretKey`] = "Required";
      }
      if (!item.key.trim()) {
        errs[`data[${i}].remoteRef.key`] = "Required";
      }
    });

    setErrors(errs);
    return Object.keys(errs).length === 0;
  }

  async function fetchPreview() {
    const seq = ++previewSeq.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = form;

    const payload = {
      name: f.name,
      namespace: f.namespace,
      storeRef: {
        name: f.storeRefName,
        kind: f.storeRefKind,
      },
      refreshInterval: f.refreshInterval || undefined,
      targetSecretName: f.targetSecretName,
      data: f.data.map((d) => ({
        secretKey: d.secretKey,
        remoteRef: {
          key: d.key,
          property: d.property || undefined,
        },
      })),
    };

    try {
      const resp = await esoApi.previewExternalSecret(payload);
      if (seq !== previewSeq.current) return;
      setPreviewYaml(resp.data.yaml);
    } catch (err) {
      if (seq !== previewSeq.current) return;
      setPreviewError(
        err instanceof Error ? err.message : "Failed to generate preview",
      );
    } finally {
      if (seq === previewSeq.current) setPreviewLoading(false);
    }
  }

  async function goNext() {
    if (currentStep === 0) {
      if (!validate()) return;
      setCurrentStep(1);
      await fetchPreview();
    }
  }

  function goBack() {
    if (currentStep > 0) setCurrentStep(currentStep - 1);
  }

  if (!isClient) return null;

  const keyIcon = (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <circle cx="7.5" cy="15.5" r="4.5" />
      <path d="M21 2l-9.6 9.6" />
      <path d="M15.5 7.5L17 6l3 3-1.5 1.5" />
    </svg>
  );

  return (
    <WizardShell
      title="Create ExternalSecret"
      icon={keyIcon}
      subtitle={`Step ${currentStep + 1} of ${STEPS.length}`}
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={async () => {
        if (currentStep === STEPS.length - 1) {
          close();
        } else {
          await goNext();
        }
      }}
      nextLabel={currentStep === STEPS.length - 1 ? "Close" : "Continue"}
      yaml={
        currentStep === STEPS.length - 1 ? previewYaml || undefined : undefined
      }
    >
      {currentStep === 0 && (
        <ExternalSecretForm
          form={form}
          errors={errors}
          namespaces={namespaceList}
          stores={stores}
          storesLoading={storesLoading}
          onUpdate={updateField}
          onUpdateData={updateData}
          onAddDataItem={addDataItem}
          onRemoveDataItem={removeDataItem}
          onPathFieldTouched={markPathTouched}
        />
      )}

      {currentStep === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/external-secrets/external-secrets"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
