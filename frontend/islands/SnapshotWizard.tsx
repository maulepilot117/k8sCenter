import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useStorageClasses } from "@/lib/hooks/use-storage-classes.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface SnapshotFormState {
  name: string;
  namespace: string;
  sourcePVC: string;
  volumeSnapshotClassName: string;
}

interface PVCItem {
  metadata: { name: string };
  spec?: {
    storageClassName?: string;
    resources?: { requests?: { storage?: string } };
  };
  status?: { phase?: string };
}

interface SnapshotClassItem {
  name: string;
  driver: string;
  deletionPolicy: string;
  isDefault: boolean;
}

const STEPS = [
  { title: "Configure" },
  { title: "Review" },
];

function generateSnapshotName(pvcName: string): string {
  const now = new Date();
  const ts = now.getFullYear().toString() +
    (now.getMonth() + 1).toString().padStart(2, "0") +
    now.getDate().toString().padStart(2, "0") + "-" +
    now.getHours().toString().padStart(2, "0") +
    now.getMinutes().toString().padStart(2, "0") +
    now.getSeconds().toString().padStart(2, "0");
  return `${pvcName}-snap-${ts}`;
}

function initialState(
  preselectedNs?: string,
  preselectedPvc?: string,
): SnapshotFormState {
  const ns = preselectedNs || initialNamespace();
  return {
    name: preselectedPvc ? generateSnapshotName(preselectedPvc) : "",
    namespace: ns,
    sourcePVC: preselectedPvc || "",
    volumeSnapshotClassName: "",
  };
}

export default function SnapshotWizard() {
  // Parse URL query params for pre-selection (from PVC action menu)
  const urlParams = IS_BROWSER
    ? new URLSearchParams(globalThis.location.search)
    : null;
  const preselectedNs = urlParams?.get("ns") || undefined;
  const preselectedPvc = urlParams?.get("pvc") || undefined;

  const currentStep = useSignal(0);
  const form = useSignal<SnapshotFormState>(
    initialState(preselectedNs, preselectedPvc),
  );
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  const pvcs = useSignal<PVCItem[]>([]);
  const snapshotClasses = useSignal<SnapshotClassItem[]>([]);
  const storageClasses = useStorageClasses();

  const snapshotsAvailable = useSignal(true);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  // Fetch snapshot classes
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<{ data: SnapshotClassItem[]; metadata: { available: boolean } }>(
      "/v1/storage/snapshot-classes",
    )
      .then((resp) => {
        if (resp.data && Array.isArray(resp.data.data)) {
          snapshotClasses.value = resp.data.data;
        }
        if (resp.data?.metadata?.available === false) {
          snapshotsAvailable.value = false;
        }
      })
      .catch(() => {});
  }, []);

  // Fetch PVCs when namespace changes
  useEffect(() => {
    if (!IS_BROWSER) return;
    const ns = form.value.namespace;
    if (!ns) return;

    apiGet<PVCItem[]>(`/v1/resources/pvcs/${ns}?limit=500`)
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          pvcs.value = resp.data;
        }
      })
      .catch(() => {
        pvcs.value = [];
      });
  }, [form.value.namespace]);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  // Filter PVCs to only show Bound
  const boundPVCs = pvcs.value.filter((p) => p.status?.phase === "Bound");

  // Get the provisioner for the selected PVC's storage class
  const selectedPVC = boundPVCs.find(
    (p) => p.metadata.name === form.value.sourcePVC,
  );
  const pvcStorageClass = selectedPVC?.spec?.storageClassName || "";
  const pvcProvisioner = storageClasses.value.find(
    (sc: StorageClassItem) => sc.metadata.name === pvcStorageClass,
  )?.provisioner || "";

  // Filter snapshot classes to those matching the PVC's provisioner
  const filteredSnapshotClasses = pvcProvisioner
    ? snapshotClasses.value.filter((sc) => sc.driver === pvcProvisioner)
    : snapshotClasses.value;

  const validateStep = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.sourcePVC) errs.sourcePVC = "Required";

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
    previewLoading.value = true;
    previewError.value = null;

    const f = form.value;
    const payload: Record<string, string> = {
      name: f.name,
      namespace: f.namespace,
      sourcePVC: f.sourcePVC,
    };
    if (f.volumeSnapshotClassName) {
      payload.volumeSnapshotClassName = f.volumeSnapshotClassName;
    }

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/snapshot/preview",
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
            installed. Install the{""}
            <a
              href="https://github.com/kubernetes-csi/external-snapshotter"
              target="_blank"
              rel="noopener noreferrer"
              class="underline"
            >
              CSI snapshot controller
            </a>
            {""}
            to enable VolumeSnapshot support.
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

  return (
    <div class="p-6">
      <div class="mb-6 flex items-center justify-between">
        <h1 class="text-2xl font-bold text-text-primary">
          Create Volume Snapshot
        </h1>
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
                Namespace <span class="text-error">*</span>
              </label>
              <select
                value={form.value.namespace}
                onChange={(e) => {
                  const ns = (e.target as HTMLSelectElement).value;
                  updateField("namespace", ns);
                  // Reset PVC selection when namespace changes
                  form.value = {
                    ...form.value,
                    namespace: ns,
                    sourcePVC: "",
                    name: "",
                  };
                }}
                class={WIZARD_INPUT_CLASS}
              >
                {namespaces.value.map((ns) => (
                  <option key={ns} value={ns}>{ns}</option>
                ))}
              </select>
              {errors.value.namespace && (
                <p class="mt-1 text-xs text-error">
                  {errors.value.namespace}
                </p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Source PVC <span class="text-error">*</span>
              </label>
              <select
                value={form.value.sourcePVC}
                onChange={(e) => {
                  const pvc = (e.target as HTMLSelectElement).value;
                  updateField("sourcePVC", pvc);
                  // Auto-generate name when PVC is selected
                  if (pvc) {
                    form.value = {
                      ...form.value,
                      sourcePVC: pvc,
                      name: generateSnapshotName(pvc),
                      volumeSnapshotClassName: "",
                    };
                  }
                }}
                class={WIZARD_INPUT_CLASS}
              >
                <option value="">Select a PVC...</option>
                {boundPVCs.map((pvc) => {
                  const size = pvc.spec?.resources?.requests?.storage ||
                    "unknown";
                  const sc = pvc.spec?.storageClassName || "default";
                  return (
                    <option key={pvc.metadata.name} value={pvc.metadata.name}>
                      {pvc.metadata.name} ({size}, {sc})
                    </option>
                  );
                })}
              </select>
              {boundPVCs.length === 0 && pvcs.value.length > 0 && (
                <p class="mt-1 text-xs text-text-muted">
                  No bound PVCs in this namespace
                </p>
              )}
              {errors.value.sourcePVC && (
                <p class="mt-1 text-xs text-error">
                  {errors.value.sourcePVC}
                </p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                VolumeSnapshot Class
              </label>
              <select
                value={form.value.volumeSnapshotClassName}
                onChange={(e) =>
                  updateField(
                    "volumeSnapshotClassName",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                <option value="">Auto (cluster default)</option>
                {filteredSnapshotClasses.map((sc) => (
                  <option key={sc.name} value={sc.name}>
                    {sc.name}
                    {sc.isDefault ? " (default)" : ""}
                  </option>
                ))}
              </select>
              {pvcProvisioner && filteredSnapshotClasses.length === 0 &&
                snapshotClasses.value.length > 0 && (
                <p class="mt-1 text-xs text-warning">
                  No snapshot classes match the provisioner"{pvcProvisioner}"
                </p>
              )}
            </div>

            <div>
              <label class="block text-sm font-medium text-text-secondary">
                Name <span class="text-error">*</span>
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class={WIZARD_INPUT_CLASS}
                placeholder="e.g. my-pvc-snap-20260323-120000"
              />
              {errors.value.name && (
                <p class="mt-1 text-xs text-error">{errors.value.name}</p>
              )}
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
            detailBasePath="/storage/snapshots"
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
