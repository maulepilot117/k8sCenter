import { type ChangeEvent, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { apiPost } from "@/lib/api.ts";
import { NS_NAME_REGEX } from "@/lib/wizard-constants.ts";

interface NamespaceSelectProps {
  value: string;
  namespaces: string[];
  error?: string;
  onChange: (ns: string) => void;
  /** Called after a namespace is created — use to add it to the parent's list. */
  onNamespaceCreated?: (ns: string) => void;
}

export function NamespaceSelect({
  value,
  namespaces,
  error,
  onChange,
  onNamespaceCreated,
}: NamespaceSelectProps) {
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const handleSelectChange = (e: ChangeEvent<HTMLSelectElement>) => {
    const val = e.currentTarget.value;
    if (val === "__create__") {
      setShowCreate(true);
    } else {
      setShowCreate(false);
      onChange(val);
    }
  };

  const handleCreate = async () => {
    const name = newName.trim();
    if (!name) return;
    if (!NS_NAME_REGEX.test(name)) {
      setCreateError(
        "Must be lowercase alphanumeric with dashes, max 63 chars",
      );
      return;
    }
    if (name.startsWith("kube-")) {
      setCreateError(
        'Names starting with"kube-" are reserved for Kubernetes system namespaces',
      );
      return;
    }
    if (namespaces.includes(name)) {
      setCreateError(`Namespace "${name}" already exists`);
      return;
    }

    setCreating(true);
    setCreateError(null);
    try {
      await apiPost("/v1/resources/namespaces", {
        metadata: { name },
      });
      setShowCreate(false);
      setNewName("");
      onChange(name);
      onNamespaceCreated?.(name);
    } catch (err: unknown) {
      setCreateError(
        err instanceof Error ? err.message : "Failed to create namespace",
      );
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="space-y-1">
      <label className="block text-sm font-medium text-text-secondary">
        Namespace
      </label>
      <select
        value={showCreate ? "__create__" : value}
        onChange={handleSelectChange}
        className={`block w-full rounded-md border px-3 py-2 text-sm shadow-sm transition-colors focus:outline-none focus:ring-2 ${
          error
            ? "border-danger focus:ring-danger/50"
            : "border-border-primary focus:border-brand focus:ring-brand/50 border-border-primary bg-surface text-text-primary"
        }`}
      >
        {namespaces.map((ns) => (
          <option key={ns} value={ns}>
            {ns}
          </option>
        ))}
        <option value="__create__">+ Create New Namespace</option>
      </select>
      {error && <p className="text-sm text-danger">{error}</p>}

      {showCreate && (
        <div className="mt-2 p-3 rounded-md border border-accent bg-accent-dim">
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={newName}
              onChange={(e) => setNewName(e.currentTarget.value)}
              placeholder="new-namespace"
              maxLength={63}
              className="flex-1 rounded-md border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-primary"
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  handleCreate();
                }
              }}
            />
            <Button
              variant="primary"
              size="sm"
              onClick={handleCreate}
              disabled={creating || !newName.trim()}
            >
              {creating ? "Creating..." : "Create"}
            </Button>
            <button
              type="button"
              onClick={() => {
                setShowCreate(false);
                setNewName("");
                setCreateError(null);
              }}
              className="text-sm text-text-muted hover:text-text-secondary"
            >
              Cancel
            </button>
          </div>
          {createError && (
            <p className="mt-1 text-sm text-danger">{createError}</p>
          )}
        </div>
      )}
    </div>
  );
}
