import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import type { Backup } from "@/lib/velero-types.ts";

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
  const ts = now.getFullYear().toString() +
    (now.getMonth() + 1).toString().padStart(2, "0") +
    now.getDate().toString().padStart(2, "0") + "-" +
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
  const urlParams = IS_BROWSER
    ? new URLSearchParams(globalThis.location.search)
    : null;
  const preselectedBackup = urlParams?.get("backup") || undefined;

  const currentStep = useSignal(0);
  const form = useSignal<RestoreFormState>(initialState(preselectedBackup));
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  const backups = useSignal<Backup[]>([]);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  // Fetch backups
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<Backup[]>("/v1/velero/backups")
      .then((resp) => {
        if (Array.isArray(resp.data)) {
          backups.value = resp.data.filter((b) =>
            b.phase === "Completed" || b.phase === "PartiallyFailed"
          );
        }
      })
      .catch(() => {});
  }, []);

  // Update name when backup changes
  useEffect(() => {
    if (form.value.backupName && !form.value.name) {
      form.value = {
        ...form.value,
        name: generateRestoreName(form.value.backupName),
      };
    }
  }, [form.value.backupName]);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const validate = useCallback((): boolean => {
    const newErrors: Record<string, string> = {};
    if (!DNS_LABEL_REGEX.test(form.value.name)) {
      newErrors.name = "Must be a valid DNS label";
    }
    if (!form.value.backupName) {
      newErrors.backupName = "Backup is required";
    }
    errors.value = newErrors;
    return Object.keys(newErrors).length === 0;
  }, []);

  const fetchPreview = useCallback(async () => {
    previewLoading.value = true;
    previewError.value = null;
    try {
      const body = {
        name: form.value.name,
        namespace: form.value.namespace,
        backupName: form.value.backupName,
        includedNamespaces: form.value.includedNamespaces.length > 0
          ? form.value.includedNamespaces
          : undefined,
        restorePVs: form.value.restorePVs,
      };
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/velero-restore/preview",
        body,
      );
      previewYaml.value = resp.data.yaml;
    } catch (e: unknown) {
      previewError.value = e instanceof Error ? e.message : "Preview failed";
    }
    previewLoading.value = false;
  }, []);

  const goNext = useCallback(async () => {
    if (currentStep.value === 0) {
      if (!validate()) return;
      await fetchPreview();
    }
    currentStep.value++;
  }, []);

  const goBack = useCallback(() => {
    currentStep.value--;
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
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M12 2a8 8 0 0 1 8 8v1a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V10a8 8 0 0 1 8-8z" />
          <path d="M12 14v6M15 17l-3 3-3-3" />
        </svg>
      }
      subtitle={`Step ${currentStep.value + 1} of 2`}
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i < currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={goBack}
      onNext={goNext}
      nextLabel={currentStep.value === 0 ? "Preview YAML" : "Apply"}
      yaml={previewYaml.value || undefined}
    >
      {currentStep.value === 0 && (
        <div class="space-y-6">
          {/* Backup Selection */}
          <div>
            <label class="block text-sm font-medium text-text-primary mb-1">
              Source Backup *
            </label>
            <select
              value={form.value.backupName}
              onChange={(e) =>
                updateField(
                  "backupName",
                  (e.target as HTMLSelectElement).value,
                )}
              class={WIZARD_INPUT_CLASS}
            >
              <option value="">Select a backup...</option>
              {backups.value.map((b) => (
                <option key={b.name} value={b.name}>
                  {b.name} ({b.phase})
                </option>
              ))}
            </select>
            {errors.value.backupName && (
              <p class="text-xs text-error mt-1">{errors.value.backupName}</p>
            )}
          </div>

          {/* Restore Name */}
          <div>
            <label class="block text-sm font-medium text-text-primary mb-1">
              Restore Name
            </label>
            <input
              type="text"
              value={form.value.name}
              onInput={(e) =>
                updateField("name", (e.target as HTMLInputElement).value)}
              class={WIZARD_INPUT_CLASS}
              placeholder="my-restore"
            />
            {errors.value.name && (
              <p class="text-xs text-error mt-1">{errors.value.name}</p>
            )}
          </div>

          {/* Included Namespaces */}
          <div>
            <label class="block text-sm font-medium text-text-primary mb-1">
              Include Namespaces (optional)
            </label>
            <p class="text-xs text-text-muted mb-2">
              Leave empty to restore all namespaces from the backup.
            </p>
            <select
              multiple
              onChange={(e) => {
                const select = e.target as HTMLSelectElement;
                const selected = Array.from(select.selectedOptions).map((o) =>
                  o.value
                );
                updateField("includedNamespaces", selected);
              }}
              class={`${WIZARD_INPUT_CLASS} h-32`}
            >
              {namespaces.value.map((ns) => (
                <option
                  key={ns}
                  value={ns}
                  selected={form.value.includedNamespaces.includes(ns)}
                >
                  {ns}
                </option>
              ))}
            </select>
          </div>

          {/* Restore PVs */}
          <div class="flex items-center gap-2">
            <input
              type="checkbox"
              id="restorePVs"
              checked={form.value.restorePVs}
              onChange={(e) =>
                updateField(
                  "restorePVs",
                  (e.target as HTMLInputElement).checked,
                )}
              class="rounded border-border"
            />
            <label
              for="restorePVs"
              class="text-sm font-medium text-text-primary"
            >
              Restore persistent volumes
            </label>
          </div>
        </div>
      )}

      {currentStep.value === 1 && (
        <WizardReviewStep
          yaml={previewYaml.value}
          onYamlChange={(v) => (previewYaml.value = v)}
          loading={previewLoading.value}
          error={previewError.value}
          detailBasePath="/backup/restores"
        />
      )}
    </WizardShell>
  );
}
