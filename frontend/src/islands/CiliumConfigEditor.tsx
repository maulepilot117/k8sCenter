import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { Card } from "@/components/ui/Card.tsx";
import { PanelStateNotice } from "@/components/ui/PanelStateNotice.tsx";
import { apiGet, apiPut } from "@/lib/api.ts";
import {
  failureOf,
  type PanelState,
  panelState,
} from "@/lib/networking-state.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

interface CiliumConfig {
  cniType: string;
  configSource: string;
  configMapName: string;
  configMapNamespace: string;
  editable: boolean;
  config: Record<string, string>;
  /**
   * The cluster and registration this config was read from. An edit echoes
   * both and is addressed to that cluster, never the live selection, so it
   * cannot land on a cluster the operator did not load (#608 KTD6).
   */
  targetCluster?: string;
  targetGeneration?: string;
}

const WHAT = "the Cilium configuration";

/** A save that landed, followed by a re-read that did not. */
const SAVED_NOT_REFRESHED: PanelState = {
  kind: "unavailable",
  message:
    "Saved. The latest configuration could not be loaded, so other values shown may be out of date.",
  action: "retry",
};

export default function CiliumConfigEditor() {
  const config = useSignal<CiliumConfig | null>(null);
  const loading = useSignal(true);
  const loadState = useSignal<PanelState | null>(null);
  const editKey = useSignal<string | null>(null);
  const editValue = useSignal("");
  const saving = useSignal(false);
  // The outcome of the last save, and what its "Try again" repeats.
  const notice = useSignal<PanelState | null>(null);
  const retry = useSignal<"save" | "refresh">("save");

  /** Reads the config. Returns the failure state, or null on success. */
  const fetchConfig = async (): Promise<PanelState | null> => {
    try {
      const resp = await apiGet<CiliumConfig | Record<string, unknown>>(
        "/v1/networking/cni/config",
      );
      config.value =
        resp.data && "config" in resp.data ? (resp.data as CiliumConfig) : null;
      return null;
    } catch (err) {
      return panelState(failureOf(err), WHAT);
    }
  };

  const load = async () => {
    loading.value = true;
    loadState.value = await fetchConfig();
    loading.value = false;
  };

  const refresh = async () => {
    notice.value = (await fetchConfig()) ? SAVED_NOT_REFRESHED : null;
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    load();
  }, []);

  const closeEdit = () => {
    editKey.value = null;
    editValue.value = "";
  };

  const startEdit = (key: string, value: string) => {
    editKey.value = key;
    editValue.value = value;
    notice.value = null;
  };

  const cancelEdit = () => {
    closeEdit();
    notice.value = null;
  };

  const saveEdit = async () => {
    const key = editKey.value;
    const loaded = config.value;
    if (key === null || !loaded) return;
    const value = editValue.value;
    saving.value = true;
    notice.value = null;
    retry.value = "save";

    try {
      await apiPut(
        "/v1/networking/cni/config",
        {
          changes: { [key]: value },
          confirmed: true,
          targetCluster: loaded.targetCluster,
          targetGeneration: loaded.targetGeneration,
        },
        { clusterId: loaded.targetCluster },
      );
      // The write landed: show it even if the re-read below fails.
      closeEdit();
      config.value = { ...loaded, config: { ...loaded.config, [key]: value } };
      retry.value = "refresh";
      await refresh();
    } catch (err) {
      const failure = failureOf(err);
      if (failure && failure.status !== 403 && !failure.reason) {
        // A local failure carries no reason; its message is the server's
        // own fixed copy, and the edit stays open to try again.
        notice.value = {
          kind: "unavailable",
          message: (err as Error).message,
          action: null,
        };
        return;
      }
      const state = panelState(failure, WHAT, "save");
      notice.value = state;
      if (state.kind === "reload" || state.kind === "outcome_unknown") {
        // Saving again cannot work, or might apply the change twice: only a
        // reload resolves it.
        closeEdit();
      } else if (state.kind === "conflict") {
        // Show the cluster's current values next to the pending edit.
        await fetchConfig();
      }
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

  if (loadState.value) {
    return (
      <Card>
        <PanelStateNotice state={loadState.value} onRetry={load} />
      </Card>
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
      {notice.value && (
        <div class="mb-4">
          <PanelStateNotice
            state={notice.value}
            onRetry={retry.value === "refresh" ? refresh : saveEdit}
          />
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
                  {editKey.value === key ? (
                    <input
                      type="text"
                      value={editValue.value}
                      onInput={(e) =>
                        (editValue.value = (e.target as HTMLInputElement).value)
                      }
                      class="w-full px-2 py-1 text-xs font-mono border border-brand rounded bg-surface text-text-primary"
                      onKeyDown={(e) => {
                        if (e.key === "Enter") saveEdit();
                        if (e.key === "Escape") cancelEdit();
                      }}
                    />
                  ) : (
                    <span class="font-mono text-xs text-text-secondary">
                      {config.value?.config[key] || (
                        <em class="text-text-muted">empty</em>
                      )}
                    </span>
                  )}
                </td>
                <td class="py-2 px-3 text-right">
                  {editKey.value === key ? (
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
                  ) : (
                    <button
                      type="button"
                      onClick={() => startEdit(key, config.value!.config[key])}
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
