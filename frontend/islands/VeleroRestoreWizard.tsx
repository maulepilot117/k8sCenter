import { useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardStepper } from "@/components/wizard/WizardStepper.tsx";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import { Button } from "@/components/ui/Button.tsx";
import type { Backup } from "@/lib/velero-types.ts";

interface RestoreFormState {
  name: string;
  namespace: string;
  backupName: string;
  includedNamespaces: string[];
  restorePVs: boolean;
}

const STEPS = [{ title: "Configure" }, { title: "Review & Apply" }];

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

export default function VeleroRestoreWizard() {
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

  if (!IS_BROWSER) return null;

  return (
    <div class="max-w-3xl mx-auto p-6">
      <h1 class="text-2xl font-bold text-text-primary mb-2">New Restore</h1>
      <p class="text-sm text-text-muted mb-6">
        Restore cluster resources from a Velero backup.
      </p>

      <WizardStepper steps={STEPS} currentStep={currentStep.value} />

      <div class="mt-6">
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
      </div>

      {currentStep.value === 0 && (
        <div class="mt-8 flex justify-between">
          <a href="/backup/restores">
            <Button type="button" variant="ghost">Cancel</Button>
          </a>
          <Button type="button" variant="primary" onClick={goNext}>
            Preview YAML
          </Button>
        </div>
      )}

      {currentStep.value === 1 && !previewLoading.value &&
        previewError.value === null && (
        <div class="mt-4 flex justify-start">
          <Button type="button" variant="ghost" onClick={goBack}>
            Back
          </Button>
        </div>
      )}
    </div>
  );
}
