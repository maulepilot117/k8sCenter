import { useEffect, useRef, useState } from "react";
import Field from "@/components/ui/form/Field.tsx";
import Select from "@/components/ui/form/Select.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useStorageClasses } from "@/lib/hooks/use-storage-classes.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import type { StorageClassItem } from "@/lib/wizard-types.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import {
  DEFAULT_NAMESPACE,
  useAdoptSelectedNamespace,
} from "@/src/lib/namespace.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

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

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Source PVC & snapshot class" },
  { label: "Review", sub: "Preview & apply" },
];

function generateSnapshotName(pvcName: string): string {
  const now = new Date();
  const ts =
    now.getFullYear().toString() +
    (now.getMonth() + 1).toString().padStart(2, "0") +
    now.getDate().toString().padStart(2, "0") +
    "-" +
    now.getHours().toString().padStart(2, "0") +
    now.getMinutes().toString().padStart(2, "0") +
    now.getSeconds().toString().padStart(2, "0");
  return `${pvcName}-snap-${ts}`;
}

function buildManifest(f: SnapshotFormState): string {
  const classLine = f.volumeSnapshotClassName
    ? `\n  volumeSnapshotClassName: ${f.volumeSnapshotClassName}`
    : "";
  return `apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: ${f.name || "<name>"}
  namespace: ${f.namespace || "<namespace>"}
spec:
  source:
    persistentVolumeClaimName: ${f.sourcePVC || "<source-pvc>"}${classLine}`;
}

export default function SnapshotWizard({
  onClose,
  preselectedNs,
  preselectedPvc,
}: {
  onClose?: () => void;
  preselectedNs?: string;
  preselectedPvc?: string;
}) {
  const close = onClose ?? (() => globalThis.history.back());
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<SnapshotFormState>(() => ({
    name: preselectedPvc ? generateSnapshotName(preselectedPvc) : "",
    namespace: preselectedNs || DEFAULT_NAMESPACE,
    sourcePVC: preselectedPvc || "",
    volumeSnapshotClassName: "",
  }));
  useAdoptSelectedNamespace(
    (namespace) => setForm((prev) => ({ ...prev, namespace })),
    !preselectedNs,
  );
  const [errors, setErrors] = useState<Record<string, string>>({});

  const [namespaceList] = useNamespaces();
  const [pvcs, setPvcs] = useState<PVCItem[]>([]);
  const [snapshotClasses, setSnapshotClasses] = useState<SnapshotClassItem[]>(
    [],
  );
  const storageClasses = useStorageClasses();
  const [snapshotsAvailable, setSnapshotsAvailable] = useState(true);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);
  const formRef = useLatest(form);

  // Fetch snapshot classes
  useEffect(() => {
    apiGet<{ data: SnapshotClassItem[]; metadata: { available: boolean } }>(
      "/v1/storage/snapshot-classes",
    )
      .then((resp) => {
        if (resp.data && Array.isArray(resp.data.data)) {
          setSnapshotClasses(resp.data.data);
        }
        if (resp.data?.metadata?.available === false) {
          setSnapshotsAvailable(false);
        }
      })
      .catch(() => {});
  }, []);

  // Fetch PVCs when namespace changes. The form moves off DEFAULT_NAMESPACE
  // right after hydration, so a response for the namespace it left must not
  // land after the one for the namespace it moved to.
  useEffect(() => {
    const ns = form.namespace;
    if (!ns) return;
    let current = true;
    apiGet<PVCItem[]>(`/v1/resources/pvcs/${ns}?limit=500`)
      .then((resp) => {
        if (current && Array.isArray(resp.data)) setPvcs(resp.data);
      })
      .catch(() => {
        if (current) setPvcs([]);
      });
    return () => {
      current = false;
    };
  }, [form.namespace]);

  const updateField = (field: string, value: unknown) => {
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  // Filter PVCs to Bound only
  const boundPVCs = pvcs.filter((p) => p.status?.phase === "Bound");

  // Filter snapshot classes by PVC's provisioner
  const selectedPVC = boundPVCs.find((p) => p.metadata.name === form.sourcePVC);
  const pvcStorageClass = selectedPVC?.spec?.storageClassName || "";
  const pvcProvisioner =
    storageClasses.find(
      (sc: StorageClassItem) => sc.metadata.name === pvcStorageClass,
    )?.provisioner || "";
  const filteredSnapshotClasses = pvcProvisioner
    ? snapshotClasses.filter((sc) => sc.driver === pvcProvisioner)
    : snapshotClasses;

  const validateStep = (): boolean => {
    const f = form;
    const errs: Record<string, string> = {};
    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.sourcePVC) errs.sourcePVC = "Required";
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = formRef.current;
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

  // CRD unavailable — render inline notice inside the modal body
  if (!snapshotsAvailable) {
    return (
      <WizardShell
        title="Create Volume Snapshot"
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
            This cluster does not have the snapshot.storage.k8s.io CRDs
            installed. Install the{" "}
            <a
              href="https://github.com/kubernetes-csi/external-snapshotter"
              target="_blank"
              rel="noopener noreferrer"
              style={{ textDecoration: "underline" }}
            >
              CSI snapshot controller
            </a>{" "}
            to enable VolumeSnapshot support.
          </p>
        </div>
      </WizardShell>
    );
  }

  const f = form;
  // Snapshot class select: use raw names as option values; "(default)" shown as hint
  const snapshotClassSelectOptions = [
    "",
    ...filteredSnapshotClasses.map((sc) => sc.name),
  ];
  const defaultSnapshotClass =
    filteredSnapshotClasses.find((sc) => sc.isDefault)?.name ?? "";

  return (
    <WizardShell
      title="Create Volume Snapshot"
      subtitle={`Step ${step + 1} of 2 · namespace ${f.namespace}`}
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
          <circle cx="10" cy="10" r="7" />
          <path d="M10 6v4l3 3" />
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
      yaml={step === 0 ? buildManifest(f) : undefined}
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
          <Field label="Namespace">
            <Select
              value={f.namespace}
              options={namespaceList}
              onChange={(v) => {
                setForm((prev) => ({
                  ...prev,
                  namespace: v,
                  sourcePVC: "",
                  name: "",
                }));
              }}
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

          <Field label="Source PVC">
            <Select
              value={f.sourcePVC}
              options={["", ...boundPVCs.map((p) => p.metadata.name)]}
              onChange={(v) => {
                setForm((prev) => ({
                  ...prev,
                  sourcePVC: v,
                  name: v ? generateSnapshotName(v) : "",
                  volumeSnapshotClassName: "",
                }));
              }}
            />
            {errors.sourcePVC && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.sourcePVC}
              </p>
            )}
            {boundPVCs.length === 0 && pvcs.length > 0 && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--text-muted)",
                  marginTop: "5px",
                }}
              >
                No bound PVCs in this namespace
              </p>
            )}
          </Field>

          <Field
            label="VolumeSnapshot Class"
            hint={
              defaultSnapshotClass
                ? `Cluster default: ${defaultSnapshotClass}`
                : "Leave blank to use cluster default"
            }
          >
            <Select
              value={f.volumeSnapshotClassName}
              options={snapshotClassSelectOptions}
              onChange={(v) => updateField("volumeSnapshotClassName", v)}
            />
            {pvcProvisioner &&
              filteredSnapshotClasses.length === 0 &&
              snapshotClasses.length > 0 && (
                <p
                  style={{
                    fontSize: "11.5px",
                    color: "var(--warning)",
                    marginTop: "5px",
                  }}
                >
                  No snapshot classes match provisioner "{pvcProvisioner}"
                </p>
              )}
          </Field>

          <Field label="Name">
            <TextField
              value={f.name}
              onInput={(v) => updateField("name", v)}
              placeholder="e.g. my-pvc-snap-20260323-120000"
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
        </div>
      )}

      {step === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/storage/snapshots"
        />
      )}
    </WizardShell>
  );
}
