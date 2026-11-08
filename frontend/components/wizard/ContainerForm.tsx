/** @jsxImportSource react */
import { Input } from "@/components/ui/Input.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { Select } from "@/components/ui/Select.tsx";
import {
  PROTOCOL_OPTIONS,
  WIZARD_INPUT_CLASS,
} from "@/lib/wizard-constants.ts";
import type { EnvVarEntry, PortEntry } from "@/lib/wizard-types.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

interface ContainerFormProps {
  image: string;
  command: string;
  args: string;
  ports: PortEntry[];
  envVars: EnvVarEntry[];
  requestCpu: string;
  requestMemory: string;
  limitCpu: string;
  limitMemory: string;
  errors: Record<string, string>;
  onChange: (field: string, value: unknown) => void;
}

type EnvSourceType = "literal" | "configmap" | "secret";

/** Derive the UI source type from an EnvVarEntry. */
function envSourceType(e: EnvVarEntry): EnvSourceType {
  if (e.configMapRef) return "configmap";
  if (e.secretRef) return "secret";
  return "literal";
}

export function ContainerForm({
  image,
  command,
  args,
  ports,
  envVars,
  requestCpu,
  requestMemory,
  limitCpu,
  limitMemory,
  errors,
  onChange,
}: ContainerFormProps) {
  const portKeys = useRowKeys(ports.length);
  const envKeys = useRowKeys(envVars.length);

  // --- Port helpers ---
  const updatePort = (
    index: number,
    field: keyof PortEntry,
    val: string | number,
  ) => {
    const updated = [...ports];
    updated[index] = { ...updated[index], [field]: val };
    onChange("ports", updated);
  };

  const addPort = () => {
    if (ports.length >= 20) return;
    onChange("ports", [...ports, { containerPort: 0, protocol: "TCP" }]);
  };

  const removePort = (index: number) => {
    portKeys.removeAt(index);
    onChange(
      "ports",
      ports.filter((_, i) => i !== index),
    );
  };

  // --- Env var helpers ---
  const updateEnvVar = (
    index: number,
    field: keyof EnvVarEntry,
    val: string,
  ) => {
    const updated = [...envVars];
    updated[index] = { ...updated[index], [field]: val };
    onChange("envVars", updated);
  };

  const changeEnvSource = (index: number, source: string) => {
    const updated = [...envVars];
    const entry = { ...updated[index] };
    // Reset ref fields when switching source type
    if (source === "literal") {
      entry.configMapRef = "";
      entry.secretRef = "";
      entry.key = "";
    } else if (source === "configmap") {
      entry.value = "";
      entry.secretRef = "";
    } else if (source === "secret") {
      entry.value = "";
      entry.configMapRef = "";
    }
    updated[index] = entry;
    onChange("envVars", updated);
  };

  const addEnvVar = () => {
    if (envVars.length >= 50) return;
    onChange("envVars", [
      ...envVars,
      { name: "", value: "", configMapRef: "", secretRef: "", key: "" },
    ]);
  };

  const removeEnvVar = (index: number) => {
    envKeys.removeAt(index);
    onChange(
      "envVars",
      envVars.filter((_, i) => i !== index),
    );
  };

  const ENV_SOURCE_OPTIONS = [
    { value: "literal", label: "Value" },
    { value: "configmap", label: "ConfigMap" },
    { value: "secret", label: "Secret" },
  ];

  return (
    <div className="space-y-8 max-w-2xl">
      {/* Image */}
      <div className="space-y-1">
        <label className="block text-sm font-medium text-text-secondary">
          Container Image <span className="text-danger">*</span>
        </label>
        <input
          type="text"
          value={image}
          onChange={(e) => onChange("image", e.currentTarget.value)}
          placeholder="nginx:latest"
          className={WIZARD_INPUT_CLASS}
        />
        {errors.image && <p className="text-sm text-danger">{errors.image}</p>}
      </div>

      {/* Command */}
      <div className="space-y-1">
        <label className="block text-sm font-medium text-text-secondary">
          Command
        </label>
        <input
          type="text"
          value={command}
          onChange={(e) => onChange("command", e.currentTarget.value)}
          placeholder="e.g. /bin/sh -c (space-separated)"
          className={WIZARD_INPUT_CLASS}
        />
        <p className="text-xs text-text-muted">
          Override the container entrypoint. Split by spaces.
        </p>
      </div>

      {/* Args */}
      <div className="space-y-1">
        <label className="block text-sm font-medium text-text-secondary">
          Arguments
        </label>
        <input
          type="text"
          value={args}
          onChange={(e) => onChange("args", e.currentTarget.value)}
          placeholder='e.g. echo"hello world" (space-separated)'
          className={WIZARD_INPUT_CLASS}
        />
        <p className="text-xs text-text-muted">
          Arguments passed to the command. Split by spaces.
        </p>
      </div>

      {/* Container Ports */}
      <div className="space-y-3">
        <label className="block text-sm font-medium text-text-secondary">
          Container Ports
        </label>
        {ports.map((port, i) => (
          <div key={portKeys.keyAt(i)} className="flex items-end gap-2">
            <div className="w-28">
              <Input
                label={i === 0 ? "Port" : undefined}
                type="number"
                value={port.containerPort ? String(port.containerPort) : ""}
                onChange={(e) =>
                  updatePort(
                    i,
                    "containerPort",
                    parseInt(e.currentTarget.value, 10) || 0,
                  )
                }
                placeholder="80"
                min={1}
                max={65535}
                error={errors[`ports[${i}].containerPort`]}
              />
            </div>
            <div className="w-24">
              <Select
                label={i === 0 ? "Protocol" : undefined}
                value={port.protocol}
                onChange={(e) =>
                  updatePort(i, "protocol", e.currentTarget.value)
                }
                options={PROTOCOL_OPTIONS}
              />
            </div>
            <RemoveButton
              onClick={() => removePort(i)}
              title="Remove port"
              className="p-2 mb-1"
            />
          </div>
        ))}
        {ports.length < 20 && (
          <button
            type="button"
            onClick={addPort}
            className="text-sm text-brand hover:text-brand/80"
          >
            + Add Port
          </button>
        )}
      </div>

      {/* Environment Variables */}
      <div className="space-y-3">
        <label className="block text-sm font-medium text-text-secondary">
          Environment Variables
        </label>
        {envVars.map((env, i) => {
          const source = envSourceType(env);
          return (
            <div key={envKeys.keyAt(i)} className="flex items-end gap-2">
              <div className="w-36">
                <Input
                  label={i === 0 ? "Name" : undefined}
                  value={env.name}
                  onChange={(e) =>
                    updateEnvVar(i, "name", e.currentTarget.value)
                  }
                  placeholder="MY_VAR"
                  error={errors[`envVars[${i}].name`]}
                />
              </div>
              <div className="w-28">
                <Select
                  label={i === 0 ? "Source" : undefined}
                  value={source}
                  onChange={(e) => changeEnvSource(i, e.currentTarget.value)}
                  options={ENV_SOURCE_OPTIONS}
                />
              </div>
              {source === "literal" ? (
                <div className="flex-1">
                  <Input
                    label={i === 0 ? "Value" : undefined}
                    value={env.value}
                    onChange={(e) =>
                      updateEnvVar(i, "value", e.currentTarget.value)
                    }
                    placeholder="value"
                  />
                </div>
              ) : (
                <>
                  <div className="flex-1">
                    <Input
                      label={
                        i === 0
                          ? source === "configmap"
                            ? "ConfigMap"
                            : "Secret"
                          : undefined
                      }
                      value={
                        source === "configmap"
                          ? env.configMapRef
                          : env.secretRef
                      }
                      onChange={(e) =>
                        updateEnvVar(
                          i,
                          source === "configmap" ? "configMapRef" : "secretRef",
                          e.currentTarget.value,
                        )
                      }
                      placeholder={
                        source === "configmap"
                          ? "configmap-name"
                          : "secret-name"
                      }
                    />
                  </div>
                  <div className="w-28">
                    <Input
                      label={i === 0 ? "Key" : undefined}
                      value={env.key}
                      onChange={(e) =>
                        updateEnvVar(i, "key", e.currentTarget.value)
                      }
                      placeholder="data-key"
                    />
                  </div>
                </>
              )}
              <RemoveButton
                onClick={() => removeEnvVar(i)}
                title="Remove env var"
                className="p-2 mb-1"
              />
            </div>
          );
        })}
        {envVars.length < 50 && (
          <button
            type="button"
            onClick={addEnvVar}
            className="text-sm text-brand hover:text-brand/80"
          >
            + Add Environment Variable
          </button>
        )}
      </div>

      {/* Resource Limits */}
      <div className="space-y-3">
        <label className="block text-sm font-medium text-text-secondary">
          Resource Requests & Limits
        </label>
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1">
            <label className="block text-xs text-text-muted">CPU Request</label>
            <input
              type="text"
              value={requestCpu}
              onChange={(e) => onChange("requestCpu", e.currentTarget.value)}
              placeholder="100m"
              className={WIZARD_INPUT_CLASS}
            />
            {errors.requestCpu && (
              <p className="text-sm text-danger">{errors.requestCpu}</p>
            )}
          </div>
          <div className="space-y-1">
            <label className="block text-xs text-text-muted">
              Memory Request
            </label>
            <input
              type="text"
              value={requestMemory}
              onChange={(e) => onChange("requestMemory", e.currentTarget.value)}
              placeholder="128Mi"
              className={WIZARD_INPUT_CLASS}
            />
            {errors.requestMemory && (
              <p className="text-sm text-danger">{errors.requestMemory}</p>
            )}
          </div>
          <div className="space-y-1">
            <label className="block text-xs text-text-muted">CPU Limit</label>
            <input
              type="text"
              value={limitCpu}
              onChange={(e) => onChange("limitCpu", e.currentTarget.value)}
              placeholder="500m"
              className={WIZARD_INPUT_CLASS}
            />
            {errors.limitCpu && (
              <p className="text-sm text-danger">{errors.limitCpu}</p>
            )}
          </div>
          <div className="space-y-1">
            <label className="block text-xs text-text-muted">
              Memory Limit
            </label>
            <input
              type="text"
              value={limitMemory}
              onChange={(e) => onChange("limitMemory", e.currentTarget.value)}
              placeholder="256Mi"
              className={WIZARD_INPUT_CLASS}
            />
            {errors.limitMemory && (
              <p className="text-sm text-danger">{errors.limitMemory}</p>
            )}
          </div>
        </div>
        <p className="text-xs text-text-muted">
          Use k8s resource format: CPU (e.g. 100m, 0.5, 1), Memory (e.g. 128Mi,
          1Gi).
        </p>
      </div>
    </div>
  );
}
