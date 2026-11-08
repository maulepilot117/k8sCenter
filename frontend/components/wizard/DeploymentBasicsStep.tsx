/** @jsxImportSource react */
import { Input } from "@/components/ui/Input.tsx";
import { KeyValueListEditor } from "@/components/ui/KeyValueListEditor.tsx";
import { NamespaceSelect } from "@/components/ui/NamespaceSelect.tsx";
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
  onNamespaceCreated?: (ns: string) => void;
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
  onNamespaceCreated,
}: DeploymentBasicsProps) {
  const updateLabel = (index: number, field: "key" | "value", val: string) => {
    const updated = [...labels];
    updated[index] = { ...updated[index], [field]: val };
    onChange("labels", updated);
  };

  const addLabel = () => {
    onChange("labels", [...labels, { key: "", value: "" }]);
  };

  const removeLabel = (index: number) => {
    onChange(
      "labels",
      labels.filter((_, i) => i !== index),
    );
  };

  return (
    <div className="space-y-6 max-w-lg">
      <Input
        label="Name"
        value={name}
        onChange={(e) => onChange("name", e.currentTarget.value)}
        placeholder="my-deployment"
        error={errors.name}
        required
      />

      <NamespaceSelect
        value={namespace}
        namespaces={namespaces}
        error={errors.namespace}
        onChange={(ns) => onChange("namespace", ns)}
        onNamespaceCreated={onNamespaceCreated}
      />

      <Input
        label="Container Image"
        value={image}
        onChange={(e) => onChange("image", e.currentTarget.value)}
        placeholder="nginx:1.25"
        error={errors.image}
        required
      />

      <Input
        label="Replicas"
        type="number"
        value={String(replicas)}
        onChange={(e) =>
          onChange("replicas", parseInt(e.currentTarget.value, 10) || 0)
        }
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
