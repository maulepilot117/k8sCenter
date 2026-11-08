/** @jsxImportSource react */
import { Input } from "@/components/ui/Input.tsx";
import { RemoveButton } from "@/components/ui/RemoveButton.tsx";
import { useRowKeys } from "@/src/lib/use-row-keys.ts";

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
  const rowKeys = useRowKeys(entries.length);
  return (
    <div className="space-y-2">
      <label className="block text-sm font-medium text-text-secondary">
        {label}
      </label>
      {entries.map((entry, i) => (
        <div key={rowKeys.keyAt(i)} className="flex items-center gap-2">
          <Input
            value={entry.key}
            onChange={(e) => onUpdate(i, "key", e.currentTarget.value)}
            placeholder={keyPlaceholder}
            className="flex-1"
          />
          <span className="text-text-muted">=</span>
          <Input
            value={entry.value}
            onChange={(e) => onUpdate(i, "value", e.currentTarget.value)}
            placeholder={valuePlaceholder}
            className="flex-1"
          />
          <RemoveButton
            onClick={() => {
              rowKeys.removeAt(i);
              onRemove(i);
            }}
            title={`Remove ${label.toLowerCase()}`}
          />
        </div>
      ))}
      <button
        type="button"
        onClick={onAdd}
        className="text-sm text-brand hover:text-brand/80"
      >
        {addLabel}
      </button>
    </div>
  );
}
