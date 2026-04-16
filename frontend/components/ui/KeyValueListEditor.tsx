import { Input } from "@/components/ui/Input.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";

export interface KeyValueEntry {
  key: string;
  value: string;
}

interface KeyValueListEditorProps {
  label: string;
  entries: KeyValueEntry[];
  onUpdate: (index: number, field: "key" | "value", val: string) => void;
  onAdd: () => void;
  onRemove: (index: number) => void;
  addLabel?: string;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}

/**
 * Shared key-value pair list editor used across wizard steps
 * for labels, selectors, and similar key=value lists.
 */
export function KeyValueListEditor({
  label,
  entries,
  onUpdate,
  onAdd,
  onRemove,
  addLabel = "+ Add",
  keyPlaceholder = "key",
  valuePlaceholder = "value",
}: KeyValueListEditorProps) {
  return (
    <div class="space-y-2">
      <label class="block text-sm font-medium text-text-secondary">
        {label}
      </label>
      {entries.map((entry, i) => (
        <div key={i} class="flex items-center gap-2">
          <Input
            value={entry.key}
            onInput={(e) =>
              onUpdate(i, "key", (e.target as HTMLInputElement).value)}
            placeholder={keyPlaceholder}
            class="flex-1"
          />
          <span class="text-text-muted">=</span>
          <Input
            value={entry.value}
            onInput={(e) =>
              onUpdate(i, "value", (e.target as HTMLInputElement).value)}
            placeholder={valuePlaceholder}
            class="flex-1"
          />
          <RemoveButton
            onClick={() => onRemove(i)}
            title={`Remove ${label.toLowerCase()}`}
          />
        </div>
      ))}
      <button
        type="button"
        onClick={onAdd}
        class="text-sm text-brand hover:text-brand/80"
      >
        {addLabel}
      </button>
    </div>
  );
}
