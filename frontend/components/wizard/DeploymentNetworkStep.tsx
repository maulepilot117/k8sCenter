import { Input } from "@/components/ui/Input.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { Select } from "@/components/ui/Select.tsx";
import { PROTOCOL_OPTIONS } from "@/lib/wizard-constants.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

interface PortEntry {
  name: string;
  containerPort: number;
  protocol: string;
}

interface EnvVarEntry {
  name: string;
  type: "literal" | "configmap" | "secret";
  value: string;
  ref: string;
  key: string;
}

interface DeploymentNetworkProps {
  ports: PortEntry[];
  envVars: EnvVarEntry[];
  errors: Record<string, string>;
  onChange: (field: string, value: unknown) => void;
}

const ENV_TYPE_OPTIONS = [
  { value: "literal", label: "Value" },
  { value: "configmap", label: "ConfigMap" },
  { value: "secret", label: "Secret" },
];

export function DeploymentNetworkStep({
  ports,
  envVars,
  errors,
  onChange,
}: DeploymentNetworkProps) {
  const portKeys = useRowKeys(ports.length);
  const envKeys = useRowKeys(envVars.length);

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
    onChange("ports", [
      ...ports,
      { name: "", containerPort: 0, protocol: "TCP" },
    ]);
  };

  const removePort = (index: number) => {
    portKeys.removeAt(index);
    onChange(
      "ports",
      ports.filter((_, i) => i !== index),
    );
  };

  const updateEnvVar = (
    index: number,
    field: keyof EnvVarEntry,
    val: string,
  ) => {
    const updated = [...envVars];
    updated[index] = { ...updated[index], [field]: val };
    onChange("envVars", updated);
  };

  const addEnvVar = () => {
    if (envVars.length >= 50) return;
    onChange("envVars", [
      ...envVars,
      { name: "", type: "literal" as const, value: "", ref: "", key: "" },
    ]);
  };

  const removeEnvVar = (index: number) => {
    envKeys.removeAt(index);
    onChange(
      "envVars",
      envVars.filter((_, i) => i !== index),
    );
  };

  return (
    <div className="space-y-8 max-w-2xl">
      {/* Container Ports */}
      <div className="space-y-3">
        <label className="block text-sm font-medium text-text-secondary">
          Container Ports
        </label>
        {ports.map((port, i) => (
          <div key={portKeys.keyAt(i)} className="flex items-end gap-2">
            <div className="flex-1">
              <Input
                label={i === 0 ? "Name" : undefined}
                value={port.name}
                onChange={(e) => updatePort(i, "name", e.currentTarget.value)}
                placeholder="http"
              />
            </div>
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
                label={i === 0 ? "Proto" : undefined}
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
        {envVars.map((env, i) => (
          <div key={envKeys.keyAt(i)} className="flex items-end gap-2">
            <div className="w-36">
              <Input
                label={i === 0 ? "Name" : undefined}
                value={env.name}
                onChange={(e) => updateEnvVar(i, "name", e.currentTarget.value)}
                placeholder="MY_VAR"
                error={errors[`envVars[${i}].name`]}
              />
            </div>
            <div className="w-28">
              <Select
                label={i === 0 ? "Source" : undefined}
                value={env.type}
                onChange={(e) => updateEnvVar(i, "type", e.currentTarget.value)}
                options={ENV_TYPE_OPTIONS}
              />
            </div>
            {env.type === "literal" ? (
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
                        ? env.type === "configmap"
                          ? "ConfigMap"
                          : "Secret"
                        : undefined
                    }
                    value={env.ref}
                    onChange={(e) =>
                      updateEnvVar(i, "ref", e.currentTarget.value)
                    }
                    placeholder={
                      env.type === "configmap"
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
        ))}
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
    </div>
  );
}
