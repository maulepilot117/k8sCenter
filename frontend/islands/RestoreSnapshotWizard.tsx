import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet, apiPost } from "@/lib/api.ts";
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

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Name, size & access mode" },
  { label: "Review", sub: "Preview & apply" },
];

const SIZE_UNITS = ["Mi", "Gi", "Ti"];
const ACCESS_MODE_VALUES = ACCESS_MODES.map((m) => m.value);

function parseSize(sizeStr: string): { value: string; unit: string } {
  const match = sizeStr.match(/^(\d+(?:\.\d+)?)\s*(Gi|Ti|Mi)?$/);
  if (match) return { value: match[1], unit: match[2] || "Gi" };
  return { value: sizeStr.replace(/[^0-9.]/g, "") || "10", unit: "Gi" };
}

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

function getSnapshotParamsFromUrl(): SnapshotParams | null {
  if (typeof globalThis.location === "undefined") return null;
  const p = new URLSearchParams(globalThis.location.search);
  const name = p.get("name");
  if (!name) return null;
  return {
    ns: p.get("ns") || "default",
    name,
    restoreSize: p.get("restoreSize") || "",
    snapshotClass: p.get("snapshotClass") || "",
  };
}

function buildManifest(f: RestoreFormState, snapshotName: string): string {
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
    - ${f.accessMode || "ReadWriteOnce"}${storageClass}
  resources:
    requests:
      storage: ${f.sizeValue || "10"}${f.sizeUnit}
  dataSource:
    name: ${snapshotName || "<snapshot>"}
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io`;
}

export default function RestoreSnapshotWizard(
  { onClose, snapshotParams: snapshotParamsProp }: {
    onClose?: () => void;
    /** Pre-populated from the snapshot's Restore action. Pass null from the
     *  deep-link route — the wizard will read URL search params itself. */
    snapshotParams?: SnapshotParams | null;
  },
) {
  const close = onClose ?? (() => globalThis.history.back());
  // When rendered from the deep-link route, snapshotParamsProp is null and we
  // fall back to parsing window.location.search on the client.
  const snapshotParams = snapshotParamsProp ?? getSnapshotParamsFromUrl();

  const parsedRestore = snapshotParams
    ? parseSize(snapshotParams.restoreSize)
    : null;
  const minSizeValue = parsedRestore?.value || "10";
  const minSizeUnit = parsedRestore?.unit || "Gi";

  const step = useSignal(0);
  const form = useSignal<RestoreFormState>({
    name: snapshotParams ? `${snapshotParams.name}-restore` : "",
    namespace: snapshotParams?.ns || "default",
    storageClassName: "",
    sizeValue: minSizeValue,
    sizeUnit: minSizeUnit,
    accessMode: "ReadWriteOnce",
  });
  const errors = useSignal<Record<string, string>>({});

  const namespaces = useNamespaces();
  const storageClasses = useStorageClasses();
  const snapshotsAvailable = useSignal(true);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useEffect(() => {
    apiGet<{ metadata: { available: boolean } }>("/v1/storage/snapshot-classes")
      .then((resp) => {
        if (resp.data?.metadata?.available === false) {
          snapshotsAvailable.value = false;
        }
      })
      .catch(() => {});
  }, []);

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
    if (isNaN(size) || size <= 0) {
      errs.sizeValue = "Must be a positive number";
    } else {
      const reqMi = toMi(f.sizeValue, f.sizeUnit);
      const minMi = toMi(minSizeValue, minSizeUnit);
      if (reqMi < minMi) {
        errs.sizeValue =
          `Must be at least ${minSizeValue}${minSizeUnit} (snapshot restore size)`;
      }
    }
    errors.value = errs;
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    if (!snapshotParams) return;
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
        dataSource: {
          name: snapshotParams.name,
          kind: "VolumeSnapshot",
          apiGroup: "snapshot.storage.k8s.io",
        },
      });
      previewYaml.value = resp.data.yaml;
    } catch (err) {
      previewError.value = err instanceof Error
        ? err.message
        : "Failed to generate preview";
    } finally {
      previewLoading.value = false;
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
  const f = form.value;

  // CRD unavailable
  if (!snapshotsAvailable.value) {
    return (
      <WizardShell
        title="Restore from Snapshot"
        steps={STEPS}
        current={0}
        onStep={() => {}}
        onCancel={close}
        onBack={() => {}}
        onNext={close}
        nextLabel="Close"
      >
        <div
          style={{
            padding: "24px",
            borderRadius: "12px",
            border: "1px solid var(--warning)",
            background: "color-mix(in srgb, var(--warning) 8%, transparent)",
            maxWidth: "480px",
          }}
        >
          <p
            style={{
              fontSize: "15px",
              fontWeight: 600,
              color: "var(--warning)",
              margin: "0 0 8px",
            }}
          >
            VolumeSnapshot CRDs Not Installed
          </p>
          <p style={{ fontSize: "13px", color: "var(--warning)", margin: 0 }}>
            VolumeSnapshot support is required for restore operations.
          </p>
        </div>
      </WizardShell>
    );
  }

  // Missing snapshot params guard
  if (!snapshotParams) {
    return (
      <WizardShell
        title="Restore from Snapshot"
        steps={STEPS}
        current={0}
        onStep={() => {}}
        onCancel={close}
        onBack={() => {}}
        onNext={close}
        nextLabel="Close"
      >
        <div
          style={{
            padding: "18px 20px",
            borderRadius: "12px",
            border: "1px solid var(--error)",
            background: "color-mix(in srgb, var(--error) 8%, transparent)",
            maxWidth: "480px",
          }}
        >
          <p
            style={{
              fontSize: "14px",
              fontWeight: 600,
              color: "var(--error)",
              margin: "0 0 6px",
            }}
          >
            Missing snapshot information
          </p>
          <p style={{ fontSize: "13px", color: "var(--error)", margin: 0 }}>
            No snapshot name was provided. Navigate here from a snapshot's
            Restore action.
          </p>
        </div>
      </WizardShell>
    );
  }

  return (
    <WizardShell
      title="Restore from Snapshot"
      subtitle={`Snapshot: ${snapshotParams.name}`}
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
          <path d="M4 10a6 6 0 1 0 6-6" />
          <path d="M4 6v4h4" />
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
      nextLabel={step.value === 0 ? "Preview YAML" : "Done"}
      yaml={step.value === 0
        ? buildManifest(f, snapshotParams.name)
        : undefined}
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
          <Field label="PVC Name">
            <TextField
              value={f.name}
              onInput={(v) => updateField("name", v)}
              placeholder="e.g. my-data-restore"
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
              value={f.namespace}
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
              value={f.storageClassName}
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

          <Field
            label="Size"
            hint={`Minimum: ${minSizeValue}${minSizeUnit} (snapshot restore size)`}
          >
            <div style={{ display: "flex", gap: "10px" }}>
              <TextField
                value={f.sizeValue}
                onInput={(v) => updateField("sizeValue", v)}
                mono
                width="100px"
                placeholder="10"
              />
              <Segmented
                value={f.sizeUnit}
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
              value={f.accessMode}
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
