import { useSignal } from "@preact/signals";
import type { Signal } from "@preact/signals";
import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type { QuotaConfig } from "@/islands/NamespaceLimitsWizard.tsx";

interface QuotaValuesStepProps {
  quota: QuotaConfig;
  errors: Signal<Record<string, string>>;
  onUpdateQuota: <K extends keyof QuotaConfig>(
    field: K,
    value: QuotaConfig[K],
  ) => void;
}

export function QuotaValuesStep({
  quota,
  errors,
  onUpdateQuota,
}: QuotaValuesStepProps) {
  const showAdvanced = useSignal(false);

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "16px",
        maxWidth: "480px",
      }}
    >
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: "12px",
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
            CPU Hard Limit <span style={{ color: "var(--error)" }}>*</span>
          </label>
          <input
            type="text"
            value={quota.cpuHard}
            onInput={(e) =>
              onUpdateQuota(
                "cpuHard",
                (e.target as HTMLInputElement).value,
              )}
            class={WIZARD_INPUT_CLASS}
            placeholder="e.g. 8 or 8000m"
          />
          {errors.value.cpuHard && (
            <p
              style={{
                marginTop: "4px",
                fontSize: "11px",
                color: "var(--error)",
              }}
            >
              {errors.value.cpuHard}
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
            Memory Hard Limit <span style={{ color: "var(--error)" }}>*</span>
          </label>
          <input
            type="text"
            value={quota.memoryHard}
            onInput={(e) =>
              onUpdateQuota(
                "memoryHard",
                (e.target as HTMLInputElement).value,
              )}
            class={WIZARD_INPUT_CLASS}
            placeholder="e.g. 16Gi"
          />
          {errors.value.memoryHard && (
            <p
              style={{
                marginTop: "4px",
                fontSize: "11px",
                color: "var(--error)",
              }}
            >
              {errors.value.memoryHard}
            </p>
          )}
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
          Max Pods <span style={{ color: "var(--error)" }}>*</span>
        </label>
        <input
          type="number"
          min={1}
          max={1000}
          value={quota.podsHard}
          onInput={(e) =>
            onUpdateQuota(
              "podsHard",
              parseInt((e.target as HTMLInputElement).value) || 1,
            )}
          class={WIZARD_INPUT_CLASS}
        />
        {errors.value.podsHard && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errors.value.podsHard}
          </p>
        )}
      </div>

      {/* Advanced toggle */}
      <button
        type="button"
        onClick={() => {
          showAdvanced.value = !showAdvanced.value;
        }}
        style={{
          alignSelf: "flex-start",
          display: "flex",
          alignItems: "center",
          gap: "5px",
          fontSize: "12.5px",
          color: "var(--accent)",
          background: "transparent",
          border: "none",
          cursor: "pointer",
          padding: 0,
          fontWeight: 600,
        }}
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          style={{
            transform: showAdvanced.value ? "rotate(90deg)" : "rotate(0deg)",
            transition: "transform 0.15s",
          }}
        >
          <path d="M7 5l5 5-5 5" />
        </svg>
        {showAdvanced.value ? "Hide" : "Show"} advanced options
      </button>

      {showAdvanced.value && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "14px",
            borderRadius: "10px",
            border: "1px solid var(--border-subtle)",
            padding: "14px",
          }}
        >
          <p
            style={{
              margin: 0,
              fontSize: "11.5px",
              fontWeight: 600,
              color: "var(--text-muted)",
            }}
          >
            Count Limits (Optional)
          </p>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "10px",
            }}
          >
            {(
              [
                ["secretsHard", "Secrets"],
                ["configMapsHard", "ConfigMaps"],
                ["servicesHard", "Services"],
                ["pvcsHard", "PVCs"],
              ] as const
            ).map(([field, label]) => (
              <div key={field}>
                <label
                  style={{
                    display: "block",
                    fontSize: "11px",
                    color: "var(--text-muted)",
                    marginBottom: "3px",
                  }}
                >
                  {label}
                </label>
                <input
                  type="number"
                  min={0}
                  value={quota[field] ?? ""}
                  onInput={(e) => {
                    const v = parseInt(
                      (e.target as HTMLInputElement).value,
                    );
                    onUpdateQuota(field, isNaN(v) ? undefined : v);
                  }}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="No limit"
                />
              </div>
            ))}
          </div>

          <div>
            <label
              style={{
                display: "block",
                fontSize: "11.5px",
                fontWeight: 600,
                color: "var(--text-muted)",
                marginBottom: "5px",
              }}
            >
              GPU Limit
            </label>
            <input
              type="text"
              value={quota.gpuHard ?? ""}
              onInput={(e) =>
                onUpdateQuota(
                  "gpuHard",
                  (e.target as HTMLInputElement).value || undefined,
                )}
              class={WIZARD_INPUT_CLASS}
              placeholder="e.g. 1 (nvidia.com/gpu)"
            />
          </div>

          <div>
            <label
              style={{
                display: "block",
                fontSize: "11.5px",
                fontWeight: 600,
                color: "var(--text-muted)",
                marginBottom: "8px",
              }}
            >
              Alert Thresholds (%)
            </label>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1fr 1fr",
                gap: "10px",
              }}
            >
              <div>
                <label
                  style={{
                    display: "block",
                    fontSize: "11px",
                    color: "var(--text-muted)",
                    marginBottom: "3px",
                  }}
                >
                  Warning (default: 80)
                </label>
                <input
                  type="number"
                  min={1}
                  max={100}
                  value={quota.warnThreshold ?? ""}
                  onInput={(e) => {
                    const v = parseInt(
                      (e.target as HTMLInputElement).value,
                    );
                    onUpdateQuota("warnThreshold", isNaN(v) ? undefined : v);
                  }}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="80"
                />
              </div>
              <div>
                <label
                  style={{
                    display: "block",
                    fontSize: "11px",
                    color: "var(--text-muted)",
                    marginBottom: "3px",
                  }}
                >
                  Critical (default: 95)
                </label>
                <input
                  type="number"
                  min={1}
                  max={100}
                  value={quota.criticalThreshold ?? ""}
                  onInput={(e) => {
                    const v = parseInt(
                      (e.target as HTMLInputElement).value,
                    );
                    onUpdateQuota(
                      "criticalThreshold",
                      isNaN(v) ? undefined : v,
                    );
                  }}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="95"
                />
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
