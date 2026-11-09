/** @jsxImportSource react */
import { useEffect, useRef, useState } from "react";
import Field from "@/components/ui/form/Field.tsx";
import Select from "@/components/ui/form/Select.tsx";
import Stepper from "@/components/ui/form/Stepper.tsx";
import TextField from "@/components/ui/form/TextField.tsx";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { DNS_LABEL_REGEX } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { initialNamespace } from "@/src/lib/namespace.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface ScheduledSnapshotFormState {
  name: string;
  namespace: string;
  sourcePVC: string;
  volumeSnapshotClassName: string;
  schedulePreset: string;
  schedule: string;
  retentionCount: number;
}

interface PVCItem {
  metadata: { name: string; namespace: string };
  status?: { phase?: string };
}

interface SnapshotClassItem {
  metadata: { name: string };
  driver?: string;
}

const STEPS: WizardStep[] = [
  { label: "Source & Schedule", sub: "PVC, class & cron" },
  { label: "Retention", sub: "Snapshot count policy" },
  { label: "Review", sub: "Preview & apply" },
];

const SCHEDULE_PRESETS: { label: string; value: string }[] = [
  { label: "Every hour", value: "0 * * * *" },
  { label: "Daily midnight", value: "0 0 * * *" },
  { label: "Weekly Sunday", value: "0 0 * * 0" },
  { label: "Custom", value: "" },
];

function cronToHuman(cron: string): string {
  const t = cron.trim();
  if (t === "0 * * * *") return "Every hour, at minute 0";
  if (t === "0 0 * * *") return "Every day at midnight";
  if (t === "0 0 * * 0") return "Every Sunday at midnight";
  if (!t) return "";
  return `Cron: ${t}`;
}

function buildManifest(f: ScheduledSnapshotFormState): string {
  const scLine = f.volumeSnapshotClassName
    ? `\n  volumeSnapshotClassName: ${f.volumeSnapshotClassName}`
    : "";
  return `# CronJob + ServiceAccount/Role/RoleBinding
apiVersion: batch/v1
kind: CronJob
metadata:
  name: ${f.name || "<name>"}
  namespace: ${f.namespace || "<namespace>"}
spec:
  schedule: "${f.schedule || "0 0 * * *"}"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: snapshot
              # snapshot source: ${f.sourcePVC || "<pvc>"}${scLine}
              # retentionCount: ${f.retentionCount}`;
}

function initialState(): ScheduledSnapshotFormState {
  return {
    name: "",
    namespace: initialNamespace(),
    sourcePVC: "",
    volumeSnapshotClassName: "",
    schedulePreset: "0 0 * * *",
    schedule: "0 0 * * *",
    retentionCount: 5,
  };
}

export default function ScheduledSnapshotWizard({
  onClose,
}: {
  onClose?: () => void;
}) {
  const close = onClose ?? (() => globalThis.history.back());
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<ScheduledSnapshotFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);
  const [pvcs, setPvcs] = useState<PVCItem[]>([]);
  const [snapshotClasses, setSnapshotClasses] = useState<SnapshotClassItem[]>(
    [],
  );
  const [snapshotsAvailable, setSnapshotsAvailable] = useState(true);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);
  const formRef = useLatest(form);

  // Fetch PVCs (bound only) when namespace changes
  const namespace = form.namespace;
  useEffect(() => {
    if (!namespace) return;
    apiGet<PVCItem[]>(`/v1/resources/pvcs/${namespace}?limit=500`)
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          const bound = resp.data.filter((p) => p.status?.phase === "Bound");
          setPvcs(bound);
          setForm((prev) =>
            prev.sourcePVC &&
            !bound.some((p) => p.metadata.name === prev.sourcePVC)
              ? { ...prev, sourcePVC: "" }
              : prev,
          );
        }
      })
      .catch(() => {
        setPvcs([]);
      });
  }, [namespace]);

  // Fetch snapshot classes
  useEffect(() => {
    apiGet<{ data: SnapshotClassItem[]; metadata: { available: boolean } }>(
      "/v1/storage/snapshot-classes",
    )
      .then((resp) => {
        if (resp.data?.metadata?.available === false) {
          setSnapshotsAvailable(false);
          return;
        }
        const classes = resp.data?.data;
        if (Array.isArray(classes)) {
          setSnapshotClasses(classes);
          if (classes.length > 0) {
            setForm((prev) =>
              prev.volumeSnapshotClassName
                ? prev
                : {
                    ...prev,
                    volumeSnapshotClassName: classes[0].metadata.name,
                  },
            );
          }
        }
      })
      .catch(() => {});
  }, []);

  const updateField = (field: string, value: unknown) => {
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  const validateStep0 = (): boolean => {
    const f = formRef.current;
    const errs: Record<string, string> = {};
    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";
    if (!f.sourcePVC) errs.sourcePVC = "Required";
    if (!f.volumeSnapshotClassName) errs.volumeSnapshotClassName = "Required";
    if (!f.schedule.trim()) errs.schedule = "Required";
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const validateStep1 = (): boolean => {
    const errs: Record<string, string> = {};
    const { retentionCount } = formRef.current;
    if (retentionCount < 1 || retentionCount > 100) {
      errs.retentionCount = "Must be between 1 and 100";
    }
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const fetchPreview = async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);
    const f = formRef.current;
    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/scheduled-snapshot/preview",
        {
          name: f.name,
          namespace: f.namespace,
          sourcePVC: f.sourcePVC,
          volumeSnapshotClassName: f.volumeSnapshotClassName,
          schedule: f.schedule,
          retentionCount: f.retentionCount,
        },
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
      if (!validateStep0()) return;
      setStep(1);
    } else if (step === 1) {
      if (!validateStep1()) return;
      setStep(2);
      await fetchPreview();
    } else {
      close();
    }
  };

  const f = form;
  const snapshotClassOptions = [
    "",
    ...snapshotClasses.map((sc) => sc.metadata.name),
  ];

  // CRD unavailable notice
  if (!snapshotsAvailable) {
    return (
      <WizardShell
        title="Schedule Snapshot"
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
            VolumeSnapshot support is required for scheduled snapshots.
          </p>
        </div>
      </WizardShell>
    );
  }

  return (
    <WizardShell
      title="Schedule Snapshot"
      subtitle={`Step ${step + 1} of 3 · namespace ${f.namespace}`}
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
          <path d="M10 6v4l2.5 2.5" />
          <path d="M14 3l1.5 1.5M6 3L4.5 4.5" />
        </svg>
      }
      steps={STEPS}
      current={step}
      onStep={(i) => {
        if (i < step) setStep(i);
      }}
      onCancel={close}
      onBack={() => setStep((current) => Math.max(0, current - 1))}
      onNext={handleNext}
      nextLabel={step === 2 ? "Done" : "Continue"}
      yaml={step < 2 ? buildManifest(f) : undefined}
    >
      {/* Step 0: Source & Schedule */}
      {step === 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "18px",
            maxWidth: "440px",
          }}
        >
          <Field label="Schedule Name">
            <TextField
              value={f.name}
              onInput={(v) => updateField("name", v)}
              placeholder="e.g. daily-db-backup"
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
              value={f.namespace}
              options={namespaceList}
              onChange={(v) => updateField("namespace", v)}
            />
          </Field>

          <Field label="Source PVC">
            <Select
              value={f.sourcePVC}
              options={["", ...pvcs.map((p) => p.metadata.name)]}
              onChange={(v) => updateField("sourcePVC", v)}
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
            {pvcs.length === 0 && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--text-muted)",
                  marginTop: "5px",
                }}
              >
                No bound PVCs found in namespace {f.namespace}
              </p>
            )}
          </Field>

          <Field label="VolumeSnapshotClass">
            <Select
              value={f.volumeSnapshotClassName}
              options={snapshotClassOptions}
              onChange={(v) => updateField("volumeSnapshotClassName", v)}
            />
            {errors.volumeSnapshotClassName && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.volumeSnapshotClassName}
              </p>
            )}
            {snapshotClasses.length === 0 && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--text-muted)",
                  marginTop: "5px",
                }}
              >
                No VolumeSnapshotClasses found in cluster
              </p>
            )}
          </Field>

          <Field label="Schedule">
            <div
              style={{
                display: "flex",
                gap: "8px",
                flexWrap: "wrap",
                marginBottom: "10px",
              }}
            >
              {SCHEDULE_PRESETS.map((preset) => {
                const active = preset.value
                  ? f.schedulePreset === preset.value
                  : f.schedulePreset === "";
                return (
                  <button
                    key={preset.label}
                    type="button"
                    onClick={() => {
                      if (preset.value) {
                        updateField("schedule", preset.value);
                        updateField("schedulePreset", preset.value);
                      } else {
                        updateField("schedulePreset", "");
                      }
                    }}
                    style={{
                      padding: "6px 14px",
                      fontSize: "12px",
                      fontWeight: 600,
                      borderRadius: "8px",
                      border: `1px solid ${
                        active ? "var(--accent)" : "var(--border-subtle)"
                      }`,
                      background: active ? "var(--accent-dim)" : "transparent",
                      color: active ? "var(--accent)" : "var(--text-secondary)",
                      cursor: "pointer",
                      fontFamily: "inherit",
                      transition: "all 0.15s",
                    }}
                  >
                    {preset.label}
                  </button>
                );
              })}
            </div>
            {f.schedulePreset === "" && (
              <TextField
                value={f.schedule}
                onInput={(v) => updateField("schedule", v)}
                mono
                placeholder="e.g. 0 */6 * * *"
              />
            )}
            {f.schedule && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--text-muted)",
                  marginTop: "5px",
                }}
              >
                {cronToHuman(f.schedule)}
              </p>
            )}
            {errors.schedule && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.schedule}
              </p>
            )}
          </Field>
        </div>
      )}

      {/* Step 1: Retention */}
      {step === 1 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "20px",
            maxWidth: "440px",
          }}
        >
          <Field
            label="Retention Count"
            hint="Number of most-recent snapshots to keep. Older ones are deleted after each run."
          >
            <Stepper
              value={f.retentionCount}
              min={1}
              max={100}
              onChange={(v) => updateField("retentionCount", v)}
            />
            {errors.retentionCount && (
              <p
                style={{
                  fontSize: "11.5px",
                  color: "var(--error)",
                  marginTop: "5px",
                }}
              >
                {errors.retentionCount}
              </p>
            )}
          </Field>

          <div
            style={{
              padding: "14px 16px",
              borderRadius: "12px",
              border: "1px solid var(--accent)",
              background: "var(--accent-dim)",
            }}
          >
            <p
              style={{
                fontSize: "13px",
                color: "var(--accent)",
                margin: 0,
                lineHeight: 1.5,
              }}
            >
              A CronJob will run on schedule{" "}
              <code
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: "12px",
                  background:
                    "color-mix(in srgb, var(--accent) 12%, transparent)",
                  borderRadius: "4px",
                  padding: "1px 5px",
                }}
              >
                {f.schedule}
              </code>
              , create a VolumeSnapshot of{" "}
              <strong>{f.sourcePVC || "(PVC)"}</strong>, and keep the{" "}
              <strong>{f.retentionCount}</strong> most recent snapshots.
            </p>
            <p
              style={{
                fontSize: "12px",
                color: "var(--accent)",
                margin: "8px 0 0",
                lineHeight: 1.5,
              }}
            >
              Also creates a ServiceAccount, Role, and RoleBinding with minimum
              required permissions.
            </p>
          </div>
        </div>
      )}

      {/* Step 2: Review */}
      {step === 2 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/workloads/cronjobs"
        />
      )}
    </WizardShell>
  );
}
