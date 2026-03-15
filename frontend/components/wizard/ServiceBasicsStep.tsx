import { Input } from "@/components/ui/Input.tsx";
import { KeyValueListEditor } from "@/components/ui/KeyValueListEditor.tsx";
import { Select } from "@/components/ui/Select.tsx";
import type { LabelEntry } from "@/lib/wizard-types.ts";

interface ServiceBasicsProps {
  name: string;
  namespace: string;
  type: string;
  labels: LabelEntry[];
  namespaces: string[];
  errors: Record<string, string>;
  onChange: (field: string, value: unknown) => void;
}

const SERVICE_TYPE_OPTIONS = [
  { value: "ClusterIP", label: "ClusterIP" },
  { value: "NodePort", label: "NodePort" },
  { value: "LoadBalancer", label: "LoadBalancer" },
];

export function ServiceBasicsStep({
  name,
  namespace,
  type,
  labels,
  namespaces,
  errors,
  onChange,
}: ServiceBasicsProps) {
  const nsOptions = namespaces.map((ns) => ({ value: ns, label: ns }));

  const updateLabel = (index: number, field: "key" | "value", val: string) => {
    const updated = [...labels];
    updated[index] = { ...updated[index], [field]: val };
    onChange("labels", updated);
  };

  const addLabel = () => {
    onChange("labels", [...labels, { key: "", value: "" }]);
  };

  const removeLabel = (index: number) => {
    onChange("labels", labels.filter((_, i) => i !== index));
  };

  return (
    <div class="space-y-6 max-w-lg">
      <Input
        label="Name"
        value={name}
        onInput={(e) => onChange("name", (e.target as HTMLInputElement).value)}
        placeholder="my-service"
        error={errors.name}
        required
      />

      <Select
        label="Namespace"
        value={namespace}
        onChange={(e) =>
          onChange("namespace", (e.target as HTMLSelectElement).value)}
        options={nsOptions}
        error={errors.namespace}
      />

      <Select
        label="Service Type"
        value={type}
        onChange={(e) =>
          onChange("type", (e.target as HTMLSelectElement).value)}
        options={SERVICE_TYPE_OPTIONS}
        error={errors.type}
      />

      <KeyValueListEditor
        label="Labels"
        entries={labels}
        onUpdate={updateLabel}
        onAdd={addLabel}
        onRemove={removeLabel}
        addLabel="+ Add Label"
      />
    </div>
  );
}
