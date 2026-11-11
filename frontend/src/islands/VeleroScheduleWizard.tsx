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

interface ScheduleFormState {
  name: string;
  namespace: string;
  schedule: string;
  includedNamespaces: string[];
  excludedNamespaces: string[];
  storageLocation: string;
  ttl: string;
  snapshotVolumes: boolean;
  paused: boolean;
}

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Name, schedule & storage" },
  { label: "Review", sub: "Preview & apply" },
];

const CRON_PRESETS = [
  { value: "0 * * * *", label: "Hourly (at minute 0)" },
  { value: "0 0 * * *", label: "Daily (at midnight UTC)" },
  { value: "0 0 * * 0", label: "Weekly (Sunday at midnight)" },
  { value: "0 0 1 * *", label: "Monthly (1st at midnight)" },
];

const TTL_OPTIONS = [
  { value: "", label: "Default" },
  { value: "24h", label: "1 day" },
  { value: "168h", label: "7 days" },
  { value: "720h", label: "30 days" },
  { value: "2160h", label: "90 days" },
  { value: "8760h", label: "1 year" },
];

function initialState(): ScheduleFormState {
  return {
    name: "",
    namespace: "velero",
    schedule: "0 0 * * *", // Daily at midnight
    includedNamespaces: [],
    excludedNamespaces: [],
    storageLocation: "",
    ttl: "720h", // 30 days default
    snapshotVolumes: true,
    paused: false,
  };
}

interface Props {
  onClose?: () => void;
}

export default function VeleroScheduleWizard({ onClose }: Props) {
  const close = onClose ?? (() => globalThis.history.back());
  const [currentStep, setCurrentStep] = useState(0);
  const [form, setForm] = useState<ScheduleFormState>(initialState);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const formRef = useLatest(form);

  const [namespaceList] = useNamespaces();
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
    const f = formRef.current;
    const newErrors: Record<string, string> = {};
    if (!DNS_LABEL_REGEX.test(f.name)) {
      newErrors.name =
        "Must be a valid DNS label (lowercase, hyphens allowed, 1-63 chars)";
    }
    if (!f.schedule) {
      newErrors.schedule = "Cron schedule is required";
    } else {
      // Basic cron validation (5 fields)
      const parts = f.schedule.trim().split(/\s+/);
      if (parts.length !== 5) {
        newErrors.schedule = "Must be a valid cron expression (5 fields)";
      }
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
        schedule: f.schedule,
        includedNamespaces:
          f.includedNamespaces.length > 0 ? f.includedNamespaces : undefined,
        excludedNamespaces:
          f.excludedNamespaces.length > 0 ? f.excludedNamespaces : undefined,
        storageLocation: f.storageLocation || undefined,
        ttl: f.ttl || undefined,
        snapshotVolumes: f.snapshotVolumes,
        paused: f.paused || undefined,
      };
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/velero-schedule/preview",
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
      title="Create Schedule"
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
          <rect x="3" y="4" width="18" height="18" rx="2" ry="2" />
          <line x1="16" y1="2" x2="16" y2="6" />
          <line x1="8" y1="2" x2="8" y2="6" />
          <line x1="3" y1="10" x2="21" y2="10" />
          <circle cx="12" cy="16" r="3" />
          <path d="M12 14v2l1 1" />
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
              htmlFor="velero-schedule-name"
            >
              Schedule Name *
            </label>
            <input
              id="velero-schedule-name"
              type="text"
              value={form.name}
              onChange={(e) => updateField("name", e.currentTarget.value)}
              className={WIZARD_INPUT_CLASS}
              placeholder="daily-backup"
            />
            {errors.name && (
              <p className="text-xs text-error mt-1">{errors.name}</p>
            )}
          </div>

          {/* Cron Schedule */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-schedule-cron"
            >
              Schedule (Cron) *
            </label>
            <div className="flex gap-2 mb-2">
              {CRON_PRESETS.map((preset) => (
                <button
                  key={preset.value}
                  type="button"
                  onClick={() => updateField("schedule", preset.value)}
                  className={`px-2 py-1 text-xs rounded border ${
                    form.schedule === preset.value
                      ? "border-accent bg-accent/10 text-accent"
                      : "border-border-primary text-text-muted hover:border-text-muted"
                  }`}
                >
                  {preset.label}
                </button>
              ))}
            </div>
            <input
              id="velero-schedule-cron"
              type="text"
              value={form.schedule}
              onChange={(e) => updateField("schedule", e.currentTarget.value)}
              className={`${WIZARD_INPUT_CLASS} font-mono`}
              placeholder="0 0 * * *"
            />
            {errors.schedule && (
              <p className="text-xs text-error mt-1">{errors.schedule}</p>
            )}
            <p className="text-xs text-text-muted mt-1">
              Format: minute hour day-of-month month day-of-week
            </p>
          </div>

          {/* Included Namespaces */}
          <div>
            <label
              className="block text-sm font-medium text-text-primary mb-1"
              htmlFor="velero-schedule-namespaces"
            >
              Include Namespaces
            </label>
            <p className="text-xs text-text-muted mb-2">
              Leave empty to back up all namespaces.
            </p>
            <select
              id="velero-schedule-namespaces"
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
              htmlFor="velero-schedule-storage-location"
            >
              Storage Location
            </label>
            <select
              id="velero-schedule-storage-location"
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
              htmlFor="velero-schedule-ttl"
            >
              Retention (TTL)
            </label>
            <select
              id="velero-schedule-ttl"
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

          {/* Checkboxes */}
          <div className="space-y-2">
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
            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                id="paused"
                checked={form.paused}
                onChange={(e) => updateField("paused", e.currentTarget.checked)}
                className="rounded border-border-primary"
              />
              <label
                htmlFor="paused"
                className="text-sm font-medium text-text-primary"
              >
                Create paused (won't run until unpaused)
              </label>
            </div>
          </div>
        </div>
      )}

      {currentStep === 1 && (
        <WizardReviewStep
          yaml={previewYaml}
          onYamlChange={setPreviewYaml}
          loading={previewLoading}
          error={previewError}
          detailBasePath="/backup/schedules"
          onDirtyChange={setDirty}
        />
      )}
    </WizardShell>
  );
}
