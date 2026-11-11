import { Input } from "@/components/ui/Input.tsx";
import { KeyValueListEditor } from "@/components/ui/KeyValueListEditor.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { Select } from "@/components/ui/Select.tsx";
import { PROTOCOL_OPTIONS } from "@/lib/wizard-constants.ts";
import type { LabelEntry } from "@/lib/wizard-types.ts";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

interface PortEntry {
  name: string;
  port: number;
  targetPort: number;
  protocol: string;
  nodePort: number;
}

interface ServicePortsProps {
  ports: PortEntry[];
  selector: LabelEntry[];
  serviceType: string;
  errors: Record<string, string>;
  onChange: (field: string, value: unknown) => void;
}

export function ServicePortsStep({
  ports,
  selector,
  serviceType,
  errors,
  onChange,
}: ServicePortsProps) {
  const portKeys = useRowKeys(ports.length);
  const showNodePort =
    serviceType === "NodePort" || serviceType === "LoadBalancer";

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
      { name: "", port: 0, targetPort: 0, protocol: "TCP", nodePort: 0 },
    ]);
  };

  const removePort = (index: number) => {
    portKeys.removeAt(index);
    onChange(
      "ports",
      ports.filter((_, i) => i !== index),
    );
  };

  const updateSelector = (
    index: number,
    field: "key" | "value",
    val: string,
  ) => {
    const updated = [...selector];
    updated[index] = { ...updated[index], [field]: val };
    onChange("selector", updated);
  };

  const addSelector = () => {
    onChange("selector", [...selector, { key: "", value: "" }]);
  };

  const removeSelector = (index: number) => {
    onChange(
      "selector",
      selector.filter((_, i) => i !== index),
    );
  };

  return (
    <div className="space-y-8 max-w-2xl">
      {/* Selector */}
      <div className="space-y-3">
        <p className="text-xs text-text-muted">
          Labels used to match target pods. Must match at least one pod's
          labels.
        </p>
        {errors.selector && (
          <p className="text-sm text-danger">{errors.selector}</p>
        )}
        <KeyValueListEditor
          label="Pod Selector"
          entries={selector}
          onUpdate={updateSelector}
          onAdd={addSelector}
          onRemove={removeSelector}
          addLabel="+ Add Selector"
        />
      </div>

      {/* Ports */}
      <div className="space-y-3">
        <label className="block text-sm font-medium text-text-secondary">
          Service Ports
        </label>
        {errors.ports && <p className="text-sm text-danger">{errors.ports}</p>}
        {ports.map((port, i) => (
          <div key={portKeys.keyAt(i)} className="flex items-end gap-2">
            <div className="w-24">
              <Input
                label={i === 0 ? "Name" : undefined}
                value={port.name}
                onChange={(e) => updatePort(i, "name", e.currentTarget.value)}
                placeholder="http"
              />
            </div>
            <div className="w-20">
              <Input
                label={i === 0 ? "Port" : undefined}
                type="number"
                value={port.port ? String(port.port) : ""}
                onChange={(e) =>
                  updatePort(
                    i,
                    "port",
                    parseInt(e.currentTarget.value, 10) || 0,
                  )
                }
                placeholder="80"
                min={1}
                max={65535}
                error={errors[`ports[${i}].port`]}
              />
            </div>
            <div className="w-24">
              <Input
                label={i === 0 ? "Target" : undefined}
                type="number"
                value={port.targetPort ? String(port.targetPort) : ""}
                onChange={(e) =>
                  updatePort(
                    i,
                    "targetPort",
                    parseInt(e.currentTarget.value, 10) || 0,
                  )
                }
                placeholder="8080"
                min={1}
                max={65535}
                error={errors[`ports[${i}].targetPort`]}
              />
            </div>
            {/* Wide enough for "SCTP" next to the select's own arrow; at
                w-20 it clipped to "TC". */}
            <div className="w-28 shrink-0">
              <Select
                label={i === 0 ? "Protocol" : undefined}
                value={port.protocol}
                onChange={(e) =>
                  updatePort(i, "protocol", e.currentTarget.value)
                }
                options={PROTOCOL_OPTIONS}
              />
            </div>
            {showNodePort && (
              <div className="w-24">
                <Input
                  label={i === 0 ? "NodePort" : undefined}
                  type="number"
                  value={port.nodePort ? String(port.nodePort) : ""}
                  onChange={(e) =>
                    updatePort(
                      i,
                      "nodePort",
                      parseInt(e.currentTarget.value, 10) || 0,
                    )
                  }
                  placeholder="30080"
                  min={30000}
                  max={32767}
                  error={errors[`ports[${i}].nodePort`]}
                />
              </div>
            )}
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
    </div>
  );
}
