import { Input } from "@/components/ui/Input.tsx";
import { Select } from "@/components/ui/Select.tsx";
import { MAX_PROBE_PATH_LENGTH } from "@/lib/wizard-constants.ts";
import type { ProbeState } from "@/lib/wizard-types.ts";

interface StrategyState {
  type: string;
  maxSurge: string;
  maxUnavailable: string;
}

interface DeploymentResourcesProps {
  cpuRequest: string;
  memoryRequest: string;
  cpuLimit: string;
  memoryLimit: string;
  livenessProbe: ProbeState | null;
  readinessProbe: ProbeState | null;
  strategy: StrategyState;
  errors: Record<string, string>;
  onChange: (field: string, value: unknown) => void;
}

const PROBE_TYPE_OPTIONS = [
  { value: "http", label: "HTTP GET" },
  { value: "tcp", label: "TCP Socket" },
];

const STRATEGY_OPTIONS = [
  { value: "RollingUpdate", label: "Rolling Update" },
  { value: "Recreate", label: "Recreate" },
];

function ProbeSection({
  label,
  probe,
  onToggle,
  onUpdate,
}: {
  label: string;
  probe: ProbeState | null;
  onToggle: () => void;
  onUpdate: (field: keyof ProbeState, value: string | number) => void;
}) {
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={probe !== null}
          onChange={onToggle}
          className="rounded border-border-primary"
        />
        <span className="text-sm font-medium text-text-secondary">{label}</span>
      </div>
      {probe && (
        <div className="ml-6 space-y-3 border-l-2 border-border-primary pl-4">
          <Select
            label="Type"
            value={probe.type}
            onChange={(e) => onUpdate("type", e.currentTarget.value)}
            options={PROBE_TYPE_OPTIONS}
          />
          {probe.type === "http" && (
            <Input
              label="Path"
              value={probe.path}
              onChange={(e) => onUpdate("path", e.currentTarget.value)}
              placeholder="/healthz"
              maxLength={MAX_PROBE_PATH_LENGTH}
            />
          )}
          <Input
            label="Port"
            type="number"
            value={probe.port ? String(probe.port) : ""}
            onChange={(e) =>
              onUpdate("port", parseInt(e.currentTarget.value, 10) || 0)
            }
            placeholder="8080"
            min={1}
            max={65535}
          />
          <div className="flex gap-4">
            <Input
              label="Initial Delay (s)"
              type="number"
              value={
                probe.initialDelaySeconds
                  ? String(probe.initialDelaySeconds)
                  : ""
              }
              onChange={(e) =>
                onUpdate(
                  "initialDelaySeconds",
                  parseInt(e.currentTarget.value, 10) || 0,
                )
              }
              placeholder="0"
              min={0}
            />
            <Input
              label="Period (s)"
              type="number"
              value={probe.periodSeconds ? String(probe.periodSeconds) : ""}
              onChange={(e) =>
                onUpdate(
                  "periodSeconds",
                  parseInt(e.currentTarget.value, 10) || 0,
                )
              }
              placeholder="10"
              min={1}
            />
          </div>
        </div>
      )}
    </div>
  );
}

export function DeploymentResourcesStep({
  cpuRequest,
  memoryRequest,
  cpuLimit,
  memoryLimit,
  livenessProbe,
  readinessProbe,
  strategy,
  errors,
  onChange,
}: DeploymentResourcesProps) {
  const defaultProbe: ProbeState = {
    type: "http",
    path: "/",
    port: 8080,
    initialDelaySeconds: 0,
    periodSeconds: 10,
  };

  return (
    <div className="space-y-8 max-w-lg">
      {/* Resource Requests & Limits */}
      <div className="space-y-3">
        <h3 className="text-sm font-medium text-text-secondary">
          Resource Requests & Limits
        </h3>
        <p className="text-xs text-text-muted">
          Optional. Set CPU and memory requests/limits for the container.
        </p>
        <div className="grid grid-cols-2 gap-4">
          <Input
            label="CPU Request"
            value={cpuRequest}
            onChange={(e) => onChange("cpuRequest", e.currentTarget.value)}
            placeholder="100m"
            error={errors["resources.requestCpu"]}
          />
          <Input
            label="CPU Limit"
            value={cpuLimit}
            onChange={(e) => onChange("cpuLimit", e.currentTarget.value)}
            placeholder="500m"
            error={errors["resources.limitCpu"]}
          />
          <Input
            label="Memory Request"
            value={memoryRequest}
            onChange={(e) => onChange("memoryRequest", e.currentTarget.value)}
            placeholder="128Mi"
            error={errors["resources.requestMemory"]}
          />
          <Input
            label="Memory Limit"
            value={memoryLimit}
            onChange={(e) => onChange("memoryLimit", e.currentTarget.value)}
            placeholder="512Mi"
            error={errors["resources.limitMemory"]}
          />
        </div>
      </div>

      {/* Health Probes */}
      <div className="space-y-4">
        <h3 className="text-sm font-medium text-text-secondary">
          Health Probes
        </h3>
        <ProbeSection
          label="Liveness Probe"
          probe={livenessProbe}
          onToggle={() =>
            onChange(
              "livenessProbe",
              livenessProbe === null ? { ...defaultProbe } : null,
            )
          }
          onUpdate={(field, value) => {
            if (livenessProbe) {
              onChange("livenessProbe", { ...livenessProbe, [field]: value });
            }
          }}
        />
        <ProbeSection
          label="Readiness Probe"
          probe={readinessProbe}
          onToggle={() =>
            onChange(
              "readinessProbe",
              readinessProbe === null ? { ...defaultProbe } : null,
            )
          }
          onUpdate={(field, value) => {
            if (readinessProbe) {
              onChange("readinessProbe", { ...readinessProbe, [field]: value });
            }
          }}
        />
      </div>

      {/* Update Strategy */}
      <div className="space-y-3">
        <h3 className="text-sm font-medium text-text-secondary">
          Update Strategy
        </h3>
        <Select
          value={strategy.type}
          onChange={(e) =>
            onChange("strategy", {
              ...strategy,
              type: e.currentTarget.value,
            })
          }
          options={STRATEGY_OPTIONS}
        />
        {strategy.type === "RollingUpdate" && (
          <div className="flex gap-4">
            <Input
              label="Max Surge"
              value={strategy.maxSurge}
              onChange={(e) =>
                onChange("strategy", {
                  ...strategy,
                  maxSurge: e.currentTarget.value,
                })
              }
              placeholder="25%"
            />
            <Input
              label="Max Unavailable"
              value={strategy.maxUnavailable}
              onChange={(e) =>
                onChange("strategy", {
                  ...strategy,
                  maxUnavailable: e.currentTarget.value,
                })
              }
              placeholder="25%"
            />
          </div>
        )}
      </div>
    </div>
  );
}
