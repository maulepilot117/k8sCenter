import { Input } from "@/components/ui/Input.tsx";
import { Select } from "@/components/ui/Select.tsx";
import type { ProbeState } from "@/lib/wizard-types.ts";
import { MAX_PROBE_PATH_LENGTH } from "@/lib/wizard-constants.ts";

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
    <div class="space-y-3">
      <div class="flex items-center gap-2">
        <input
          type="checkbox"
          checked={probe !== null}
          onChange={onToggle}
          class="rounded border-slate-300 dark:border-slate-600"
        />
        <span class="text-sm font-medium text-slate-700 dark:text-slate-300">
          {label}
        </span>
      </div>
      {probe && (
        <div class="ml-6 space-y-3 border-l-2 border-slate-200 dark:border-slate-700 pl-4">
          <Select
            label="Type"
            value={probe.type}
            onChange={(e) =>
              onUpdate("type", (e.target as HTMLSelectElement).value)}
            options={PROBE_TYPE_OPTIONS}
          />
          {probe.type === "http" && (
            <Input
              label="Path"
              value={probe.path}
              onInput={(e) =>
                onUpdate("path", (e.target as HTMLInputElement).value)}
              placeholder="/healthz"
              maxLength={MAX_PROBE_PATH_LENGTH}
            />
          )}
          <Input
            label="Port"
            type="number"
            value={probe.port ? String(probe.port) : ""}
            onInput={(e) =>
              onUpdate(
                "port",
                parseInt((e.target as HTMLInputElement).value) || 0,
              )}
            placeholder="8080"
            min={1}
            max={65535}
          />
          <div class="flex gap-4">
            <Input
              label="Initial Delay (s)"
              type="number"
              value={probe.initialDelaySeconds
                ? String(probe.initialDelaySeconds)
                : ""}
              onInput={(e) =>
                onUpdate(
                  "initialDelaySeconds",
                  parseInt((e.target as HTMLInputElement).value) || 0,
                )}
              placeholder="0"
              min={0}
            />
            <Input
              label="Period (s)"
              type="number"
              value={probe.periodSeconds ? String(probe.periodSeconds) : ""}
              onInput={(e) =>
                onUpdate(
                  "periodSeconds",
                  parseInt((e.target as HTMLInputElement).value) || 0,
                )}
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
    <div class="space-y-8 max-w-lg">
      {/* Resource Requests & Limits */}
      <div class="space-y-3">
        <h3 class="text-sm font-medium text-slate-700 dark:text-slate-300">
          Resource Requests & Limits
        </h3>
        <p class="text-xs text-slate-500">
          Optional. Set CPU and memory requests/limits for the container.
        </p>
        <div class="grid grid-cols-2 gap-4">
          <Input
            label="CPU Request"
            value={cpuRequest}
            onInput={(e) =>
              onChange("cpuRequest", (e.target as HTMLInputElement).value)}
            placeholder="100m"
            error={errors["resources.requestCpu"]}
          />
          <Input
            label="CPU Limit"
            value={cpuLimit}
            onInput={(e) =>
              onChange("cpuLimit", (e.target as HTMLInputElement).value)}
            placeholder="500m"
            error={errors["resources.limitCpu"]}
          />
          <Input
            label="Memory Request"
            value={memoryRequest}
            onInput={(e) =>
              onChange("memoryRequest", (e.target as HTMLInputElement).value)}
            placeholder="128Mi"
            error={errors["resources.requestMemory"]}
          />
          <Input
            label="Memory Limit"
            value={memoryLimit}
            onInput={(e) =>
              onChange("memoryLimit", (e.target as HTMLInputElement).value)}
            placeholder="512Mi"
            error={errors["resources.limitMemory"]}
          />
        </div>
      </div>

      {/* Health Probes */}
      <div class="space-y-4">
        <h3 class="text-sm font-medium text-slate-700 dark:text-slate-300">
          Health Probes
        </h3>
        <ProbeSection
          label="Liveness Probe"
          probe={livenessProbe}
          onToggle={() =>
            onChange(
              "livenessProbe",
              livenessProbe === null ? { ...defaultProbe } : null,
            )}
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
            )}
          onUpdate={(field, value) => {
            if (readinessProbe) {
              onChange("readinessProbe", { ...readinessProbe, [field]: value });
            }
          }}
        />
      </div>

      {/* Update Strategy */}
      <div class="space-y-3">
        <h3 class="text-sm font-medium text-slate-700 dark:text-slate-300">
          Update Strategy
        </h3>
        <Select
          value={strategy.type}
          onChange={(e) =>
            onChange("strategy", {
              ...strategy,
              type: (e.target as HTMLSelectElement).value,
            })}
          options={STRATEGY_OPTIONS}
        />
        {strategy.type === "RollingUpdate" && (
          <div class="flex gap-4">
            <Input
              label="Max Surge"
              value={strategy.maxSurge}
              onInput={(e) =>
                onChange("strategy", {
                  ...strategy,
                  maxSurge: (e.target as HTMLInputElement).value,
                })}
              placeholder="25%"
            />
            <Input
              label="Max Unavailable"
              value={strategy.maxUnavailable}
              onInput={(e) =>
                onChange("strategy", {
                  ...strategy,
                  maxUnavailable: (e.target as HTMLInputElement).value,
                })}
              placeholder="25%"
            />
          </div>
        )}
      </div>
    </div>
  );
}
