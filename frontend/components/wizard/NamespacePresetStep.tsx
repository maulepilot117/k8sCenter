/** @jsxImportSource react */
import type { Signal } from "@preact/signals-core";
import type { PresetKey } from "@/lib/namespace-presets.ts";
import { PRESETS } from "@/lib/namespace-presets.ts";
import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type { FormState } from "@/src/islands/NamespaceLimitsWizard.tsx";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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
  const errorMap = useSignalValue(errors);
  const namespaceList = useSignalValue(namespaces);
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
          htmlFor="ns-limits-namespace"
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
          id="ns-limits-namespace"
          value={form.namespace}
          onChange={(e) => onUpdateField("namespace", e.currentTarget.value)}
          className={WIZARD_INPUT_CLASS}
        >
          {namespaceList.map((ns) => (
            <option key={ns} value={ns}>
              {ns}
            </option>
          ))}
        </select>
        {errorMap.namespace && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errorMap.namespace}
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
          htmlFor="ns-limits-quota-name"
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
          id="ns-limits-quota-name"
          type="text"
          value={form.quotaName}
          onChange={(e) => onUpdateField("quotaName", e.currentTarget.value)}
          className={WIZARD_INPUT_CLASS}
          placeholder="e.g. default-quota"
        />
        {errorMap.quotaName && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errorMap.quotaName}
          </p>
        )}
      </div>

      <div>
        <label
          htmlFor="ns-limits-limitrange-name"
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
          id="ns-limits-limitrange-name"
          type="text"
          value={form.limitRangeName}
          onChange={(e) =>
            onUpdateField("limitRangeName", e.currentTarget.value)
          }
          className={WIZARD_INPUT_CLASS}
          placeholder="e.g. default-limits"
        />
        {errorMap.limitRangeName && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errorMap.limitRangeName}
          </p>
        )}
      </div>
    </div>
  );
}
