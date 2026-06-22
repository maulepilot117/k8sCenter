import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type { Signal } from "@preact/signals";
import type { FormState, PresetKey } from "@/islands/NamespaceLimitsWizard.tsx";
import { PRESETS } from "@/islands/NamespaceLimitsWizard.tsx";

interface NamespacePresetStepProps {
  form: FormState;
  errors: Signal<Record<string, string>>;
  namespaces: Signal<string[]>;
  onUpdateField: <K extends keyof FormState>(
    field: K,
    value: FormState[K],
  ) => void;
  onApplyPreset: (preset: PresetKey) => void;
}

export function NamespacePresetStep({
  form,
  errors,
  namespaces,
  onUpdateField,
  onApplyPreset,
}: NamespacePresetStepProps) {
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "20px",
        maxWidth: "480px",
      }}
    >
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
          Namespace <span style={{ color: "var(--error)" }}>*</span>
        </label>
        <select
          value={form.namespace}
          onChange={(e) =>
            onUpdateField(
              "namespace",
              (e.target as HTMLSelectElement).value,
            )}
          class={WIZARD_INPUT_CLASS}
        >
          {namespaces.value.map((ns) => (
            <option key={ns} value={ns}>{ns}</option>
          ))}
        </select>
        {errors.value.namespace && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errors.value.namespace}
          </p>
        )}
      </div>

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
          Preset
        </label>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "8px",
          }}
        >
          {(Object.keys(PRESETS) as PresetKey[]).map((key) => {
            const p = PRESETS[key];
            const isSelected = form.preset === key;
            return (
              <button
                key={key}
                type="button"
                onClick={() => onApplyPreset(key)}
                style={{
                  borderRadius: "10px",
                  border: `1.5px solid ${
                    isSelected ? "var(--accent)" : "var(--border-subtle)"
                  }`,
                  padding: "10px 12px",
                  textAlign: "left",
                  cursor: "pointer",
                  background: isSelected
                    ? "var(--accent-dim)"
                    : "var(--bg-elevated)",
                  transition: "background 0.12s, border-color 0.12s",
                }}
              >
                <div
                  style={{
                    fontSize: "13px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                  }}
                >
                  {p.label}
                </div>
                <div
                  style={{
                    fontSize: "11px",
                    color: "var(--text-muted)",
                    marginTop: "2px",
                  }}
                >
                  {p.description}
                </div>
              </button>
            );
          })}
        </div>
      </div>

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
          ResourceQuota Name <span style={{ color: "var(--error)" }}>*</span>
        </label>
        <input
          type="text"
          value={form.quotaName}
          onInput={(e) =>
            onUpdateField(
              "quotaName",
              (e.target as HTMLInputElement).value,
            )}
          class={WIZARD_INPUT_CLASS}
          placeholder="e.g. default-quota"
        />
        {errors.value.quotaName && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errors.value.quotaName}
          </p>
        )}
      </div>

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
          LimitRange Name <span style={{ color: "var(--error)" }}>*</span>
        </label>
        <input
          type="text"
          value={form.limitRangeName}
          onInput={(e) =>
            onUpdateField(
              "limitRangeName",
              (e.target as HTMLInputElement).value,
            )}
          class={WIZARD_INPUT_CLASS}
          placeholder="e.g. default-limits"
        />
        {errors.value.limitRangeName && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errors.value.limitRangeName}
          </p>
        )}
      </div>
    </div>
  );
}
