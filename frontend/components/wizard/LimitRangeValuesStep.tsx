import { useSignal } from "@preact/signals";
import type { Signal } from "@preact/signals";
import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";
import type {
  LimitConfig,
  ResourcePair,
} from "@/islands/NamespaceLimitsWizard.tsx";

interface LimitRangeValuesStepProps {
  limits: LimitConfig;
  errors: Signal<Record<string, string>>;
  onUpdateLimits: <K extends keyof LimitConfig>(
    field: K,
    value: LimitConfig[K],
  ) => void;
  onUpdateResourcePair: (
    configKey: keyof LimitConfig,
    resourceKey: "cpu" | "memory",
    value: string,
  ) => void;
}

export function LimitRangeValuesStep({
  limits,
  errors,
  onUpdateLimits,
  onUpdateResourcePair,
}: LimitRangeValuesStepProps) {
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
      <h3
        style={{
          margin: 0,
          fontSize: "13.5px",
          fontWeight: 600,
          color: "var(--text-primary)",
        }}
      >
        Container Limits
      </h3>

      <div
        style={{
          borderRadius: "10px",
          border: "1px solid var(--border-subtle)",
          padding: "14px",
          display: "flex",
          flexDirection: "column",
          gap: "14px",
        }}
      >
        {(
          [
            [
              "containerDefault",
              "Default Limits",
              "Applied to containers without explicit limits",
              true,
            ],
            ["containerDefaultRequest", "Default Requests", null, false],
            ["containerMax", "Maximum Limits", null, false],
            ["containerMin", "Minimum Limits", null, false],
          ] as const
        ).map(([key, title, hint, required]) => (
          <div key={key}>
            <label
              style={{
                display: "block",
                fontSize: "12.5px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                marginBottom: hint ? "2px" : "6px",
              }}
            >
              {title}
              {required && <span style={{ color: "var(--error)" }}>*</span>}
            </label>
            {hint && (
              <p
                style={{
                  margin: "0 0 6px",
                  fontSize: "11px",
                  color: "var(--text-muted)",
                }}
              >
                {hint}
              </p>
            )}
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
                  CPU
                </label>
                <input
                  type="text"
                  value={(limits[key] as ResourcePair | undefined)?.cpu ?? ""}
                  onInput={(e) =>
                    onUpdateResourcePair(
                      key,
                      "cpu",
                      (e.target as HTMLInputElement).value,
                    )}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="e.g. 250m"
                />
                {key === "containerDefault" &&
                  errors.value.containerDefaultCpu && (
                  <p
                    style={{
                      marginTop: "3px",
                      fontSize: "10px",
                      color: "var(--error)",
                    }}
                  >
                    {errors.value.containerDefaultCpu}
                  </p>
                )}
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
                  Memory
                </label>
                <input
                  type="text"
                  value={(limits[key] as ResourcePair | undefined)?.memory ??
                    ""}
                  onInput={(e) =>
                    onUpdateResourcePair(
                      key,
                      "memory",
                      (e.target as HTMLInputElement).value,
                    )}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="e.g. 256Mi"
                />
                {key === "containerDefault" &&
                  errors.value.containerDefaultMemory && (
                  <p
                    style={{
                      marginTop: "3px",
                      fontSize: "10px",
                      color: "var(--error)",
                    }}
                  >
                    {errors.value.containerDefaultMemory}
                  </p>
                )}
              </div>
            </div>
          </div>
        ))}
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
          <div>
            <p
              style={{
                margin: "0 0 8px",
                fontSize: "11.5px",
                fontWeight: 600,
                color: "var(--text-muted)",
              }}
            >
              Pod Limits (Optional)
            </p>
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
                  Max CPU per Pod
                </label>
                <input
                  type="text"
                  value={limits.podMax?.cpu ?? ""}
                  onInput={(e) => {
                    const cpu = (e.target as HTMLInputElement).value;
                    const current = limits.podMax ?? { cpu: "", memory: "" };
                    onUpdateLimits(
                      "podMax",
                      cpu || current.memory ? { ...current, cpu } : undefined,
                    );
                  }}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="No limit"
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
                  Max Memory per Pod
                </label>
                <input
                  type="text"
                  value={limits.podMax?.memory ?? ""}
                  onInput={(e) => {
                    const memory = (e.target as HTMLInputElement).value;
                    const current = limits.podMax ?? { cpu: "", memory: "" };
                    onUpdateLimits(
                      "podMax",
                      memory || current.cpu
                        ? { ...current, memory }
                        : undefined,
                    );
                  }}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="No limit"
                />
              </div>
            </div>
          </div>

          <div>
            <p
              style={{
                margin: "0 0 8px",
                fontSize: "11.5px",
                fontWeight: 600,
                color: "var(--text-muted)",
              }}
            >
              PVC Storage Limits (Optional)
            </p>
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
                  Min Storage
                </label>
                <input
                  type="text"
                  value={limits.pvcMinStorage ?? ""}
                  onInput={(e) =>
                    onUpdateLimits(
                      "pvcMinStorage",
                      (e.target as HTMLInputElement).value || undefined,
                    )}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="e.g. 1Gi"
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
                  Max Storage
                </label>
                <input
                  type="text"
                  value={limits.pvcMaxStorage ?? ""}
                  onInput={(e) =>
                    onUpdateLimits(
                      "pvcMaxStorage",
                      (e.target as HTMLInputElement).value || undefined,
                    )}
                  class={WIZARD_INPUT_CLASS}
                  placeholder="e.g. 100Gi"
                />
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
