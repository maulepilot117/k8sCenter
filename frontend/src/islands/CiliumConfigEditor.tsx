import { useEffect, useState } from "react";
import { Card } from "@/components/ui/Card.tsx";
import { PanelStateNotice } from "@/components/ui/PanelStateNotice.tsx";
import { apiGet, apiPut } from "@/lib/api.ts";
import {
  failureOf,
  type PanelState,
  panelState,
} from "@/lib/networking-state.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

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
  const isClient = useIsClient();
  const [config, setConfig] = useState<CiliumConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadState, setLoadState] = useState<PanelState | null>(null);
  const [editKey, setEditKey] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  // Read through the ref: a second Enter can arrive before the render that
  // disables the Save button.
  const [saving, setSaving, savingRef] = useStateRef(false);
  // The outcome of the last save, and what its "Try again" repeats.
  const [notice, setNotice] = useState<PanelState | null>(null);
  const [retry, setRetry] = useState<"save" | "refresh">("save");

  /** Reads the config. Returns the failure state, or null on success. */
  const fetchConfig = async (): Promise<PanelState | null> => {
    try {
      const resp = await apiGet<CiliumConfig | Record<string, unknown>>(
        "/v1/networking/cni/config",
      );
      setConfig(
        resp.data && "config" in resp.data ? (resp.data as CiliumConfig) : null,
      );
      return null;
    } catch (err) {
      return panelState(failureOf(err), WHAT);
    }
  };

  const load = async () => {
    setLoading(true);
    setLoadState(await fetchConfig());
    setLoading(false);
  };

  const refresh = async () => {
    setNotice((await fetchConfig()) ? SAVED_NOT_REFRESHED : null);
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `load` is recreated every render
  useEffect(() => {
    load();
  }, []);

  const closeEdit = () => {
    setEditKey(null);
    setEditValue("");
  };

  const startEdit = (key: string, value: string) => {
    setEditKey(key);
    setEditValue(value);
    setNotice(null);
  };

  const cancelEdit = () => {
    closeEdit();
    setNotice(null);
  };

  const saveEdit = async () => {
    const key = editKey;
    const loaded = config;
    // A save in flight blocks another: Enter and the notice's Try again
    // reach here even while the Save button is disabled.
    if (key === null || !loaded || savingRef.current) return;
    const value = editValue;
    setSaving(true);
    setNotice(null);
    setRetry("save");

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
      setConfig({ ...loaded, config: { ...loaded.config, [key]: value } });
      setRetry("refresh");
      await refresh();
    } catch (err) {
      const failure = failureOf(err);
      if (failure && failure.status !== 403 && !failure.reason) {
        // A local failure carries no reason; its message is the server's
        // own fixed copy, and the edit stays open to try again.
        setNotice({
          kind: "unavailable",
          message: (err as Error).message,
          action: null,
        });
        return;
      }
      const state = panelState(failure, WHAT, "save");
      setNotice(state);
      if (state.kind === "reload" || state.kind === "outcome_unknown") {
        // Saving again cannot work, or might apply the change twice: only a
        // reload resolves it.
        closeEdit();
      } else if (state.kind === "conflict") {
        // Show the cluster's current values next to the pending edit.
        await fetchConfig();
      }
    } finally {
      setSaving(false);
    }
  };

  if (!isClient || loading) {
    return (
      <div className="animate-pulse space-y-4">
        <div className="h-8 bg-elevated rounded w-48" />
        <div className="h-64 bg-elevated rounded" />
      </div>
    );
  }

  if (loadState) {
    return (
      <Card>
        <PanelStateNotice state={loadState} onRetry={load} />
      </Card>
    );
  }

  if (!config) {
    return (
      <Card>
        <p className="text-text-muted text-center py-8">
          Configuration not available.
        </p>
      </Card>
    );
  }

  const sortedKeys = Object.keys(config.config).sort();

  return (
    <Card
      title={`Cilium Configuration (${config.configMapNamespace}/${config.configMapName})`}
    >
      {notice && (
        <div className="mb-4">
          <PanelStateNotice
            state={notice}
            onRetry={retry === "refresh" ? refresh : saveEdit}
          />
        </div>
      )}
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border-primary">
              <th className="text-left py-2 px-3 font-medium text-text-muted w-1/3">
                Key
              </th>
              <th className="text-left py-2 px-3 font-medium text-text-muted">
                Value
              </th>
              <th className="py-2 px-3 w-20" />
            </tr>
          </thead>
          <tbody>
            {sortedKeys.map((key) => (
              <tr
                key={key}
                className="border-b border-border-subtle hover:bg-hover/50"
              >
                <td className="py-2 px-3 font-mono text-xs text-text-secondary">
                  {key}
                </td>
                <td className="py-2 px-3">
                  {editKey === key ? (
                    <input
                      type="text"
                      value={editValue}
                      onChange={(e) => setEditValue(e.currentTarget.value)}
                      className="w-full px-2 py-1 text-xs font-mono border border-brand rounded bg-surface text-text-primary"
                      onKeyDown={(e) => {
                        if (e.key === "Enter") saveEdit();
                        if (e.key === "Escape") cancelEdit();
                      }}
                    />
                  ) : (
                    <span className="font-mono text-xs text-text-secondary">
                      {config.config[key] || (
                        <em className="text-text-muted">empty</em>
                      )}
                    </span>
                  )}
                </td>
                <td className="py-2 px-3 text-right">
                  {editKey === key ? (
                    <div className="flex gap-1 justify-end">
                      <button
                        type="button"
                        onClick={saveEdit}
                        disabled={saving}
                        className="text-xs text-success hover:text-success font-medium"
                      >
                        {saving ? "..." : "Save"}
                      </button>
                      <button
                        type="button"
                        onClick={cancelEdit}
                        className="text-xs text-text-muted hover:text-text-secondary"
                      >
                        Cancel
                      </button>
                    </div>
                  ) : (
                    <button
                      type="button"
                      onClick={() => startEdit(key, config.config[key])}
                      className="text-xs text-brand hover:text-brand/80"
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
