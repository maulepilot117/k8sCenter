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
import type { BackupStorageLocation } from "@/lib/velero-types.ts";

interface BackupFormState {
  name: string;
  namespace: string;
  includedNamespaces: string[];
  excludedNamespaces: string[];
  storageLocation: string;
  ttl: string;
  snapshotVolumes: boolean;
}

const STEPS = [{ title: "Configure" }, { title: "Review & Apply" }];

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
  const ts = now.getFullYear().toString() +
    (now.getMonth() + 1).toString().padStart(2, "0") +
    now.getDate().toString().padStart(2, "0") + "-" +
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

export default function VeleroBackupWizard() {
  const currentStep = useSignal(0);
  const form = useSignal<BackupFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();
  const bsls = useSignal<BackupStorageLocation[]>([]);

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  // Fetch BSLs
  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<{ backupStorageLocations: BackupStorageLocation[] }>(
      "/v1/velero/locations",
    )
      .then((resp) => {
        if (resp.data?.backupStorageLocations) {
          bsls.value = resp.data.backupStorageLocations;
          const defaultBsl = resp.data.backupStorageLocations.find((b) =>
            b.default
          );
          if (defaultBsl && !form.value.storageLocation) {
            form.value = { ...form.value, storageLocation: defaultBsl.name };
          }
        }
      })
      .catch(() => {});
  }, []);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const validate = useCallback((): boolean => {
    const newErrors: Record<string, string> = {};
    if (!DNS_LABEL_REGEX.test(form.value.name)) {
      newErrors.name = "Must be a valid DNS label (lowercase, hyphens allowed)";
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
        includedNamespaces: form.value.includedNamespaces.length > 0
          ? form.value.includedNamespaces
          : undefined,
        excludedNamespaces: form.value.excludedNamespaces.length > 0
          ? form.value.excludedNamespaces
          : undefined,
        storageLocation: form.value.storageLocation || undefined,
        ttl: form.value.ttl || undefined,
        snapshotVolumes: form.value.snapshotVolumes,
      };
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/velero-backup/preview",
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
      <h1 class="text-2xl font-bold text-text-primary mb-2">New Backup</h1>
      <p class="text-sm text-text-muted mb-6">
        Create a one-time Velero backup of cluster resources.
      </p>

      <WizardStepper steps={STEPS} currentStep={currentStep.value} />

      <div class="mt-6">
        {currentStep.value === 0 && (
          <div class="space-y-6">
            {/* Name */}
            <div>
              <label class="block text-sm font-medium text-text-primary mb-1">
                Backup Name
              </label>
              <input
                type="text"
                value={form.value.name}
                onInput={(e) =>
                  updateField("name", (e.target as HTMLInputElement).value)}
                class={WIZARD_INPUT_CLASS}
                placeholder="my-backup"
              />
              {errors.value.name && (
                <p class="text-xs text-error mt-1">{errors.value.name}</p>
              )}
            </div>

            {/* Included Namespaces */}
            <div>
              <label class="block text-sm font-medium text-text-primary mb-1">
                Include Namespaces
              </label>
              <p class="text-xs text-text-muted mb-2">
                Leave empty to back up all namespaces. Use Ctrl/Cmd+click to
                select multiple.
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

            {/* Storage Location */}
            <div>
              <label class="block text-sm font-medium text-text-primary mb-1">
                Storage Location
              </label>
              <select
                value={form.value.storageLocation}
                onChange={(e) =>
                  updateField(
                    "storageLocation",
                    (e.target as HTMLSelectElement).value,
                  )}
                class={WIZARD_INPUT_CLASS}
              >
                <option value="">Default</option>
                {bsls.value.map((bsl) => (
                  <option key={bsl.name} value={bsl.name}>
                    {bsl.name} ({bsl.provider})
                    {bsl.default ? " - default" : ""}
                  </option>
                ))}
              </select>
            </div>

            {/* TTL */}
            <div>
              <label class="block text-sm font-medium text-text-primary mb-1">
                Retention (TTL)
              </label>
              <select
                value={form.value.ttl}
                onChange={(e) =>
                  updateField("ttl", (e.target as HTMLSelectElement).value)}
                class={WIZARD_INPUT_CLASS}
              >
                {TTL_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>{opt.label}</option>
                ))}
              </select>
            </div>

            {/* Snapshot Volumes */}
            <div class="flex items-center gap-2">
              <input
                type="checkbox"
                id="snapshotVolumes"
                checked={form.value.snapshotVolumes}
                onChange={(e) =>
                  updateField(
                    "snapshotVolumes",
                    (e.target as HTMLInputElement).checked,
                  )}
                class="rounded border-border"
              />
              <label
                for="snapshotVolumes"
                class="text-sm font-medium text-text-primary"
              >
                Snapshot persistent volumes
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
            detailBasePath="/backup/backups"
          />
        )}
      </div>

      {currentStep.value === 0 && (
        <div class="mt-8 flex justify-between">
          <a href="/backup/backups">
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
