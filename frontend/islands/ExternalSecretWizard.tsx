import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { esoApi } from "@/lib/eso-api.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { ExternalSecretForm } from "@/components/wizard/ExternalSecretForm.tsx";
import { Button } from "@/components/ui/Button.tsx";

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

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
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

export default function ExternalSecretWizard() {
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

  useDirtyGuard(dirty);

  // Load both Namespaced and Cluster stores once so the dropdown can switch
  // between them without an extra round-trip.
  useEffect(() => {
    if (!IS_BROWSER) return;
    Promise.all([esoApi.listStores(), esoApi.listClusterStores()])
      .then(([nsResp, clResp]) => {
        const list: ExternalSecretWizardStoreOption[] = [];
        for (const s of nsResp.data ?? []) {
          list.push({
            name: s.name,
            namespace: s.namespace ?? "",
            kind: "SecretStore",
            provider: s.provider ?? "",
          });
        }
        for (const s of clResp.data ?? []) {
          list.push({
            name: s.name,
            kind: "ClusterSecretStore",
            provider: s.provider ?? "",
          });
        }
        stores.value = list;
      })
      .catch(() => {
        stores.value = [];
      })
      .finally(() => {
        storesLoading.value = false;
      });
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
      !/^[0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h)?$/i.test(f.refreshInterval)
    ) {
      errs.refreshInterval = "Must be a Go duration (e.g. 1h, 30m)";
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
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      previewLoading.value = false;
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

  if (!IS_BROWSER) return <div class="p-6">Loading wizard...</div>;

  return (
    <div class="p-6 max-w-4xl mx-auto">
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-2xl font-bold text-text-primary">
          Create ExternalSecret
        </h1>
        <a
          href="/external-secrets/external-secrets"
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
      </div>

      {currentStep.value < 1 && (
        <div class="flex justify-between mt-8">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={currentStep.value === 0}
          >
            Back
          </Button>
          <Button variant="primary" onClick={goNext}>
            Preview YAML
          </Button>
        </div>
      )}

      {currentStep.value === 1 && !previewLoading.value &&
        previewError.value === null && (
        <div class="flex justify-start mt-4">
          <Button variant="ghost" onClick={goBack}>Back</Button>
        </div>
      )}
    </div>
  );
}
