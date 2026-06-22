import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { esoApi } from "@/lib/eso-api.ts";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { ExternalSecretForm } from "@/components/wizard/ExternalSecretForm.tsx";

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
    namespace: initialNamespace(),
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

export default function ExternalSecretWizard(
  { onClose }: ExternalSecretWizardProps,
) {
  const close = onClose ?? (() => globalThis.history.back());
  const currentStep = useSignal(0);
  const form = useSignal<ExternalSecretWizardForm>(initialForm());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);
  const namespaces = useNamespaces();

  const stores = useSignal<ExternalSecretWizardStoreOption[]>([]);
  const storesLoading = useSignal(true);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);
  const previewSeq = useRef(0);

  useDirtyGuard(dirty);

  // Load both Namespaced and Cluster stores once so the dropdown can switch
  // between them without an extra round-trip. allSettled ensures a single
  // failure (e.g. user lacks ClusterSecretStore permission) doesn't discard
  // the other slice.
  useEffect(() => {
    if (!IS_BROWSER) return;
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
        stores.value = list;
      })
      .finally(() => {
        if (!cancelled) storesLoading.value = false;
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function updateField(field: keyof ExternalSecretWizardForm, value: unknown) {
    dirty.value = true;
    // Switching kind clears the selected store so a stale selection doesn't
    // bleed into the new kind's option list.
    if (field === "storeRefKind") {
      form.value = {
        ...form.value,
        storeRefKind: value as "SecretStore" | "ClusterSecretStore",
        storeRefName: "",
      };
      return;
    }
    form.value = { ...form.value, [field]: value } as ExternalSecretWizardForm;
  }

  function updateData(
    index: number,
    field: "secretKey" | "key" | "property",
    value: string,
  ) {
    dirty.value = true;
    const next = form.value.data.map((item, i) =>
      i === index ? { ...item, [field]: value } : item
    );
    form.value = { ...form.value, data: next };
  }

  function markPathTouched(index: number) {
    if (form.value.data[index].pathTouched) return;
    const next = form.value.data.map((item, i) =>
      i === index ? { ...item, pathTouched: true } : item
    );
    form.value = { ...form.value, data: next };
  }

  function addDataItem() {
    dirty.value = true;
    form.value = { ...form.value, data: [...form.value.data, newDataItem()] };
  }

  function removeDataItem(index: number) {
    dirty.value = true;
    form.value = {
      ...form.value,
      data: form.value.data.filter((_, i) => i !== index),
    };
  }

  function validate(): boolean {
    const f = form.value;
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

    errors.value = errs;
    return Object.keys(errs).length === 0;
  }

  async function fetchPreview() {
    const seq = ++previewSeq.current;
    previewLoading.value = true;
    previewError.value = null;
    const f = form.value;

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
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      if (seq !== previewSeq.current) return;
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      if (seq === previewSeq.current) previewLoading.value = false;
    }
  }

  async function goNext() {
    if (currentStep.value === 0) {
      if (!validate()) return;
      currentStep.value = 1;
      await fetchPreview();
    }
  }

  function goBack() {
    if (currentStep.value > 0) currentStep.value = currentStep.value - 1;
  }

  if (!IS_BROWSER) return null;

  const keyIcon = (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
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
      subtitle={`Step ${currentStep.value + 1} of ${STEPS.length}`}
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i < currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={goBack}
      onNext={async () => {
        if (currentStep.value === STEPS.length - 1) {
          close();
        } else {
          await goNext();
        }
      }}
      nextLabel={currentStep.value === STEPS.length - 1 ? "Close" : "Continue"}
      yaml={currentStep.value === STEPS.length - 1
        ? (previewYaml.value || undefined)
        : undefined}
    >
      {currentStep.value === 0 && (
        <ExternalSecretForm
          form={form.value}
          errors={errors.value}
          namespaces={namespaces.value}
          stores={stores.value}
          storesLoading={storesLoading.value}
          onUpdate={updateField}
          onUpdateData={updateData}
          onAddDataItem={addDataItem}
          onRemoveDataItem={removeDataItem}
          onPathFieldTouched={markPathTouched}
        />
      )}

      {currentStep.value === 1 && (
        <WizardReviewStep
          yaml={previewYaml.value}
          onYamlChange={(v) => {
            previewYaml.value = v;
          }}
          loading={previewLoading.value}
          error={previewError.value}
          detailBasePath="/external-secrets/external-secrets"
        />
      )}
    </WizardShell>
  );
}
