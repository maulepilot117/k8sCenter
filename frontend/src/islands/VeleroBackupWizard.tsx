/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import type { BackupStorageLocation } from "@/lib/velero-types.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface BackupFormState {
  name: string;
  namespace: string;
  includedNamespaces: string[];
  excludedNamespaces: string[];
  storageLocation: string;
  ttl: string;
  snapshotVolumes: boolean;
}

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Name, namespaces & storage" },
  { label: "Review", sub: "Preview & apply" },
];

const TTL_OPTIONS = [
  { value: "", label: "Default" },
  { value: "24h", label: "1 day" },
  { value: "168h", label: "7 days" },
  { value: "720h", label: "30 days" },
  { value: "2160h", label: "90 days" },
  { value: "8760h", label: "1 year" },
];

function generateBackupName(): string {
  const now = new Date();
  const ts =
    now.getFullYear().toString() +
    (now.getMonth() + 1).toString().padStart(2, "0") +
    now.getDate().toString().padStart(2, "0") +
    "-" +
    now.getHours().toString().padStart(2, "0") +
    now.getMinutes().toString().padStart(2, "0") +
    now.getSeconds().toString().padStart(2, "0");
  return `backup-${ts}`;
}

function initialState(): BackupFormState {
  return {
    name: generateBackupName(),
    namespace: "velero",
    includedNamespaces: [],
    excludedNamespaces: [],
    storageLocation: "",
    ttl: "",
    snapshotVolumes: true,
  };
}

interface Props {
  onClose?: () => void;
}

export default function VeleroBackupWizard({ onClose }: Props) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<BackupFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const formRef = useLatest(form);

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);
  const [bsls, setBsls] = useState<BackupStorageLocation[]>([]);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  // Fetch BSLs
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<{ backupStorageLocations: BackupStorageLocation[] }>(
      "/v1/velero/locations",
    )
      .then((resp) => {
        if (resp.data?.backupStorageLocations) {
          setBsls(resp.data.backupStorageLocations);
          const defaultBsl = resp.data.backupStorageLocations.find(
            (b) => b.default,
          );
          if (defaultBsl) {
            setForm((prev) =>
              prev.storageLocation
                ? prev
                : { ...prev, storageLocation: defaultBsl.name },
            );
          }
        }
      })
      .catch(() => {});
  }, []);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    setDirty(true);
    setForm((prev) => ({ ...prev, [field]: value }));
  }, []);

  const validate = useCallback((): boolean => {
    const newErrors: Record<string, string> = {};
    if (!DNS_LABEL_REGEX.test(formRef.current.name)) {
      newErrors.name = "Must be a valid DNS label (lowercase, hyphens allowed)";
    }
    setErrors(newErrors);
    return Object.keys(newErrors).length === 0;
  }, []);

  const fetchPreview = useCallback(async () => {
    const gen = ++previewGen.current;
    setPreviewLoading(true);
    setPreviewError(null);
    try {
      const f = formRef.current;
      const body = {
        name: f.name,
        namespace: f.namespace,
        includedNamespaces:
          f.includedNamespaces.length > 0 ? f.includedNamespaces : undefined,
        excludedNamespaces:
          f.excludedNamespaces.length > 0 ? f.excludedNamespaces : undefined,
        storageLocation: f.storageLocation || undefined,
        ttl: f.ttl || undefined,
        snapshotVolumes: f.snapshotVolumes,
      };
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/velero-backup/preview",
        body,
      );
      if (gen !== previewGen.current) return;
      setPreviewYaml(resp.data.yaml);
    } catch (e: unknown) {
      if (gen !== previewGen.current) return;
      setPreviewError(e instanceof Error ? e.message : "Preview failed");
    }
    if (gen === previewGen.current) setPreviewLoading(false);
  }, []);

  const goNext = useCallback(async () => {
    if (currentStep === 0) {
      if (!validate()) return;
      await fetchPreview();
    }
    setCurrentStep((s) => s + 1);
  }, [currentStep, validate, fetchPreview]);

  const goBack = useCallback(() => {
    setCurrentStep((s) => s - 1);
  }, []);

  return (
    <WizardShell
      title="Create Backup"
      icon={
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M12 2a8 8 0 0 1 8 8v1a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V10a8 8 0 0 1 8-8z" />
          <path d="M12 14v6M9 17l3 3 3-3" />
        </svg>
      }
      subtitle={`Step ${currentStep + 1} of 2`}
      steps={STEPS}
      current={currentStep}
      onStep={(i) => {
        if (i < currentStep) setCurrentStep(i);
      }}
      onCancel={close}
      onBack={goBack}
      onNext={currentStep === 0 ? goNext : close}
      nextLabel={currentStep === 0 ? "Continue" : "Close"}
      yaml={previewYaml || undefined}
    >
      {currentStep === 0 && (
        <div className="space-y-6">
          {/* Name */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-backup-name"
            >
              Backup Name
            </label>
            <input
              id="velero-backup-name"
              type="text"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
              placeholder="my-backup"
            />
            {errors.name && (
              <p className="text-xs text-error mt-1">{errors.name}</p>
            )}
          </div>

          {/* Included Namespaces */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-backup-namespaces"
            >
              Include Namespaces
            </label>
            <p className="text-xs text-text-muted mb-2">
              Leave empty to back up all namespaces. Use Ctrl/Cmd+click to
              select multiple.
            </p>
            <select
              id="velero-backup-namespaces"
              multiple
              onChange={(e) =>
                updateField(
                  "includedNamespaces",
                  Array.from(e.currentTarget.selectedOptions).map(
                    (o) => o.value,
                  ),
                )
              }
              className={`${WIZARD_INPUT_CLASS} h-32`}
            >
              {namespaceList.map((ns) => (
                <option
                  key={ns}
                  value={ns}
                  // The selection of a multi-select is not a single value the
                  // JSX types accept, so each option carries its own state.
                  ref={(el) => {
                    if (el) el.selected = form.includedNamespaces.includes(ns);
                  }}
                >
                  {ns}
                </option>
              ))}
            </select>
          </div>

          {/* Storage Location */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-backup-storage-location"
            >
              Storage Location
            </label>
            <select
              id="velero-backup-storage-location"
              value={form.storageLocation}
              onChange={(e) =>
                updateField("storageLocation", e.currentTarget.value)
              }
              className={WIZARD_INPUT_CLASS}
            >
              <option value="">Default</option>
              {bsls.map((bsl) => (
                <option key={bsl.name} value={bsl.name}>
                  {bsl.name} ({bsl.provider}){bsl.default ? " - default" : ""}
                </option>
              ))}
            </select>
          </div>

          {/* TTL */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-backup-ttl"
            >
              Retention (TTL)
            </label>
            <select
              id="velero-backup-ttl"
              value={form.ttl}
              onChange={(e) => updateField("ttl", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
            >
              {TTL_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>

          {/* Snapshot Volumes */}
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="snapshotVolumes"
              checked={form.snapshotVolumes}
              onChange={(e) =>
                updateField("snapshotVolumes", e.currentTarget.checked)
              }
              className="rounded border-border-primary"
            />
            <label
              htmlFor="snapshotVolumes"
              className="text-sm font-medium text-text-primary"
            >
              Snapshot persistent volumes
            </label>
          </div>
        </div>
      )}

      {currentStep === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/backup/backups"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
