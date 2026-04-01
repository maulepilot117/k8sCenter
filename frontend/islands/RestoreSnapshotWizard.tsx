import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import {
  ACCESS_MODES,
  DNS_LABEL_REGEX,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useStorageClasses } from "@/lib/hooks/use-storage-classes.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface RestoreFormState {
  name: string;
  namespace: string;
  storageClassName: string;
  sizeValue: string;
  sizeUnit: string;
  accessMode: string;
}

interface SnapshotParams {
  ns: string;
  name: string;
  restoreSize: string;
  snapshotClass: string;
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

/** Parse restoreSize string (e.g."10Gi","500Mi","1Ti") into value and unit. */
function parseSize(sizeStr: string): { value: string; unit: string } {
  const match = sizeStr.match(/^(\d+(?:\.\d+)?)\s*(Gi|Ti|Mi)?$/);
  if (match) {
    return { value: match[1], unit: match[2] || "Gi" };
  }
  return { value: sizeStr.replace(/[^0-9.]/g, "") || "10", unit: "Gi" };
}

/** Convert size value + unit to a comparable number in Mi. */
function toMi(value: string, unit: string): number {
  const num = parseFloat(value);
  if (isNaN(num)) return 0;
  switch (unit) {
    case "Ti":
      return num * 1024 * 1024;
    case "Gi":
      return num * 1024;
    case "Mi":
      return num;
    default:
      return num * 1024;
  }
}

function getSnapshotParams(): SnapshotParams | null {
  if (!IS_BROWSER) return null;
  const params = new URLSearchParams(globalThis.location.search);
  const ns = params.get("ns");
  const name = params.get("name");
  const restoreSize = params.get("restoreSize");
  const snapshotClass = params.get("snapshotClass") || "";
  if (!name) return null;
  return {
    ns: ns || "default",
    name,
    restoreSize: restoreSize || "",
    snapshotClass,
  };
}

export default function RestoreSnapshotWizard() {
  const snapshotParams = IS_BROWSER ? getSnapshotParams() : null;

  const currentStep = useSignal(0);
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  const storageClasses = useStorageClasses();

  const snapshotsAvailable = useSignal(true);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<{ metadata: { available: boolean } }>("/v1/storage/snapshot-classes")
      .then((resp) => {
        if (resp.data?.metadata?.available === false) {
          snapshotsAvailable.value = false;
        }
      })
      .catch(() => {});
  }, []);

  // Parse restoreSize from params for initial form state and validation
  const parsedRestore = snapshotParams
    ? parseSize(snapshotParams.restoreSize)
    : null;
  const minRestoreSizeValue = parsedRestore?.value || "10";
  const minRestoreSizeUnit = parsedRestore?.unit || "Gi";

  const form = useSignal<RestoreFormState>({
    name: snapshotParams ? `${snapshotParams.name}-restore` : "",
    namespace: snapshotParams?.ns || "default",
    storageClassName: "",
    sizeValue: minRestoreSizeValue,
    sizeUnit: minRestoreSizeUnit,
    accessMode: "ReadWriteOnce",
  });

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

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
    if (isNaN(size) || size <= 0) {
      errs.sizeValue = "Must be a positive number";
    } else {
      // Validate size >= restoreSize
      const requestedMi = toMi(f.sizeValue, f.sizeUnit);
      const minMi = toMi(minRestoreSizeValue, minRestoreSizeUnit);
      if (requestedMi < minMi) {
        errs.sizeValue =
          `Must be at least ${minRestoreSizeValue}${minRestoreSizeUnit} (snapshot restore size)`;
      }
    }

    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const goNext = async () => {
    if (!validateStep()) return;
    currentStep.value = 1;
    await fetchPreview();
  };

  const goBack = () => {
    if (currentStep.value > 0) currentStep.value = 0;
  };

  const fetchPreview = async () => {
    if (!snapshotParams) return;
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    const payload = {
      name: f.name,
      namespace: f.namespace,
      storageClassName: f.storageClassName,
      size: `${f.sizeValue}${f.sizeUnit}`,
      accessMode: f.accessMode,
      dataSource: {
        name: snapshotParams.name,
        kind: "VolumeSnapshot",
        apiGroup: "snapshot.storage.k8s.io",
      },
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/pvc/preview",
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

  if (!snapshotsAvailable.value) {
    return (
      <div class="p-6">
        <div class="rounded-lg border border-warning bg-warning-dim p-6 text-center">
          <p class="text-lg font-medium text-warning">
            VolumeSnapshot CRDs Not Installed
          </p>
          <p class="mt-2 text-sm text-warning">
            This cluster does not have the snapshot.storage.k8s.io CRDs
            installed. VolumeSnapshot support is required for restore
            operations.
          </p>
          <a
            href="/storage/snapshots"
            class="mt-4 inline-block text-sm text-warning hover:text-warning"
          >
            Back to Snapshots
          </a>
        </div>
      </div>
    );
  }

  // Guard: if no snapshot name in URL params, show error and link back
  if (!snapshotParams) {
    return (
      <div class="p-6">
        <div class="rounded-md bg-danger/10 border border-danger/30 p-4 text-danger text-sm">
          <p class="font-medium">Missing snapshot information</p>
          <p class="mt-1">
            No snapshot name was provided. Please navigate to this page from a
            snapshot's"Restore" action.
          </p>
          <a
            href="/storage/snapshots"
            class="mt-3 inline-block text-sm font-medium text-brand hover:text-brand/80"
          >
            Back to Snapshots
          </a>
        </div>
      </div>
    );
  }

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-text-primary">
            Restore from Snapshot
          </h1>
          <p class="mt-1 text-sm text-text-muted">
            Creating a new PVC from snapshot{""}
            <span class="font-medium text-text-secondary">
              {snapshotParams.name}
            </span>
          </p>
        </div>
        <a
          href="/storage/snapshots"
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
          <div class="mx-auto max-w-lg space-y-4">
            <div>
              <label class="block text-sm font-medium text-text-secondary">
                PVC Name <span class="text-error">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class={WIZARD_INPUT_CLASS}
                placeholder="e.g. my-data-restore"
              />
              {errors.value.name && (
                <p class="mt-1 text-xs text-error">{errors.value.name}</p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Namespace <span class="text-error">*</span>
              </label>
              <select
                value={form.value.namespace}
                onChange={(e) =>
                  updateField(
                    "namespace",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                {namespaces.value.map((ns) => (
                  <option key={ns} value={ns}>{ns}</option>
                ))}
              </select>
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Storage Class <span class="text-error">*</span>
              </label>
              <select
                value={form.value.storageClassName}
                onChange={(e) =>
                  updateField(
                    "storageClassName",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                <option value="">Select a storage class...</option>
                {storageClasses.value.map((sc: StorageClassItem) => (
                  <option key={sc.metadata.name} value={sc.metadata.name}>
                    {sc.metadata.name}
                  </option>
                ))}
              </select>
              {errors.value.storageClassName && (
                <p class="mt-1 text-xs text-error">
                  {errors.value.storageClassName}
                </p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Size <span class="text-error">*</span>
              </label>
              <p class="text-xs text-text-muted mb-1">
                Minimum: {minRestoreSizeValue}
                {minRestoreSizeUnit} (snapshot restore size)
              </p>
              <div class="mt-1 flex gap-2">
                <input
                  type="number"
                  min="1"
                  value={form.value.sizeValue}
                  onInput={(e) =>
                    updateField(
                      "sizeValue",
                      (e.target as HTMLInputElement).value,
                    )}
                  class="w-32 rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand"
                />
                <select
                  value={form.value.sizeUnit}
                  onChange={(e) =>
                    updateField(
                      "sizeUnit",
                      (e.target as HTMLSelectElement).value,
                    )}
                  class="rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary"
                >
                  <option value="Gi">Gi</option>
                  <option value="Ti">Ti</option>
                </select>
              </div>
              {errors.value.sizeValue && (
                <p class="mt-1 text-xs text-error">
                  {errors.value.sizeValue}
                </p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Access Mode
              </label>
              <div class="mt-2 space-y-2">
                {ACCESS_MODES.map((mode) => (
                  <label
                    key={mode.value}
                    class="flex items-center gap-3 rounded-md border border-border-primary px-3 py-2 cursor-pointer hover:bg-surface /50"
                  >
                    <input
                      type="radio"
                      name="accessMode"
                      value={mode.value}
                      checked={form.value.accessMode === mode.value}
                      onChange={() => updateField("accessMode", mode.value)}
                      class="text-brand focus:ring-brand"
                    />
                    <div>
                      <span class="text-sm font-medium text-text-secondary">
                        {mode.label}
                      </span>
                      <span class="ml-2 text-xs text-text-muted">
                        {mode.desc}
                      </span>
                    </div>
                  </label>
                ))}
              </div>
            </div>
          </div>
        )}

        {currentStep.value === 1 && (
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
      </div>

      {currentStep.value === 0 && (
        <div class="mt-8 flex justify-end">
          <Button variant="primary" onClick={goNext}>
            Preview YAML
          </Button>
        </div>
      )}

      {currentStep.value === 1 && !previewLoading.value &&
        previewError.value === null && (
        <div class="mt-4 flex justify-start">
          <Button variant="ghost" onClick={goBack}>
            Back
          </Button>
        </div>
      )}
    </div>
  );
}
