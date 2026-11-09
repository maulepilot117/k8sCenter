/** @jsxImportSource react */
import { useCallback, useEffect, useRef, useState } from "react";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import type { Backup } from "@/lib/velero-types.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { WizardReviewStep } from "@/src/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/src/islands/WizardShell.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

interface RestoreFormState {
  name: string;
  namespace: string;
  backupName: string;
  includedNamespaces: string[];
  restorePVs: boolean;
}

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Source backup & options" },
  { label: "Review", sub: "Preview & apply" },
];

function generateRestoreName(backupName: string): string {
  const now = new Date();
  const ts =
    now.getFullYear().toString() +
    (now.getMonth() + 1).toString().padStart(2, "0") +
    now.getDate().toString().padStart(2, "0") +
    "-" +
    now.getHours().toString().padStart(2, "0") +
    now.getMinutes().toString().padStart(2, "0") +
    now.getSeconds().toString().padStart(2, "0");
  return `${backupName}-restore-${ts}`;
}

function initialState(preselectedBackup?: string): RestoreFormState {
  return {
    name: preselectedBackup ? generateRestoreName(preselectedBackup) : "",
    namespace: "velero",
    backupName: preselectedBackup || "",
    includedNamespaces: [],
    restorePVs: true,
  };
}

interface Props {
  onClose?: () => void;
}

export default function VeleroRestoreWizard({ onClose }: Props) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<RestoreFormState>(() => {
    const urlParams = IS_BROWSER
      ? new URLSearchParams(globalThis.location.search)
      : null;
    return initialState(urlParams?.get("backup") || undefined);
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const formRef = useLatest(form);

  const namespaces = useNamespaces();
  const namespaceList = useSignalValue(namespaces);
  const [backups, setBackups] = useState<Backup[]>([]);

  const [previewYaml, setPreviewYaml] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const previewGen = useRef(0);

  // Fetch backups
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Backup[]>("/v1/velero/backups")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          setBackups(
            resp.data.filter(
              (b) => b.phase === "Completed" || b.phase === "PartiallyFailed",
            ),
          );
        }
      })
      .catch(() => {});
  }, []);

  // Update name when backup changes
  const selectedBackup = form.backupName;
  useEffect(() => {
    if (!selectedBackup) return;
    setForm((prev) =>
      prev.name ? prev : { ...prev, name: generateRestoreName(selectedBackup) },
    );
  }, [selectedBackup]);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    setDirty(true);
    setForm((prev) => ({ ...prev, [field]: value }));
  }, []);

  const validate = useCallback((): boolean => {
    const f = formRef.current;
    const newErrors: Record<string, string> = {};
    if (!DNS_LABEL_REGEX.test(f.name)) {
      newErrors.name = "Must be a valid DNS label";
    }
    if (!f.backupName) {
      newErrors.backupName = "Backup is required";
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
        backupName: f.backupName,
        includedNamespaces:
          f.includedNamespaces.length > 0 ? f.includedNamespaces : undefined,
        restorePVs: f.restorePVs,
      };
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/velero-restore/preview",
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
      title="Create Restore"
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
          <path d="M12 14v6M15 17l-3 3-3-3" />
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
          {/* Backup Selection */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-restore-backup"
            >
              Source Backup *
            </label>
            <select
              id="velero-restore-backup"
              value={form.backupName}
              onChange={(e) => updateField("backupName", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
            >
              <option value="">Select a backup...</option>
              {backups.map((b) => (
                <option key={b.name} value={b.name}>
                  {b.name} ({b.phase})
                </option>
              ))}
            </select>
            {errors.backupName && (
              <p className="text-xs text-error mt-1">{errors.backupName}</p>
            )}
          </div>

          {/* Restore Name */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-restore-name"
            >
              Restore Name
            </label>
            <input
              id="velero-restore-name"
              type="text"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
              placeholder="my-restore"
            />
            {errors.name && (
              <p className="text-xs text-error mt-1">{errors.name}</p>
            )}
          </div>

          {/* Included Namespaces */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-restore-namespaces"
            >
              Include Namespaces (optional)
            </label>
            <p className="text-xs text-text-muted mb-2">
              Leave empty to restore all namespaces from the backup.
            </p>
            <select
              id="velero-restore-namespaces"
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

          {/* Restore PVs */}
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="restorePVs"
              checked={form.restorePVs}
              onChange={(e) =>
                updateField("restorePVs", e.currentTarget.checked)
              }
              className="rounded border-border-primary"
            />
            <label
              htmlFor="restorePVs"
              className="text-sm font-medium text-text-primary"
            >
              Restore persistent volumes
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
          detailBasePath="/backup/restores"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
