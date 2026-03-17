import { useSignal } from "@preact/signals";
import { apiPost } from "@/lib/api.ts";
import { Button } from "@/components/ui/Button.tsx";
import { NS_NAME_REGEX } from "@/lib/wizard-constants.ts";

interface NamespaceSelectProps {
  value: string;
  namespaces: string[];
  error?: string;
  onChange: (ns: string) => void;
  /** Called after a namespace is created — use to add it to the parent's list. */
  onNamespaceCreated?: (ns: string) => void;
}

export function NamespaceSelect(
  { value, namespaces, error, onChange, onNamespaceCreated }:
    NamespaceSelectProps,
) {
  const showCreate = useSignal(false);
  const newName = useSignal("");
  const creating = useSignal(false);
  const createError = useSignal<string | null>(null);

  const handleSelectChange = (e: Event) => {
    const val = (e.target as HTMLSelectElement).value;
    if (val === "__create__") {
      showCreate.value = true;
    } else {
      showCreate.value = false;
      onChange(val);
    }
  };

  const handleCreate = async () => {
    const name = newName.value.trim();
    if (!name) return;
    if (!NS_NAME_REGEX.test(name)) {
      createError.value =
        "Must be lowercase alphanumeric with dashes, max 63 chars";
      return;
    }
    if (name.startsWith("kube-")) {
      createError.value =
        'Names starting with "kube-" are reserved for Kubernetes system namespaces';
      return;
    }
    if (namespaces.includes(name)) {
      createError.value = `Namespace "${name}" already exists`;
      return;
    }

    creating.value = true;
    createError.value = null;
    try {
      await apiPost("/v1/resources/namespaces", {
        metadata: { name },
      });
      showCreate.value = false;
      newName.value = "";
      onChange(name);
      onNamespaceCreated?.(name);
    } catch (err: unknown) {
      createError.value = err instanceof Error
        ? err.message
        : "Failed to create namespace";
    } finally {
      creating.value = false;
    }
  };

  return (
    <div class="space-y-1">
      <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
        Namespace
      </label>
      <select
        value={showCreate.value ? "__create__" : value}
        onChange={handleSelectChange}
        class={`block w-full rounded-md border px-3 py-2 text-sm shadow-sm transition-colors focus:outline-none focus:ring-2 ${
          error
            ? "border-danger focus:ring-danger/50"
            : "border-slate-300 focus:border-brand focus:ring-brand/50 dark:border-slate-600 dark:bg-slate-800 dark:text-white"
        }`}
      >
        {namespaces.map((ns) => <option key={ns} value={ns}>{ns}</option>)}
        <option value="__create__">+ Create New Namespace</option>
      </select>
      {error && <p class="text-sm text-danger">{error}</p>}

      {showCreate.value && (
        <div class="mt-2 p-3 rounded-md border border-blue-200 dark:border-blue-800 bg-blue-50 dark:bg-blue-900/20">
          <div class="flex items-center gap-2">
            <input
              type="text"
              value={newName.value}
              onInput={(e) =>
                newName.value = (e.target as HTMLInputElement).value}
              placeholder="new-namespace"
              maxLength={63}
              class="flex-1 rounded-md border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-900 dark:text-white"
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
              disabled={creating.value || !newName.value.trim()}
            >
              {creating.value ? "Creating..." : "Create"}
            </Button>
            <button
              type="button"
              onClick={() => {
                showCreate.value = false;
                newName.value = "";
                createError.value = null;
              }}
              class="text-sm text-slate-500 hover:text-slate-700"
            >
              Cancel
            </button>
          </div>
          {createError.value && (
            <p class="mt-1 text-sm text-red-600 dark:text-red-400">
              {createError.value}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
