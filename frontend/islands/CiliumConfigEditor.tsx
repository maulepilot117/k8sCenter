import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPut } from "@/lib/api.ts";
import { Card } from "@/components/ui/Card.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

interface CiliumConfig {
  cniType: string;
  configSource: string;
  configMapName: string;
  configMapNamespace: string;
  editable: boolean;
  config: Record<string, string>;
}

export default function CiliumConfigEditor() {
  const config = useSignal<CiliumConfig | null>(null);
  const loading = useSignal(true);
  const editKey = useSignal<string | null>(null);
  const editValue = useSignal("");
  const saving = useSignal(false);
  const saveError = useSignal<string | null>(null);

  const fetchConfig = async () => {
    try {
      const resp = await apiGet<CiliumConfig | Record<string, unknown>>(
        "/v1/networking/cni/config",
      );
      if (resp.data && "config" in resp.data) {
        config.value = resp.data as CiliumConfig;
      } else {
        config.value = null;
      }
    } catch {
      // Config may not be available
    } finally {
      loading.value = false;
    }
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchConfig();
  }, []);

  const startEdit = (key: string, value: string) => {
    editKey.value = key;
    editValue.value = value;
    saveError.value = null;
  };

  const cancelEdit = () => {
    editKey.value = null;
    editValue.value = "";
    saveError.value = null;
  };

  const saveEdit = async () => {
    if (editKey.value === null) return;
    saving.value = true;
    saveError.value = null;

    try {
      await apiPut("/v1/networking/cni/config", {
        changes: { [editKey.value]: editValue.value },
        confirmed: true,
      });
      editKey.value = null;
      editValue.value = "";
      fetchConfig();
    } catch (err) {
      saveError.value = err instanceof Error
        ? err.message
        : "Failed to save configuration";
    } finally {
      saving.value = false;
    }
  };

  if (!IS_BROWSER || loading.value) {
    return (
      <div class="animate-pulse space-y-4">
        <div class="h-8 bg-elevated rounded w-48" />
        <div class="h-64 bg-elevated rounded" />
      </div>
    );
  }

  if (!config.value) {
    return (
      <Card>
        <p class="text-text-muted text-center py-8">
          Configuration not available.
        </p>
      </Card>
    );
  }

  const sortedKeys = Object.keys(config.value.config).sort();

  return (
    <Card
      title={`Cilium Configuration (${config.value.configMapNamespace}/${config.value.configMapName})`}
    >
      {saveError.value && (
        <div class="mb-4">
          <ErrorBanner message={saveError.value} />
        </div>
      )}
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-border-primary">
              <th class="text-left py-2 px-3 font-medium text-text-muted w-1/3">
                Key
              </th>
              <th class="text-left py-2 px-3 font-medium text-text-muted">
                Value
              </th>
              <th class="py-2 px-3 w-20" />
            </tr>
          </thead>
          <tbody>
            {sortedKeys.map((key) => (
              <tr
                key={key}
                class="border-b border-border-subtle hover:bg-hover/50"
              >
                <td class="py-2 px-3 font-mono text-xs text-text-secondary">
                  {key}
                </td>
                <td class="py-2 px-3">
                  {editKey.value === key
                    ? (
                      <input
                        type="text"
                        value={editValue.value}
                        onInput={(e) =>
                          editValue.value =
                            (e.target as HTMLInputElement).value}
                        class="w-full px-2 py-1 text-xs font-mono border border-brand rounded bg-surface text-text-primary"
                        onKeyDown={(e) => {
                          if (e.key === "Enter") saveEdit();
                          if (e.key === "Escape") cancelEdit();
                        }}
                      />
                    )
                    : (
                      <span class="font-mono text-xs text-text-secondary">
                        {config.value!.config[key] || (
                          <em class="text-text-muted">empty</em>
                        )}
                      </span>
                    )}
                </td>
                <td class="py-2 px-3 text-right">
                  {editKey.value === key
                    ? (
                      <div class="flex gap-1 justify-end">
                        <button
                          type="button"
                          onClick={saveEdit}
                          disabled={saving.value}
                          class="text-xs text-success hover:text-success font-medium"
                        >
                          {saving.value ? "..." : "Save"}
                        </button>
                        <button
                          type="button"
                          onClick={cancelEdit}
                          class="text-xs text-text-muted hover:text-text-secondary"
                        >
                          Cancel
                        </button>
                      </div>
                    )
                    : (
                      <button
                        type="button"
                        onClick={() =>
                          startEdit(key, config.value!.config[key])}
                        class="text-xs text-brand hover:text-brand/80"
                      >
                        Edit
                      </button>
                    )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
