import { Input } from "@/components/ui/Input.tsx";
import { Select } from "@/components/ui/Select.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { WIZARD_INPUT_CLASS } from "@/lib/wizard-constants.ts";

interface PortEntry {
  containerPort: number;
  protocol: string;
}

interface EnvVarEntry {
  name: string;
  value: string;
  configMapRef: string;
  secretRef: string;
  key: string;
}

export interface ContainerFormProps {
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

const PROTOCOL_OPTIONS = [
  { value: "TCP", label: "TCP" },
  { value: "UDP", label: "UDP" },
];

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
    onChange("ports", ports.filter((_, i) => i !== index));
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
    onChange("envVars", envVars.filter((_, i) => i !== index));
  };

  const ENV_SOURCE_OPTIONS = [
    { value: "literal", label: "Value" },
    { value: "configmap", label: "ConfigMap" },
    { value: "secret", label: "Secret" },
  ];

  return (
    <div class="space-y-8 max-w-2xl">
      {/* Image */}
      <div class="space-y-1">
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
          Container Image <span class="text-danger">*</span>
        </label>
        <input
          type="text"
          value={image}
          onInput={(e) =>
            onChange("image", (e.target as HTMLInputElement).value)}
          placeholder="nginx:latest"
          class={WIZARD_INPUT_CLASS}
        />
        {errors.image && <p class="text-sm text-danger">{errors.image}</p>}
      </div>

      {/* Command */}
      <div class="space-y-1">
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
          Command
        </label>
        <input
          type="text"
          value={command}
          onInput={(e) =>
            onChange("command", (e.target as HTMLInputElement).value)}
          placeholder="e.g. /bin/sh -c (space-separated)"
          class={WIZARD_INPUT_CLASS}
        />
        <p class="text-xs text-slate-500">
          Override the container entrypoint. Split by spaces.
        </p>
      </div>

      {/* Args */}
      <div class="space-y-1">
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
          Arguments
        </label>
        <input
          type="text"
          value={args}
          onInput={(e) =>
            onChange("args", (e.target as HTMLInputElement).value)}
          placeholder='e.g. echo "hello world" (space-separated)'
          class={WIZARD_INPUT_CLASS}
        />
        <p class="text-xs text-slate-500">
          Arguments passed to the command. Split by spaces.
        </p>
      </div>

      {/* Container Ports */}
      <div class="space-y-3">
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
          Container Ports
        </label>
        {ports.map((port, i) => (
          <div key={i} class="flex items-end gap-2">
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
                label={i === 0 ? "Protocol" : undefined}
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
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
          Environment Variables
        </label>
        {envVars.map((env, i) => {
          const source = envSourceType(env);
          return (
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
                  value={source}
                  onChange={(e) =>
                    changeEnvSource(
                      i,
                      (e.target as HTMLSelectElement).value,
                    )}
                  options={ENV_SOURCE_OPTIONS}
                />
              </div>
              {source === "literal"
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
                          ? (source === "configmap" ? "ConfigMap" : "Secret")
                          : undefined}
                        value={source === "configmap"
                          ? env.configMapRef
                          : env.secretRef}
                        onInput={(e) =>
                          updateEnvVar(
                            i,
                            source === "configmap"
                              ? "configMapRef"
                              : "secretRef",
                            (e.target as HTMLInputElement).value,
                          )}
                        placeholder={source === "configmap"
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
          );
        })}
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

      {/* Resource Limits */}
      <div class="space-y-3">
        <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
          Resource Requests & Limits
        </label>
        <div class="grid grid-cols-2 gap-4">
          <div class="space-y-1">
            <label class="block text-xs text-slate-500 dark:text-slate-400">
              CPU Request
            </label>
            <input
              type="text"
              value={requestCpu}
              onInput={(e) =>
                onChange("requestCpu", (e.target as HTMLInputElement).value)}
              placeholder="100m"
              class={WIZARD_INPUT_CLASS}
            />
            {errors.requestCpu && (
              <p class="text-sm text-danger">{errors.requestCpu}</p>
            )}
          </div>
          <div class="space-y-1">
            <label class="block text-xs text-slate-500 dark:text-slate-400">
              Memory Request
            </label>
            <input
              type="text"
              value={requestMemory}
              onInput={(e) => onChange(
                "requestMemory",
                (e.target as HTMLInputElement).value,
              )}
              placeholder="128Mi"
              class={WIZARD_INPUT_CLASS}
            />
            {errors.requestMemory && (
              <p class="text-sm text-danger">{errors.requestMemory}</p>
            )}
          </div>
          <div class="space-y-1">
            <label class="block text-xs text-slate-500 dark:text-slate-400">
              CPU Limit
            </label>
            <input
              type="text"
              value={limitCpu}
              onInput={(e) =>
                onChange("limitCpu", (e.target as HTMLInputElement).value)}
              placeholder="500m"
              class={WIZARD_INPUT_CLASS}
            />
            {errors.limitCpu && (
              <p class="text-sm text-danger">{errors.limitCpu}</p>
            )}
          </div>
          <div class="space-y-1">
            <label class="block text-xs text-slate-500 dark:text-slate-400">
              Memory Limit
            </label>
            <input
              type="text"
              value={limitMemory}
              onInput={(e) =>
                onChange("limitMemory", (e.target as HTMLInputElement).value)}
              placeholder="256Mi"
              class={WIZARD_INPUT_CLASS}
            />
            {errors.limitMemory && (
              <p class="text-sm text-danger">{errors.limitMemory}</p>
            )}
          </div>
        </div>
        <p class="text-xs text-slate-500">
          Use k8s resource format: CPU (e.g. 100m, 0.5, 1), Memory (e.g. 128Mi,
          1Gi).
        </p>
      </div>
    </div>
  );
}
