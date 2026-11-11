import { useState } from "react";
import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type { QuotaConfig } from "@/src/islands/NamespaceLimitsWizard.tsx";

interface QuotaValuesStepProps {
  quota: QuotaConfig;
  errors: Record<string, string>;
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
  const [showAdvanced, setShowAdvanced] = useState(false);

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
            onChange={(e) => onUpdateQuota("cpuHard", e.currentTarget.value)}
            className={WIZARD_INPUT_CLASS}
            placeholder="e.g. 8 or 8000m"
          />
          {errors.cpuHard && (
            <p
              style={{
                marginTop: "4px",
                fontSize: "11px",
                color: "var(--error)",
              }}
            >
              {errors.cpuHard}
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
            onChange={(e) => onUpdateQuota("memoryHard", e.currentTarget.value)}
            className={WIZARD_INPUT_CLASS}
            placeholder="e.g. 16Gi"
          />
          {errors.memoryHard && (
            <p
              style={{
                marginTop: "4px",
                fontSize: "11px",
                color: "var(--error)",
              }}
            >
              {errors.memoryHard}
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
          onChange={(e) =>
            onUpdateQuota("podsHard", parseInt(e.currentTarget.value, 10) || 1)
          }
          className={WIZARD_INPUT_CLASS}
        />
        {errors.podsHard && (
          <p
            style={{
              marginTop: "4px",
              fontSize: "11px",
              color: "var(--error)",
            }}
          >
            {errors.podsHard}
          </p>
        )}
      </div>

      {/* Advanced toggle */}
      <button
        type="button"
        onClick={() => {
          setShowAdvanced((prev) => !prev);
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
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{
            transform: showAdvanced ? "rotate(90deg)" : "rotate(0deg)",
            transition: "transform 0.15s",
          }}
        >
          <path d="M7 5l5 5-5 5" />
        </svg>
        {showAdvanced ? "Hide" : "Show"} advanced options
      </button>

      {showAdvanced && (
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
                  onChange={(e) => {
                    const v = parseInt(e.currentTarget.value, 10);
                    onUpdateQuota(field, Number.isNaN(v) ? undefined : v);
                  }}
                  className={WIZARD_INPUT_CLASS}
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
              onChange={(e) =>
                onUpdateQuota("gpuHard", e.currentTarget.value || undefined)
              }
              className={WIZARD_INPUT_CLASS}
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
                  onChange={(e) => {
                    const v = parseInt(e.currentTarget.value, 10);
                    onUpdateQuota(
                      "warnThreshold",
                      Number.isNaN(v) ? undefined : v,
                    );
                  }}
                  className={WIZARD_INPUT_CLASS}
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
                  onChange={(e) => {
                    const v = parseInt(e.currentTarget.value, 10);
                    onUpdateQuota(
                      "criticalThreshold",
                      Number.isNaN(v) ? undefined : v,
                    );
                  }}
                  className={WIZARD_INPUT_CLASS}
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
