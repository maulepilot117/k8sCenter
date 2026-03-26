import { Input } from "@/components/ui/Input.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { Select } from "@/components/ui/Select.tsx";

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

const PROTOCOL_OPTIONS = [
  { value: "TCP", label: "TCP" },
  { value: "UDP", label: "UDP" },
];

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
    onChange("ports", ports.filter((_, i) => i !== index));
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
    onChange("envVars", envVars.filter((_, i) => i !== index));
  };

  return (
    <div class="space-y-8 max-w-2xl">
      {/* Container Ports */}
      <div class="space-y-3">
        <label class="block text-sm font-medium text-text-secondary">
          Container Ports
        </label>
        {ports.map((port, i) => (
          <div key={i} class="flex items-end gap-2">
            <div class="flex-1">
              <Input
                label={i === 0 ? "Name" : undefined}
                value={port.name}
                onInput={(e) =>
                  updatePort(i, "name", (e.target as HTMLInputElement).value)}
                placeholder="http"
              />
            </div>
            <div class="w-28">
              <Input
                label={i === 0 ? "Port" : undefined}
                type="number"
                value={port.containerPort ? String(port.containerPort) : ""}
                onInput={(e) =>
                  updatePort(
                    i,
                    "containerPort",
                    parseInt((e.target as HTMLInputElement).value) || 0,
                  )}
                placeholder="80"
                min={1}
                max={65535}
                error={errors[`ports[${i}].containerPort`]}
              />
            </div>
            <div class="w-24">
              <Select
                label={i === 0 ? "Proto" : undefined}
                value={port.protocol}
                onChange={(e) =>
                  updatePort(
                    i,
                    "protocol",
                    (e.target as HTMLSelectElement).value,
                  )}
                options={PROTOCOL_OPTIONS}
              />
            </div>
            <RemoveButton
              onClick={() => removePort(i)}
              title="Remove port"
              class="p-2 mb-1"
            />
          </div>
        ))}
        {ports.length < 20 && (
          <button
            type="button"
            onClick={addPort}
            class="text-sm text-brand hover:text-brand/80"
          >
            + Add Port
          </button>
        )}
      </div>

      {/* Environment Variables */}
      <div class="space-y-3">
        <label class="block text-sm font-medium text-text-secondary">
          Environment Variables
        </label>
        {envVars.map((env, i) => (
          <div key={i} class="flex items-end gap-2">
            <div class="w-36">
              <Input
                label={i === 0 ? "Name" : undefined}
                value={env.name}
                onInput={(e) =>
                  updateEnvVar(
                    i,
                    "name",
                    (e.target as HTMLInputElement).value,
                  )}
                placeholder="MY_VAR"
                error={errors[`envVars[${i}].name`]}
              />
            </div>
            <div class="w-28">
              <Select
                label={i === 0 ? "Source" : undefined}
                value={env.type}
                onChange={(e) =>
                  updateEnvVar(
                    i,
                    "type",
                    (e.target as HTMLSelectElement).value,
                  )}
                options={ENV_TYPE_OPTIONS}
              />
            </div>
            {env.type === "literal"
              ? (
                <div class="flex-1">
                  <Input
                    label={i === 0 ? "Value" : undefined}
                    value={env.value}
                    onInput={(e) =>
                      updateEnvVar(
                        i,
                        "value",
                        (e.target as HTMLInputElement).value,
                      )}
                    placeholder="value"
                  />
                </div>
              )
              : (
                <>
                  <div class="flex-1">
                    <Input
                      label={i === 0
                        ? (env.type === "configmap" ? "ConfigMap" : "Secret")
                        : undefined}
                      value={env.ref}
                      onInput={(e) =>
                        updateEnvVar(
                          i,
                          "ref",
                          (e.target as HTMLInputElement).value,
                        )}
                      placeholder={env.type === "configmap"
                        ? "configmap-name"
                        : "secret-name"}
                    />
                  </div>
                  <div class="w-28">
                    <Input
                      label={i === 0 ? "Key" : undefined}
                      value={env.key}
                      onInput={(e) =>
                        updateEnvVar(
                          i,
                          "key",
                          (e.target as HTMLInputElement).value,
                        )}
                      placeholder="data-key"
                    />
                  </div>
                </>
              )}
            <RemoveButton
              onClick={() => removeEnvVar(i)}
              title="Remove env var"
              class="p-2 mb-1"
            />
          </div>
        ))}
        {envVars.length < 50 && (
          <button
            type="button"
            onClick={addEnvVar}
            class="text-sm text-brand hover:text-brand/80"
          >
            + Add Environment Variable
          </button>
        )}
      </div>
    </div>
  );
}
