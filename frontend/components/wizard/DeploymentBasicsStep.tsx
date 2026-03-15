import { Input } from "@/components/ui/Input.tsx";
import { KeyValueListEditor } from "@/components/ui/KeyValueListEditor.tsx";
import { Select } from "@/components/ui/Select.tsx";
import type { LabelEntry } from "@/lib/wizard-types.ts";

interface DeploymentBasicsProps {
  name: string;
  namespace: string;
  image: string;
  replicas: number;
  labels: LabelEntry[];
  namespaces: string[];
  errors: Record<string, string>;
  onChange: (field: string, value: unknown) => void;
}

export function DeploymentBasicsStep({
  name,
  namespace,
  image,
  replicas,
  labels,
  namespaces,
  errors,
  onChange,
}: DeploymentBasicsProps) {
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
        placeholder="my-deployment"
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

      <Input
        label="Container Image"
        value={image}
        onInput={(e) => onChange("image", (e.target as HTMLInputElement).value)}
        placeholder="nginx:1.25"
        error={errors.image}
        required
      />

      <Input
        label="Replicas"
        type="number"
        value={String(replicas)}
        onInput={(e) =>
          onChange(
            "replicas",
            parseInt((e.target as HTMLInputElement).value) ||
              0,
          )}
        min={0}
        max={1000}
        error={errors.replicas}
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
