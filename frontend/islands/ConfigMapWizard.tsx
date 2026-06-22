import { useSignal } from "@preact/signals";
import { useCallback } from "preact/hooks";
import { apiPost } from "@/lib/api.ts";
import { initialNamespace } from "@/lib/namespace.ts";
import { DNS_LABEL_REGEX, WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import { useNamespaces } from "@/lib/hooks/use-namespaces.ts";
import { useDirtyGuard } from "@/lib/hooks/use-dirty-guard.ts";
import { WizardReviewStep } from "@/components/wizard/WizardReviewStep.tsx";
import WizardShell, { type WizardStep } from "@/islands/WizardShell.tsx";

import type { KeyValueEntry } from "@/components/ui/KeyValueListEditor.tsx";

interface ConfigMapFormState {
  name: string;
  namespace: string;
  entries: KeyValueEntry[];
}

const STEPS: WizardStep[] = [
  { label: "Configure", sub: "Name & data entries" },
  { label: "Review", sub: "Preview & apply" },
];

function initialState(): ConfigMapFormState {
  const ns = initialNamespace();
  return {
    name: "",
    namespace: ns,
    entries: [{ key: "", value: "" }],
  };
}

function buildManifest(f: ConfigMapFormState): string {
  const validEntries = f.entries.filter((e) => e.key.trim());
  const dataLines = validEntries.length > 0
    ? validEntries.map((e) => `  ${e.key}: "${e.value}"`).join("\n")
    : "  # no data yet";
  return `apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ${
    f.name || "<name>"
  }\n  namespace: ${f.namespace}\ndata:\n${dataLines}`;
}

export default function ConfigMapWizard(
  { onClose }: { onClose?: () => void },
) {
  const close = onClose ?? (() => globalThis.history.back());
  const currentStep = useSignal(0);
  const form = useSignal<ConfigMapFormState>(initialState());
  const errors = useSignal<Record<string, string>>({});
  const dirty = useSignal(false);

  const namespaces = useNamespaces();

  const previewYaml = useSignal("");
  const previewLoading = useSignal(false);
  const previewError = useSignal<string | null>(null);

  useDirtyGuard(dirty);

  const updateField = useCallback((field: string, value: unknown) => {
    dirty.value = true;
    form.value = { ...form.value, [field]: value };
  }, []);

  const updateEntry = useCallback(
    (index: number, field: "key" | "value", val: string) => {
      dirty.value = true;
      const entries = [...form.value.entries];
      entries[index] = { ...entries[index], [field]: val };
      form.value = { ...form.value, entries };
    },
    [],
  );

  const addEntry = useCallback(() => {
    dirty.value = true;
    form.value = {
      ...form.value,
      entries: [...form.value.entries, { key: "", value: "" }],
    };
  }, []);

  const removeEntry = useCallback((index: number) => {
    dirty.value = true;
    const entries = form.value.entries.filter((_, i) => i !== index);
    form.value = {
      ...form.value,
      entries: entries.length > 0 ? entries : [{ key: "", value: "" }],
    };
  }, []);

  const validateStep = (): boolean => {
    const f = form.value;
    const errs: Record<string, string> = {};

    if (!f.name || !DNS_LABEL_REGEX.test(f.name)) {
      errs.name =
        "Must be lowercase alphanumeric with hyphens, 1-63 characters";
    }
    if (!f.namespace) errs.namespace = "Required";

    const KEY_REGEX = /^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,251}[a-zA-Z0-9])?$/;
    const seenKeys = new Set<string>();
    let totalSize = 0;

    for (let i = 0; i < f.entries.length; i++) {
      const entry = f.entries[i];
      if (!entry.key && !entry.value) continue; // skip empty rows
      if (!entry.key) {
        errs[`entry_${i}_key`] = "Key is required";
      } else if (!KEY_REGEX.test(entry.key)) {
        errs[`entry_${i}_key`] =
          "Must be alphanumeric with hyphens, underscores, or dots";
      } else if (seenKeys.has(entry.key)) {
        errs[`entry_${i}_key`] = "Duplicate key";
      } else {
        seenKeys.add(entry.key);
      }
      totalSize += entry.key.length + entry.value.length;
    }

    if (totalSize > 1024 * 1024) {
      errs.data = "Total data size must be less than 1MB";
    }

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
    const data: Record<string, string> = {};
    for (const entry of f.entries) {
      if (entry.key) {
        data[entry.key] = entry.value;
      }
    }

    const payload = {
      name: f.name,
      namespace: f.namespace,
      data,
    };

    try {
      const resp = await apiPost<{ yaml: string }>(
        "/v1/wizards/configmap/preview",
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

  const f = form.value;

  return (
    <WizardShell
      title="Create ConfigMap"
      subtitle={`Step ${currentStep.value + 1} of 2 · namespace ${f.namespace}`}
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
          <rect x="3" y="3" width="14" height="14" rx="2" />
          <path d="M7 7h6M7 10h6M7 13h4" />
        </svg>
      }
      steps={STEPS}
      current={currentStep.value}
      onStep={(i) => {
        if (i < currentStep.value) currentStep.value = i;
      }}
      onCancel={close}
      onBack={goBack}
      onNext={currentStep.value === 0 ? goNext : close}
      nextLabel={currentStep.value === 0 ? "Preview YAML" : "Close"}
      yaml={currentStep.value === 0 ? buildManifest(f) : undefined}
    >
      {currentStep.value === 0 && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "18px",
            maxWidth: "480px",
          }}
        >
          {/* Name */}
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              Name <span style={{ color: "var(--danger)" }}>*</span>
            </label>
            <input
              type="text"
              value={f.name}
              onInput={(e) =>
                updateField("name", (e.target as HTMLInputElement).value)}
              class={WIZARD_INPUT_CLASS}
              placeholder="e.g. my-config"
            />
            {errors.value.name && (
              <p
                style={{
                  marginTop: "4px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.name}
              </p>
            )}
          </div>

          {/* Namespace */}
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "5px",
              }}
            >
              Namespace <span style={{ color: "var(--danger)" }}>*</span>
            </label>
            <select
              value={f.namespace}
              onChange={(e) =>
                updateField(
                  "namespace",
                  (e.target as HTMLSelectElement).value,
                )}
              class={WIZARD_INPUT_CLASS}
            >
              {namespaces.value.map((ns) => (
                <option key={ns} value={ns}>{ns}</option>
              ))}
            </select>
          </div>

          {/* Data entries */}
          <div>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: "8px",
              }}
            >
              Data
            </label>
            <div
              style={{ display: "flex", flexDirection: "column", gap: "8px" }}
            >
              {f.entries.map((entry, i) => (
                <div
                  key={i}
                  style={{
                    display: "flex",
                    gap: "8px",
                    alignItems: "flex-start",
                  }}
                >
                  <div style={{ flex: 1 }}>
                    <input
                      type="text"
                      value={entry.key}
                      onInput={(e) =>
                        updateEntry(
                          i,
                          "key",
                          (e.target as HTMLInputElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="Key"
                    />
                    {errors.value[`entry_${i}_key`] && (
                      <p
                        style={{
                          marginTop: "3px",
                          fontSize: "10px",
                          color: "var(--danger)",
                        }}
                      >
                        {errors.value[`entry_${i}_key`]}
                      </p>
                    )}
                  </div>
                  <div style={{ flex: 1 }}>
                    <textarea
                      value={entry.value}
                      onInput={(e) =>
                        updateEntry(
                          i,
                          "value",
                          (e.target as HTMLTextAreaElement).value,
                        )}
                      class={WIZARD_INPUT_CLASS}
                      placeholder="Value"
                      rows={1}
                      style={{ minHeight: "36px", resize: "vertical" }}
                    />
                  </div>
                  <button
                    type="button"
                    onClick={() => removeEntry(i)}
                    style={{
                      marginTop: "2px",
                      padding: "6px",
                      borderRadius: "6px",
                      border: "none",
                      cursor: "pointer",
                      background: "transparent",
                      color: "var(--text-muted)",
                      flexShrink: 0,
                    }}
                    title="Remove entry"
                  >
                    <svg
                      width="14"
                      height="14"
                      fill="none"
                      viewBox="0 0 20 20"
                      stroke="currentColor"
                      stroke-width="1.8"
                      stroke-linecap="round"
                    >
                      <path d="M5 5l10 10M15 5L5 15" />
                    </svg>
                  </button>
                </div>
              ))}
            </div>
            {errors.value.data && (
              <p
                style={{
                  marginTop: "6px",
                  fontSize: "11px",
                  color: "var(--danger)",
                }}
              >
                {errors.value.data}
              </p>
            )}
            <button
              type="button"
              onClick={addEntry}
              style={{
                marginTop: "10px",
                fontSize: "12.5px",
                color: "var(--accent)",
                background: "transparent",
                border: "none",
                cursor: "pointer",
                padding: 0,
                fontWeight: 600,
              }}
            >
              + Add entry
            </button>
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
          detailBasePath="/config/configmaps"
        />
      )}
    </WizardShell>
  );
}
